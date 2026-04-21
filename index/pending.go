package index

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"time"
)

// Pending entry types match the NDJSON record types documented in CLAUDE.md.
const (
	PendingContent    = "content"
	PendingRename     = "rename"
	PendingCreate     = "create"
	PendingDelete     = "delete"
	PendingAttachment = "attachment"
)

// PendingEntry is one record in the .confluencer-pending NDJSON file.
// Fields are a union across all record types; unused fields are omitted
// via omitempty.
type PendingEntry struct {
	Type         string    `json:"type"`
	PageID       string    `json:"page_id,omitempty"`
	ParentPageID string    `json:"parent_page_id,omitempty"`
	LocalPath    string    `json:"local_path,omitempty"`
	OldPath      string    `json:"old_path,omitempty"`
	NewPath      string    `json:"new_path,omitempty"`
	NewTitle     string    `json:"new_title,omitempty"`
	Title        string    `json:"title,omitempty"`
	Attempt      int       `json:"attempt"`
	LastError    string    `json:"last_error"`
	QueuedAt     time.Time `json:"queued_at"`
}

// LoadPending reads all pending entries from an NDJSON file. Returns nil
// (not an error) if the file does not exist — a missing file means an
// empty queue.
func LoadPending(path string) ([]PendingEntry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var entries []PendingEntry
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var e PendingEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, scanner.Err()
}

// AppendPending appends a single entry to the NDJSON file. The file is
// created if it does not exist.
func AppendPending(path string, entry PendingEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

// SavePending rewrites the pending file with the given entries. If the
// slice is empty the file is removed — an empty queue should not leave a
// stale file on disk.
func SavePending(path string, entries []PendingEntry) error {
	if len(entries) == 0 {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	var buf bytes.Buffer
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}
