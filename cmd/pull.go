package cmd

import (
	"fmt"
	"io"
	"os"
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

var pullCmd = &cobra.Command{
	Use:   "pull",
	Short: "Fetch Confluence tree, apply typed change set locally, commit as sync",
	Long: `Invoked by the post-merge and post-rewrite Git hooks. Fetches the
full Confluence page tree, computes a typed change set against the local
index and filesystem, applies renames/moves/promotions/deletes/content
updates, downloads attachments, and creates a sync commit.`,
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

	// Acquire file lock to prevent double-fires from post-merge + post-rewrite.
	lockPath := filepath.Join(root, ".confluencer", ".pull.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	lock, err := acquireLock(lockPath)
	if err != nil {
		if stdinIsTerminal() {
			// Direct invocation — stale lock is likely from a crashed run.
			removeStaleLock(lockPath)
			lock, err = acquireLock(lockPath)
			if err != nil {
				return fmt.Errorf("acquire pull lock: %w", err)
			}
		} else {
			// Hook invocation — another hook is already running pull.
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
		// Disambiguate via direct GET.
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
			// Page exists but is outside our tree — orphaned.
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
	// Pages with structural changes are already handled; for the rest,
	// a version mismatch means Confluence content may have changed.
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

		// Version mismatch — fetch body and compare against local file.
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
			// Content matches despite version bump (e.g. metadata-only change).
			// Update version in index to avoid re-checking next time.
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
		// Save index even when no content changes — version updates may have occurred.
		if err := idx.Save(filepath.Join(root, indexFile)); err != nil {
			return fmt.Errorf("save index: %w", err)
		}
		fmt.Fprintf(out, "[confluencer] no changes from Confluence\n")
		return nil
	}

	fmt.Fprintf(out, "[confluencer] found %d change(s) to apply\n", len(changes))
	var staged []string

	// Process deletions first.
	for _, c := range changes {
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
		// Remove attachment directory if present.
		attDir := tree.AttachmentDir(c.OldPath, cfg.LocalRoot, cfg.AttachmentsDir)
		attAbs := filepath.Join(root, filepath.FromSlash(attDir))
		if info, err := os.Stat(attAbs); err == nil && info.IsDir() {
			gitutil.Remove(root, attDir)
		}
		idx.Remove(c.PageID)
	}

	// Plan and execute renames/moves/promotions/demotions.
	ops := tree.PlanMoves(changes, cfg.LocalRoot)
	for _, op := range ops {
		fmt.Fprintf(out, "[confluencer] move: %s → %s\n", op.From, op.To)
		if err := gitutil.Move(root, op.From, op.To); err != nil {
			fmt.Fprintf(out, "[confluencer] WARNING: git mv %s %s: %v\n", op.From, op.To, err)
			continue
		}
		// Move attachment directory for place/direct ops (the final move).
		if op.Phase == tree.PhasePlace || op.Phase == tree.PhaseDirect {
			moveAttachments(root, cfg, op, changes)
		}
	}

	// Update index entries for renames/moves.
	for _, c := range changes {
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
	for _, c := range changes {
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

			// Download attachments for new page.
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

			// Check for local uncommitted changes and three-way merge if needed.
			absPath := filepath.Join(root, filepath.FromSlash(localPath))
			localContent, _ := os.ReadFile(absPath)
			if string(localContent) != md {
				// Check for uncommitted local modifications.
				baseline, _ := gitutil.Baseline(root, localPath)
				if string(localContent) != baseline && baseline != "" {
					// Three-way merge.
					merged, conflict, mergeErr := gitutil.MergeFile(string(localContent), baseline, md)
					if mergeErr != nil {
						fmt.Fprintf(out, "[confluencer] WARNING: merge %s: %v\n", localPath, mergeErr)
						continue
					}
					md = merged
					if conflict {
						fmt.Fprintf(out, "[confluencer] CONFLICT: %s has conflicting changes. Resolve before pushing.\n", localPath)
					}
				}

				if err := writeLocalFile(root, localPath, md); err != nil {
					fmt.Fprintf(out, "[confluencer] ERROR: write %s: %v\n", localPath, err)
					continue
				}
				staged = append(staged, localPath)
				fmt.Fprintf(out, "[confluencer] update: %s\n", localPath)
			}

			// Update index metadata.
			if c.Title != "" {
				idx.UpdateTitle(c.PageID, c.Title)
			}
			idx.UpdateVersion(c.PageID, page.Version)
		}
	}

	// Update versions in the index for all unchanged pages in the tree.
	ct.Walk(func(node *tree.CfNode) {
		idx.UpdateVersion(node.PageID, node.Version)
	})

	// Warn about orphaned and unknown pages.
	for _, c := range changes {
		switch c.Type {
		case tree.Orphaned:
			fmt.Fprintf(out, "[confluencer] WARNING: page %s (%s) is orphaned — moved outside sync scope\n", c.PageID, c.Title)
		case tree.MissingUnknown:
			fmt.Fprintf(out, "[confluencer] WARNING: page %s (%s) — status could not be determined, skipping\n", c.PageID, c.Title)
		}
	}

	// Save updated index.
	if err := idx.Save(filepath.Join(root, indexFile)); err != nil {
		return fmt.Errorf("save index: %w", err)
	}

	// Stage everything and commit.
	allStaged := append(staged, indexFile)
	if err := gitutil.Add(root, allStaged...); err != nil {
		return fmt.Errorf("git add: %w", err)
	}
	if err := gitutil.Commit(root, gitutil.SyncPrefix); err != nil {
		// Commit may fail if there are no actual changes staged.
		// This is not an error — it means all changes were no-ops.
		if !strings.Contains(err.Error(), "nothing to commit") {
			return fmt.Errorf("sync commit: %w", err)
		}
	}

	fmt.Fprintf(out, "[confluencer] done — %d change(s) synced from Confluence\n", len(changes))
	return nil
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
	// Find the corresponding change to get old and new paths.
	for _, c := range changes {
		if c.PageID != op.PageID {
			continue
		}
		oldAttDir := tree.AttachmentDir(c.OldPath, cfg.LocalRoot, cfg.AttachmentsDir)
		newAttDir := tree.AttachmentDir(c.NewPath, cfg.LocalRoot, cfg.AttachmentsDir)
		if oldAttDir == newAttDir {
			return // No attachment move needed (e.g. promotion).
		}
		absOld := filepath.Join(root, filepath.FromSlash(oldAttDir))
		if _, err := os.Stat(absOld); err != nil {
			return // No attachment directory exists.
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
