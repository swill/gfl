package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/swill/confluencer/index"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report pending writes, orphaned pages, and pending deletions",
	RunE:  runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	// Load pending queue.
	pendingPath := filepath.Join(root, pendingFile)
	entries, err := index.LoadPending(pendingPath)
	if err != nil {
		return fmt.Errorf("load pending queue: %w", err)
	}

	if len(entries) == 0 {
		fmt.Fprintln(out, "No pending Confluence writes.")
	} else {
		fmt.Fprintf(out, "%d pending Confluence write(s):\n", len(entries))
		for _, e := range entries {
			path := e.LocalPath
			if path == "" {
				path = e.PageID
			}
			fmt.Fprintf(out, "  %-12s %-40s attempt %d: %s\n",
				e.Type, path, e.Attempt, e.LastError)
		}
	}

	return nil
}
