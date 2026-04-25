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

// configFile is the name of the tracked confluencer configuration file.
const configFile = ".confluencer.json"
