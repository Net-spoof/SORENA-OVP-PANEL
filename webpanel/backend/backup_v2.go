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
	"strings"
)

const (
	backupV2Root      = "/opt/irangate/backups/v2"
	backupV2MaxUpload = int64(512 << 20)
)

func exportBackupV2Handler(w http.ResponseWriter, r *http.Request) {
	cmd := exec.Command(
		"/usr/local/bin/irangate-backup-v2-export",
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

		sendError(
			w,
			"Backup v2 export failed: "+message,
			http.StatusInternalServerError,
		)

		return
	}

	backupPath := filepath.Clean(
		strings.TrimSpace(string(output)),
	)

	root := filepath.Clean(
		backupV2Root,
	)

	rel, relErr := filepath.Rel(
		root,
		backupPath,
	)

	if relErr != nil ||
		rel == "." ||
		strings.HasPrefix(rel, "..") ||
		filepath.IsAbs(rel) {

		sendError(
			w,
			"Backup v2 exporter returned an invalid path",
			http.StatusInternalServerError,
		)

		return
	}

	info, statErr := os.Stat(
		backupPath,
	)

	if statErr != nil ||
		!info.Mode().IsRegular() ||
		info.Size() == 0 {

		sendError(
			w,
			"Backup v2 archive is missing or empty",
			http.StatusInternalServerError,
		)

		return
	}

	filename := filepath.Base(
		backupPath,
	)

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
		output,
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
