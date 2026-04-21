// Package gitutil wraps Git plumbing operations needed by the confluencer
// sync workflow: commit range walking, diff parsing, rename execution, and
// sync-baseline lookup. All functions accept a repoDir parameter so they
// operate on any repository, not just the working directory.
package gitutil

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// SyncPrefix is the commit message prefix that identifies sync commits
// produced by confluencer pull.
const SyncPrefix = "chore(sync): confluence"

// zeroSHA is the all-zeros SHA that Git uses to indicate a nonexistent ref
// (e.g. a new branch being pushed for the first time).
const zeroSHA = "0000000000000000000000000000000000000000"

// PushRef represents one ref-pair from pre-push hook stdin.
type PushRef struct {
	LocalRef  string
	LocalSHA  string
	RemoteRef string
	RemoteSHA string
}

// ParsePushRefs parses the stdin lines provided to a pre-push hook.
// Each line has the format: <local-ref> <local-sha> <remote-ref> <remote-sha>.
// Blank lines and lines with fewer than four fields are skipped.
func ParsePushRefs(r io.Reader) ([]PushRef, error) {
	var refs []PushRef
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 4 {
			continue
		}
		refs = append(refs, PushRef{
			LocalRef:  parts[0],
			LocalSHA:  parts[1],
			RemoteRef: parts[2],
			RemoteSHA: parts[3],
		})
	}
	return refs, scanner.Err()
}

// IsSyncCommit returns true if the commit message starts with the sync prefix.
func IsSyncCommit(message string) bool {
	return strings.HasPrefix(message, SyncPrefix)
}

// IsNewBranch returns true if the remote SHA is all zeros, indicating this
// is the first push for this branch.
func IsNewBranch(ref PushRef) bool {
	return ref.RemoteSHA == zeroSHA
}

// IsDeleteBranch returns true if the local SHA is all zeros, indicating
// the branch is being deleted.
func IsDeleteBranch(ref PushRef) bool {
	return ref.LocalSHA == zeroSHA
}

// ListCommits returns commit SHAs in the range base..head, oldest first.
// If baseSHA is the zero SHA or empty, all ancestors of headSHA are returned.
func ListCommits(repoDir, baseSHA, headSHA string) ([]string, error) {
	rangeSpec := headSHA
	if baseSHA != "" && baseSHA != zeroSHA {
		rangeSpec = baseSHA + ".." + headSHA
	}
	cmd := exec.Command("git", "log", "--format=%H", "--reverse", rangeSpec)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git log %s: %w", rangeSpec, err)
	}
	return splitLines(string(out)), nil
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

// LastSyncCommit returns the SHA of the most recent sync commit on the current
// branch, or empty string if none exists.
func LastSyncCommit(repoDir string) (string, error) {
	cmd := exec.Command("git", "log",
		"--grep=^"+SyncPrefix,
		"--format=%H",
		"-1")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git log for last sync commit: %w", err)
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

// splitLines splits on newlines, discarding empty trailing entries.
func splitLines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
