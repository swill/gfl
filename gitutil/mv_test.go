package gitutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMove(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "docs/page.md", "# Page\n")
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-m", "add page")

	if err := Move(dir, "docs/page.md", "docs/renamed.md"); err != nil {
		t.Fatalf("Move: %v", err)
	}

	// Old path should not exist.
	if _, err := os.Stat(filepath.Join(dir, "docs/page.md")); !os.IsNotExist(err) {
		t.Error("old file should be gone")
	}
	// New path should exist.
	data, err := os.ReadFile(filepath.Join(dir, "docs/renamed.md"))
	if err != nil {
		t.Fatalf("read new file: %v", err)
	}
	if string(data) != "# Page\n" {
		t.Errorf("content = %q", string(data))
	}
}

func TestMove_CreatesDirectory(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "docs/page.md", "# Page\n")
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-m", "add page")

	// Move into a new subdirectory that doesn't exist yet.
	if err := Move(dir, "docs/page.md", "docs/subdir/index.md"); err != nil {
		t.Fatalf("Move: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "docs/subdir/index.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "# Page\n" {
		t.Errorf("content = %q", string(data))
	}
}

func TestRemove(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "docs/page.md", "# Page\n")
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-m", "add page")

	if err := Remove(dir, "docs/page.md"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "docs/page.md")); !os.IsNotExist(err) {
		t.Error("file should be removed")
	}
}

func TestRemove_Directory(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "docs/attachments/img.png", "PNG")
	writeFile(t, dir, "docs/attachments/other.png", "PNG2")
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-m", "add attachments")

	if err := Remove(dir, "docs/attachments"); err != nil {
		t.Fatalf("Remove dir: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "docs/attachments")); !os.IsNotExist(err) {
		t.Error("directory should be removed")
	}
}

func TestRemove_Empty(t *testing.T) {
	dir := initTestRepo(t)
	// No-op for empty paths.
	if err := Remove(dir); err != nil {
		t.Fatalf("Remove empty: %v", err)
	}
}

