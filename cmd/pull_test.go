package cmd

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/swill/gfl/tree"
)

// makeTree builds a small CfTree with the given (id, parent, title) triples.
// The first entry must be the root (parent == "").
func makeTree(t *testing.T, nodes [][3]string, versions ...int) *tree.CfTree {
	t.Helper()
	if len(nodes) == 0 {
		t.Fatal("makeTree: need at least the root")
	}
	v := func(i int) int {
		if i < len(versions) {
			return versions[i]
		}
		return 1
	}
	root := &tree.CfNode{PageID: nodes[0][0], Title: nodes[0][2], ParentPageID: nodes[0][1], Version: v(0)}
	ct := tree.NewCfTree(root)
	for i := 1; i < len(nodes); i++ {
		n := &tree.CfNode{PageID: nodes[i][0], Title: nodes[i][2], ParentPageID: nodes[i][1], Version: v(i)}
		if !ct.Add(n) {
			t.Fatalf("makeTree: parent %s of %s not found", n.ParentPageID, n.PageID)
		}
	}
	return ct
}

func TestPlanPull_AllCreates(t *testing.T) {
	ct := makeTree(t, [][3]string{
		{"1", "", "Root"},
		{"2", "1", "Child"},
	})
	pm := tree.ComputePaths(ct, "docs/")

	plan := planPull(ct, pm, map[string]localManagedFile{})

	if len(plan.Renames) != 0 {
		t.Errorf("expected no renames, got %d", len(plan.Renames))
	}
	if len(plan.Deletes) != 0 {
		t.Errorf("expected no deletes, got %d", len(plan.Deletes))
	}
	if len(plan.PendingWrites) != 2 {
		t.Errorf("expected 2 pending writes (one per page), got %d", len(plan.PendingWrites))
	}
}

func TestPlanPull_VersionMatchIsNoop(t *testing.T) {
	ct := makeTree(t, [][3]string{
		{"1", "", "Root"},
		{"2", "1", "Child"},
	}, 1, 5)
	pm := tree.ComputePaths(ct, "docs/")

	current := map[string]localManagedFile{
		"1": {Path: "docs/index.md", PageID: "1", Version: 1},
		"2": {Path: "docs/child.md", PageID: "2", Version: 5},
	}

	plan := planPull(ct, pm, current)
	if !plan.IsNoOp() {
		t.Errorf("expected no-op plan, got %+v", plan)
	}
}

func TestPlanPull_VersionMismatchTriggersUpdate(t *testing.T) {
	ct := makeTree(t, [][3]string{
		{"1", "", "Root"},
	}, 7) // Confluence is at v7
	pm := tree.ComputePaths(ct, "docs/")

	current := map[string]localManagedFile{
		"1": {Path: "docs/index.md", PageID: "1", Version: 5}, // local is at v5
	}

	plan := planPull(ct, pm, current)
	if len(plan.PendingWrites) != 1 {
		t.Fatalf("expected 1 pending write for outdated page, got %d", len(plan.PendingWrites))
	}
	if plan.PendingWrites[0].PageID != "1" {
		t.Errorf("write for wrong page: %+v", plan.PendingWrites[0])
	}
}

func TestPlanPull_RenameDetectedByPathMismatch(t *testing.T) {
	// Confluence has the page at one path; local has it at another.
	// Title was changed on Confluence so the slug differs.
	ct := makeTree(t, [][3]string{
		{"1", "", "Root"},
		{"2", "1", "New Title"},
	})
	pm := tree.ComputePaths(ct, "docs/")

	current := map[string]localManagedFile{
		"1": {Path: "docs/index.md", PageID: "1", Version: 1},
		"2": {Path: "docs/old-title.md", PageID: "2", Version: 1},
	}

	plan := planPull(ct, pm, current)
	if len(plan.Renames) != 1 {
		t.Fatalf("expected 1 rename, got %d (%+v)", len(plan.Renames), plan.Renames)
	}
	if plan.Renames[0].From != "docs/old-title.md" || plan.Renames[0].To != "docs/new-title.md" {
		t.Errorf("rename: got %+v", plan.Renames[0])
	}
}

func TestPlanPull_DeleteCandidate(t *testing.T) {
	// Local has a page that's no longer in the tree.
	ct := makeTree(t, [][3]string{
		{"1", "", "Root"},
	})
	pm := tree.ComputePaths(ct, "docs/")

	current := map[string]localManagedFile{
		"1": {Path: "docs/index.md", PageID: "1", Version: 1},
		"2": {Path: "docs/orphan.md", PageID: "2", Version: 1},
	}

	plan := planPull(ct, pm, current)
	if len(plan.Deletes) != 1 {
		t.Fatalf("expected 1 delete candidate, got %d", len(plan.Deletes))
	}
	if plan.Deletes[0].PageID != "2" {
		t.Errorf("delete candidate: %+v", plan.Deletes[0])
	}
}

func TestScanManagedFiles_SkipsFilesWithoutFrontMatter(t *testing.T) {
	dir := t.TempDir()
	docs := filepath.Join(dir, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}

	// File with front-matter — should be picked up.
	withFM := "---\nconfluence_page_id: \"42\"\nconfluence_version: 7\n---\n\n# Hello\n"
	if err := os.WriteFile(filepath.Join(docs, "with-fm.md"), []byte(withFM), 0o644); err != nil {
		t.Fatal(err)
	}

	// File without front-matter — should be skipped.
	withoutFM := "# Just a regular markdown file\n"
	if err := os.WriteFile(filepath.Join(docs, "without-fm.md"), []byte(withoutFM), 0o644); err != nil {
		t.Fatal(err)
	}

	// Non-markdown file — should be skipped.
	if err := os.WriteFile(filepath.Join(docs, "readme.txt"), []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, err := scanManagedFiles(dir, "docs/")
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 managed file, got %d (%+v)", len(files), files)
	}
	if files[0].PageID != "42" || files[0].Version != 7 {
		t.Errorf("file: %+v", files[0])
	}
	if files[0].Path != "docs/with-fm.md" {
		t.Errorf("path: got %q, want docs/with-fm.md", files[0].Path)
	}
}

func TestScanManagedFiles_RecursesIntoSubdirs(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "docs", "architecture")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"docs/index.md": "---\nconfluence_page_id: \"1\"\n---\n\nroot\n",
		"docs/architecture/index.md":    "---\nconfluence_page_id: \"2\"\n---\n\narch\n",
		"docs/architecture/database.md": "---\nconfluence_page_id: \"3\"\n---\n\ndb\n",
	}
	for relPath, content := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(relPath)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := scanManagedFiles(dir, "docs/")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("expected 3 files, got %d", len(got))
	}
	// Sort for stable comparison.
	sort.Slice(got, func(i, j int) bool { return got[i].Path < got[j].Path })
	wantIDs := []string{"3", "2", "1"} // sorted by path: architecture/database, architecture/index, index
	for i, want := range wantIDs {
		if got[i].PageID != want {
			t.Errorf("file %d: pageID = %q, want %q (%+v)", i, got[i].PageID, want, got[i])
		}
	}
}

func TestHasCollisions(t *testing.T) {
	// No collision: targets and sources don't overlap.
	noColl := []renameOp{
		{From: "a.md", To: "b.md"},
		{From: "c.md", To: "d.md"},
	}
	if hasCollisions(noColl) {
		t.Error("expected no collision")
	}

	// Collision: target of #1 is source of #2.
	coll := []renameOp{
		{From: "a.md", To: "b.md"},
		{From: "b.md", To: "c.md"},
	}
	if !hasCollisions(coll) {
		t.Error("expected collision")
	}

	// Swap: a→b and b→a (each is the other's source/target).
	swap := []renameOp{
		{From: "a.md", To: "b.md"},
		{From: "b.md", To: "a.md"},
	}
	if !hasCollisions(swap) {
		t.Error("expected collision (swap)")
	}
}
