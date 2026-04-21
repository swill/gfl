package tree

import (
	"testing"
)

func TestComputePaths_NilTree(t *testing.T) {
	pm := ComputePaths(nil, "docs")
	if pm.Size() != 0 {
		t.Errorf("expected 0, got %d", pm.Size())
	}
}

func TestComputePaths_NilRoot(t *testing.T) {
	tr := NewCfTree(nil)
	pm := ComputePaths(tr, "docs")
	if pm.Size() != 0 {
		t.Errorf("expected 0, got %d", pm.Size())
	}
}

func TestComputePaths_RootOnly(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root Page"}
	tr := NewCfTree(root)
	pm := ComputePaths(tr, "docs")

	p, ok := pm.Path("1")
	if !ok || p != "docs/index.md" {
		t.Errorf("root path: got %q (ok=%v), want docs/index.md", p, ok)
	}
	id, ok := pm.PageID("docs/index.md")
	if !ok || id != "1" {
		t.Errorf("reverse lookup: got %q (ok=%v), want 1", id, ok)
	}
	if pm.Size() != 1 {
		t.Errorf("size: got %d, want 1", pm.Size())
	}
}

func TestComputePaths_LeafChildren(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Architecture", ParentPageID: "1"},
		{PageID: "3", Title: "API Reference", ParentPageID: "1"},
	}}
	tr := NewCfTree(root)
	pm := ComputePaths(tr, "docs")

	cases := map[string]string{
		"1": "docs/index.md",
		"2": "docs/architecture.md",
		"3": "docs/api-reference.md",
	}
	for id, want := range cases {
		got, ok := pm.Path(id)
		if !ok || got != want {
			t.Errorf("page %s: got %q (ok=%v), want %q", id, got, ok, want)
		}
	}
}

func TestComputePaths_PageWithChildren(t *testing.T) {
	// Architecture has a child -> promoted to directory.
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Architecture", ParentPageID: "1", Children: []*CfNode{
			{PageID: "3", Title: "Database Design", ParentPageID: "2"},
		}},
		{PageID: "4", Title: "API Reference", ParentPageID: "1"},
	}}
	tr := NewCfTree(root)
	pm := ComputePaths(tr, "docs")

	cases := map[string]string{
		"1": "docs/index.md",
		"2": "docs/architecture/index.md",
		"3": "docs/architecture/database-design.md",
		"4": "docs/api-reference.md",
	}
	for id, want := range cases {
		got, ok := pm.Path(id)
		if !ok || got != want {
			t.Errorf("page %s: got %q, want %q", id, got, want)
		}
	}
}

func TestComputePaths_DeepNesting(t *testing.T) {
	// Root > LevelOne > LevelTwo > LevelThree (3 levels of nesting)
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Level One", ParentPageID: "1", Children: []*CfNode{
			{PageID: "3", Title: "Level Two", ParentPageID: "2", Children: []*CfNode{
				{PageID: "4", Title: "Level Three", ParentPageID: "3"},
			}},
		}},
	}}
	tr := NewCfTree(root)
	pm := ComputePaths(tr, "docs")

	cases := map[string]string{
		"1": "docs/index.md",
		"2": "docs/level-one/index.md",
		"3": "docs/level-one/level-two/index.md",
		"4": "docs/level-one/level-two/level-three.md",
	}
	for id, want := range cases {
		got, ok := pm.Path(id)
		if !ok || got != want {
			t.Errorf("page %s: got %q, want %q", id, got, want)
		}
	}
}

func TestComputePaths_SiblingCollision(t *testing.T) {
	// "Database Design" (100000) and "Database-Design" (100042) both slugify
	// to "database-design". Lowest page ID wins the plain slug.
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "100000", Title: "Database Design", ParentPageID: "1"},
		{PageID: "100042", Title: "Database-Design", ParentPageID: "1"},
	}}
	tr := NewCfTree(root)
	pm := ComputePaths(tr, "docs")

	p1, _ := pm.Path("100000")
	if p1 != "docs/database-design.md" {
		t.Errorf("canonical sibling: got %q, want docs/database-design.md", p1)
	}
	p2, _ := pm.Path("100042")
	if p2 != "docs/database-design-100042.md" {
		t.Errorf("collision sibling: got %q, want docs/database-design-100042.md", p2)
	}
}

func TestComputePaths_CLAUDEMdExample(t *testing.T) {
	// Reproduce the exact tree from CLAUDE.md's example:
	//   Root Page          -> docs/index.md
	//     Architecture     -> docs/architecture/index.md
	//       Database Design -> docs/architecture/database-design.md
	//       API Design     -> docs/architecture/api-design.md
	//     Onboarding       -> docs/onboarding/index.md
	//       For Developers -> docs/onboarding/for-developers.md
	//       For Managers   -> docs/onboarding/for-managers.md
	//     API Reference    -> docs/api-reference.md
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
	tr := NewCfTree(root)
	pm := ComputePaths(tr, "docs")

	want := map[string]string{
		"1": "docs/index.md",
		"2": "docs/architecture/index.md",
		"3": "docs/onboarding/index.md",
		"4": "docs/api-reference.md",
		"5": "docs/architecture/database-design.md",
		"6": "docs/architecture/api-design.md",
		"7": "docs/onboarding/for-developers.md",
		"8": "docs/onboarding/for-managers.md",
	}
	if pm.Size() != len(want) {
		t.Fatalf("size: got %d, want %d", pm.Size(), len(want))
	}
	for id, wantPath := range want {
		got, ok := pm.Path(id)
		if !ok {
			t.Errorf("page %s: not found", id)
			continue
		}
		if got != wantPath {
			t.Errorf("page %s: got %q, want %q", id, got, wantPath)
		}
	}
}

func TestComputePaths_All_ReturnsCopy(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Child", ParentPageID: "1"},
	}}
	tr := NewCfTree(root)
	pm := ComputePaths(tr, "docs")

	all := pm.All()
	if len(all) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(all))
	}
	// Mutating the returned map must not affect the PathMap.
	all["2"] = "modified"
	p, _ := pm.Path("2")
	if p == "modified" {
		t.Error("All() returned internal map, not a copy")
	}
}

func TestComputePaths_ReverseLookup(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Arch", ParentPageID: "1"},
	}}
	tr := NewCfTree(root)
	pm := ComputePaths(tr, "docs")

	id, ok := pm.PageID("docs/arch.md")
	if !ok || id != "2" {
		t.Errorf("reverse lookup: got %q (ok=%v), want 2", id, ok)
	}
	_, ok = pm.PageID("docs/nonexistent.md")
	if ok {
		t.Error("reverse lookup should fail for unknown path")
	}
}

func TestComputePaths_EmptyLocalRoot(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Child", ParentPageID: "1"},
	}}
	tr := NewCfTree(root)
	pm := ComputePaths(tr, "")

	p, ok := pm.Path("1")
	if !ok || p != "index.md" {
		t.Errorf("root with empty localRoot: got %q, want index.md", p)
	}
	p, ok = pm.Path("2")
	if !ok || p != "child.md" {
		t.Errorf("child with empty localRoot: got %q, want child.md", p)
	}
}

func TestComputePaths_PromotionWithCollision(t *testing.T) {
	// A page that has children AND whose siblings have a collision.
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "100000", Title: "Guides", ParentPageID: "1", Children: []*CfNode{
			{PageID: "3", Title: "Setup", ParentPageID: "100000"},
		}},
		{PageID: "100042", Title: "Guides!", ParentPageID: "1"},
	}}
	tr := NewCfTree(root)
	pm := ComputePaths(tr, "docs")

	// 100000 wins the plain slug "guides" and has children -> directory.
	p1, _ := pm.Path("100000")
	if p1 != "docs/guides/index.md" {
		t.Errorf("promoted canonical: got %q, want docs/guides/index.md", p1)
	}
	// 100042 gets the suffixed slug and is a leaf.
	p2, _ := pm.Path("100042")
	if p2 != "docs/guides-100042.md" {
		t.Errorf("collision leaf: got %q, want docs/guides-100042.md", p2)
	}
	// Child of promoted page.
	p3, _ := pm.Path("3")
	if p3 != "docs/guides/setup.md" {
		t.Errorf("child of promoted: got %q, want docs/guides/setup.md", p3)
	}
}
