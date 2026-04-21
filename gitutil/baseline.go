package gitutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FindSyncCommit locates the most recent sync commit that modified filePath.
// Returns the commit SHA, or empty string if no sync commit has touched the file.
// filePath is relative to the repository root.
func FindSyncCommit(repoDir, filePath string) (string, error) {
	cmd := exec.Command("git", "log",
		"--follow",
		"--grep=^"+SyncPrefix,
		"--format=%H",
		"-1",
		"--", filePath)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		// git log exits 0 even with no matches; a real error is unusual.
		return "", fmt.Errorf("git log for sync commit of %s: %w", filePath, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// ShowFile returns the content of a file at a specific commit.
// Both commitSHA and filePath are required. filePath is relative to the repo root.
func ShowFile(repoDir, commitSHA, filePath string) (string, error) {
	cmd := exec.Command("git", "show", commitSHA+":"+filePath)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git show %s:%s: %w", commitSHA, filePath, err)
	}
	return string(out), nil
}

// Baseline returns the content of filePath at the most recent sync commit.
// If no sync commit has ever touched the file, returns an empty string and nil error.
func Baseline(repoDir, filePath string) (string, error) {
	sha, err := FindSyncCommit(repoDir, filePath)
	if err != nil {
		return "", err
	}
	if sha == "" {
		return "", nil
	}
	return ShowFile(repoDir, sha, filePath)
}

// MergeFile performs a three-way merge using `git merge-file --stdout`.
// It does not require a git repository — it operates on temporary files.
//
// Returns the merged content and whether conflict markers are present.
// On conflict, the merged content contains standard conflict markers and
// the caller decides how to proceed.
func MergeFile(oursContent, baseContent, theirsContent string) (string, bool, error) {
	dir, err := os.MkdirTemp("", "confluencer-merge-*")
	if err != nil {
		return "", false, err
	}
	defer os.RemoveAll(dir)

	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(content), 0o644)
		return p
	}

	oursFile := write("ours", oursContent)
	baseFile := write("base", baseContent)
	theirsFile := write("theirs", theirsContent)

	cmd := exec.Command("git", "merge-file", "--stdout", "--no-diff3",
		oursFile, baseFile, theirsFile)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() > 0 {
			// Positive exit code = number of conflicts. Output still has merged content.
			return string(out), true, nil
		}
		return "", false, fmt.Errorf("git merge-file: %w", err)
	}
	return string(out), false, nil
}
