package index

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

var testTime = time.Date(2026, 4, 13, 9, 20, 11, 0, time.UTC)

func TestLoadPending_FileNotFound(t *testing.T) {
	entries, err := LoadPending("/nonexistent/pending")
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil for missing file, got %v", entries)
	}
}

func TestLoadPending_Empty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending")
	os.WriteFile(path, []byte(""), 0o644)

	entries, err := LoadPending(path)
	if err != nil {
		t.Fatalf("LoadPending: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestLoadPending_SkipsBlankLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending")
	content := `{"type":"content","page_id":"1","attempt":1,"last_error":"err","queued_at":"2026-04-13T09:20:11Z"}

{"type":"delete","page_id":"2","attempt":1,"last_error":"err","queued_at":"2026-04-13T09:20:11Z"}
`
	os.WriteFile(path, []byte(content), 0o644)

	entries, err := LoadPending(path)
	if err != nil {
		t.Fatalf("LoadPending: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Type != PendingContent || entries[0].PageID != "1" {
		t.Errorf("entry 0: %+v", entries[0])
	}
	if entries[1].Type != PendingDelete || entries[1].PageID != "2" {
		t.Errorf("entry 1: %+v", entries[1])
	}
}

func TestLoadPending_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending")
	os.WriteFile(path, []byte("{bad\n"), 0o644)

	_, err := LoadPending(path)
	if err == nil {
		t.Error("expected error for invalid JSON line")
	}
}

func TestAppendPending(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending")

	e1 := PendingEntry{
		Type:      PendingContent,
		PageID:    "100",
		LocalPath: "docs/page.md",
		Attempt:   1,
		LastError: "409 version conflict",
		QueuedAt:  testTime,
	}
	e2 := PendingEntry{
		Type:      PendingDelete,
		PageID:    "200",
		LocalPath: "docs/gone.md",
		Attempt:   1,
		LastError: "network timeout",
		QueuedAt:  testTime,
	}

	if err := AppendPending(path, e1); err != nil {
		t.Fatalf("AppendPending 1: %v", err)
	}
	if err := AppendPending(path, e2); err != nil {
		t.Fatalf("AppendPending 2: %v", err)
	}

	// Reload and verify.
	entries, err := LoadPending(path)
	if err != nil {
		t.Fatalf("LoadPending: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].PageID != "100" || entries[0].Type != PendingContent {
		t.Errorf("entry 0: %+v", entries[0])
	}
	if entries[1].PageID != "200" || entries[1].Type != PendingDelete {
		t.Errorf("entry 1: %+v", entries[1])
	}
}

func TestSavePending_Rewrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending")

	// Write 3 entries.
	for _, id := range []string{"1", "2", "3"} {
		AppendPending(path, PendingEntry{
			Type:      PendingContent,
			PageID:    id,
			Attempt:   1,
			LastError: "err",
			QueuedAt:  testTime,
		})
	}

	// Rewrite with only 1 entry (simulate draining successful retries).
	remaining := []PendingEntry{{
		Type:      PendingContent,
		PageID:    "2",
		Attempt:   2,
		LastError: "retry failed",
		QueuedAt:  testTime,
	}}
	if err := SavePending(path, remaining); err != nil {
		t.Fatalf("SavePending: %v", err)
	}

	entries, _ := LoadPending(path)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after rewrite, got %d", len(entries))
	}
	if entries[0].PageID != "2" || entries[0].Attempt != 2 {
		t.Errorf("after rewrite: %+v", entries[0])
	}
}

func TestSavePending_EmptyRemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending")

	// Create a file first.
	AppendPending(path, PendingEntry{
		Type: PendingContent, PageID: "1", Attempt: 1, LastError: "err", QueuedAt: testTime,
	})

	// Save empty → file should be removed.
	if err := SavePending(path, nil); err != nil {
		t.Fatalf("SavePending: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should be removed when queue is empty")
	}
}

func TestSavePending_EmptyNoFile(t *testing.T) {
	// SavePending with empty slice when file doesn't exist → no error.
	dir := t.TempDir()
	path := filepath.Join(dir, "pending")

	if err := SavePending(path, nil); err != nil {
		t.Errorf("SavePending on non-existent file: %v", err)
	}
}

func TestPendingEntry_AllTypes(t *testing.T) {
	// Verify all record types from CLAUDE.md round-trip through JSON.
	dir := t.TempDir()
	path := filepath.Join(dir, "pending")

	entries := []PendingEntry{
		{
			Type:      PendingContent,
			PageID:    "100",
			LocalPath: "docs/page.md",
			Attempt:   1,
			LastError: "409 version conflict",
			QueuedAt:  testTime,
		},
		{
			Type:      PendingRename,
			PageID:    "200",
			OldPath:   "docs/old.md",
			NewPath:   "docs/new.md",
			NewTitle:  "New Title",
			Attempt:   1,
			LastError: "network timeout",
			QueuedAt:  testTime,
		},
		{
			Type:         PendingCreate,
			ParentPageID: "100",
			LocalPath:    "docs/new-page.md",
			Title:        "New Page",
			Attempt:      1,
			LastError:    "500 internal",
			QueuedAt:     testTime,
		},
		{
			Type:      PendingDelete,
			PageID:    "300",
			LocalPath: "docs/gone.md",
			Attempt:   1,
			LastError: "connection refused",
			QueuedAt:  testTime,
		},
		{
			Type:      PendingAttachment,
			PageID:    "100",
			LocalPath: "docs/_attachments/page/img.png",
			Attempt:   1,
			LastError: "413 too large",
			QueuedAt:  testTime,
		},
	}

	if err := SavePending(path, entries); err != nil {
		t.Fatalf("SavePending: %v", err)
	}

	loaded, err := LoadPending(path)
	if err != nil {
		t.Fatalf("LoadPending: %v", err)
	}
	if len(loaded) != len(entries) {
		t.Fatalf("expected %d entries, got %d", len(entries), len(loaded))
	}

	for i, want := range entries {
		got := loaded[i]
		if got.Type != want.Type {
			t.Errorf("entry %d type: got %q, want %q", i, got.Type, want.Type)
		}
		if got.PageID != want.PageID {
			t.Errorf("entry %d page_id: got %q, want %q", i, got.PageID, want.PageID)
		}
		if got.LocalPath != want.LocalPath {
			t.Errorf("entry %d local_path: got %q, want %q", i, got.LocalPath, want.LocalPath)
		}
		if got.Attempt != want.Attempt {
			t.Errorf("entry %d attempt: got %d, want %d", i, got.Attempt, want.Attempt)
		}
		if got.LastError != want.LastError {
			t.Errorf("entry %d last_error: got %q, want %q", i, got.LastError, want.LastError)
		}
		if !got.QueuedAt.Equal(want.QueuedAt) {
			t.Errorf("entry %d queued_at: got %v, want %v", i, got.QueuedAt, want.QueuedAt)
		}
	}

	// Spot-check type-specific fields.
	if loaded[1].OldPath != "docs/old.md" || loaded[1].NewPath != "docs/new.md" || loaded[1].NewTitle != "New Title" {
		t.Errorf("rename fields: %+v", loaded[1])
	}
	if loaded[2].ParentPageID != "100" || loaded[2].Title != "New Page" {
		t.Errorf("create fields: %+v", loaded[2])
	}
}
