package gitutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Move executes `git mv src dst` within repoDir. The destination
// directory is created if it does not exist. Paths are relative to repoDir.
func Move(repoDir, src, dst string) error {
	dstAbs := filepath.Join(repoDir, dst)
	if err := os.MkdirAll(filepath.Dir(dstAbs), 0o755); err != nil {
		return fmt.Errorf("mkdir for %s: %w", dst, err)
	}
	cmd := exec.Command("git", "mv", src, dst)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git mv %s %s: %s: %w", src, dst, out, err)
	}
	return nil
}

// Remove executes `git rm` for the given paths within repoDir.
// Paths are relative to repoDir. The -r flag is included so
// directories (e.g. attachment subdirectories) can be removed.
func Remove(repoDir string, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"rm", "-r", "--"}, paths...)
	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git rm %v: %s: %w", paths, out, err)
	}
	return nil
}

// Add stages the given paths within repoDir.
func Add(repoDir string, paths ...string) error {
	if len(paths) == 0 {
		return nil
	}
	args := append([]string{"add", "--"}, paths...)
	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git add %v: %s: %w", paths, out, err)
	}
	return nil
}

// Commit creates a commit with the given message in repoDir.
func Commit(repoDir, message string) error {
	cmd := exec.Command("git", "commit", "-m", message)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %s: %w", out, err)
	}
	return nil
}
