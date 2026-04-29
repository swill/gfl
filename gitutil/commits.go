// Package gitutil wraps Git plumbing operations needed by the gfl
// sync workflow: branch management, content reads at refs, diff parsing,
// rename execution, and merge-state observation. All functions accept a
// repoDir parameter so they operate on any repository, not just the working
// directory.
package gitutil

import (
	"fmt"
	"os/exec"
	"strings"
)

// SyncPrefix is the commit message prefix that identifies sync commits
// produced by gfl (pull and push both use it). Pull adds " @ <ts>";
// push adds "-push @ <ts>". Anything starting with this prefix is a
// machine-generated sync commit.
const SyncPrefix = "chore(sync): confluence"

// IsSyncCommit returns true if the commit message starts with the sync prefix.
func IsSyncCommit(message string) bool {
	return strings.HasPrefix(message, SyncPrefix)
}

// CommitMessage returns the full commit message for a single commit.
func CommitMessage(repoDir, commitSHA string) (string, error) {
	cmd := exec.Command("git", "log", "-1", "--format=%B", commitSHA)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git log -1 %s: %w", commitSHA, err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// GitDir returns the absolute path to the .git directory for repoDir.
// For a normal repository this is <repoDir>/.git; for worktrees and
// submodules it resolves to the correct storage location.
func GitDir(repoDir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--absolute-git-dir")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --absolute-git-dir: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// HeadSHA returns the SHA of the current HEAD commit.
func HeadSHA(repoDir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
