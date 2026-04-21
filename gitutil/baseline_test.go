package gitutil

import (
	"strings"
	"testing"
)

func TestFindSyncCommit(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "docs/page.md", "v1\n")
	commitAll(t, dir, "chore(sync): confluence")

	writeFile(t, dir, "docs/page.md", "v2\n")
	commitAll(t, dir, "manual edit")

	sha, err := FindSyncCommit(dir, "docs/page.md")
	if err != nil {
		t.Fatalf("FindSyncCommit: %v", err)
	}
	if sha == "" {
		t.Fatal("expected a sync commit SHA")
	}

	// The sync commit should have "v1" content.
	content, err := ShowFile(dir, sha, "docs/page.md")
	if err != nil {
		t.Fatalf("ShowFile: %v", err)
	}
	if content != "v1\n" {
		t.Errorf("content = %q, want v1", content)
	}
}

func TestFindSyncCommit_None(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "docs/page.md", "v1\n")
	commitAll(t, dir, "manual add")

	sha, err := FindSyncCommit(dir, "docs/page.md")
	if err != nil {
		t.Fatalf("FindSyncCommit: %v", err)
	}
	if sha != "" {
		t.Errorf("expected empty, got %q", sha)
	}
}

func TestFindSyncCommit_MultipleSyncs(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "docs/page.md", "v1\n")
	commitAll(t, dir, "chore(sync): confluence")

	writeFile(t, dir, "docs/page.md", "v2\n")
	latestSync := commitAll(t, dir, "chore(sync): confluence")

	sha, err := FindSyncCommit(dir, "docs/page.md")
	if err != nil {
		t.Fatalf("FindSyncCommit: %v", err)
	}
	if sha != latestSync {
		t.Errorf("expected most recent sync %s, got %s", latestSync, sha)
	}
}

func TestShowFile(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "docs/page.md", "original\n")
	sha := commitAll(t, dir, "add page")

	writeFile(t, dir, "docs/page.md", "modified\n")
	commitAll(t, dir, "modify page")

	// Show at the old commit.
	content, err := ShowFile(dir, sha, "docs/page.md")
	if err != nil {
		t.Fatalf("ShowFile: %v", err)
	}
	if content != "original\n" {
		t.Errorf("content = %q", content)
	}
}

func TestBaseline(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "docs/page.md", "synced content\n")
	commitAll(t, dir, "chore(sync): confluence")

	writeFile(t, dir, "docs/page.md", "local edit\n")
	commitAll(t, dir, "manual edit")

	content, err := Baseline(dir, "docs/page.md")
	if err != nil {
		t.Fatalf("Baseline: %v", err)
	}
	if content != "synced content\n" {
		t.Errorf("baseline = %q", content)
	}
}

func TestBaseline_NoSync(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "docs/page.md", "new file\n")
	commitAll(t, dir, "add page")

	content, err := Baseline(dir, "docs/page.md")
	if err != nil {
		t.Fatalf("Baseline: %v", err)
	}
	if content != "" {
		t.Errorf("expected empty baseline, got %q", content)
	}
}

func TestMergeFile_Clean(t *testing.T) {
	base := "line 1\nline 2\nline 3\nline 4\nline 5\n"
	ours := "ours line 1\nline 2\nline 3\nline 4\nline 5\n"
	theirs := "line 1\nline 2\nline 3\nline 4\ntheirs line 5\n"

	merged, conflict, err := MergeFile(ours, base, theirs)
	if err != nil {
		t.Fatalf("MergeFile: %v", err)
	}
	if conflict {
		t.Error("expected clean merge")
	}
	if !strings.Contains(merged, "ours line 1") {
		t.Errorf("merged should contain ours change: %q", merged)
	}
	if !strings.Contains(merged, "theirs line 5") {
		t.Errorf("merged should contain theirs change: %q", merged)
	}
}

func TestMergeFile_Conflict(t *testing.T) {
	base := "line 1\nline 2\nline 3\n"
	ours := "line 1\nours line 2\nline 3\n"
	theirs := "line 1\ntheirs line 2\nline 3\n"

	merged, conflict, err := MergeFile(ours, base, theirs)
	if err != nil {
		t.Fatalf("MergeFile: %v", err)
	}
	if !conflict {
		t.Error("expected conflict")
	}
	if !strings.Contains(merged, "<<<<<<<") {
		t.Errorf("merged should contain conflict markers: %q", merged)
	}
}

func TestMergeFile_Identical(t *testing.T) {
	content := "same content\n"
	merged, conflict, err := MergeFile(content, content, content)
	if err != nil {
		t.Fatalf("MergeFile: %v", err)
	}
	if conflict {
		t.Error("identical inputs should not conflict")
	}
	if merged != content {
		t.Errorf("merged = %q", merged)
	}
}

func TestMergeFile_EmptyBase(t *testing.T) {
	// Both sides added the same content from nothing — should be clean.
	ours := "hello\n"
	theirs := "hello\n"
	merged, conflict, err := MergeFile(ours, "", theirs)
	if err != nil {
		t.Fatalf("MergeFile: %v", err)
	}
	if conflict {
		t.Error("expected clean merge for identical additions")
	}
	if merged != "hello\n" {
		t.Errorf("merged = %q", merged)
	}
}
