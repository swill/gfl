package gitutil

import (
	"fmt"
	"os/exec"
	"strings"
)

// FileAction represents the type of change to a file in a diff.
type FileAction string

const (
	ActionAdded    FileAction = "A"
	ActionModified FileAction = "M"
	ActionDeleted  FileAction = "D"
	ActionRenamed  FileAction = "R"
)

// FileDiff represents a single file change in a commit or range.
type FileDiff struct {
	Action  FileAction
	Path    string // current path (new path for renames)
	OldPath string // previous path (renames only)
}

// emptyTree is the well-known SHA of an empty tree object in Git.
// Used as the base when diffing from nothing (e.g. new branch).
const emptyTree = "4b825dc642cb6eb9a060e54bf899d15363d7aa7d"

// DiffRange returns file changes between two commits with rename detection.
// If baseSHA is empty or the zero SHA, diffs against the empty tree.
func DiffRange(repoDir, baseSHA, headSHA string) ([]FileDiff, error) {
	base := baseSHA
	if base == "" || base == zeroSHA {
		base = emptyTree
	}
	cmd := exec.Command("git", "diff", "--name-status", "-M", base+".."+headSHA)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff %s..%s: %w", base, headSHA, err)
	}
	return parseDiffOutput(string(out))
}

// DiffCommit returns file changes for a single commit with rename detection.
// For the root commit (no parent), uses --root to diff against the empty tree.
func DiffCommit(repoDir, commitSHA string) ([]FileDiff, error) {
	cmd := exec.Command("git", "diff-tree", "--no-commit-id", "-r",
		"--name-status", "-M", "--root", commitSHA)
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff-tree %s: %w", commitSHA, err)
	}
	return parseDiffOutput(string(out))
}

// parseDiffOutput parses git diff --name-status output.
// Format: ACTION\tPATH or ACTION\tOLD_PATH\tNEW_PATH (for renames).
// The action for renames includes a similarity percentage (e.g. "R100").
func parseDiffOutput(output string) ([]FileDiff, error) {
	var diffs []FileDiff
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		action := parts[0]

		switch {
		case action == "A":
			diffs = append(diffs, FileDiff{Action: ActionAdded, Path: parts[1]})
		case action == "M":
			diffs = append(diffs, FileDiff{Action: ActionModified, Path: parts[1]})
		case action == "D":
			diffs = append(diffs, FileDiff{Action: ActionDeleted, Path: parts[1]})
		case strings.HasPrefix(action, "R"):
			if len(parts) < 3 {
				continue
			}
			diffs = append(diffs, FileDiff{
				Action:  ActionRenamed,
				OldPath: parts[1],
				Path:    parts[2],
			})
		}
	}
	return diffs, nil
}

// FilterMd returns only diffs where the relevant path ends in ".md".
// For renames, either old or new path ending in .md qualifies.
func FilterMd(diffs []FileDiff) []FileDiff {
	var out []FileDiff
	for _, d := range diffs {
		if strings.HasSuffix(d.Path, ".md") {
			out = append(out, d)
		} else if d.Action == ActionRenamed && strings.HasSuffix(d.OldPath, ".md") {
			out = append(out, d)
		}
	}
	return out
}
