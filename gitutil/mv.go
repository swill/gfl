package gitutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// CurrentBranch returns the name of the current branch, or empty string if detached.
func CurrentBranch(repoDir string) (string, error) {
	cmd := exec.Command("git", "symbolic-ref", "--short", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return "", nil // detached HEAD
	}
	return strings.TrimSpace(string(out)), nil
}

// CreateBranch creates a new branch at the given start point.
func CreateBranch(repoDir, branchName, startPoint string) error {
	cmd := exec.Command("git", "branch", branchName, startPoint)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git branch %s %s: %s: %w", branchName, startPoint, out, err)
	}
	return nil
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

// DeleteBranch deletes a local branch.
func DeleteBranch(repoDir, branchName string) error {
	cmd := exec.Command("git", "branch", "-D", branchName)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git branch -D %s: %s: %w", branchName, out, err)
	}
	return nil
}

// Rebase rebases the current branch onto the given upstream ref.
func Rebase(repoDir, onto string) error {
	cmd := exec.Command("git", "rebase", onto)
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git rebase %s: %s: %w", onto, out, err)
	}
	return nil
}

// RebaseAbort aborts an in-progress rebase.
func RebaseAbort(repoDir string) error {
	cmd := exec.Command("git", "rebase", "--abort")
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git rebase --abort: %s: %w", out, err)
	}
	return nil
}

// StashPush stashes the current working tree changes.
func StashPush(repoDir string) error {
	cmd := exec.Command("git", "stash", "push", "-m", "confluencer-pull-stash")
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

// HasUncommittedChanges returns true if the working tree has staged or unstaged changes.
func HasUncommittedChanges(repoDir string) (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}
