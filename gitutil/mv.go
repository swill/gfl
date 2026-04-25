package gitutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Move executes `git mv src dst` within repoDir. Creates the destination
// directory if it does not exist. Paths are relative to repoDir.
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

// Remove executes `git rm -r` for the given paths within repoDir.
// The -r is included so attachment subdirectories can be removed.
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

// CurrentBranch returns the name of the current branch, or empty string if
// HEAD is detached.
func CurrentBranch(repoDir string) (string, error) {
	cmd := exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return "", nil // detached HEAD
	}
	return strings.TrimSpace(string(out)), nil
}

// Checkout switches to the given branch or ref.
func Checkout(repoDir, ref string) error {
	cmd := exec.Command("git", "checkout", ref)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout %s: %s: %w", ref, out, err)
	}
	return nil
}

// StashPush stashes the current working tree changes (tracked + untracked)
// under a confluencer-named stash entry.
func StashPush(repoDir string) error {
	cmd := exec.Command("git", "stash", "push", "--include-untracked", "-m", "confluencer-stash")
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git stash push: %s: %w", out, err)
	}
	return nil
}

// StashPop pops the most recent stash entry.
func StashPop(repoDir string) error {
	cmd := exec.Command("git", "stash", "pop")
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git stash pop: %s: %w", out, err)
	}
	return nil
}
