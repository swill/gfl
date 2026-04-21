package cmd

import (
	"fmt"
	"os/exec"
	"strings"
)

// repoRoot returns the Git repository root for the current working directory.
func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not inside a Git repository: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// configPath returns the path to .confluencer.json relative to the repo root.
const configFile = ".confluencer.json"

// indexFile is the name of the page-to-path index file.
const indexFile = ".confluencer-index.json"

// pendingFile is the name of the pending queue file.
const pendingFile = ".confluencer-pending"
