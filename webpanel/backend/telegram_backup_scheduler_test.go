package main

import (
    "bytes"
    "mime/multipart"
    "testing"
)

func TestTelegramBackupMultipartOverhead(t *testing.T) {
    for _, tc := range []struct {
        name string
        chatID string
        caption string
        uploadName string
        payload string
    }{
        {
            name: "ascii",
            chatID: "-100123456789",
            caption: "IranGate Backup v2",
            uploadName: "irangate-backup.tar.gz",
            payload: "sample-backup-bytes",
        },
        {
            name: "unicode and multipart filename",
            chatID: "999963110",
            caption: "پشتیبان‌گیری ایران‌گیت",
            uploadName: "backup.tar.gz.part001",
            payload: "compresseddatacompresseddatacompresseddata",
        },
    } {
        t.Run(tc.name, func(t *testing.T) {
            var actual bytes.Buffer
            writer := multipart.NewWriter(&actual)
            boundary := writer.Boundary()
            if err := writer.WriteField("chat_id", tc.chatID); err != nil {
                t.Fatal(err)
            }
            if err := writer.WriteField("caption", tc.caption); err != nil {
                t.Fatal(err)
            }
            part, err := writer.CreateFormFile("document", tc.uploadName)
            if err != nil {
                t.Fatal(err)
            }
            if _, err := part.Write([]byte(tc.payload)); err != nil {
                t.Fatal(err)
            }
            if err := writer.Close(); err != nil {
                t.Fatal(err)
            }

            overhead, err := telegramBackupMultipartOverhead(
                boundary, tc.chatID, tc.caption, tc.uploadName,
            )
            if err != nil {
                t.Fatal(err)
            }

            want := overhead + int64(len(tc.payload))
            if got := int64(actual.Len()); got != want {
                t.Fatalf("Content-Length mismatch: got=%d want=%d", got, want)
            }
        })
    }
}

func TestTelegramBackupRunTracking(t *testing.T) {
    runID := beginTelegramBackupRun()
    if runID == "" {
        t.Fatal("expected nonempty run ID")
    }

    running, activeID, _ := readTelegramBackupRunStatus()
    if !running || activeID != runID {
        t.Fatalf("run must be marked active: running=%v, active=%q, expected=%q", running, activeID, runID)
    }

    // A stale completion must not finalize a newer run.
    finishTelegramBackupRun("stale-run-id")
    running, _, _ = readTelegramBackupRunStatus()
    if !running {
        t.Fatal("a stale run completed the currently active run")
    }

    finishTelegramBackupRun(runID)
    running, activeID, completedID := readTelegramBackupRunStatus()
    if running || activeID != runID || completedID != runID {
        t.Fatalf("incorrect completed status: running=%v, active=%q, completed=%q", running, activeID, completedID)
    }
}
