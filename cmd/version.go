package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the confluencer version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Fprintf(cmd.OutOrStdout(), "confluencer %s (commit %s, built %s)\n", version, commit, buildDate)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
