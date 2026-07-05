package main

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const backupV2Root = "/opt/irangate/backups/v2"

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
