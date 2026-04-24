package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/swill/confluencer/api"
	cfgpkg "github.com/swill/confluencer/config"
	"github.com/swill/confluencer/gitutil"
	"github.com/swill/confluencer/index"
	"github.com/swill/confluencer/lexer"
)

var pushRetry bool

var pushCmd = &cobra.Command{
	Use:   "push",
	Short: "Write changed, added, renamed, and deleted Markdown files to Confluence",
	Long: `Push local Markdown changes to Confluence. When invoked by the pre-push
Git hook, parses stdin to identify the commit range. When run directly, scans
commits since the last sync for changes. In both cases, drains the pending
queue first, then processes deletions, renames, creates, and content updates.`,
	RunE: runPush,
}

func init() {
	pushCmd.Flags().BoolVar(&pushRetry, "retry", false, "Drain .confluencer-pending without requiring a Git push")
	rootCmd.AddCommand(pushCmd)
}

func runPush(cmd *cobra.Command, args []string) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	out := cmd.ErrOrStderr()

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

	client := api.NewClient(creds.BaseURL, creds.User, creds.APIToken)

	// Load index.
	idx, err := index.Load(filepath.Join(root, indexFile))
	if err != nil {
		return fmt.Errorf("load index: %w", err)
	}

	pendingPath := filepath.Join(root, pendingFile)

	// Drain pending queue first.
	if err := drainPending(client, idx, pendingPath, cfg, root, out); err != nil {
		fmt.Fprintf(out, "[confluencer] WARNING: drain pending: %v\n", err)
	}

	if pushRetry {
		fmt.Fprintf(out, "[confluencer] done (retry only)\n")
		return nil
	}

	// Determine commit ranges to scan for .md changes.
	type commitRange struct{ base, head string }
	var ranges []commitRange

	if stdinIsTerminal() {
		// Direct invocation — compute range from last sync commit to HEAD.
		fmt.Fprintf(out, "[confluencer] scanning for changes since last sync...\n")
		head, err := gitutil.HeadSHA(root)
		if err != nil {
			return fmt.Errorf("get HEAD: %w", err)
		}
		base, err := gitutil.LastSyncCommit(root)
		if err != nil {
			fmt.Fprintf(out, "[confluencer] WARNING: find last sync commit: %v\n", err)
			base = ""
		}
		if base == head {
			fmt.Fprintf(out, "[confluencer] no changes since last sync\n")
			return nil
		}
		ranges = append(ranges, commitRange{base: base, head: head})
	} else {
		// Pre-push hook — parse refs from stdin.
		refs, err := gitutil.ParsePushRefs(os.Stdin)
		if err != nil {
			return fmt.Errorf("parse push refs: %w", err)
		}
		for _, ref := range refs {
			if gitutil.IsDeleteBranch(ref) {
				continue
			}
			ranges = append(ranges, commitRange{base: ref.RemoteSHA, head: ref.LocalSHA})
		}
	}

	totalProcessed := 0
	for _, r := range ranges {
		diffs, err := gitutil.DiffRange(root, r.base, r.head)
		if err != nil {
			fmt.Fprintf(out, "[confluencer] WARNING: diff range: %v\n", err)
			continue
		}
		mdDiffs := gitutil.FilterMd(diffs)
		mdDiffs = filterNonSyncDiffs(root, mdDiffs, r.base, r.head)

		if len(mdDiffs) == 0 {
			continue
		}

		// Sort so index.md files are processed before siblings — ensures parent
		// pages exist in the index before child pages look them up.
		sort.Slice(mdDiffs, func(i, j int) bool {
			iIdx := strings.HasSuffix(mdDiffs[i].Path, "/index.md")
			jIdx := strings.HasSuffix(mdDiffs[j].Path, "/index.md")
			if iIdx != jIdx {
				return iIdx
			}
			return mdDiffs[i].Path < mdDiffs[j].Path
		})

		fmt.Fprintf(out, "[confluencer] found %d .md file(s) to push\n", len(mdDiffs))

		for _, d := range mdDiffs {
			switch d.Action {
			case gitutil.ActionDeleted:
				pushDelete(client, idx, d, pendingPath, root, out)

			case gitutil.ActionRenamed:
				pushRename(client, idx, cfg, d, pendingPath, root, out)

			case gitutil.ActionAdded:
				if _, ok := idx.ByPath(d.Path); ok {
					pushUpdate(client, idx, cfg, d, pendingPath, root, out)
				} else {
					pushCreate(client, idx, cfg, d, pendingPath, root, out)
				}

			case gitutil.ActionModified:
				pushUpdate(client, idx, cfg, d, pendingPath, root, out)
			}
			totalProcessed++
		}
	}

	if totalProcessed == 0 {
		fmt.Fprintf(out, "[confluencer] no changes to push\n")
	} else {
		fmt.Fprintf(out, "[confluencer] done — %d file(s) processed\n", totalProcessed)
	}

	return nil
}

// stdinIsTerminal returns true if stdin is connected to a terminal (interactive),
// as opposed to a pipe (e.g. from a Git hook).
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// filterNonSyncDiffs removes diffs where the file's most recent modifying
// commit is a sync commit (these were already pushed to Confluence).
func filterNonSyncDiffs(root string, diffs []gitutil.FileDiff, baseSHA, headSHA string) []gitutil.FileDiff {
	commits, err := gitutil.ListCommits(root, baseSHA, headSHA)
	if err != nil || len(commits) == 0 {
		return diffs
	}

	// For each diff, check if the most recent commit touching that file is a sync commit.
	var out []gitutil.FileDiff
	for _, d := range diffs {
		path := d.Path
		if d.Action == gitutil.ActionDeleted {
			// For deletes, the file doesn't exist at HEAD; still process.
			out = append(out, d)
			continue
		}
		// Find the most recent commit in the range that touched this file.
		lastCommit := findLastCommitForFile(root, commits, path)
		if lastCommit == "" {
			out = append(out, d)
			continue
		}
		msg, err := gitutil.CommitMessage(root, lastCommit)
		if err != nil || !gitutil.IsSyncCommit(msg) {
			out = append(out, d)
		}
	}
	return out
}

// findLastCommitForFile returns the most recent commit in the list that modified path.
func findLastCommitForFile(root string, commits []string, path string) string {
	// Walk commits in reverse (most recent first).
	for i := len(commits) - 1; i >= 0; i-- {
		diffs, err := gitutil.DiffCommit(root, commits[i])
		if err != nil {
			continue
		}
		for _, d := range diffs {
			if d.Path == path || d.OldPath == path {
				return commits[i]
			}
		}
	}
	return ""
}

// pushDelete handles a deleted .md file.
func pushDelete(client *api.Client, idx *index.Index, d gitutil.FileDiff, pendingPath, root string, out io.Writer) {
	entry, ok := idx.ByPath(d.Path)
	if !ok {
		return // Not tracked.
	}

	fmt.Fprintf(out, "[confluencer] delete: page %s (%s)\n", entry.PageID, d.Path)
	if err := client.DeletePage(entry.PageID); err != nil {
		if !api.IsNotFound(err) {
			fmt.Fprintf(out, "[confluencer] WARNING: delete page %s: %v — queued\n", entry.PageID, err)
			queuePending(pendingPath, index.PendingEntry{
				Type: index.PendingDelete, PageID: entry.PageID, LocalPath: d.Path,
				Attempt: 1, LastError: err.Error(), QueuedAt: time.Now().UTC(),
			})
			return
		}
		// Already deleted — proceed with index cleanup.
	}
	idx.Remove(entry.PageID)
}

// pushRename handles a renamed .md file.
func pushRename(client *api.Client, idx *index.Index, cfg *cfgpkg.Config, d gitutil.FileDiff, pendingPath, root string, out io.Writer) {
	entry, ok := idx.ByPath(d.OldPath)
	if !ok {
		// Not tracked under old path — treat as a create.
		pushCreate(client, idx, cfg, gitutil.FileDiff{
			Action: gitutil.ActionAdded, Path: d.Path,
		}, pendingPath, root, out)
		return
	}

	// Apply Title Stability Rule.
	newSlug := strings.TrimSuffix(filepath.Base(d.Path), ".md")
	var newTitle string
	if !lexer.TitleSlugsMatch(entry.Title, newSlug) {
		newTitle = lexer.ReverseSlugify(filepath.Base(d.Path))
	} else {
		newTitle = entry.Title // Preserve existing title.
	}

	// Fetch current page for version number.
	page, err := client.GetPage(entry.PageID)
	if err != nil {
		fmt.Fprintf(out, "[confluencer] WARNING: fetch page %s for rename: %v — queued\n", entry.PageID, err)
		queuePending(pendingPath, index.PendingEntry{
			Type: index.PendingRename, PageID: entry.PageID,
			OldPath: d.OldPath, NewPath: d.Path, NewTitle: newTitle,
			Attempt: 1, LastError: err.Error(), QueuedAt: time.Now().UTC(),
		})
		return
	}

	// Determine new parent from directory structure.
	newParentID := ensureParentPages(client, idx, cfg, d.Path, root, out)

	// Read local content for the renamed file.
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(d.Path)))
	if err != nil {
		fmt.Fprintf(out, "[confluencer] WARNING: read %s: %v\n", d.Path, err)
		return
	}
	storageXML, err := lexer.MdToCf(string(content), lexer.MdToCfOpts{})
	if err != nil {
		fmt.Fprintf(out, "[confluencer] WARNING: convert %s: %v\n", d.Path, err)
		storageXML = page.Body // Fall back to existing body.
	}

	if err := client.UpdatePage(entry.PageID, page.Version+1, newTitle, storageXML, newParentID); err != nil {
		if api.IsConflict(err) {
			fmt.Fprintf(out, "[confluencer] WARNING: version conflict on %s — queued for retry\n", d.Path)
		} else {
			fmt.Fprintf(out, "[confluencer] WARNING: update page %s: %v — queued\n", entry.PageID, err)
		}
		queuePending(pendingPath, index.PendingEntry{
			Type: index.PendingRename, PageID: entry.PageID,
			OldPath: d.OldPath, NewPath: d.Path, NewTitle: newTitle,
			Attempt: 1, LastError: err.Error(), QueuedAt: time.Now().UTC(),
		})
		return
	}

	idx.UpdatePath(entry.PageID, d.Path)
	idx.UpdateTitle(entry.PageID, newTitle)
	if newParentID != "" {
		idx.UpdateParent(entry.PageID, newParentID)
	}
	fmt.Fprintf(out, "[confluencer] rename: %s → %s\n", d.OldPath, d.Path)
}

// pushCreate handles a newly added .md file.
func pushCreate(client *api.Client, idx *index.Index, cfg *cfgpkg.Config, d gitutil.FileDiff, pendingPath, root string, out io.Writer) {
	// Read local content.
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(d.Path)))
	if err != nil {
		fmt.Fprintf(out, "[confluencer] WARNING: read %s: %v\n", d.Path, err)
		return
	}

	storageXML, err := lexer.MdToCf(string(content), lexer.MdToCfOpts{})
	if err != nil {
		fmt.Fprintf(out, "[confluencer] WARNING: convert %s: %v\n", d.Path, err)
		return
	}

	title := lexer.ReverseSlugify(filepath.Base(d.Path))
	parentID := ensureParentPages(client, idx, cfg, d.Path, root, out)

	page, err := client.CreatePage(cfg.SpaceKey, parentID, title, storageXML)
	if err != nil {
		fmt.Fprintf(out, "[confluencer] WARNING: create page for %s: %v — queued\n", d.Path, err)
		queuePending(pendingPath, index.PendingEntry{
			Type: index.PendingCreate, ParentPageID: parentID,
			LocalPath: d.Path, Title: title,
			Attempt: 1, LastError: err.Error(), QueuedAt: time.Now().UTC(),
		})
		return
	}

	idx.Add(index.Entry{
		PageID:       page.PageID,
		Title:        title,
		LocalPath:    d.Path,
		ParentPageID: parentID,
	})
	fmt.Fprintf(out, "[confluencer] create: %s (page %s)\n", d.Path, page.PageID)
}

// pushUpdate handles a modified .md file.
func pushUpdate(client *api.Client, idx *index.Index, cfg *cfgpkg.Config, d gitutil.FileDiff, pendingPath, root string, out io.Writer) {
	entry, ok := idx.ByPath(d.Path)
	if !ok {
		// Not tracked — treat as create.
		pushCreate(client, idx, cfg, d, pendingPath, root, out)
		return
	}

	// Read local content.
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(d.Path)))
	if err != nil {
		fmt.Fprintf(out, "[confluencer] WARNING: read %s: %v\n", d.Path, err)
		return
	}

	storageXML, err := lexer.MdToCf(string(content), lexer.MdToCfOpts{})
	if err != nil {
		fmt.Fprintf(out, "[confluencer] WARNING: convert %s: %v\n", d.Path, err)
		return
	}

	// Fetch current page from Confluence for version number.
	page, err := client.GetPage(entry.PageID)
	if err != nil {
		fmt.Fprintf(out, "[confluencer] WARNING: fetch page %s: %v — queued\n", entry.PageID, err)
		queuePending(pendingPath, index.PendingEntry{
			Type: index.PendingContent, PageID: entry.PageID, LocalPath: d.Path,
			Attempt: 1, LastError: err.Error(), QueuedAt: time.Now().UTC(),
		})
		return
	}

	if err := client.UpdatePage(entry.PageID, page.Version+1, entry.Title, storageXML, ""); err != nil {
		if api.IsConflict(err) {
			// Concurrent edit between GET and PUT — retry with latest version.
			latest, retryErr := client.GetPage(entry.PageID)
			if retryErr != nil {
				fmt.Fprintf(out, "[confluencer] WARNING: re-fetch %s: %v — queued\n", entry.PageID, retryErr)
			} else if retryErr = client.UpdatePage(entry.PageID, latest.Version+1, entry.Title, storageXML, ""); retryErr != nil {
				fmt.Fprintf(out, "[confluencer] WARNING: retry update %s: %v — queued\n", entry.PageID, retryErr)
			} else {
				fmt.Fprintf(out, "[confluencer] update: %s (retried after conflict)\n", d.Path)
				return
			}
			queuePending(pendingPath, index.PendingEntry{
				Type: index.PendingContent, PageID: entry.PageID, LocalPath: d.Path,
				Attempt: 1, LastError: err.Error(), QueuedAt: time.Now().UTC(),
			})
			return
		}
		fmt.Fprintf(out, "[confluencer] WARNING: update page %s: %v — queued\n", entry.PageID, err)
		queuePending(pendingPath, index.PendingEntry{
			Type: index.PendingContent, PageID: entry.PageID, LocalPath: d.Path,
			Attempt: 1, LastError: err.Error(), QueuedAt: time.Now().UTC(),
		})
		return
	}

	fmt.Fprintf(out, "[confluencer] update: %s\n", d.Path)
}

// ensureParentPages walks up the directory tree from filePath to localRoot,
// creating intermediate Confluence pages (and local index.md files) for any
// directory that doesn't already have a corresponding page in the index.
// Returns the page ID of the immediate parent, or cfg.RootPageID if the file
// sits directly in the local root.
func ensureParentPages(client *api.Client, idx *index.Index, cfg *cfgpkg.Config, filePath, root string, out io.Writer) string {
	localRoot := strings.TrimSuffix(cfg.LocalRoot, "/")

	dir := filepath.Dir(filePath)

	// index.md represents the directory's own page, not a child of it.
	// To find its parent we need to go up one more level.
	if filepath.Base(filePath) == "index.md" {
		dir = filepath.Dir(dir)
	}

	// File sits directly in the local root — parent is the root page.
	if dir == localRoot || dir == "." || !strings.HasPrefix(dir, localRoot) {
		return cfg.RootPageID
	}

	// Check if this directory already has a page (index.md entry).
	indexPath := dir + "/index.md"
	if entry, ok := idx.ByPath(indexPath); ok {
		return entry.PageID
	}

	// Recurse: ensure the grandparent exists before creating this directory's page.
	grandparentID := ensureParentPages(client, idx, cfg, indexPath, root, out)

	// Create the intermediate Confluence page for this directory.
	dirName := filepath.Base(dir)
	title := lexer.ReverseSlugify(dirName + ".md")

	page, err := client.CreatePage(cfg.SpaceKey, grandparentID, title, "")
	if err != nil {
		fmt.Fprintf(out, "[confluencer] WARNING: create intermediate page for %s: %v\n", dir, err)
		return grandparentID
	}

	// Write an empty local index.md so subsequent files in this directory find the parent.
	writeLocalFile(root, indexPath, "")

	// Add to index.
	idx.Add(index.Entry{
		PageID:       page.PageID,
		Title:        title,
		LocalPath:    indexPath,
		ParentPageID: grandparentID,
	})

	fmt.Fprintf(out, "[confluencer] create (intermediate): %s (page %s)\n", indexPath, page.PageID)
	return page.PageID
}

// drainPending retries entries from .confluencer-pending.
func drainPending(client *api.Client, idx *index.Index, pendingPath string, cfg *cfgpkg.Config, root string, out io.Writer) error {
	entries, err := index.LoadPending(pendingPath)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}

	var remaining []index.PendingEntry
	for _, e := range entries {
		var retryErr error
		switch e.Type {
		case index.PendingContent:
			retryErr = retryContent(client, idx, e, root)
		case index.PendingDelete:
			retryErr = retryDelete(client, idx, e)
		case index.PendingCreate:
			retryErr = retryCreate(client, idx, cfg, e, root)
		case index.PendingRename:
			retryErr = retryRename(client, idx, e, root)
		case index.PendingAttachment:
			retryErr = retryAttachment(client, e, root)
		}

		if retryErr != nil {
			e.Attempt++
			e.LastError = retryErr.Error()
			e.QueuedAt = time.Now().UTC()
			remaining = append(remaining, e)
			fmt.Fprintf(out, "[confluencer] retry failed: %s %s: %v\n", e.Type, e.LocalPath, retryErr)
		} else {
			fmt.Fprintf(out, "[confluencer] retry succeeded: %s %s\n", e.Type, e.LocalPath)
		}
	}

	return index.SavePending(pendingPath, remaining)
}

func retryContent(client *api.Client, idx *index.Index, e index.PendingEntry, root string) error {
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(e.LocalPath)))
	if err != nil {
		return err
	}
	storageXML, err := lexer.MdToCf(string(content), lexer.MdToCfOpts{})
	if err != nil {
		return err
	}
	page, err := client.GetPage(e.PageID)
	if err != nil {
		return err
	}
	entry, _ := idx.ByPageID(e.PageID)
	return client.UpdatePage(e.PageID, page.Version+1, entry.Title, storageXML, "")
}

func retryDelete(client *api.Client, idx *index.Index, e index.PendingEntry) error {
	if err := client.DeletePage(e.PageID); err != nil && !api.IsNotFound(err) {
		return err
	}
	idx.Remove(e.PageID)
	return nil
}

func retryCreate(client *api.Client, idx *index.Index, cfg *cfgpkg.Config, e index.PendingEntry, root string) error {
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(e.LocalPath)))
	if err != nil {
		return err
	}
	storageXML, err := lexer.MdToCf(string(content), lexer.MdToCfOpts{})
	if err != nil {
		return err
	}
	parentID := e.ParentPageID
	if parentID == "" {
		parentID = cfg.RootPageID
	}
	page, err := client.CreatePage(cfg.SpaceKey, parentID, e.Title, storageXML)
	if err != nil {
		return err
	}
	idx.Add(index.Entry{
		PageID:       page.PageID,
		Title:        e.Title,
		LocalPath:    e.LocalPath,
		ParentPageID: parentID,
	})
	return nil
}

func retryRename(client *api.Client, idx *index.Index, e index.PendingEntry, root string) error {
	page, err := client.GetPage(e.PageID)
	if err != nil {
		return err
	}
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(e.NewPath)))
	if err != nil {
		return err
	}
	storageXML, err := lexer.MdToCf(string(content), lexer.MdToCfOpts{})
	if err != nil {
		return err
	}
	if err := client.UpdatePage(e.PageID, page.Version+1, e.NewTitle, storageXML, ""); err != nil {
		return err
	}
	idx.UpdatePath(e.PageID, e.NewPath)
	idx.UpdateTitle(e.PageID, e.NewTitle)
	return nil
}

func retryAttachment(client *api.Client, e index.PendingEntry, root string) error {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(e.LocalPath)))
	if err != nil {
		return err
	}
	filename := filepath.Base(e.LocalPath)
	return client.UploadAttachment(e.PageID, filename, data)
}

// queuePending appends a single entry to .confluencer-pending.
func queuePending(path string, e index.PendingEntry) {
	index.AppendPending(path, e)
}
