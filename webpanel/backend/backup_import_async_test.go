package main

import (
    "errors"
    "strings"
    "testing"
)

func TestBackupImportJobID(t *testing.T) {
    first, err := newBackupImportJobID()
    if err != nil {
        t.Fatal(err)
    }
    second, err := newBackupImportJobID()
    if err != nil {
        t.Fatal(err)
    }
    if first == second || !backupImportJobIDRE.MatchString(first) ||
        !backupImportJobIDRE.MatchString(second) {
        t.Fatalf("invalid or duplicate backup import job IDs")
    }
    if _, err := backupImportJobFile("../other"); err == nil {
        t.Fatal("unsafe status path was accepted")
    }
}

func TestBackupImportFailureText(t *testing.T) {
    msg := backupImportFailureText(
        []byte("logging only"),
        []byte(`{"success":false,"error":"PostgreSQL restore failed"}`),
        errors.New("exit status 1"),
    )
    if msg != "PostgreSQL restore failed" {
        t.Fatalf("structured importer error not surfaced: %q", msg)
    }

    msg = backupImportFailureText(
        []byte("short diagnostic"),
        []byte("invalid JSON"),
        errors.New("exit status 1"),
    )
    if msg != "short diagnostic" {
        t.Fatalf("stderr fallback not used: %q", msg)
    }

    long := strings.Repeat("X", 3000)
    msg = backupImportFailureText([]byte(long), nil, errors.New("failed"))
    if len(msg) > 2500 {
        t.Fatal("restore diagnostic exceeded size limit")
    }
}
