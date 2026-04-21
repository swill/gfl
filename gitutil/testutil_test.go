package gitutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initTestRepo creates a temporary git repository with a single initial commit.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@test.com")
	run(t, dir, "git", "config", "user.name", "Test")
	// Create an initial commit so HEAD exists.
	writeFile(t, dir, "README.md", "# test\n")
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-m", "initial commit")
	return dir
}

// run executes a command in dir and returns stdout. Fails the test on error.
func run(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %s: %v", name, args, out, err)
	}
	return string(out)
}

// writeFile writes content to a file relative to dir, creating parent dirs.
func writeFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commitAll stages everything and commits, returning the commit SHA.
func commitAll(t *testing.T, dir, msg string) string {
	t.Helper()
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-m", msg)
	return strings.TrimSpace(run(t, dir, "git", "rev-parse", "HEAD"))
}

// headSHA returns the current HEAD commit SHA.
func headSHA(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(run(t, dir, "git", "rev-parse", "HEAD"))
}
