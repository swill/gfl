package tree

import (
	"testing"
)

func TestNewCfTree_NilRoot(t *testing.T) {
	tr := NewCfTree(nil)
	if tr.Root != nil {
		t.Fatal("expected nil root")
	}
	if tr.Size() != 0 {
		t.Errorf("expected size 0, got %d", tr.Size())
	}
}

func TestCfTree_SingleRoot(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root"}
	tr := NewCfTree(root)
	if tr.Size() != 1 {
		t.Errorf("expected size 1, got %d", tr.Size())
	}
	if tr.Page("1") != root {
		t.Error("root not found by page ID")
	}
	if tr.Page("2") != nil {
		t.Error("phantom page found")
	}
	if tr.Contains("2") {
		t.Error("Contains returned true for absent page")
	}
}

func TestCfTree_Add(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root"}
	tr := NewCfTree(root)

	child := &CfNode{PageID: "2", Title: "Architecture", ParentPageID: "1"}
	if !tr.Add(child) {
		t.Fatal("Add returned false for valid parent")
	}
	if tr.Size() != 2 {
		t.Errorf("expected size 2, got %d", tr.Size())
	}
	if tr.Page("2") != child {
		t.Error("child not found")
	}
	if len(tr.Root.Children) != 1 || tr.Root.Children[0] != child {
		t.Error("child not attached to root")
	}
}

func TestCfTree_Add_OrphanParent(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root"}
	tr := NewCfTree(root)

	orphan := &CfNode{PageID: "2", Title: "X", ParentPageID: "999"}
	if tr.Add(orphan) {
		t.Error("Add should return false when parent not in tree")
	}
	if tr.Size() != 1 {
		t.Error("orphan should not be indexed")
	}
}

func TestCfTree_Add_WithPreAttachedChildren(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root"}
	tr := NewCfTree(root)

	// Add a node that already has children attached.
	node := &CfNode{PageID: "2", Title: "A", ParentPageID: "1", Children: []*CfNode{
		{PageID: "3", Title: "B", ParentPageID: "2"},
	}}
	if !tr.Add(node) {
		t.Fatal("Add returned false")
	}
	if tr.Size() != 3 {
		t.Errorf("expected 3, got %d", tr.Size())
	}
	if !tr.Contains("3") {
		t.Error("pre-attached child not indexed")
	}
}

func TestCfTree_PreAttachedAtConstruction(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "A", ParentPageID: "1"},
		{PageID: "3", Title: "B", ParentPageID: "1", Children: []*CfNode{
			{PageID: "4", Title: "C", ParentPageID: "3"},
		}},
	}}
	tr := NewCfTree(root)
	if tr.Size() != 4 {
		t.Errorf("expected 4, got %d", tr.Size())
	}
	for _, id := range []string{"1", "2", "3", "4"} {
		if !tr.Contains(id) {
			t.Errorf("missing page %s", id)
		}
	}
}

func TestCfTree_Walk_BFS(t *testing.T) {
	//   Root(1)
	//     Arch(2)
	//       DB(4)
	//     Onboard(3)
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Architecture", ParentPageID: "1", Children: []*CfNode{
			{PageID: "4", Title: "Database", ParentPageID: "2"},
		}},
		{PageID: "3", Title: "Onboarding", ParentPageID: "1"},
	}}
	tr := NewCfTree(root)

	var order []string
	tr.Walk(func(n *CfNode) { order = append(order, n.PageID) })

	want := []string{"1", "2", "3", "4"}
	if len(order) != len(want) {
		t.Fatalf("walk visited %d nodes, want %d", len(order), len(want))
	}
	for i, id := range want {
		if order[i] != id {
			t.Errorf("position %d: got %s, want %s", i, order[i], id)
		}
	}
}

func TestCfTree_Walk_Empty(t *testing.T) {
	tr := NewCfTree(nil)
	visited := false
	tr.Walk(func(*CfNode) { visited = true })
	if visited {
		t.Error("walk on nil-root tree should not visit anything")
	}
}

func TestCfTree_Ancestors(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Arch", ParentPageID: "1", Children: []*CfNode{
			{PageID: "3", Title: "DB", ParentPageID: "2"},
		}},
	}}
	tr := NewCfTree(root)

	// Root has no ancestors.
	if a := tr.Ancestors("1"); len(a) != 0 {
		t.Errorf("root ancestors: got %v, want nil", a)
	}
	// Page 2: ancestors = [1].
	if a := tr.Ancestors("2"); len(a) != 1 || a[0] != "1" {
		t.Errorf("page 2 ancestors: got %v, want [1]", a)
	}
	// Page 3: ancestors = [1, 2].
	a := tr.Ancestors("3")
	if len(a) != 2 || a[0] != "1" || a[1] != "2" {
		t.Errorf("page 3 ancestors: got %v, want [1 2]", a)
	}
	// Unknown page.
	if a := tr.Ancestors("99"); a != nil {
		t.Errorf("unknown page ancestors: got %v, want nil", a)
	}
}

func TestCfTree_HasChildren(t *testing.T) {
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{
		{PageID: "2", Title: "Leaf", ParentPageID: "1"},
	}}
	tr := NewCfTree(root)
	if !tr.HasChildren("1") {
		t.Error("root should have children")
	}
	if tr.HasChildren("2") {
		t.Error("leaf should not have children")
	}
	if tr.HasChildren("99") {
		t.Error("unknown page should not have children")
	}
}

func TestCfTree_ChildrenOf(t *testing.T) {
	c1 := &CfNode{PageID: "2", Title: "A", ParentPageID: "1"}
	c2 := &CfNode{PageID: "3", Title: "B", ParentPageID: "1"}
	root := &CfNode{PageID: "1", Title: "Root", Children: []*CfNode{c1, c2}}
	tr := NewCfTree(root)

	children := tr.ChildrenOf("1")
	if len(children) != 2 {
		t.Fatalf("expected 2 children, got %d", len(children))
	}
	if children[0] != c1 || children[1] != c2 {
		t.Error("children mismatch")
	}
	if c := tr.ChildrenOf("2"); len(c) != 0 {
		t.Errorf("leaf children: got %d, want 0", len(c))
	}
	if c := tr.ChildrenOf("99"); c != nil {
		t.Errorf("unknown children: got %v, want nil", c)
	}
}
