package cmd

import (
	"os"
	"testing"
)

func TestRepoRoot(t *testing.T) {
	dir := initTestRepo(t)
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot: %v", err)
	}
	if root == "" {
		t.Error("expected non-empty root")
	}
}

func TestCopyFile(t *testing.T) {
	dir := t.TempDir()
	src := dir + "/src.txt"
	dst := dir + "/dst.txt"

	os.WriteFile(src, []byte("hello"), 0o644)
	if err := copyFile(src, dst, 0o755); err != nil {
		t.Fatalf("copyFile: %v", err)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q", string(data))
	}

	info, _ := os.Stat(dst)
	if info.Mode()&0o111 == 0 {
		t.Error("dst should be executable")
	}
}

func TestWriteGitignoreStub(t *testing.T) {
	dir := initTestRepo(t)

	writeGitignoreStub(dir)

	data, err := os.ReadFile(dir + "/.gitignore")
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	content := string(data)
	for _, entry := range []string{".env", ".confluencer-pending", ".confluencer/bin/"} {
		if !containsLine(content, entry) {
			t.Errorf(".gitignore should contain %q: %q", entry, content)
		}
	}

	// Run again — should not duplicate.
	writeGitignoreStub(dir)
	data2, _ := os.ReadFile(dir + "/.gitignore")
	if len(data2) != len(data) {
		t.Errorf("second run added duplicate entries: %d vs %d bytes", len(data2), len(data))
	}
}

func containsLine(content, line string) bool {
	for _, l := range splitLines(content) {
		if l == line {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
