// Package tree represents Confluence page hierarchies and their local
// file-system projections. All types are pure data structures with no
// network or filesystem access — the API and gitutil packages populate
// them.
package tree

// CfNode represents a single page in the Confluence page hierarchy.
type CfNode struct {
	PageID       string
	Title        string
	ParentPageID string // empty for the root page
	SpaceKey     string
	Body         string // storage XML (body.storage.value)
	Version      int    // Confluence version number

	Children []*CfNode
}

// CfTree is the full Confluence page hierarchy rooted at the sync anchor page.
// It provides O(1) lookup by page ID and tree-walking helpers.
type CfTree struct {
	Root *CfNode
	byID map[string]*CfNode
}

// NewCfTree creates a tree from the root page node. The root and any
// pre-attached children are indexed. Additional nodes can be added via Add.
func NewCfTree(root *CfNode) *CfTree {
	t := &CfTree{
		Root: root,
		byID: make(map[string]*CfNode),
	}
	if root != nil {
		t.index(root)
	}
	return t
}

// index recursively registers a node and all its children in the byID map.
func (t *CfTree) index(n *CfNode) {
	t.byID[n.PageID] = n
	for _, c := range n.Children {
		t.index(c)
	}
}

// Add inserts a node under its parent (identified by ParentPageID).
// The parent must already be in the tree. Returns false if the parent
// is not found; the node is not added in that case.
func (t *CfTree) Add(node *CfNode) bool {
	parent, ok := t.byID[node.ParentPageID]
	if !ok {
		return false
	}
	parent.Children = append(parent.Children, node)
	t.index(node)
	return true
}

// Page returns the node with the given page ID, or nil.
func (t *CfTree) Page(pageID string) *CfNode {
	return t.byID[pageID]
}

// Size returns the total number of pages in the tree.
func (t *CfTree) Size() int {
	return len(t.byID)
}

// Contains returns true if a page with the given ID is in the tree.
func (t *CfTree) Contains(pageID string) bool {
	_, ok := t.byID[pageID]
	return ok
}

// Walk visits every node in breadth-first order, calling fn for each.
func (t *CfTree) Walk(fn func(*CfNode)) {
	if t.Root == nil {
		return
	}
	queue := []*CfNode{t.Root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		fn(n)
		queue = append(queue, n.Children...)
	}
}

// Ancestors returns the chain of page IDs from the root to (but not
// including) the given page. Returns nil if the page is the root or is
// not in the tree.
func (t *CfTree) Ancestors(pageID string) []string {
	n := t.byID[pageID]
	if n == nil || n.ParentPageID == "" {
		return nil
	}
	// Walk parent pointers from the target up to the root.
	var chain []string
	for cur := t.byID[n.ParentPageID]; cur != nil; cur = t.byID[cur.ParentPageID] {
		chain = append(chain, cur.PageID)
	}
	// Reverse: collected child-to-root, need root-to-child.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// HasChildren returns true if the page has at least one child in the tree.
func (t *CfTree) HasChildren(pageID string) bool {
	n := t.byID[pageID]
	return n != nil && len(n.Children) > 0
}

// ChildrenOf returns the direct children of the given page.
// Returns nil if the page is not in the tree or has no children.
func (t *CfTree) ChildrenOf(pageID string) []*CfNode {
	n := t.byID[pageID]
	if n == nil {
		return nil
	}
	return n.Children
}
