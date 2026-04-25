package gitutil

import (
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

// FileDiff represents a single file change in a commit or diff range.
type FileDiff struct {
	Action  FileAction
	Path    string // current path (new path for renames)
	OldPath string // previous path (renames only)
}

// parseDiffOutput parses the output of `git diff --name-status` (with -M for
// rename detection). The format is one of:
//
//	A\t<path>
//	M\t<path>
//	D\t<path>
//	R<percent>\t<old-path>\t<new-path>
//
// Lines that don't match a known action are skipped silently.
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
