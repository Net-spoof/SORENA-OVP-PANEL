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
	"strings"
	"time"
)

const (
	backupV2Root      = "/opt/irangate/backups/v2"
	backupV2MaxUpload = int64(512 << 20)
)

var backupV2ArchivePathRE = regexp.MustCompile(`/[^\s]+irangate-backup-v2-[^\s]+\.tar\.gz`)

func extractBackupV2ArchivePath(output string) string {
	matches := backupV2ArchivePathRE.FindAllString(output, -1)
	if len(matches) == 0 {
		return strings.TrimSpace(output)
	}

	return strings.TrimSpace(matches[len(matches)-1])
}

func extractBackupV2JSONPayload(output []byte) []byte {
	start := bytes.IndexByte(output, '{')
	end := bytes.LastIndexByte(output, '}')

	if start < 0 || end < start {
		return output
	}

	return output[start : end+1]
}

func exportBackupV2Handler(w http.ResponseWriter, r *http.Request) {
	// All backup routes share this coordinator. A short wait handles
	// normal contention with scheduled/Telegram exports without an instant
	// "already running" error. Never wait indefinitely on an HTTP request.
	locked := backupExecutionMu.TryLock()
	if !locked {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.NewTimer(30 * time.Second)
		defer deadline.Stop()

		for !locked {
			select {
			case <-r.Context().Done():
				return
			case <-deadline.C:
				sendError(w,
					"Another IranGate backup is still running; please retry after it finishes",
					http.StatusConflict)
				return
			case <-ticker.C:
				locked = backupExecutionMu.TryLock()
			}
		}
	}

	backupPath, err := func() (string, error) {
		defer backupExecutionMu.Unlock()
		archive, exportErr := runBackupV2Export()
		if exportErr != nil {
			return "", exportErr
		}
		// Never offer a broken archive as a successful browser download.
		if validateErr := validateBackupV2(archive); validateErr != nil {
			return "", fmt.Errorf("Backup v2 validation failed: %w", validateErr)
		}
		return archive, nil
	}()

	if err != nil {
		sendError(
			w,
			"Backup v2 export failed: "+err.Error(),
			http.StatusInternalServerError,
		)
		return
	}

	filename := filepath.Base(backupPath)

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

	w.Header().Set(
		"X-IranGate-Backup-Format",
		"v2",
	)

	http.ServeFile(
		w,
		r,
		backupPath,
	)
}

func importBackupV2Handler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(
		w,
		r.Body,
		backupV2MaxUpload,
	)

	if err := r.ParseMultipartForm(backupV2MaxUpload); err != nil {
		sendError(
			w,
			"Backup upload is invalid or too large",
			http.StatusBadRequest,
		)

		return
	}

	file, handler, err := r.FormFile("backup")

	if err != nil {
		sendError(
			w,
			"Failed to read backup upload",
			http.StatusBadRequest,
		)

		return
	}

	defer file.Close()

	name := strings.ToLower(
		handler.Filename,
	)

	if !strings.HasSuffix(name, ".tar.gz") &&
		!strings.HasSuffix(name, ".tgz") &&
		!strings.HasSuffix(name, ".gz") {

		sendError(
			w,
			"Backup v2 file must use .tar.gz, .tgz, or .gz",
			http.StatusBadRequest,
		)

		return
	}

	importDir := filepath.Join(
		backupV2Root,
		"imports",
	)

	if err := os.MkdirAll(importDir, 0700); err != nil {
		sendError(
			w,
			"Failed to create import directory",
			http.StatusInternalServerError,
		)

		return
	}

	tempFile, err := os.CreateTemp(
		importDir,
		"upload-*.tar.gz",
	)

	if err != nil {
		sendError(
			w,
			"Failed to create temporary import file",
			http.StatusInternalServerError,
		)

		return
	}

	tempPath := tempFile.Name()

	defer func() {
		_ = os.Remove(tempPath)
	}()

	if err := tempFile.Chmod(0600); err != nil {
		_ = tempFile.Close()

		sendError(
			w,
			"Failed to secure temporary import file",
			http.StatusInternalServerError,
		)

		return
	}

	if _, err := io.Copy(tempFile, file); err != nil {
		_ = tempFile.Close()

		sendError(
			w,
			"Failed to save uploaded backup",
			http.StatusInternalServerError,
		)

		return
	}

	if err := tempFile.Close(); err != nil {
		sendError(
			w,
			"Failed to finalize uploaded backup",
			http.StatusInternalServerError,
		)

		return
	}

	mode := "--restore"

	if r.URL.Query().Get("validate_only") == "1" {
		mode = "--validate-only"
	} else {
		// Restore mutates live PostgreSQL, clients, OpenVPN and node state.
		// It must not race scheduled/manual exports or another restore.
		// Validation-only is read-only and deliberately needs no lock.
		if !backupExecutionMu.TryLock() {
			sendError(
				w,
				"Another backup or restore is already running; retry after it completes",
				http.StatusConflict,
			)
			return
		}
		defer backupExecutionMu.Unlock()
	}

	cmd := exec.Command(
		"/usr/local/bin/irangate-backup-v2-import",
		mode,
		tempPath,
	)

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	output, cmdErr := cmd.Output()

	var result map[string]interface{}

	jsonErr := json.Unmarshal(
		extractBackupV2JSONPayload(output),
		&result,
	)

	if cmdErr != nil {
		message := strings.TrimSpace(
			stderr.String(),
		)

		if jsonErr == nil {
			if value, ok := result["error"].(string); ok &&
				strings.TrimSpace(value) != "" {

				message = value
			}
		}

		if message == "" {
			message = cmdErr.Error()
		}

		sendError(
			w,
			"Backup v2 import failed: "+message,
			http.StatusInternalServerError,
		)

		return
	}

	if jsonErr != nil {
		sendError(
			w,
			"Backup importer returned invalid output",
			http.StatusInternalServerError,
		)

		return
	}

	sendSuccess(
		w,
		func() string {
			if mode == "--validate-only" {
				return "Backup v2 validation successful"
			}

			return "Backup v2 restored successfully"
		}(),
		result,
	)
}
