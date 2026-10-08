package main

import (
    "bytes"
    "crypto/rand"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "os"
    "os/exec"
    "path/filepath"
    "regexp"
    "strings"
    "sync"
    "time"

    "github.com/gorilla/mux"
)

const backupImportJobsDir = "/opt/irangate/backups/v2/import-jobs"

var (
    backupImportJobIDRE = regexp.MustCompile(`^[a-f0-9]{32}$`)
    backupImportJobMu sync.Mutex
    // Running jobs only belong to this webpanel process. The persisted
    // result survives page reloads and service restarts.
    backupImportActive sync.Map
)

type backupImportJob struct {
    ID                string    `json:"job_id"`
    Status            string    `json:"status"`
    StartedAt         time.Time `json:"started_at"`
    CompletedAt       *time.Time `json:"completed_at,omitempty"`
    Error             string    `json:"error,omitempty"`
    RestoreSuccess    bool      `json:"restore_success"`
    RollbackPerformed bool      `json:"rollback_performed,omitempty"`
    SafetyBackup      string    `json:"safety_backup,omitempty"`
}

func newBackupImportJobID() (string, error) {
    var random [16]byte
    if _, err := rand.Read(random[:]); err != nil {
        return "", err
    }
    return hex.EncodeToString(random[:]), nil
}

func backupImportJobFile(id string) (string, error) {
    if !backupImportJobIDRE.MatchString(id) {
        return "", fmt.Errorf("invalid import job ID")
    }
    return filepath.Join(backupImportJobsDir, id+".json"), nil
}

func saveBackupImportJob(job backupImportJob) error {
    target, err := backupImportJobFile(job.ID)
    if err != nil {
        return err
    }

    backupImportJobMu.Lock()
    defer backupImportJobMu.Unlock()

    if err := os.MkdirAll(backupImportJobsDir, 0700); err != nil {
        return err
    }
    data, err := json.MarshalIndent(job, "", "  ")
    if err != nil {
        return err
    }
    file, err := os.CreateTemp(backupImportJobsDir, ".job-*.tmp")
    if err != nil {
        return err
    }
    temp := file.Name()
    defer os.Remove(temp)

    if err := file.Chmod(0600); err != nil {
        file.Close()
        return err
    }
    if _, err := file.Write(data); err != nil {
        file.Close()
        return err
    }
    if err := file.Close(); err != nil {
        return err
    }
    return os.Rename(temp, target)
}

func readBackupImportJob(id string) (backupImportJob, error) {
    var job backupImportJob
    path, err := backupImportJobFile(id)
    if err != nil {
        return job, err
    }
    content, err := os.ReadFile(path)
    if err != nil {
        return job, err
    }
    if err := json.Unmarshal(content, &job); err != nil {
        return job, err
    }
    if job.ID != id {
        return job, fmt.Errorf("backup import job ID mismatch")
    }

    if job.Status == "running" {
        // A panel restart interrupts any in-process restore. The job can
        // no longer be polled as if it is definitely still running.
        if _, active := backupImportActive.Load(id); !active {
            job.Status = "interrupted"
            job.Error = "Panel restarted during restore; inspect the server before retrying."
        }
    }

    return job, nil
}

func createBackupImportJob() (backupImportJob, error) {
    id, err := newBackupImportJobID()
    if err != nil {
        return backupImportJob{}, err
    }
    job := backupImportJob{
        ID: id,
        Status: "running",
        StartedAt: time.Now().UTC(),
    }
    backupImportActive.Store(id, struct{}{})
    if err := saveBackupImportJob(job); err != nil {
        backupImportActive.Delete(id)
        return backupImportJob{}, err
    }
    return job, nil
}

func backupImportFailureText(stderr, stdout []byte, runErr error) string {
    var result map[string]interface{}
    if json.Unmarshal(extractBackupV2JSONPayload(stdout), &result) == nil {
        if description, ok := result["error"].(string); ok && strings.TrimSpace(description) != "" {
            return strings.TrimSpace(description)
        }
    }
    description := strings.TrimSpace(string(stderr))
    if description == "" {
        description = runErr.Error()
    }
    if len(description) > 2500 {
        description = description[len(description)-2500:]
    }
    return description
}

// The backupExecutionMu lock is acquired by the request handler before this
// function is launched. Only the worker releases it. The uploaded file is
// retained until the restore process finishes, not until the HTTP 202 reply.
func runBackupImportAsync(job backupImportJob, uploadPath string) {
    defer backupExecutionMu.Unlock()
    defer os.Remove(uploadPath)
    defer backupImportActive.Delete(job.ID)

    var stdout, stderr bytes.Buffer
    cmd := exec.Command(
        "/usr/local/bin/irangate-backup-v2-import",
        "--restore",
        uploadPath,
    )
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr
    runErr := cmd.Run()

    completed := time.Now().UTC()
    job.CompletedAt = &completed

    var result map[string]interface{}
    parseErr := json.Unmarshal(
        extractBackupV2JSONPayload(stdout.Bytes()),
        &result,
    )

    if safety, ok := result["safety_backup"].(string); ok {
        job.SafetyBackup = safety
    }
    if rollback, ok := result["rollback_performed"].(bool); ok {
        job.RollbackPerformed = rollback
    }

    if runErr != nil {
        job.Status = "failed"
        job.Error = backupImportFailureText(stderr.Bytes(), stdout.Bytes(), runErr)
        log.Printf("Backup Import job %s failed: %s", job.ID, job.Error)
    } else if parseErr != nil {
        job.Status = "failed"
        job.Error = "Backup importer completed without a valid JSON result: " + parseErr.Error()
        log.Printf("Backup Import job %s returned invalid JSON", job.ID)
    } else if restored, ok := result["restore_success"].(bool); !ok || !restored {
        job.Status = "failed"
        job.Error = "Backup importer did not confirm restore_success"
        log.Printf("Backup Import job %s did not report restore_success", job.ID)
    } else {
        job.Status = "completed"
        job.RestoreSuccess = true
        log.Printf("Backup Import job %s completed successfully", job.ID)
    }

    if err := saveBackupImportJob(job); err != nil {
        log.Printf("Backup Import: failed to persist job %s status: %v", job.ID, err)
    }
}

func getBackupImportJobStatusHandler(w http.ResponseWriter, r *http.Request) {
    if !delegatedRootOnly(w, r) {
        return
    }
    id := mux.Vars(r)["jobID"]
    job, err := readBackupImportJob(id)
    if err != nil {
        sendError(w, "Backup import job not found", http.StatusNotFound)
        return
    }
    sendSuccess(w, "Backup import job status", job)
}
