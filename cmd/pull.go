package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/swill/confluencer/api"
	cfgpkg "github.com/swill/confluencer/config"
	"github.com/swill/confluencer/gitutil"
	"github.com/swill/confluencer/index"
	"github.com/swill/confluencer/lexer"
	"github.com/swill/confluencer/tree"
)

const syncBranch = "confluencer-sync"

var pullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Fetch Confluence tree, apply typed change set locally, commit as sync",
	Long: `Fetches the full Confluence page tree, computes a typed change set
against the local index and filesystem, writes changes on a temporary branch,
and rebases the current branch onto it. This uses git's merge machinery
for safe conflict resolution.`,
	RunE: runPull,
}

func init() {
	rootCmd.AddCommand(pullCmd)
}

func runPull(cmd *cobra.Command, args []string) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	out := cmd.ErrOrStderr()

	// Acquire file lock to prevent double-fires from concurrent hooks.
	// Kept under .git/ so it is never visible to `git status` and cannot
	// make HasUncommittedChanges return a false positive during the hook.
	gitDir, err := gitutil.GitDir(root)
	if err != nil {
		return err
	}
	lockPath := filepath.Join(gitDir, "confluencer-pull.lock")
	lock, err := acquireLock(lockPath)
	if err != nil {
		if stdinIsTerminal() {
			removeStaleLock(lockPath)
			lock, err = acquireLock(lockPath)
			if err != nil {
				return fmt.Errorf("acquire pull lock: %w", err)
			}
		} else {
			return nil
		}
	}
	defer releaseLock(lock, lockPath)

	fmt.Fprintf(out, "[confluencer] fetching page tree from Confluence...\n")

	// Load config.
	cfg, err := cfgpkg.LoadConfig(filepath.Join(root, configFile))
	if err != nil {
		return err
	}

	// Load credentials.
	creds, err := cfgpkg.LoadCredentials(root)
	if err != nil {
		return err
	}

	// Load index.
	idx, err := index.Load(filepath.Join(root, indexFile))
	if err != nil {
		return fmt.Errorf("load index: %w", err)
	}

	client := api.NewClient(creds.BaseURL, creds.User, creds.APIToken)

	// Fetch the full tree (without bodies — we'll fetch individually for changed pages).
	ct, err := client.FetchTree(cfg.RootPageID, false)
	if err != nil {
		return fmt.Errorf("fetch tree: %w", err)
	}

	// Compute expected paths from the live tree.
	pm := tree.ComputePaths(ct, cfg.LocalRoot)

	// Build index entries for Diff.
	var indexEntries []tree.IndexEntry
	for _, e := range idx.Entries() {
		indexEntries = append(indexEntries, tree.IndexEntry{
			PageID:       e.PageID,
			Title:        e.Title,
			LocalPath:    e.LocalPath,
			ParentPageID: e.ParentPageID,
		})
	}

	// Resolve missing pages (in index but not in tree).
	var missingPages []tree.MissingPage
	for _, e := range idx.Entries() {
		if ct.Contains(e.PageID) {
			continue
		}
		_, err := client.GetPage(e.PageID)
		if err != nil {
			if api.IsNotFound(err) {
				missingPages = append(missingPages, tree.MissingPage{
					PageID: e.PageID, Status: tree.StatusDeleted,
				})
			} else {
				fmt.Fprintf(out, "[confluencer] WARNING: cannot determine status of page %s: %v\n", e.PageID, err)
				missingPages = append(missingPages, tree.MissingPage{
					PageID: e.PageID, Status: tree.StatusUnknown,
				})
			}
		} else {
			missingPages = append(missingPages, tree.MissingPage{
				PageID: e.PageID, Status: tree.StatusOrphaned,
			})
		}
	}

	// Compute the typed change set (structural changes only).
	changes := tree.Diff(tree.DiffInput{
		Tree:    ct,
		Paths:   pm,
		Index:   indexEntries,
		Missing: missingPages,
	})

	// Detect content changes via Confluence version comparison.
	structuralIDs := make(map[string]bool, len(changes))
	for _, c := range changes {
		structuralIDs[c.PageID] = true
	}

	pageRes := newPullPageResolver(ct, pm)

	ct.Walk(func(node *tree.CfNode) {
		if structuralIDs[node.PageID] {
			return
		}
		entry, ok := idx.ByPageID(node.PageID)
		if !ok {
			return
		}
		if entry.Version == node.Version {
			return
		}

		page, err := client.GetPage(node.PageID)
		if err != nil {
			fmt.Fprintf(out, "[confluencer] WARNING: fetch page %s for content check: %v\n", node.PageID, err)
			return
		}

		attRes := &stubAttachmentResolver{
			localPath: entry.LocalPath, attachmentsDir: cfg.AttachmentsDir, localRoot: cfg.LocalRoot,
		}
		md, err := lexer.CfToMd(page.Body, lexer.CfToMdOpts{
			Pages: pageRes, Attachments: attRes,
		})
		if err != nil {
			fmt.Fprintf(out, "[confluencer] WARNING: convert page %s: %v\n", node.PageID, err)
			return
		}

		absPath := filepath.Join(root, filepath.FromSlash(entry.LocalPath))
		localContent, err := os.ReadFile(absPath)
		if err != nil {
			return
		}

		if string(localContent) == md {
			idx.UpdateVersion(node.PageID, page.Version)
			return
		}

		changes = append(changes, tree.Change{
			Type:    tree.ContentChanged,
			PageID:  node.PageID,
			Title:   node.Title,
			OldPath: entry.LocalPath,
			NewPath: entry.LocalPath,
		})
	})

	if len(changes) == 0 {
		fmt.Fprintf(out, "[confluencer] no changes from Confluence\n")
		return nil
	}

	// Warn about orphaned and unknown pages (informational, no file changes).
	for _, c := range changes {
		switch c.Type {
		case tree.Orphaned:
			fmt.Fprintf(out, "[confluencer] WARNING: page %s (%s) is orphaned — moved outside sync scope\n", c.PageID, c.Title)
		case tree.MissingUnknown:
			fmt.Fprintf(out, "[confluencer] WARNING: page %s (%s) — status could not be determined, skipping\n", c.PageID, c.Title)
		}
	}

	// Filter to only actionable changes.
	var actionable []tree.Change
	for _, c := range changes {
		if c.Type != tree.Orphaned && c.Type != tree.MissingUnknown {
			actionable = append(actionable, c)
		}
	}
	if len(actionable) == 0 {
		fmt.Fprintf(out, "[confluencer] no changes from Confluence\n")
		return nil
	}

	fmt.Fprintf(out, "[confluencer] found %d change(s) to apply\n", len(actionable))

	// --- Branch-based pull: write Confluence changes on a temp branch, then rebase ---

	// Remember the current branch so we can return to it.
	origBranch, err := gitutil.CurrentBranch(root)
	if err != nil {
		return fmt.Errorf("get current branch: %w", err)
	}
	if origBranch == "" {
		return fmt.Errorf("cannot pull in detached HEAD state")
	}

	// Stash any uncommitted changes so we can switch branches.
	hasChanges, _ := gitutil.HasUncommittedChanges(root)
	if hasChanges {
		if err := gitutil.StashPush(root); err != nil {
			return fmt.Errorf("stash uncommitted changes: %w", err)
		}
		defer func() {
			if popErr := gitutil.StashPop(root); popErr != nil {
				fmt.Fprintf(out, "[confluencer] WARNING: stash pop failed: %v\n", popErr)
			}
		}()
	}

	// Find the base commit for the sync branch. This commit defines the
	// "last-known in-sync with Confluence" point — git's 3-way merge uses it
	// as the common ancestor when rebasing the developer's work onto the
	// sync branch, which is what surfaces genuine edit conflicts.
	//
	// Preference order:
	//   1. Last `chore(sync): confluence` commit — the canonical sync point.
	//   2. Last commit that modified `.confluencer-index.json` — the index
	//      is only rewritten by `confluencer init` and `confluencer pull`,
	//      so this always points at a committed in-sync state.
	//
	// Falling back to HEAD here is wrong: HEAD may contain local edits that
	// diverged from Confluence, which would cause the sync branch to
	// silently overwrite them during fast-forward.
	baseSHA, err := gitutil.LastSyncCommit(root)
	if err != nil || baseSHA == "" {
		baseSHA, err = gitutil.LastCommitTouching(root, indexFile)
		if err != nil {
			return fmt.Errorf("find baseline commit: %w", err)
		}
		if baseSHA == "" {
			return fmt.Errorf("no baseline commit found: commit %s (from `confluencer init`) before running pull", indexFile)
		}
	}

	// Clean up any leftover sync branch from a prior interrupted run.
	gitutil.DeleteBranch(root, syncBranch)

	// Create and switch to the temp sync branch.
	if err := gitutil.CreateBranch(root, syncBranch, baseSHA); err != nil {
		return fmt.Errorf("create sync branch: %w", err)
	}
	if err := gitutil.Checkout(root, syncBranch); err != nil {
		gitutil.DeleteBranch(root, syncBranch)
		return fmt.Errorf("checkout sync branch: %w", err)
	}

	// Ensure we always return to the original branch and clean up.
	cleanup := func() {
		gitutil.Checkout(root, origBranch)
		gitutil.DeleteBranch(root, syncBranch)
	}

	// Apply all changes on the sync branch.
	var staged []string

	// Process deletions first.
	for _, c := range actionable {
		if c.Type != tree.Deleted {
			continue
		}
		fmt.Fprintf(out, "[confluencer] delete: %s\n", c.OldPath)
		absPath := filepath.Join(root, filepath.FromSlash(c.OldPath))
		if _, err := os.Stat(absPath); err == nil {
			if err := gitutil.Remove(root, c.OldPath); err != nil {
				fmt.Fprintf(out, "[confluencer] WARNING: git rm %s: %v\n", c.OldPath, err)
			}
		}
		attDir := tree.AttachmentDir(c.OldPath, cfg.LocalRoot, cfg.AttachmentsDir)
		attAbs := filepath.Join(root, filepath.FromSlash(attDir))
		if info, err := os.Stat(attAbs); err == nil && info.IsDir() {
			gitutil.Remove(root, attDir)
		}
		idx.Remove(c.PageID)
	}

	// Plan and execute renames/moves/promotions/demotions.
	ops := tree.PlanMoves(actionable, cfg.LocalRoot)
	for _, op := range ops {
		fmt.Fprintf(out, "[confluencer] move: %s → %s\n", op.From, op.To)
		if err := gitutil.Move(root, op.From, op.To); err != nil {
			fmt.Fprintf(out, "[confluencer] WARNING: git mv %s %s: %v\n", op.From, op.To, err)
			continue
		}
		if op.Phase == tree.PhasePlace || op.Phase == tree.PhaseDirect {
			moveAttachments(root, cfg, op, actionable)
		}
	}

	// Update index entries for renames/moves.
	for _, c := range actionable {
		switch c.Type {
		case tree.RenamedInPlace, tree.Moved, tree.Promoted, tree.Demoted, tree.AncestorRenamed:
			if c.NewPath != "" {
				idx.UpdatePath(c.PageID, c.NewPath)
			}
			if c.Title != "" {
				idx.UpdateTitle(c.PageID, c.Title)
			}
			if c.NewParentPageID != "" {
				idx.UpdateParent(c.PageID, c.NewParentPageID)
			}
		}
	}

	// Process creates and content changes.
	for _, c := range actionable {
		switch c.Type {
		case tree.Created:
			page, err := client.GetPage(c.PageID)
			if err != nil {
				fmt.Fprintf(out, "[confluencer] WARNING: fetch page %s: %v\n", c.PageID, err)
				continue
			}
			attRes := &stubAttachmentResolver{
				localPath: c.NewPath, attachmentsDir: cfg.AttachmentsDir, localRoot: cfg.LocalRoot,
			}
			md, err := lexer.CfToMd(page.Body, lexer.CfToMdOpts{
				Pages: pageRes, Attachments: attRes,
			})
			if err != nil {
				fmt.Fprintf(out, "[confluencer] WARNING: convert page %s: %v\n", c.PageID, err)
				md = ""
			}
			if err := writeLocalFile(root, c.NewPath, md); err != nil {
				fmt.Fprintf(out, "[confluencer] ERROR: write %s: %v\n", c.NewPath, err)
				continue
			}
			node := ct.Page(c.PageID)
			ver := 0
			if node != nil {
				ver = page.Version
			}
			idx.Add(index.Entry{
				PageID:       c.PageID,
				Title:        c.Title,
				LocalPath:    c.NewPath,
				ParentPageID: c.NewParentPageID,
				Version:      ver,
			})
			staged = append(staged, c.NewPath)
			fmt.Fprintf(out, "[confluencer] create: %s\n", c.NewPath)

			downloadPageAttachments(client, root, cfg, c.PageID, c.NewPath, out)

		case tree.ContentChanged:
			page, err := client.GetPage(c.PageID)
			if err != nil {
				fmt.Fprintf(out, "[confluencer] WARNING: fetch page %s: %v\n", c.PageID, err)
				continue
			}
			localPath := c.NewPath
			if localPath == "" {
				localPath = c.OldPath
			}
			attRes := &stubAttachmentResolver{
				localPath: localPath, attachmentsDir: cfg.AttachmentsDir, localRoot: cfg.LocalRoot,
			}
			md, err := lexer.CfToMd(page.Body, lexer.CfToMdOpts{
				Pages: pageRes, Attachments: attRes,
			})
			if err != nil {
				fmt.Fprintf(out, "[confluencer] WARNING: convert page %s: %v\n", c.PageID, err)
				continue
			}

			if err := writeLocalFile(root, localPath, md); err != nil {
				fmt.Fprintf(out, "[confluencer] ERROR: write %s: %v\n", localPath, err)
				continue
			}
			staged = append(staged, localPath)
			fmt.Fprintf(out, "[confluencer] update: %s\n", localPath)

			if c.Title != "" {
				idx.UpdateTitle(c.PageID, c.Title)
			}
			idx.UpdateVersion(c.PageID, page.Version)
		}
	}

	// Update versions for all pages in the tree.
	ct.Walk(func(node *tree.CfNode) {
		idx.UpdateVersion(node.PageID, node.Version)
	})

	// Save updated index.
	if err := idx.Save(filepath.Join(root, indexFile)); err != nil {
		cleanup()
		return fmt.Errorf("save index: %w", err)
	}

	// Stage everything and commit on the sync branch.
	allStaged := append(staged, indexFile)
	if err := gitutil.Add(root, allStaged...); err != nil {
		cleanup()
		return fmt.Errorf("git add: %w", err)
	}
	if err := gitutil.Commit(root, gitutil.SyncPrefix); err != nil {
		if strings.Contains(err.Error(), "nothing to commit") {
			cleanup()
			fmt.Fprintf(out, "[confluencer] no changes from Confluence\n")
			return nil
		}
		cleanup()
		return fmt.Errorf("sync commit: %w", err)
	}

	// Switch back to the original branch and rebase onto the sync branch.
	if err := gitutil.Checkout(root, origBranch); err != nil {
		cleanup()
		return fmt.Errorf("checkout original branch: %w", err)
	}

	if err := gitutil.Rebase(root, syncBranch); err != nil {
		// Rebase failed — likely a conflict. Abort the rebase and fall back
		// to a direct merge so the developer can resolve conflicts manually.
		fmt.Fprintf(out, "[confluencer] WARNING: rebase failed, attempting merge...\n")
		gitutil.RebaseAbort(root)

		// Fall back: cherry-pick the sync commit's changes directly.
		// The sync branch has exactly one commit on top of baseSHA.
		// Merge it instead.
		mergeErr := gitMerge(root, syncBranch)
		if mergeErr != nil {
			fmt.Fprintf(out, "[confluencer] CONFLICT: merge has conflicts. Resolve with 'git merge --continue' after fixing.\n")
			gitutil.DeleteBranch(root, syncBranch)
			return nil
		}
	}

	// Clean up the sync branch.
	gitutil.DeleteBranch(root, syncBranch)

	fmt.Fprintf(out, "[confluencer] done — %d change(s) synced from Confluence\n", len(actionable))
	return nil
}

// gitMerge performs a git merge of the given branch into the current branch.
func gitMerge(repoDir, branch string) error {
	cmd := gitCommand(repoDir, "merge", branch, "-m", gitutil.SyncPrefix)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git merge %s: %s: %w", branch, out, err)
	}
	return nil
}

// gitCommand creates an exec.Cmd for a git operation.
func gitCommand(repoDir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Dir = repoDir
	return cmd
}

// writeLocalFile writes content to a repo-relative path, creating directories.
func writeLocalFile(root, repoPath, content string) error {
	absPath := filepath.Join(root, filepath.FromSlash(repoPath))
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(absPath, []byte(content), 0o644)
}

// moveAttachments moves the attachment directory when a page is renamed.
func moveAttachments(root string, cfg *cfgpkg.Config, op tree.MoveOp, changes []tree.Change) {
	for _, c := range changes {
		if c.PageID != op.PageID {
			continue
		}
		oldAttDir := tree.AttachmentDir(c.OldPath, cfg.LocalRoot, cfg.AttachmentsDir)
		newAttDir := tree.AttachmentDir(c.NewPath, cfg.LocalRoot, cfg.AttachmentsDir)
		if oldAttDir == newAttDir {
			return
		}
		absOld := filepath.Join(root, filepath.FromSlash(oldAttDir))
		if _, err := os.Stat(absOld); err != nil {
			return
		}
		gitutil.Move(root, oldAttDir, newAttDir)
		return
	}
}

// downloadPageAttachments downloads all attachments for a page.
func downloadPageAttachments(client *api.Client, root string, cfg *cfgpkg.Config, pageID, localPath string, out io.Writer) {
	atts, err := client.GetAttachments(pageID, "")
	if err != nil {
		fmt.Fprintf(out, "[confluencer] WARNING: list attachments for %s: %v\n", pageID, err)
		return
	}
	attDir := tree.AttachmentDir(localPath, cfg.LocalRoot, cfg.AttachmentsDir)
	for _, att := range atts {
		data, err := client.DownloadAttachment(att.DownloadPath)
		if err != nil {
			fmt.Fprintf(out, "[confluencer] WARNING: download %s: %v\n", att.Filename, err)
			continue
		}
		attPath := filepath.Join(root, filepath.FromSlash(attDir), att.Filename)
		os.MkdirAll(filepath.Dir(attPath), 0o755)
		os.WriteFile(attPath, data, 0o644)
	}
}

// acquireLock creates a lock file exclusively. Returns an error if the lock is held.
func acquireLock(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
}

// releaseLock closes and removes the lock file.
func releaseLock(f *os.File, path string) {
	f.Close()
	os.Remove(path)
}

// removeStaleLock removes a lock file left behind by a crashed process.
func removeStaleLock(path string) {
	os.Remove(path)
}

// pullPageResolver resolves cross-page links during pull.
type pullPageResolver struct {
	tree  *tree.CfTree
	paths *tree.PathMap
}

func newPullPageResolver(ct *tree.CfTree, pm *tree.PathMap) *pullPageResolver {
	return &pullPageResolver{tree: ct, paths: pm}
}

func (r *pullPageResolver) ResolvePageByTitle(title, spaceKey string) (localPath string, ok bool) {
	var found *tree.CfNode
	r.tree.Walk(func(n *tree.CfNode) {
		if found != nil {
			return
		}
		if n.Title == title {
			found = n
		}
	})
	if found == nil {
		return "", false
	}
	return r.paths.Path(found.PageID)
}
