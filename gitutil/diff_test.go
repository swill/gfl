package gitutil

import (
	"testing"
)

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
