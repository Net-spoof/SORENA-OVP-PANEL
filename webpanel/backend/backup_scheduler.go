package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/robfig/cron/v3"
)

type BackupSchedule struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Description    string     `json:"description"`
	CronExpression string     `json:"cron_expression"`
	Enabled        bool       `json:"enabled"`
	RetentionDays  int        `json:"retention_days"`
	BackupPath     string     `json:"backup_path"`
	IncludePaths   []string   `json:"include_paths"`
	LastRun        *time.Time `json:"last_run,omitempty"`
	NextRun        *time.Time `json:"next_run,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

type BackupRequest struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	CronExpression string   `json:"cron_expression"`
	Enabled        bool     `json:"enabled"`
	RetentionDays  int      `json:"retention_days"`
	BackupPath     string   `json:"backup_path"`
	IncludePaths   []string `json:"include_paths"`
}

type BackupFile struct {
	Filename   string    `json:"filename"`
	Size       int64     `json:"size"`
	CreatedAt  time.Time `json:"created_at"`
	BackupType string    `json:"backup_type"`
	ScheduleID string    `json:"schedule_id"`
}

type BackupExecution struct {
	ID          string    `json:"id"`
	ScheduleID  string    `json:"schedule_id"`
	Status      string    `json:"status"`
	Message     string    `json:"message"`
	FilesBacked int       `json:"files_backed"`
	TotalSize   int64     `json:"total_size"`
	Duration    int64     `json:"duration"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

type scheduledBackupDiskFile struct {
	Path       string
	Filename   string
	ScheduleID string
	Info       os.FileInfo
}

const (
	BackupSchedulerDataDir        = "/opt/irangate/webpanel/backups"
	scheduledBackupExporter       = "/usr/local/bin/irangate-backup-v2-export"
	scheduledBackupValidator      = "/usr/local/bin/irangate-backup-v2-import"
	scheduledBackupMaxPerSchedule = 30
)

var (
	cronScheduler = cron.New()

	cronEntriesMu sync.Mutex
	cronEntries   = make(map[string]cron.EntryID)

	backupExecutionMu sync.Mutex

	scheduledBackupFilenameRE = regexp.MustCompile(
		`^backup_(.+)_\d{8}_\d{6}_\d+\.tar\.gz$`,
	)
)

func init() {
	schedules, err := loadAllBackupSchedules()
	if err != nil {
		fmt.Printf(
			"Scheduled backup startup load failed: %v\n",
			err,
		)
	} else {
		for _, schedule := range schedules {
			if schedule.Enabled {
				scheduleBackup(schedule)
			}
		}
	}

	cronScheduler.Start()
}

func scheduledBackupDir() string {
	return filepath.Join(
		BackupSchedulerDataDir,
		"backups",
	)
}

func getBackupSchedulesHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	schedules, err := loadAllBackupSchedules()
	if err != nil {
		sendError(
			w,
			"Failed to load backup schedules",
			http.StatusInternalServerError,
		)
		return
	}

	sendSuccess(
		w,
		"Backup schedules retrieved successfully",
		schedules,
	)
}

func createBackupScheduleHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	var req BackupRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(
			w,
			"Invalid request body",
			http.StatusBadRequest,
		)
		return
	}

	if req.Name == "" || req.CronExpression == "" {
		sendError(
			w,
			"Name and cron expression are required",
			http.StatusBadRequest,
		)
		return
	}

	if _, err := cron.ParseStandard(req.CronExpression); err != nil {
		sendError(
			w,
			fmt.Sprintf(
				"Invalid cron expression: %v",
				err,
			),
			http.StatusBadRequest,
		)
		return
	}

	if req.RetentionDays <= 0 {
		req.RetentionDays = 30
	}

	if len(req.IncludePaths) == 0 {
		req.IncludePaths = getDefaultBackupPaths()
	}

	schedule := BackupSchedule{
		ID:             generateScheduleID(req.Name),
		Name:           req.Name,
		Description:    req.Description,
		CronExpression: req.CronExpression,
		Enabled:        req.Enabled,
		RetentionDays:  req.RetentionDays,

		// Scheduled Backup v2 همیشه در مسیر کنترل‌شده ذخیره می‌شود.
		BackupPath: scheduledBackupDir(),

		// برای backward compatibility در API نگهداری می‌شود.
		// موتور v2 خودش componentهای کامل Backup را تعیین می‌کند.
		IncludePaths: req.IncludePaths,

		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := saveBackupSchedule(&schedule); err != nil {
		sendError(
			w,
			"Failed to save backup schedule",
			http.StatusInternalServerError,
		)
		return
	}

	if schedule.Enabled {
		scheduleBackup(&schedule)
	}

	sendSuccess(
		w,
		"Backup schedule created successfully",
		schedule,
	)
}

func updateBackupScheduleHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	vars := mux.Vars(r)
	scheduleID := vars["id"]

	var req BackupRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendError(
			w,
			"Invalid request body",
			http.StatusBadRequest,
		)
		return
	}

	schedule, err := loadBackupSchedule(scheduleID)
	if err != nil {
		sendError(
			w,
			"Backup schedule not found",
			http.StatusNotFound,
		)
		return
	}

	if req.CronExpression != "" {
		if _, err := cron.ParseStandard(req.CronExpression); err != nil {
			sendError(
				w,
				fmt.Sprintf(
					"Invalid cron expression: %v",
					err,
				),
				http.StatusBadRequest,
			)
			return
		}
	}

	if req.Name != "" {
		schedule.Name = req.Name
	}

	if req.Description != "" {
		schedule.Description = req.Description
	}

	if req.CronExpression != "" {
		schedule.CronExpression = req.CronExpression
	}

	if req.IncludePaths != nil {
		schedule.IncludePaths = req.IncludePaths
	}

	if req.RetentionDays > 0 {
		schedule.RetentionDays = req.RetentionDays
	}

	schedule.Enabled = req.Enabled
	schedule.BackupPath = scheduledBackupDir()
	schedule.UpdatedAt = time.Now()

	if err := saveBackupSchedule(schedule); err != nil {
		sendError(
			w,
			"Failed to update backup schedule",
			http.StatusInternalServerError,
		)
		return
	}

	// جلوگیری از Cron job تکراری.
	removeScheduleFromCron(schedule.ID)

	if schedule.Enabled {
		scheduleBackup(schedule)
	}

	sendSuccess(
		w,
		"Backup schedule updated successfully",
		schedule,
	)
}

func deleteBackupScheduleHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	vars := mux.Vars(r)
	scheduleID := vars["id"]

	schedule, err := loadBackupSchedule(scheduleID)
	if err != nil {
		sendError(
			w,
			"Backup schedule not found",
			http.StatusNotFound,
		)
		return
	}

	removeScheduleFromCron(schedule.ID)

	scheduleFile := filepath.Join(
		BackupSchedulerDataDir,
		"schedules",
		scheduleID+".json",
	)

	if err := os.Remove(scheduleFile); err != nil {
		sendError(
			w,
			"Failed to delete backup schedule",
			http.StatusInternalServerError,
		)
		return
	}

	sendSuccess(
		w,
		"Backup schedule deleted successfully",
		map[string]interface{}{
			"schedule_id": scheduleID,
		},
	)
}

func runBackupScheduleHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	vars := mux.Vars(r)
	scheduleID := vars["id"]

	schedule, err := loadBackupSchedule(scheduleID)
	if err != nil {
		sendError(
			w,
			"Backup schedule not found",
			http.StatusNotFound,
		)
		return
	}

	go executeBackup(schedule)

	sendSuccess(
		w,
		"Backup execution started",
		map[string]interface{}{
			"schedule_id": scheduleID,
			"status":      "started",
		},
	)
}

func listBackupsHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	scheduleID := r.URL.Query().Get("schedule_id")

	backups, err := listBackupFiles(scheduleID)
	if err != nil {
		sendError(
			w,
			"Failed to list backup files",
			http.StatusInternalServerError,
		)
		return
	}

	sendSuccess(
		w,
		"Backup files retrieved successfully",
		backups,
	)
}

func deleteBackupHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	filename := mux.Vars(r)["filename"]

	if filepath.Base(filename) != filename ||
		strings.Contains(filename, "..") ||
		!strings.HasSuffix(filename, ".tar.gz") {

		sendError(
			w,
			"Invalid filename",
			http.StatusBadRequest,
		)
		return
	}

	backupPath := filepath.Join(
		scheduledBackupDir(),
		filename,
	)

	if err := os.Remove(backupPath); err != nil {
		sendError(
			w,
			"Failed to delete backup file",
			http.StatusInternalServerError,
		)
		return
	}

	sendSuccess(
		w,
		"Backup file deleted successfully",
		map[string]interface{}{
			"filename": filename,
		},
	)
}

func downloadBackupHandler(
	w http.ResponseWriter,
	r *http.Request,
) {
	filename := mux.Vars(r)["filename"]

	if filepath.Base(filename) != filename ||
		strings.Contains(filename, "..") ||
		!strings.HasSuffix(filename, ".tar.gz") {

		sendError(
			w,
			"Invalid filename",
			http.StatusBadRequest,
		)
		return
	}

	backupPath := filepath.Join(
		scheduledBackupDir(),
		filename,
	)

	info, err := os.Stat(backupPath)

	if err != nil ||
		!info.Mode().IsRegular() ||
		info.Size() == 0 {

		sendError(
			w,
			"Backup file not found",
			http.StatusNotFound,
		)
		return
	}

	w.Header().Set(
		"Content-Type",
		"application/gzip",
	)

	w.Header().Set(
		"Content-Disposition",
		fmt.Sprintf(
			"attachment; filename=\"%s\"",
			filename,
		),
	)

	http.ServeFile(
		w,
		r,
		backupPath,
	)
}

func generateScheduleID(name string) string {
	return fmt.Sprintf(
		"schedule_%s_%d",
		sanitizeFilename(name),
		time.Now().UnixNano(),
	)
}

func getDefaultBackupPaths() []string {
	return []string{
		"/opt/irangate/database",
		"/etc/openvpn",
		"/opt/irangate/clients",
	}
}

func saveBackupSchedule(
	schedule *BackupSchedule,
) error {
	schedulesDir := filepath.Join(
		BackupSchedulerDataDir,
		"schedules",
	)

	if err := os.MkdirAll(
		schedulesDir,
		0700,
	); err != nil {
		return err
	}

	scheduleFile := filepath.Join(
		schedulesDir,
		schedule.ID+".json",
	)

	data, err := json.MarshalIndent(
		schedule,
		"",
		"  ",
	)

	if err != nil {
		return err
	}

	return os.WriteFile(
		scheduleFile,
		data,
		0600,
	)
}

func loadBackupSchedule(
	scheduleID string,
) (*BackupSchedule, error) {
	scheduleFile := filepath.Join(
		BackupSchedulerDataDir,
		"schedules",
		scheduleID+".json",
	)

	data, err := os.ReadFile(scheduleFile)
	if err != nil {
		return nil, err
	}

	var schedule BackupSchedule

	if err := json.Unmarshal(
		data,
		&schedule,
	); err != nil {
		return nil, err
	}

	return &schedule, nil
}

func loadAllBackupSchedules() (
	[]*BackupSchedule,
	error,
) {
	schedulesDir := filepath.Join(
		BackupSchedulerDataDir,
		"schedules",
	)

	if err := os.MkdirAll(
		schedulesDir,
		0700,
	); err != nil {
		return nil, err
	}

	pattern := filepath.Join(
		schedulesDir,
		"*.json",
	)

	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}

	var schedules []*BackupSchedule

	for _, file := range files {
		schedule, err := loadBackupScheduleFromFile(file)
		if err != nil {
			continue
		}

		schedule.BackupPath = scheduledBackupDir()

		schedules = append(
			schedules,
			schedule,
		)
	}

	return schedules, nil
}

func loadBackupScheduleFromFile(
	filename string,
) (*BackupSchedule, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var schedule BackupSchedule

	if err := json.Unmarshal(
		data,
		&schedule,
	); err != nil {
		return nil, err
	}

	return &schedule, nil
}

func scheduleBackup(
	schedule *BackupSchedule,
) {
	removeScheduleFromCron(
		schedule.ID,
	)

	entryID, err := cronScheduler.AddFunc(
		schedule.CronExpression,
		func() {
			executeBackup(schedule)
		},
	)

	if err != nil {
		fmt.Printf(
			"Failed to schedule backup %s: %v\n",
			schedule.ID,
			err,
		)
		return
	}

	cronEntriesMu.Lock()
	cronEntries[schedule.ID] = entryID
	cronEntriesMu.Unlock()
}

func removeScheduleFromCron(
	scheduleID string,
) {
	cronEntriesMu.Lock()
	defer cronEntriesMu.Unlock()

	entryID, ok := cronEntries[scheduleID]
	if !ok {
		return
	}

	cronScheduler.Remove(entryID)

	delete(
		cronEntries,
		scheduleID,
	)
}

func executeBackup(
	schedule *BackupSchedule,
) {
	// Backupهای v2 را serial اجرا می‌کنیم تا Snapshotها روی هم نیفتند.
	backupExecutionMu.Lock()
	defer backupExecutionMu.Unlock()

	execution := BackupExecution{
		ID: fmt.Sprintf(
			"exec_%d",
			time.Now().UnixNano(),
		),
		ScheduleID: schedule.ID,
		StartedAt:  time.Now(),
		Status:     "running",
	}

	defer func() {
		execution.CompletedAt = time.Now()

		execution.Duration = execution.CompletedAt.
			Sub(execution.StartedAt).
			Milliseconds()

		_ = saveBackupExecution(
			&execution,
		)

		now := time.Now()
		schedule.LastRun = &now

		_ = saveBackupSchedule(
			schedule,
		)
	}()

	backupDir := scheduledBackupDir()

	if err := os.MkdirAll(
		backupDir,
		0700,
	); err != nil {

		execution.Status = "failed"

		execution.Message = fmt.Sprintf(
			"Failed to create backup directory: %v",
			err,
		)

		return
	}

	sourceBackup, err := runBackupV2Export()
	if err != nil {
		execution.Status = "failed"

		execution.Message = fmt.Sprintf(
			"Backup v2 export failed: %v",
			err,
		)

		return
	}

	if err := validateBackupV2(
		sourceBackup,
	); err != nil {

		_ = os.Remove(sourceBackup)

		execution.Status = "failed"

		execution.Message = fmt.Sprintf(
			"Backup v2 validation failed: %v",
			err,
		)

		return
	}

	timestamp := time.Now().Format(
		"20060102_150405",
	)

	filename := fmt.Sprintf(
		"backup_%s_%s_%d.tar.gz",
		schedule.ID,
		timestamp,
		time.Now().UnixNano(),
	)

	backupPath := filepath.Join(
		backupDir,
		filename,
	)

	if err := moveBackupFile(
		sourceBackup,
		backupPath,
	); err != nil {

		execution.Status = "failed"

		execution.Message = fmt.Sprintf(
			"Failed to move Backup v2 archive: %v",
			err,
		)

		return
	}

	fileInfo, err := os.Stat(backupPath)
	if err != nil {
		execution.Status = "failed"

		execution.Message = fmt.Sprintf(
			"Failed to stat Backup v2 archive: %v",
			err,
		)

		return
	}

	execution.Status = "success"

	execution.Message = fmt.Sprintf(
		"Backup v2 completed successfully: %s",
		filename,
	)

	execution.TotalSize = fileInfo.Size()

	// یک آرشیو v2 کامل ساخته شده است.
	execution.FilesBacked = 1

	cleanupOldBackups(schedule)
}

func runBackupV2Export() (
	string,
	error,
) {
	cmd := exec.Command(
		scheduledBackupExporter,
	)

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	output, err := cmd.Output()

	if err != nil {
		message := strings.TrimSpace(
			stderr.String(),
		)

		if message == "" {
			message = err.Error()
		}

		return "", fmt.Errorf(
			"%s",
			message,
		)
	}

	source := filepath.Clean(
		strings.TrimSpace(
			string(output),
		),
	)

	if source == "" {
		return "", fmt.Errorf(
			"exporter returned empty path",
		)
	}

	root := filepath.Clean(
		backupV2Root,
	)

	rel, err := filepath.Rel(
		root,
		source,
	)

	if err != nil ||
		rel == "." ||
		rel == ".." ||
		filepath.IsAbs(rel) ||
		strings.HasPrefix(
			rel,
			".."+string(os.PathSeparator),
		) {

		return "", fmt.Errorf(
			"exporter returned unsafe path: %s",
			source,
		)
	}

	info, err := os.Stat(source)

	if err != nil ||
		!info.Mode().IsRegular() ||
		info.Size() == 0 {

		return "", fmt.Errorf(
			"exported archive missing or empty",
		)
	}

	return source, nil
}

func validateBackupV2(
	path string,
) error {
	cmd := exec.Command(
		scheduledBackupValidator,
		"--validate-only",
		path,
	)

	output, err := cmd.CombinedOutput()

	if err != nil {
		return fmt.Errorf(
			"%s",
			strings.TrimSpace(
				string(output),
			),
		)
	}

	return nil
}

func moveBackupFile(
	source string,
	destination string,
) error {
	if err := os.Rename(
		source,
		destination,
	); err == nil {

		return os.Chmod(
			destination,
			0600,
		)
	}

	sourceFile, err := os.Open(source)
	if err != nil {
		return err
	}

	defer sourceFile.Close()

	tempDestination := destination + ".tmp"

	destinationFile, err := os.OpenFile(
		tempDestination,
		os.O_CREATE|
			os.O_WRONLY|
			os.O_TRUNC,
		0600,
	)

	if err != nil {
		return err
	}

	copyOK := false

	defer func() {
		_ = destinationFile.Close()

		if !copyOK {
			_ = os.Remove(
				tempDestination,
			)
		}
	}()

	if _, err := io.Copy(
		destinationFile,
		sourceFile,
	); err != nil {
		return err
	}

	if err := destinationFile.Sync(); err != nil {
		return err
	}

	if err := destinationFile.Close(); err != nil {
		return err
	}

	if err := os.Rename(
		tempDestination,
		destination,
	); err != nil {
		return err
	}

	copyOK = true

	if err := os.Remove(source); err != nil {
		return err
	}

	return os.Chmod(
		destination,
		0600,
	)
}

func scheduleIDFromBackupFilename(
	filename string,
) string {
	match := scheduledBackupFilenameRE.
		FindStringSubmatch(filename)

	if len(match) != 2 {
		return ""
	}

	return match[1]
}

func getScheduledBackupDiskFiles(
	scheduleID string,
) (
	[]scheduledBackupDiskFile,
	error,
) {
	backupDir := scheduledBackupDir()

	if err := os.MkdirAll(
		backupDir,
		0700,
	); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(
		backupDir,
	)

	if err != nil {
		return nil, err
	}

	files := make(
		[]scheduledBackupDiskFile,
		0,
	)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		filename := entry.Name()

		if !strings.HasPrefix(
			filename,
			"backup_",
		) ||
			!strings.HasSuffix(
				filename,
				".tar.gz",
			) {

			continue
		}

		extractedScheduleID :=
			scheduleIDFromBackupFilename(
				filename,
			)

		if extractedScheduleID == "" {
			continue
		}

		if scheduleID != "" &&
			extractedScheduleID != scheduleID {

			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		files = append(
			files,
			scheduledBackupDiskFile{
				Path: filepath.Join(
					backupDir,
					filename,
				),
				Filename:   filename,
				ScheduleID: extractedScheduleID,
				Info:       info,
			},
		)
	}

	sort.Slice(
		files,
		func(i, j int) bool {
			return files[i].Info.ModTime().
				After(
					files[j].Info.ModTime(),
				)
		},
	)

	return files, nil
}

func cleanupOldBackups(
	schedule *BackupSchedule,
) {
	files, err := getScheduledBackupDiskFiles(
		schedule.ID,
	)

	if err != nil {
		fmt.Printf(
			"Scheduled backup cleanup failed for %s: %v\n",
			schedule.ID,
			err,
		)

		return
	}

	retentionDays := schedule.RetentionDays

	if retentionDays <= 0 {
		retentionDays = 30
	}

	cutoff := time.Now().AddDate(
		0,
		0,
		-retentionDays,
	)

	for index, file := range files {
		removeForAge := file.Info.ModTime().
			Before(cutoff)

		removeForCount :=
			index >= scheduledBackupMaxPerSchedule

		if !removeForAge &&
			!removeForCount {

			continue
		}

		if err := os.Remove(file.Path); err != nil {
			fmt.Printf(
				"Failed to remove old scheduled backup %s: %v\n",
				file.Path,
				err,
			)
		}
	}
}

func listBackupFiles(
	scheduleID string,
) (
	[]*BackupFile,
	error,
) {
	files, err := getScheduledBackupDiskFiles(
		scheduleID,
	)

	if err != nil {
		return nil, err
	}

	backupFiles := make(
		[]*BackupFile,
		0,
		len(files),
	)

	for _, file := range files {
		backupFiles = append(
			backupFiles,
			&BackupFile{
				Filename:   file.Filename,
				Size:       file.Info.Size(),
				CreatedAt:  file.Info.ModTime(),
				BackupType: "scheduled-v2",
				ScheduleID: file.ScheduleID,
			},
		)
	}

	return backupFiles, nil
}

func saveBackupExecution(
	execution *BackupExecution,
) error {
	if err := os.MkdirAll(
		BackupSchedulerDataDir,
		0700,
	); err != nil {
		return err
	}

	executionsFile := filepath.Join(
		BackupSchedulerDataDir,
		"executions.json",
	)

	var executions []*BackupExecution

	if data, err := os.ReadFile(
		executionsFile,
	); err == nil {

		_ = json.Unmarshal(
			data,
			&executions,
		)
	}

	executions = append(
		executions,
		execution,
	)

	if len(executions) > 1000 {
		executions = executions[len(executions)-1000:]
	}

	data, err := json.MarshalIndent(
		executions,
		"",
		"  ",
	)

	if err != nil {
		return err
	}

	return os.WriteFile(
		executionsFile,
		data,
		0600,
	)
}
