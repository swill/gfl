package gitutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiffRange_AddModifyDelete(t *testing.T) {
	dir := initTestRepo(t)
	base := headSHA(t, dir)

	// Add a file.
	writeFile(t, dir, "docs/page.md", "# Page\n")
	commitAll(t, dir, "add page")

	// Modify the file.
	writeFile(t, dir, "docs/page.md", "# Page v2\n")
	commitAll(t, dir, "update page")

	head := headSHA(t, dir)
	diffs, err := DiffRange(dir, base, head)
	if err != nil {
		t.Fatalf("DiffRange: %v", err)
	}

	// The range diff should show net result: one Added file.
	if len(diffs) != 1 {
		t.Fatalf("len = %d, want 1 (net add)", len(diffs))
	}
	if diffs[0].Action != ActionAdded || diffs[0].Path != "docs/page.md" {
		t.Errorf("diff: %+v", diffs[0])
	}
}

func TestDiffRange_Rename(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "docs/old-name.md", "# Old Name\n\nsome content that is long enough for rename detection\n")
	base := commitAll(t, dir, "add old")

	// Rename the file.
	os.Rename(
		filepath.Join(dir, "docs/old-name.md"),
		filepath.Join(dir, "docs/new-name.md"),
	)
	head := commitAll(t, dir, "rename")

	diffs, err := DiffRange(dir, base, head)
	if err != nil {
		t.Fatalf("DiffRange: %v", err)
	}

	var found bool
	for _, d := range diffs {
		if d.Action == ActionRenamed {
			found = true
			if d.OldPath != "docs/old-name.md" {
				t.Errorf("OldPath = %q", d.OldPath)
			}
			if d.Path != "docs/new-name.md" {
				t.Errorf("Path = %q", d.Path)
			}
		}
	}
	if !found {
		t.Errorf("expected rename in diffs: %+v", diffs)
	}
}

func TestDiffRange_Delete(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "docs/gone.md", "# Gone\n")
	base := commitAll(t, dir, "add")

	os.Remove(filepath.Join(dir, "docs/gone.md"))
	head := commitAll(t, dir, "delete")

	diffs, err := DiffRange(dir, base, head)
	if err != nil {
		t.Fatalf("DiffRange: %v", err)
	}

	if len(diffs) != 1 || diffs[0].Action != ActionDeleted {
		t.Errorf("expected delete: %+v", diffs)
	}
	if diffs[0].Path != "docs/gone.md" {
		t.Errorf("Path = %q", diffs[0].Path)
	}
}

func TestDiffCommit(t *testing.T) {
	dir := initTestRepo(t)

	writeFile(t, dir, "docs/page.md", "# Page\n")
	writeFile(t, dir, "config.json", `{"key":"val"}`)
	sha := commitAll(t, dir, "add files")

	diffs, err := DiffCommit(dir, sha)
	if err != nil {
		t.Fatalf("DiffCommit: %v", err)
	}

	// Should include both files.
	if len(diffs) < 2 {
		t.Fatalf("expected at least 2 diffs, got %d", len(diffs))
	}

	mdDiffs := FilterMd(diffs)
	if len(mdDiffs) != 1 {
		t.Errorf("FilterMd: expected 1, got %d", len(mdDiffs))
	}
	if mdDiffs[0].Path != "docs/page.md" {
		t.Errorf("Path = %q", mdDiffs[0].Path)
	}
}

func TestDiffCommit_RootCommit(t *testing.T) {
	dir := initTestRepo(t)
	// The initial commit has README.md.
	sha := headSHA(t, dir)

	diffs, err := DiffCommit(dir, sha)
	if err != nil {
		t.Fatalf("DiffCommit root: %v", err)
	}
	if len(diffs) != 1 || diffs[0].Path != "README.md" {
		t.Errorf("root commit diffs: %+v", diffs)
	}
}

func TestFilterMd(t *testing.T) {
	diffs := []FileDiff{
		{Action: ActionAdded, Path: "docs/page.md"},
		{Action: ActionAdded, Path: "config.json"},
		{Action: ActionModified, Path: "docs/other.md"},
		{Action: ActionRenamed, OldPath: "docs/a.md", Path: "docs/b.md"},
		{Action: ActionDeleted, Path: "build.sh"},
	}

	md := FilterMd(diffs)
	if len(md) != 3 {
		t.Fatalf("len = %d, want 3", len(md))
	}
	if md[0].Path != "docs/page.md" {
		t.Errorf("md[0] = %+v", md[0])
	}
	if md[1].Path != "docs/other.md" {
		t.Errorf("md[1] = %+v", md[1])
	}
	if md[2].Action != ActionRenamed {
		t.Errorf("md[2] = %+v", md[2])
	}
}

func TestParseDiffOutput(t *testing.T) {
	output := "A\tdocs/new.md\n" +
		"M\tdocs/changed.md\n" +
		"D\tdocs/gone.md\n" +
		"R100\tdocs/old.md\tdocs/renamed.md\n" +
		"\n"

	diffs, err := parseDiffOutput(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 4 {
		t.Fatalf("len = %d, want 4", len(diffs))
	}
	if diffs[0].Action != ActionAdded || diffs[0].Path != "docs/new.md" {
		t.Errorf("diffs[0] = %+v", diffs[0])
	}
	if diffs[1].Action != ActionModified {
		t.Errorf("diffs[1] = %+v", diffs[1])
	}
	if diffs[2].Action != ActionDeleted {
		t.Errorf("diffs[2] = %+v", diffs[2])
	}
	if diffs[3].Action != ActionRenamed || diffs[3].OldPath != "docs/old.md" || diffs[3].Path != "docs/renamed.md" {
		t.Errorf("diffs[3] = %+v", diffs[3])
	}
}

func TestParseDiffOutput_Empty(t *testing.T) {
	diffs, err := parseDiffOutput("")
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs) != 0 {
		t.Errorf("expected empty, got %v", diffs)
	}
}
