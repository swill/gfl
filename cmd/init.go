package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/swill/confluencer/api"
	cfgpkg "github.com/swill/confluencer/config"
	"github.com/swill/confluencer/index"
	"github.com/swill/confluencer/lexer"
	"github.com/swill/confluencer/tree"
)

var (
	initPageID    string
	initLocalRoot string
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Populate a local repo from an existing Confluence page tree",
	Long: `Fetches a Confluence page tree rooted at --page-id and writes it to
--local-root as a tree of Markdown files. Also writes .confluencer.json
(including the cached space key) and .confluencer-index.json.`,
	RunE: runInit,
}

func init() {
	initCmd.Flags().StringVar(&initPageID, "page-id", "", "Confluence root page ID (required)")
	initCmd.Flags().StringVar(&initLocalRoot, "local-root", "docs/", "Local root directory for the mirrored tree")
	_ = initCmd.MarkFlagRequired("page-id")
	rootCmd.AddCommand(initCmd)
}

func runInit(cmd *cobra.Command, args []string) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()

	// Check that .confluencer.json does not already exist.
	cfgPath := filepath.Join(root, configFile)
	if _, err := os.Stat(cfgPath); err == nil {
		return fmt.Errorf("%s already exists — confluencer is already initialised in this repository", configFile)
	}

	// Ensure local root has trailing slash for consistency.
	localRoot := initLocalRoot
	if !strings.HasSuffix(localRoot, "/") {
		localRoot += "/"
	}
	attachmentsDir := localRoot + "_attachments"

	// Load credentials.
	creds, err := cfgpkg.LoadCredentials(root)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Fetching page tree from %s (root page %s)...\n", creds.BaseURL, initPageID)

	client := api.NewClient(creds.BaseURL, creds.User, creds.APIToken)

	// Fetch the full tree with body content.
	ct, err := client.FetchTree(initPageID, true)
	if err != nil {
		return fmt.Errorf("fetch tree: %w", err)
	}

	fmt.Fprintf(out, "Found %d pages.\n", ct.Size())

	// Compute local paths from the tree.
	pm := tree.ComputePaths(ct, localRoot)

	// Build the index and write files.
	idx := index.New()
	pageResolver := newInitPageResolver(ct, pm)
	var fileCount int

	ct.Walk(func(n *tree.CfNode) {
		localPath, ok := pm.Path(n.PageID)
		if !ok {
			fmt.Fprintf(out, "  WARNING: no path computed for page %s (%s)\n", n.PageID, n.Title)
			return
		}

		// Convert storage XML to Markdown.
		md, err := lexer.CfToMd(n.Body, lexer.CfToMdOpts{
			Pages:       pageResolver,
			Attachments: &stubAttachmentResolver{localPath: localPath, attachmentsDir: attachmentsDir, localRoot: localRoot},
		})
		if err != nil {
			fmt.Fprintf(out, "  WARNING: conversion failed for %s (%s): %v\n", n.PageID, n.Title, err)
			md = ""
		}

		// Write the Markdown file.
		absPath := filepath.Join(root, filepath.FromSlash(localPath))
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			fmt.Fprintf(out, "  ERROR: mkdir for %s: %v\n", localPath, err)
			return
		}
		if err := os.WriteFile(absPath, []byte(md), 0o644); err != nil {
			fmt.Fprintf(out, "  ERROR: write %s: %v\n", localPath, err)
			return
		}

		// Add to index.
		idx.Add(index.Entry{
			PageID:       n.PageID,
			Title:        n.Title,
			LocalPath:    localPath,
			ParentPageID: n.ParentPageID,
			Version:      n.Version,
		})

		fileCount++
	})

	// Download attachments.
	attCount := 0
	ct.Walk(func(n *tree.CfNode) {
		atts, err := client.GetAttachments(n.PageID, "")
		if err != nil {
			fmt.Fprintf(out, "  WARNING: cannot list attachments for %s: %v\n", n.PageID, err)
			return
		}
		if len(atts) == 0 {
			return
		}

		localPath, ok := pm.Path(n.PageID)
		if !ok {
			return
		}
		attDir := tree.AttachmentDir(localPath, localRoot, attachmentsDir)

		for _, att := range atts {
			data, err := client.DownloadAttachment(att.DownloadPath)
			if err != nil {
				fmt.Fprintf(out, "  WARNING: download %s for page %s: %v\n", att.Filename, n.PageID, err)
				continue
			}
			attPath := filepath.Join(root, filepath.FromSlash(attDir), att.Filename)
			if err := os.MkdirAll(filepath.Dir(attPath), 0o755); err != nil {
				fmt.Fprintf(out, "  ERROR: mkdir for %s: %v\n", attPath, err)
				continue
			}
			if err := os.WriteFile(attPath, data, 0o644); err != nil {
				fmt.Fprintf(out, "  ERROR: write %s: %v\n", attPath, err)
				continue
			}
			attCount++
		}
	})

	// Write .confluencer.json.
	cfg := &cfgpkg.Config{
		RootPageID:     initPageID,
		SpaceKey:       ct.Root.SpaceKey,
		LocalRoot:      localRoot,
		AttachmentsDir: attachmentsDir,
	}
	if err := cfg.Save(cfgPath); err != nil {
		return fmt.Errorf("write %s: %w", configFile, err)
	}

	// Write .confluencer-index.json.
	idxPath := filepath.Join(root, indexFile)
	if err := idx.Save(idxPath); err != nil {
		return fmt.Errorf("write %s: %w", indexFile, err)
	}

	// Write .gitignore stub if it doesn't exist.
	writeGitignoreStub(root)

	// Create .confluencer/hooks/ with hook shims.
	if err := writeHookShims(root); err != nil {
		return fmt.Errorf("write hook shims: %w", err)
	}

	// Install hooks into .git/hooks/.
	if err := installHooks(root, out); err != nil {
		return fmt.Errorf("install hooks: %w", err)
	}

	fmt.Fprintf(out, "\nInitialised confluencer:\n")
	fmt.Fprintf(out, "  Pages:       %d\n", fileCount)
	fmt.Fprintf(out, "  Attachments: %d\n", attCount)
	fmt.Fprintf(out, "  Config:      %s\n", configFile)
	fmt.Fprintf(out, "  Index:       %s\n", indexFile)
	fmt.Fprintf(out, "  Hooks:       .confluencer/hooks/ → .git/hooks/\n")
	fmt.Fprintln(out, "\nReview the files, then git add and commit.")

	return nil
}

// writeHookShims creates .confluencer/hooks/ and writes the Git hook shims.
func writeHookShims(root string) error {
	hooksDir := filepath.Join(root, ".confluencer", "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return err
	}

	shims := map[string]string{
		"pre-push":     "#!/bin/sh\nset -e\nconfluencer push\n",
		"post-merge":   "#!/bin/sh\nset -e\nconfluencer pull\n",
		"post-rewrite": "#!/bin/sh\nset -e\nconfluencer pull\n",
	}

	for name, content := range shims {
		path := filepath.Join(hooksDir, name)
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// writeGitignoreStub appends confluencer entries to .gitignore if they're
// not already present. Does not fail — best effort.
func writeGitignoreStub(root string) {
	gitignorePath := filepath.Join(root, ".gitignore")
	existing, _ := os.ReadFile(gitignorePath)
	content := string(existing)

	entries := []string{".env", ".confluencer-pending"}
	var toAdd []string
	for _, e := range entries {
		if !strings.Contains(content, e) {
			toAdd = append(toAdd, e)
		}
	}

	if len(toAdd) == 0 {
		return
	}

	f, err := os.OpenFile(gitignorePath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	// Add a newline separator if the file doesn't end with one.
	if len(content) > 0 && content[len(content)-1] != '\n' {
		f.WriteString("\n")
	}
	f.WriteString("# confluencer\n")
	for _, e := range toAdd {
		f.WriteString(e + "\n")
	}
}

// initPageResolver resolves cross-page links during init.
type initPageResolver struct {
	tree  *tree.CfTree
	paths *tree.PathMap
}

func newInitPageResolver(ct *tree.CfTree, pm *tree.PathMap) *initPageResolver {
	return &initPageResolver{tree: ct, paths: pm}
}

func (r *initPageResolver) ResolvePageByTitle(title, spaceKey string) (localPath string, ok bool) {
	// Walk the tree to find by title.
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
	p, pOk := r.paths.Path(found.PageID)
	if !pOk {
		return "", false
	}
	return p, true
}

// stubAttachmentResolver resolves attachment image references during init.
type stubAttachmentResolver struct {
	localPath      string
	attachmentsDir string
	localRoot      string
}

func (r *stubAttachmentResolver) AttachmentSrc(filename string) string {
	attDir := tree.AttachmentDir(r.localPath, r.localRoot, r.attachmentsDir)
	return attDir + "/" + filename
}
