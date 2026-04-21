package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNew_Empty(t *testing.T) {
	idx := New()
	if idx.Size() != 0 {
		t.Errorf("expected 0, got %d", idx.Size())
	}
}

func TestAdd_And_Lookup(t *testing.T) {
	idx := New()
	idx.Add(Entry{
		PageID:       "100",
		Title:        "Root Page",
		LocalPath:    "docs/index.md",
		ParentPageID: "",
	})
	idx.Add(Entry{
		PageID:       "200",
		Title:        "Architecture",
		LocalPath:    "docs/architecture/index.md",
		ParentPageID: "100",
	})

	if idx.Size() != 2 {
		t.Fatalf("size: got %d, want 2", idx.Size())
	}

	// ByPageID
	e, ok := idx.ByPageID("100")
	if !ok {
		t.Fatal("page 100 not found")
	}
	if e.Title != "Root Page" || e.LocalPath != "docs/index.md" {
		t.Errorf("page 100: %+v", e)
	}
	if e.ParentPageID != "" {
		t.Errorf("root parent should be empty, got %q", e.ParentPageID)
	}

	// ByPath
	e, ok = idx.ByPath("docs/architecture/index.md")
	if !ok {
		t.Fatal("path lookup failed")
	}
	if e.PageID != "200" {
		t.Errorf("expected page 200, got %q", e.PageID)
	}

	// Not found
	_, ok = idx.ByPageID("999")
	if ok {
		t.Error("should not find non-existent page")
	}
	_, ok = idx.ByPath("nope.md")
	if ok {
		t.Error("should not find non-existent path")
	}
}

func TestAdd_ReplacesExisting(t *testing.T) {
	idx := New()
	idx.Add(Entry{PageID: "100", Title: "Old", LocalPath: "docs/old.md"})
	idx.Add(Entry{PageID: "100", Title: "New", LocalPath: "docs/new.md"})

	if idx.Size() != 1 {
		t.Fatalf("size should be 1 after replace, got %d", idx.Size())
	}
	e, _ := idx.ByPageID("100")
	if e.Title != "New" || e.LocalPath != "docs/new.md" {
		t.Errorf("after replace: %+v", e)
	}
	// Old path should no longer resolve.
	_, ok := idx.ByPath("docs/old.md")
	if ok {
		t.Error("old path should be removed after replace")
	}
	// New path should resolve.
	e, ok = idx.ByPath("docs/new.md")
	if !ok || e.PageID != "100" {
		t.Error("new path should resolve")
	}
}

func TestRemove(t *testing.T) {
	idx := New()
	idx.Add(Entry{PageID: "100", Title: "Root", LocalPath: "docs/index.md"})
	idx.Add(Entry{PageID: "200", Title: "Page", LocalPath: "docs/page.md"})

	if !idx.Remove("100") {
		t.Error("Remove should return true for existing entry")
	}
	if idx.Size() != 1 {
		t.Errorf("size after remove: got %d", idx.Size())
	}
	_, ok := idx.ByPageID("100")
	if ok {
		t.Error("removed page should not be found by ID")
	}
	_, ok = idx.ByPath("docs/index.md")
	if ok {
		t.Error("removed page should not be found by path")
	}

	// Remove non-existent.
	if idx.Remove("999") {
		t.Error("Remove should return false for non-existent page")
	}
}

func TestUpdatePath(t *testing.T) {
	idx := New()
	idx.Add(Entry{PageID: "100", Title: "Page", LocalPath: "docs/old.md"})

	if !idx.UpdatePath("100", "docs/new.md") {
		t.Fatal("UpdatePath returned false")
	}

	e, _ := idx.ByPageID("100")
	if e.LocalPath != "docs/new.md" {
		t.Errorf("path not updated: %q", e.LocalPath)
	}

	// Old path gone, new path works.
	_, ok := idx.ByPath("docs/old.md")
	if ok {
		t.Error("old path should not resolve")
	}
	e, ok = idx.ByPath("docs/new.md")
	if !ok || e.PageID != "100" {
		t.Error("new path should resolve")
	}

	// Non-existent page.
	if idx.UpdatePath("999", "x") {
		t.Error("UpdatePath should return false for non-existent page")
	}
}

func TestUpdateTitle(t *testing.T) {
	idx := New()
	idx.Add(Entry{PageID: "100", Title: "Old", LocalPath: "docs/p.md"})

	if !idx.UpdateTitle("100", "New Title") {
		t.Fatal("UpdateTitle returned false")
	}
	e, _ := idx.ByPageID("100")
	if e.Title != "New Title" {
		t.Errorf("title not updated: %q", e.Title)
	}

	if idx.UpdateTitle("999", "x") {
		t.Error("UpdateTitle should return false for non-existent page")
	}
}

func TestUpdateParent(t *testing.T) {
	idx := New()
	idx.Add(Entry{PageID: "200", Title: "X", LocalPath: "docs/x.md", ParentPageID: "100"})

	if !idx.UpdateParent("200", "300") {
		t.Fatal("UpdateParent returned false")
	}
	e, _ := idx.ByPageID("200")
	if e.ParentPageID != "300" {
		t.Errorf("parent not updated: %q", e.ParentPageID)
	}

	if idx.UpdateParent("999", "x") {
		t.Error("UpdateParent should return false for non-existent page")
	}
}

func TestEntries_ReturnsCopy(t *testing.T) {
	idx := New()
	idx.Add(Entry{PageID: "100", Title: "A", LocalPath: "a.md"})

	entries := idx.Entries()
	entries[0].Title = "mutated"

	e, _ := idx.ByPageID("100")
	if e.Title == "mutated" {
		t.Error("Entries should return copies, not internal pointers")
	}
}

func TestPageIDs(t *testing.T) {
	idx := New()
	idx.Add(Entry{PageID: "100", LocalPath: "a.md"})
	idx.Add(Entry{PageID: "200", LocalPath: "b.md"})

	ids := idx.PageIDs()
	if len(ids) != 2 {
		t.Fatalf("expected 2 IDs, got %d", len(ids))
	}
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	if !seen["100"] || !seen["200"] {
		t.Errorf("missing IDs: %v", ids)
	}
}

// --- Load / Save roundtrip --------------------------------------------------

func TestLoadSave_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")

	// Build and save.
	idx := New()
	idx.Add(Entry{PageID: "100", Title: "Root Page", LocalPath: "docs/index.md"})
	idx.Add(Entry{PageID: "200", Title: "Architecture", LocalPath: "docs/architecture/index.md", ParentPageID: "100"})
	idx.Add(Entry{PageID: "300", Title: "DB Design", LocalPath: "docs/architecture/db-design.md", ParentPageID: "200"})

	if err := idx.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Reload.
	idx2, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if idx2.Size() != 3 {
		t.Fatalf("loaded size: got %d, want 3", idx2.Size())
	}

	// Verify entries.
	e, ok := idx2.ByPageID("200")
	if !ok {
		t.Fatal("page 200 not found after reload")
	}
	if e.Title != "Architecture" || e.LocalPath != "docs/architecture/index.md" || e.ParentPageID != "100" {
		t.Errorf("page 200 after reload: %+v", e)
	}

	// Root has no parent — omitempty should omit the field.
	e, _ = idx2.ByPageID("100")
	if e.ParentPageID != "" {
		t.Errorf("root parent after reload: %q", e.ParentPageID)
	}
}

func TestSave_DeterministicOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")

	idx := New()
	// Add in reverse path order to verify Save sorts.
	idx.Add(Entry{PageID: "300", Title: "Z", LocalPath: "docs/z.md"})
	idx.Add(Entry{PageID: "100", Title: "A", LocalPath: "docs/a.md"})
	idx.Add(Entry{PageID: "200", Title: "M", LocalPath: "docs/m.md"})

	if err := idx.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, _ := os.ReadFile(path)
	var f indexFile
	json.Unmarshal(data, &f)

	if len(f.Pages) != 3 {
		t.Fatalf("expected 3 pages, got %d", len(f.Pages))
	}
	if f.Pages[0].LocalPath != "docs/a.md" || f.Pages[1].LocalPath != "docs/m.md" || f.Pages[2].LocalPath != "docs/z.md" {
		t.Errorf("not sorted by path: %v, %v, %v", f.Pages[0].LocalPath, f.Pages[1].LocalPath, f.Pages[2].LocalPath)
	}
}

func TestSave_TrailingNewline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")

	idx := New()
	idx.Add(Entry{PageID: "1", Title: "R", LocalPath: "index.md"})
	idx.Save(path)

	data, _ := os.ReadFile(path)
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Error("saved file should end with newline")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/path/index.json")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")
	os.WriteFile(path, []byte("{invalid"), 0o644)

	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestLoad_NullParentPageID(t *testing.T) {
	// Verify that null parent_page_id (as documented in CLAUDE.md) reads
	// correctly as empty string.
	dir := t.TempDir()
	path := filepath.Join(dir, "index.json")
	data := `{"pages":[{"confluence_page_id":"1","confluence_title":"Root","local_path":"index.md","parent_page_id":null}]}`
	os.WriteFile(path, []byte(data), 0o644)

	idx, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	e, ok := idx.ByPageID("1")
	if !ok {
		t.Fatal("page not found")
	}
	if e.ParentPageID != "" {
		t.Errorf("null should read as empty string, got %q", e.ParentPageID)
	}
}
