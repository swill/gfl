// Package index reads and writes the .confluencer-index.json file that
// maps Confluence page IDs to local file paths. The index is the stable
// bridge between Confluence's identity system (page IDs) and Git's
// identity system (file paths).
package index

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// Entry represents one page's record in the index.
type Entry struct {
	PageID       string `json:"confluence_page_id"`
	Title        string `json:"confluence_title"`
	LocalPath    string `json:"local_path"`
	ParentPageID string `json:"parent_page_id,omitempty"`
}

// indexFile is the on-disk JSON shape.
type indexFile struct {
	Pages []Entry `json:"pages"`
}

// Index provides O(1) lookup of page entries by page ID or local path.
// All mutations keep both maps in sync.
type Index struct {
	byPageID map[string]*Entry
	byPath   map[string]*Entry
}

// New creates an empty index.
func New() *Index {
	return &Index{
		byPageID: make(map[string]*Entry),
		byPath:   make(map[string]*Entry),
	}
}

// Load reads an index from a JSON file. Returns an error if the file
// cannot be read or parsed.
func Load(path string) (*Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f indexFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	idx := New()
	for _, e := range f.Pages {
		idx.Add(e)
	}
	return idx, nil
}

// Save writes the index to a JSON file. Entries are sorted by local path
// for deterministic, diff-friendly output.
func (idx *Index) Save(path string) error {
	entries := idx.Entries()
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].LocalPath < entries[j].LocalPath
	})
	f := indexFile{Pages: entries}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// Add inserts or replaces an entry. If an entry with the same page ID
// already exists, the old entry is removed first (including its path
// mapping). This makes Add safe for updates — the caller does not need
// to Remove before re-adding.
func (idx *Index) Add(e Entry) {
	if old, ok := idx.byPageID[e.PageID]; ok {
		delete(idx.byPath, old.LocalPath)
	}
	entry := e // heap copy — both maps share the same pointer
	idx.byPageID[e.PageID] = &entry
	idx.byPath[e.LocalPath] = &entry
}

// Remove deletes the entry for the given page ID. Returns true if an
// entry was removed.
func (idx *Index) Remove(pageID string) bool {
	e, ok := idx.byPageID[pageID]
	if !ok {
		return false
	}
	delete(idx.byPath, e.LocalPath)
	delete(idx.byPageID, pageID)
	return true
}

// ByPageID returns the entry for a page ID. Returns (Entry{}, false)
// if not found.
func (idx *Index) ByPageID(id string) (Entry, bool) {
	e, ok := idx.byPageID[id]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}

// ByPath returns the entry for a local file path. Returns (Entry{}, false)
// if not found.
func (idx *Index) ByPath(p string) (Entry, bool) {
	e, ok := idx.byPath[p]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}

// Entries returns a copy of all entries in the index. The order is not
// guaranteed; use Save for deterministic output.
func (idx *Index) Entries() []Entry {
	out := make([]Entry, 0, len(idx.byPageID))
	for _, e := range idx.byPageID {
		out = append(out, *e)
	}
	return out
}

// PageIDs returns all page IDs in the index.
func (idx *Index) PageIDs() []string {
	out := make([]string, 0, len(idx.byPageID))
	for id := range idx.byPageID {
		out = append(out, id)
	}
	return out
}

// Size returns the number of entries.
func (idx *Index) Size() int {
	return len(idx.byPageID)
}

// UpdatePath changes the local path for an existing entry. Both the
// byPath and byPageID maps are kept in sync. Returns false if the page
// ID is not in the index.
func (idx *Index) UpdatePath(pageID, newPath string) bool {
	e, ok := idx.byPageID[pageID]
	if !ok {
		return false
	}
	delete(idx.byPath, e.LocalPath)
	e.LocalPath = newPath
	idx.byPath[newPath] = e
	return true
}

// UpdateTitle changes the Confluence title for an existing entry.
// Returns false if the page ID is not in the index.
func (idx *Index) UpdateTitle(pageID, newTitle string) bool {
	e, ok := idx.byPageID[pageID]
	if !ok {
		return false
	}
	e.Title = newTitle
	return true
}

// UpdateParent changes the parent page ID for an existing entry.
// Returns false if the page ID is not in the index.
func (idx *Index) UpdateParent(pageID, newParentID string) bool {
	e, ok := idx.byPageID[pageID]
	if !ok {
		return false
	}
	e.ParentPageID = newParentID
	return true
}
