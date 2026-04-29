package tree

import (
	"path"

	"github.com/swill/gfl/lexer"
)

// PathMap maps every page in a CfTree to its expected local file path.
// It is computed by walking the tree, slugifying titles, disambiguating
// sibling collisions, and applying the CLAUDE.md directory/flat-file rules:
//
//   - Root page        → <localRoot>/index.md
//   - Page with children → <parentDir>/<slug>/index.md
//   - Leaf page        → <parentDir>/<slug>.md
type PathMap struct {
	pageToPath map[string]string // page ID → path
	pathToPage map[string]string // path → page ID
}

// ComputePaths walks the Confluence tree and computes the expected local
// file path for every page. localRoot is the prefix directory (e.g. "docs").
func ComputePaths(ct *CfTree, localRoot string) *PathMap {
	pm := &PathMap{
		pageToPath: make(map[string]string),
		pathToPage: make(map[string]string),
	}
	if ct == nil || ct.Root == nil {
		return pm
	}

	// Root page is always index.md at localRoot.
	rootPath := path.Join(localRoot, "index.md")
	pm.set(ct.Root.PageID, rootPath)

	// Process children level by level.
	pm.computeChildren(ct.Root.Children, localRoot)

	return pm
}

// computeChildren assigns paths to one level of siblings under parentDir,
// then recurses into each child that itself has children.
func (pm *PathMap) computeChildren(children []*CfNode, parentDir string) {
	if len(children) == 0 {
		return
	}

	// Build PageRef slice for sibling slug disambiguation.
	refs := make([]lexer.PageRef, len(children))
	for i, c := range children {
		refs[i] = lexer.PageRef{PageID: c.PageID, Title: c.Title}
	}
	slugs := lexer.DisambiguateSiblings(refs)

	for _, child := range children {
		slug := slugs[child.PageID]
		if len(child.Children) > 0 {
			// Directory page: <parentDir>/<slug>/index.md
			dir := path.Join(parentDir, slug)
			pm.set(child.PageID, path.Join(dir, "index.md"))
			pm.computeChildren(child.Children, dir)
		} else {
			// Leaf page: <parentDir>/<slug>.md
			pm.set(child.PageID, path.Join(parentDir, slug+".md"))
		}
	}
}

func (pm *PathMap) set(pageID, filePath string) {
	pm.pageToPath[pageID] = filePath
	pm.pathToPage[filePath] = pageID
}

// Path returns the expected local file path for a page ID.
// Returns ("", false) if the page ID is not in the map.
func (pm *PathMap) Path(pageID string) (string, bool) {
	p, ok := pm.pageToPath[pageID]
	return p, ok
}

// PageID returns the page ID for a given local path.
// Returns ("", false) if the path is not in the map.
func (pm *PathMap) PageID(filePath string) (string, bool) {
	id, ok := pm.pathToPage[filePath]
	return id, ok
}

// All returns a copy of the page-ID-to-path mapping.
func (pm *PathMap) All() map[string]string {
	out := make(map[string]string, len(pm.pageToPath))
	for k, v := range pm.pageToPath {
		out[k] = v
	}
	return out
}

// Size returns the number of pages in the mapping.
func (pm *PathMap) Size() int {
	return len(pm.pageToPath)
}
