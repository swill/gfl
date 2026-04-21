package tree

import (
	"testing"
)

// stubContent is a test ContentChecker backed by pre-seeded equality answers.
type stubContent struct {
	equal map[string]bool // localPath → whether content matches
}

func (s *stubContent) ContentEqual(localPath, _ string) (bool, error) {
	eq, ok := s.equal[localPath]
	if !ok {
		return true, nil // unknown paths default to equal
	}
	return eq, nil
}

// findChange returns the first Change for the given page ID.
func findChange(changes []Change, pageID string) (Change, bool) {
	for _, c := range changes {
		if c.PageID == pageID {
			return c, true
		}
	}
	return Change{}, false
}

// --- Empty / no-change cases ------------------------------------------------

func TestDiff_EmptyInputs(t *testing.T) {
	changes := Diff(DiffInput{})
	if len(changes) != 0 {
		t.Errorf("expected no changes, got %d", len(changes))
	}
}

func TestDiff_NoChanges(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Page", ParentPageID: "1"},
	}}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
			{PageID: "2", Title: "Page", LocalPath: "docs/page.md", ParentPageID: "1"},
		},
	})

	if len(changes) != 0 {
		t.Errorf("expected no changes, got %d: %+v", len(changes), changes)
	}
}

func TestDiff_NoChanges_WithContentChecker(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root", Body: "<p>ok</p>"}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
		},
		Content: &stubContent{equal: map[string]bool{
			"docs/index.md": true,
		}},
	})
	if len(changes) != 0 {
		t.Errorf("expected no changes, got %d", len(changes))
	}
}

// --- Created ----------------------------------------------------------------

func TestDiff_Created(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "New Page", ParentPageID: "1"},
	}}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
		},
	})

	c, ok := findChange(changes, "2")
	if !ok {
		t.Fatal("expected Created change for page 2")
	}
	if c.Type != Created {
		t.Errorf("type: got %v, want Created", c.Type)
	}
	if c.NewPath != "docs/new-page.md" {
		t.Errorf("path: got %q, want docs/new-page.md", c.NewPath)
	}
	if c.NewParentPageID != "1" {
		t.Errorf("parent: got %q, want 1", c.NewParentPageID)
	}
	if c.OldPath != "" {
		t.Errorf("old path should be empty for Created, got %q", c.OldPath)
	}
}

func TestDiff_Created_Subtree(t *testing.T) {
	// A new page with children — all three are Created.
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "New Parent", ParentPageID: "1", Children: []*CfNode{
			{PageID: "3", Title: "New Child", ParentPageID: "2"},
		}},
	}}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
		},
	})

	c2, _ := findChange(changes, "2")
	if c2.Type != Created {
		t.Errorf("page 2: got %v, want Created", c2.Type)
	}
	if c2.NewPath != "docs/new-parent/index.md" {
		t.Errorf("page 2 path: got %q", c2.NewPath)
	}

	c3, _ := findChange(changes, "3")
	if c3.Type != Created {
		t.Errorf("page 3: got %v, want Created", c3.Type)
	}
	if c3.NewPath != "docs/new-parent/new-child.md" {
		t.Errorf("page 3 path: got %q", c3.NewPath)
	}
}

// --- Deleted / Orphaned / MissingUnknown ------------------------------------

func TestDiff_Deleted(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root"}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
			{PageID: "2", Title: "Gone", LocalPath: "docs/gone.md", ParentPageID: "1"},
		},
		Missing: []MissingPage{
			{PageID: "2", Status: StatusDeleted},
		},
	})

	c, ok := findChange(changes, "2")
	if !ok {
		t.Fatal("expected Deleted change")
	}
	if c.Type != Deleted {
		t.Errorf("type: got %v, want Deleted", c.Type)
	}
	if c.Title != "Gone" {
		t.Errorf("title should come from index: got %q", c.Title)
	}
	if c.OldPath != "docs/gone.md" {
		t.Errorf("old path: got %q", c.OldPath)
	}
	if c.NewPath != "" {
		t.Errorf("new path should be empty for Deleted: got %q", c.NewPath)
	}
}

func TestDiff_Orphaned(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root"}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
			{PageID: "2", Title: "Moved Away", LocalPath: "docs/moved.md", ParentPageID: "1"},
		},
		Missing: []MissingPage{
			{PageID: "2", Status: StatusOrphaned},
		},
	})

	c, _ := findChange(changes, "2")
	if c.Type != Orphaned {
		t.Errorf("type: got %v, want Orphaned", c.Type)
	}
}

func TestDiff_MissingUnknown(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root"}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
			{PageID: "2", Title: "Error", LocalPath: "docs/err.md", ParentPageID: "1"},
		},
		Missing: []MissingPage{
			{PageID: "2", Status: StatusUnknown},
		},
	})

	c, _ := findChange(changes, "2")
	if c.Type != MissingUnknown {
		t.Errorf("type: got %v, want MissingUnknown", c.Type)
	}
}

func TestDiff_MissingPage_NotInIndex_Skipped(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root"}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
		},
		Missing: []MissingPage{
			{PageID: "999", Status: StatusDeleted}, // not in index
		},
	})
	if len(changes) != 0 {
		t.Errorf("expected no changes for spurious missing entry, got %d", len(changes))
	}
}

func TestDiff_MissingPage_AlsoInTree_Skipped(t *testing.T) {
	// Defensive: if a page is in both the tree and the missing list (caller
	// bug), the walk handles it and the missing loop skips it.
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Page", ParentPageID: "1"},
	}}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
			{PageID: "2", Title: "Page", LocalPath: "docs/page.md", ParentPageID: "1"},
		},
		Missing: []MissingPage{
			{PageID: "2", Status: StatusDeleted}, // also in tree — should be skipped
		},
	})
	// Page 2 has no change (path matches), and the Missing entry is skipped.
	if len(changes) != 0 {
		t.Errorf("expected no changes, got %d: %+v", len(changes), changes)
	}
}

// --- RenamedInPlace ---------------------------------------------------------

func TestDiff_RenamedInPlace(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "New Name", ParentPageID: "1"},
	}}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
			{PageID: "2", Title: "Old Name", LocalPath: "docs/old-name.md", ParentPageID: "1"},
		},
	})

	c, ok := findChange(changes, "2")
	if !ok {
		t.Fatal("expected change for page 2")
	}
	if c.Type != RenamedInPlace {
		t.Errorf("type: got %v, want RenamedInPlace", c.Type)
	}
	if c.OldPath != "docs/old-name.md" {
		t.Errorf("old path: got %q", c.OldPath)
	}
	if c.NewPath != "docs/new-name.md" {
		t.Errorf("new path: got %q", c.NewPath)
	}
}

func TestDiff_RenamedInPlace_DirectoryPage(t *testing.T) {
	// A directory page (has children) is renamed.
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Architecture", ParentPageID: "1", Children: []*CfNode{
			{PageID: "3", Title: "DB", ParentPageID: "2"},
		}},
	}}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
			{PageID: "2", Title: "Arch", LocalPath: "docs/arch/index.md", ParentPageID: "1"},
			{PageID: "3", Title: "DB", LocalPath: "docs/arch/db.md", ParentPageID: "2"},
		},
	})

	c2, _ := findChange(changes, "2")
	if c2.Type != RenamedInPlace {
		t.Errorf("page 2: got %v, want RenamedInPlace", c2.Type)
	}
	if c2.NewPath != "docs/architecture/index.md" {
		t.Errorf("page 2 new path: got %q", c2.NewPath)
	}

	// Child's directory changed → AncestorRenamed.
	c3, _ := findChange(changes, "3")
	if c3.Type != AncestorRenamed {
		t.Errorf("page 3: got %v, want AncestorRenamed", c3.Type)
	}
}

func TestDiff_SiblingCollisionShift(t *testing.T) {
	// Page 100042 had a collision suffix. The canonical winner (100000) was
	// deleted, so 100042 now gets the plain slug. Title and parent unchanged
	// but filename changed → classified as RenamedInPlace.
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "100042", Title: "Database-Design", ParentPageID: "1"},
	}}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
			{PageID: "100042", Title: "Database-Design", LocalPath: "docs/database-design-100042.md", ParentPageID: "1"},
		},
	})

	c, ok := findChange(changes, "100042")
	if !ok {
		t.Fatal("expected change for collision shift")
	}
	if c.Type != RenamedInPlace {
		t.Errorf("type: got %v, want RenamedInPlace", c.Type)
	}
	if c.NewPath != "docs/database-design.md" {
		t.Errorf("new path: got %q, want docs/database-design.md", c.NewPath)
	}
}

// --- Moved ------------------------------------------------------------------

func TestDiff_Moved(t *testing.T) {
	// Page 3 was under root, now moved under page 2.
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Arch", ParentPageID: "1", Children: []*CfNode{
			{PageID: "3", Title: "Design", ParentPageID: "2"},
		}},
	}}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
			{PageID: "2", Title: "Arch", LocalPath: "docs/arch/index.md", ParentPageID: "1"},
			{PageID: "3", Title: "Design", LocalPath: "docs/design.md", ParentPageID: "1"},
		},
	})

	c, ok := findChange(changes, "3")
	if !ok {
		t.Fatal("expected change for page 3")
	}
	if c.Type != Moved {
		t.Errorf("type: got %v, want Moved", c.Type)
	}
	if c.OldParentPageID != "1" || c.NewParentPageID != "2" {
		t.Errorf("parent: got %s→%s, want 1→2", c.OldParentPageID, c.NewParentPageID)
	}
	if c.OldPath != "docs/design.md" {
		t.Errorf("old path: got %q", c.OldPath)
	}
	if c.NewPath != "docs/arch/design.md" {
		t.Errorf("new path: got %q", c.NewPath)
	}
}

func TestDiff_Moved_WithTitleChange(t *testing.T) {
	// Page moved AND renamed — Moved takes precedence.
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Arch", ParentPageID: "1", Children: []*CfNode{
			{PageID: "3", Title: "New Name", ParentPageID: "2"},
		}},
	}}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
			{PageID: "2", Title: "Arch", LocalPath: "docs/arch/index.md", ParentPageID: "1"},
			{PageID: "3", Title: "Old Name", LocalPath: "docs/old-name.md", ParentPageID: "1"},
		},
	})

	c, _ := findChange(changes, "3")
	if c.Type != Moved {
		t.Errorf("type: got %v, want Moved (precedence over rename)", c.Type)
	}
}

func TestDiff_Moved_TakesPrecedenceOverPromotion(t *testing.T) {
	// Page moved to new parent AND gained children.
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Arch", ParentPageID: "1"},
		{PageID: "3", Title: "Design", ParentPageID: "1", Children: []*CfNode{
			{PageID: "4", Title: "Sub", ParentPageID: "3"},
		}},
	}}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
			{PageID: "2", Title: "Arch", LocalPath: "docs/arch/index.md", ParentPageID: "1"},
			{PageID: "3", Title: "Design", LocalPath: "docs/arch/design.md", ParentPageID: "2"},
		},
	})

	c, _ := findChange(changes, "3")
	if c.Type != Moved {
		t.Errorf("type: got %v, want Moved (takes precedence over Promoted)", c.Type)
	}
}

// --- AncestorRenamed --------------------------------------------------------

func TestDiff_AncestorRenamed(t *testing.T) {
	// Parent "Arch" renamed to "Architecture" → child's directory changes.
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Architecture", ParentPageID: "1", Children: []*CfNode{
			{PageID: "3", Title: "DB", ParentPageID: "2"},
		}},
	}}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
			{PageID: "2", Title: "Arch", LocalPath: "docs/arch/index.md", ParentPageID: "1"},
			{PageID: "3", Title: "DB", LocalPath: "docs/arch/db.md", ParentPageID: "2"},
		},
	})

	// Parent is RenamedInPlace.
	c2, _ := findChange(changes, "2")
	if c2.Type != RenamedInPlace {
		t.Errorf("page 2: got %v, want RenamedInPlace", c2.Type)
	}

	// Child's path changed because parent dir changed → AncestorRenamed.
	c3, ok := findChange(changes, "3")
	if !ok {
		t.Fatal("expected change for page 3")
	}
	if c3.Type != AncestorRenamed {
		t.Errorf("page 3: got %v, want AncestorRenamed", c3.Type)
	}
	if c3.OldPath != "docs/arch/db.md" {
		t.Errorf("page 3 old: got %q", c3.OldPath)
	}
	if c3.NewPath != "docs/architecture/db.md" {
		t.Errorf("page 3 new: got %q", c3.NewPath)
	}
}

func TestDiff_AncestorRenamed_DeepNesting(t *testing.T) {
	// Grandparent renamed → both child and grandchild are AncestorRenamed.
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "New Top", ParentPageID: "1", Children: []*CfNode{
			{PageID: "3", Title: "Mid", ParentPageID: "2", Children: []*CfNode{
				{PageID: "4", Title: "Leaf", ParentPageID: "3"},
			}},
		}},
	}}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
			{PageID: "2", Title: "Old Top", LocalPath: "docs/old-top/index.md", ParentPageID: "1"},
			{PageID: "3", Title: "Mid", LocalPath: "docs/old-top/mid/index.md", ParentPageID: "2"},
			{PageID: "4", Title: "Leaf", LocalPath: "docs/old-top/mid/leaf.md", ParentPageID: "3"},
		},
	})

	c2, _ := findChange(changes, "2")
	if c2.Type != RenamedInPlace {
		t.Errorf("page 2: got %v, want RenamedInPlace", c2.Type)
	}
	c3, _ := findChange(changes, "3")
	if c3.Type != AncestorRenamed {
		t.Errorf("page 3: got %v, want AncestorRenamed", c3.Type)
	}
	c4, _ := findChange(changes, "4")
	if c4.Type != AncestorRenamed {
		t.Errorf("page 4: got %v, want AncestorRenamed", c4.Type)
	}
}

// --- Promoted ---------------------------------------------------------------

func TestDiff_Promoted(t *testing.T) {
	// Page 2 was a leaf, now has a child.
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Arch", ParentPageID: "1", Children: []*CfNode{
			{PageID: "3", Title: "DB", ParentPageID: "2"},
		}},
	}}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
			{PageID: "2", Title: "Arch", LocalPath: "docs/arch.md", ParentPageID: "1"},
		},
	})

	c, ok := findChange(changes, "2")
	if !ok {
		t.Fatal("expected Promoted change for page 2")
	}
	if c.Type != Promoted {
		t.Errorf("type: got %v, want Promoted", c.Type)
	}
	if c.OldPath != "docs/arch.md" {
		t.Errorf("old: got %q", c.OldPath)
	}
	if c.NewPath != "docs/arch/index.md" {
		t.Errorf("new: got %q", c.NewPath)
	}

	// New child is Created.
	c3, _ := findChange(changes, "3")
	if c3.Type != Created {
		t.Errorf("page 3: got %v, want Created", c3.Type)
	}
}

func TestDiff_Promoted_WithRename(t *testing.T) {
	// Page gained children AND was renamed — Promoted subsumes the rename.
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Architecture", ParentPageID: "1", Children: []*CfNode{
			{PageID: "3", Title: "DB", ParentPageID: "2"},
		}},
	}}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
			{PageID: "2", Title: "Arch", LocalPath: "docs/arch.md", ParentPageID: "1"},
		},
	})

	c, _ := findChange(changes, "2")
	if c.Type != Promoted {
		t.Errorf("type: got %v, want Promoted (subsumes rename)", c.Type)
	}
	if c.NewPath != "docs/architecture/index.md" {
		t.Errorf("new path includes new slug: got %q", c.NewPath)
	}
}

// --- Demoted ----------------------------------------------------------------

func TestDiff_Demoted(t *testing.T) {
	// Page 2 had children (was index.md), now is a leaf.
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Arch", ParentPageID: "1"},
	}}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
			{PageID: "2", Title: "Arch", LocalPath: "docs/arch/index.md", ParentPageID: "1"},
			{PageID: "3", Title: "DB", LocalPath: "docs/arch/db.md", ParentPageID: "2"},
		},
		Missing: []MissingPage{
			{PageID: "3", Status: StatusDeleted},
		},
	})

	c, ok := findChange(changes, "2")
	if !ok {
		t.Fatal("expected Demoted change for page 2")
	}
	if c.Type != Demoted {
		t.Errorf("type: got %v, want Demoted", c.Type)
	}
	if c.OldPath != "docs/arch/index.md" {
		t.Errorf("old: got %q", c.OldPath)
	}
	if c.NewPath != "docs/arch.md" {
		t.Errorf("new: got %q", c.NewPath)
	}

	// Deleted child.
	c3, _ := findChange(changes, "3")
	if c3.Type != Deleted {
		t.Errorf("page 3: got %v, want Deleted", c3.Type)
	}
}

func TestDiff_RootNotPromotedOrDemoted(t *testing.T) {
	// Root gains children — root is always index.md, never promoted.
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Child", ParentPageID: "1"},
	}}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
		},
	})

	// Root has no change — only the new child is Created.
	_, ok := findChange(changes, "1")
	if ok {
		t.Error("root should have no change when gaining children")
	}
	c2, _ := findChange(changes, "2")
	if c2.Type != Created {
		t.Errorf("child: got %v, want Created", c2.Type)
	}
}

// --- ContentChanged ---------------------------------------------------------

func TestDiff_ContentChanged(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root", Body: "<p>same</p>", Children: []*CfNode{
		{PageID: "2", Title: "Page", ParentPageID: "1", Body: "<p>updated</p>"},
	}}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
			{PageID: "2", Title: "Page", LocalPath: "docs/page.md", ParentPageID: "1"},
		},
		Content: &stubContent{equal: map[string]bool{
			"docs/page.md":  false, // content differs
			"docs/index.md": true,  // content same
		}},
	})

	c, ok := findChange(changes, "2")
	if !ok {
		t.Fatal("expected ContentChanged for page 2")
	}
	if c.Type != ContentChanged {
		t.Errorf("type: got %v, want ContentChanged", c.Type)
	}
	if c.OldPath != "docs/page.md" || c.NewPath != "docs/page.md" {
		t.Errorf("paths should be equal: old=%q new=%q", c.OldPath, c.NewPath)
	}

	// Root has same content → no change.
	_, ok = findChange(changes, "1")
	if ok {
		t.Error("root should have no change (content equal)")
	}
}

func TestDiff_NilContentChecker_SkipsContentComparison(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root", Body: "<p>changed</p>"}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
		},
		Content: nil, // no checker
	})

	if len(changes) != 0 {
		t.Errorf("expected no changes without content checker, got %d", len(changes))
	}
}

// --- Mixed / integration ----------------------------------------------------

func TestDiff_MultipleChangeTypes(t *testing.T) {
	// Mix: one Created, one RenamedInPlace, one ContentChanged, one Deleted.
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Renamed", ParentPageID: "1"},
		{PageID: "3", Title: "Same", ParentPageID: "1", Body: "<p>new</p>"},
		{PageID: "5", Title: "Brand New", ParentPageID: "1"},
	}}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: []IndexEntry{
			{PageID: "1", Title: "Root", LocalPath: "docs/index.md"},
			{PageID: "2", Title: "Old Name", LocalPath: "docs/old-name.md", ParentPageID: "1"},
			{PageID: "3", Title: "Same", LocalPath: "docs/same.md", ParentPageID: "1"},
			{PageID: "4", Title: "Deleted", LocalPath: "docs/deleted.md", ParentPageID: "1"},
		},
		Missing: []MissingPage{
			{PageID: "4", Status: StatusDeleted},
		},
		Content: &stubContent{equal: map[string]bool{
			"docs/same.md": false,
		}},
	})

	if len(changes) != 4 {
		t.Fatalf("expected 4 changes, got %d: %+v", len(changes), changes)
	}

	c2, _ := findChange(changes, "2")
	if c2.Type != RenamedInPlace {
		t.Errorf("page 2: got %v, want RenamedInPlace", c2.Type)
	}
	c3, _ := findChange(changes, "3")
	if c3.Type != ContentChanged {
		t.Errorf("page 3: got %v, want ContentChanged", c3.Type)
	}
	c4, _ := findChange(changes, "4")
	if c4.Type != Deleted {
		t.Errorf("page 4: got %v, want Deleted", c4.Type)
	}
	c5, _ := findChange(changes, "5")
	if c5.Type != Created {
		t.Errorf("page 5: got %v, want Created", c5.Type)
	}
}

func TestDiff_CLAUDEMdExample_FullSync(t *testing.T) {
	// Simulate a realistic initial sync: entire tree is new (empty index).
	root := &CfNode{PageID: "1", Title: "Root Page", Children: []*CfNode{
		{PageID: "2", Title: "Architecture", ParentPageID: "1", Children: []*CfNode{
			{PageID: "5", Title: "Database Design", ParentPageID: "2"},
			{PageID: "6", Title: "API Design", ParentPageID: "2"},
		}},
		{PageID: "3", Title: "Onboarding", ParentPageID: "1", Children: []*CfNode{
			{PageID: "7", Title: "For Developers", ParentPageID: "3"},
			{PageID: "8", Title: "For Managers", ParentPageID: "3"},
		}},
		{PageID: "4", Title: "API Reference", ParentPageID: "1"},
	}}
	ct := NewCfTree(root)
	pm := ComputePaths(ct, "docs")

	changes := Diff(DiffInput{
		Tree:  ct,
		Paths: pm,
		Index: nil, // empty index
	})

	// All 8 pages should be Created.
	if len(changes) != 8 {
		t.Fatalf("expected 8 Created, got %d", len(changes))
	}
	for _, c := range changes {
		if c.Type != Created {
			t.Errorf("page %s: got %v, want Created", c.PageID, c.Type)
		}
	}
}

// --- ChangeType.String() ----------------------------------------------------

func TestChangeType_String(t *testing.T) {
	cases := []struct {
		ct   ChangeType
		want string
	}{
		{ContentChanged, "ContentChanged"},
		{RenamedInPlace, "RenamedInPlace"},
		{Moved, "Moved"},
		{AncestorRenamed, "AncestorRenamed"},
		{Promoted, "Promoted"},
		{Demoted, "Demoted"},
		{Created, "Created"},
		{Deleted, "Deleted"},
		{Orphaned, "Orphaned"},
		{MissingUnknown, "MissingUnknown"},
		{ChangeType(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.ct.String(); got != c.want {
			t.Errorf("%d.String() = %q, want %q", c.ct, got, c.want)
		}
	}
}
