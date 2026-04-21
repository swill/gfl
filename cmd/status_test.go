package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/swill/confluencer/index"
)

func TestRunStatus_NoPending(t *testing.T) {
	dir := initTestRepo(t)
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)

	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("runStatus: %v", err)
	}

	if !strings.Contains(buf.String(), "No pending") {
		t.Errorf("output = %q", buf.String())
	}
}

func TestRunStatus_WithPending(t *testing.T) {
	dir := initTestRepo(t)
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	// Create a pending file.
	pendingPath := filepath.Join(dir, pendingFile)
	index.AppendPending(pendingPath, index.PendingEntry{
		Type:      index.PendingContent,
		PageID:    "123",
		LocalPath: "docs/page.md",
		Attempt:   2,
		LastError: "409 version conflict",
		QueuedAt:  time.Now().UTC(),
	})
	index.AppendPending(pendingPath, index.PendingEntry{
		Type:      index.PendingDelete,
		PageID:    "456",
		LocalPath: "docs/gone.md",
		Attempt:   1,
		LastError: "network timeout",
		QueuedAt:  time.Now().UTC(),
	})

	var buf bytes.Buffer
	statusCmd.SetOut(&buf)

	if err := statusCmd.RunE(statusCmd, nil); err != nil {
		t.Fatalf("runStatus: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "2 pending") {
		t.Errorf("should report 2 pending: %q", output)
	}
	if !strings.Contains(output, "docs/page.md") {
		t.Errorf("should mention page.md: %q", output)
	}
	if !strings.Contains(output, "docs/gone.md") {
		t.Errorf("should mention gone.md: %q", output)
	}
	if !strings.Contains(output, "409 version conflict") {
		t.Errorf("should show error: %q", output)
	}
}
