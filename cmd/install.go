package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// hookNames lists the Git hooks that gfl manages.
var hookNames = []string{"pre-push", "post-commit", "post-merge", "post-rewrite"}

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Copy Git hook shims into .git/hooks/",
	Long: `Copies the pre-push, post-commit, post-merge, and post-rewrite shims from
.gfl/hooks/ into .git/hooks/ and marks them executable. The shims
resolve the binary path at runtime, so subsequent upgrades do not require
re-installation. Idempotent.`,
	RunE: runInstall,
}

func init() {
	rootCmd.AddCommand(installCmd)
}

func runInstall(cmd *cobra.Command, args []string) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	return installHooks(root, cmd.OutOrStdout())
}

// installHooks copies hook shims from .gfl/hooks/ into .git/hooks/.
func installHooks(root string, out io.Writer) error {
	srcDir := filepath.Join(root, ".gfl", "hooks")
	dstDir := filepath.Join(root, ".git", "hooks")

	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("create .git/hooks: %w", err)
	}

	for _, name := range hookNames {
		src := filepath.Join(srcDir, name)
		dst := filepath.Join(dstDir, name)

		if err := copyFile(src, dst, 0o755); err != nil {
			return fmt.Errorf("install %s hook: %w", name, err)
		}
		fmt.Fprintf(out, "installed %s → .git/hooks/%s\n", name, name)
	}

	return nil
}

// copyFile copies src to dst with the given permissions.
func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Chmod(perm)
}
