package cmd

import (
	"github.com/spf13/cobra"
)

// Version information populated at build time via -ldflags.
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "gfl",
	Short: "Deterministic bidirectional sync between Markdown and Confluence",
	Long: `gfl synchronises Markdown files in a Git repository with
pages in an Atlassian Confluence instance. It operates through Git hooks
and the Confluence REST API, with no external runtime dependencies.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the CLI.
func Execute() error {
	return rootCmd.Execute()
}

// SetVersionInfo allows main or tests to inject build-time version info.
func SetVersionInfo(v, c, d string) {
	version = v
	commit = c
	buildDate = d
	rootCmd.Version = v
}
