package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	Long: `Invoked by the pre-push Git hook. Parses pre-push stdin to identify
the commit range, drains the pending queue, then processes deletions, renames,
creates, content updates, and attachment uploads against Confluence.`,
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
		// --retry mode: only drain pending, don't process new commits.
		return nil
	}

	// Parse pre-push stdin to identify the commit range.
	refs, err := gitutil.ParsePushRefs(os.Stdin)
	if err != nil {
		return fmt.Errorf("parse push refs: %w", err)
	}

	for _, ref := range refs {
		if gitutil.IsDeleteBranch(ref) {
			continue
		}

		baseSHA := ref.RemoteSHA
		headSHA := ref.LocalSHA

		// Get all .md file changes in the range.
		diffs, err := gitutil.DiffRange(root, baseSHA, headSHA)
		if err != nil {
			fmt.Fprintf(out, "[confluencer] WARNING: diff range: %v\n", err)
			continue
		}
		mdDiffs := gitutil.FilterMd(diffs)

		// Filter out files whose most recent commit is a sync commit.
		mdDiffs = filterNonSyncDiffs(root, mdDiffs, baseSHA, headSHA)

		for _, d := range mdDiffs {
			switch d.Action {
			case gitutil.ActionDeleted:
				pushDelete(client, idx, d, pendingPath, root, out)

			case gitutil.ActionRenamed:
				pushRename(client, idx, cfg, d, pendingPath, root, out)

			case gitutil.ActionAdded:
				pushCreate(client, idx, cfg, d, pendingPath, root, out)

			case gitutil.ActionModified:
				pushUpdate(client, idx, cfg, d, pendingPath, root, out)
			}
		}
	}

	// Save updated index.
	if err := idx.Save(filepath.Join(root, indexFile)); err != nil {
		return fmt.Errorf("save index: %w", err)
	}

	return nil
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
	newParentID := resolveParentFromPath(idx, d.Path, cfg.LocalRoot)

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
	parentID := resolveParentFromPath(idx, d.Path, cfg.LocalRoot)
	if parentID == "" {
		// Fall back to root page.
		parentID = cfg.RootPageID
	}

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

	// Fetch current page for version.
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
			// Three-way merge on 409.
			merged, resolved := handleConflict(client, root, d.Path, storageXML, entry, out)
			if !resolved {
				queuePending(pendingPath, index.PendingEntry{
					Type: index.PendingContent, PageID: entry.PageID, LocalPath: d.Path,
					Attempt: 1, LastError: err.Error(), QueuedAt: time.Now().UTC(),
				})
				return
			}
			storageXML = merged
		} else {
			fmt.Fprintf(out, "[confluencer] WARNING: update page %s: %v — queued\n", entry.PageID, err)
			queuePending(pendingPath, index.PendingEntry{
				Type: index.PendingContent, PageID: entry.PageID, LocalPath: d.Path,
				Attempt: 1, LastError: err.Error(), QueuedAt: time.Now().UTC(),
			})
			return
		}
	}

	fmt.Fprintf(out, "[confluencer] update: %s\n", d.Path)
}

// handleConflict performs a three-way merge on 409 conflict.
// Returns the merged storage XML and whether resolution succeeded.
func handleConflict(client *api.Client, root, path, oursXML string, entry index.Entry, out io.Writer) (string, bool) {
	// Re-fetch to get the latest version.
	latest, err := client.GetPage(entry.PageID)
	if err != nil {
		fmt.Fprintf(out, "[confluencer] WARNING: re-fetch %s for merge: %v\n", entry.PageID, err)
		return "", false
	}

	// Convert Confluence content to Markdown.
	theirsMd, err := lexer.CfToMd(latest.Body, lexer.CfToMdOpts{})
	if err != nil {
		fmt.Fprintf(out, "[confluencer] WARNING: convert theirs for merge: %v\n", err)
		return "", false
	}

	// Read our local content.
	localContent, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return "", false
	}
	oursMd := string(localContent)

	// Get baseline.
	baseMd, _ := gitutil.Baseline(root, path)

	// Three-way merge at Markdown level.
	merged, conflict, err := gitutil.MergeFile(oursMd, baseMd, theirsMd)
	if err != nil {
		fmt.Fprintf(out, "[confluencer] WARNING: merge-file: %v\n", err)
		return "", false
	}

	if conflict {
		// Write conflict markers to the file.
		absPath := filepath.Join(root, filepath.FromSlash(path))
		os.WriteFile(absPath, []byte(merged), 0o644)
		fmt.Fprintf(out, "[confluencer] CONFLICT: %s has conflicting changes with Confluence. Resolve, commit, and re-push.\n", path)
		return "", false
	}

	// Convert merged Markdown back to storage XML.
	mergedXML, err := lexer.MdToCf(merged, lexer.MdToCfOpts{})
	if err != nil {
		return "", false
	}

	// Write merged content locally.
	writeLocalFile(root, path, merged)

	// Retry the PUT with the merged content and latest version.
	if err := client.UpdatePage(entry.PageID, latest.Version+1, entry.Title, mergedXML, ""); err != nil {
		fmt.Fprintf(out, "[confluencer] WARNING: merge-update %s: %v\n", entry.PageID, err)
		return "", false
	}

	fmt.Fprintf(out, "[confluencer] merged: %s\n", path)
	return mergedXML, true
}

// resolveParentFromPath determines the parent page ID from the file's directory.
func resolveParentFromPath(idx *index.Index, filePath, localRoot string) string {
	dir := filepath.Dir(filePath)
	// Look for index.md in the parent directory → that's the parent page.
	parentIndexPath := dir + "/index.md"
	if entry, ok := idx.ByPath(parentIndexPath); ok {
		return entry.PageID
	}
	// If we're in the local root, there's no parent in the index.
	return ""
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
