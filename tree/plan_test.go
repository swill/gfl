package tree

import (
	"testing"
)

func TestPlanMoves_NilForEmpty(t *testing.T) {
	ops := PlanMoves(nil, "docs/")
	if ops != nil {
		t.Errorf("expected nil, got %v", ops)
	}
}

func TestPlanMoves_IgnoresNonMoveTypes(t *testing.T) {
	changes := []Change{
		{Type: ContentChanged, PageID: "1", OldPath: "docs/a.md", NewPath: "docs/a.md"},
		{Type: Created, PageID: "2", NewPath: "docs/b.md"},
		{Type: Deleted, PageID: "3", OldPath: "docs/c.md"},
		{Type: AncestorRenamed, PageID: "4", OldPath: "docs/d.md", NewPath: "docs/e.md"},
	}
	ops := PlanMoves(changes, "docs/")
	if ops != nil {
		t.Errorf("expected nil, got %v", ops)
	}
}

func TestPlanMoves_DirectRename(t *testing.T) {
	changes := []Change{
		{Type: RenamedInPlace, PageID: "1", OldPath: "docs/old.md", NewPath: "docs/new.md"},
	}
	ops := PlanMoves(changes, "docs/")
	if len(ops) != 1 {
		t.Fatalf("len = %d, want 1", len(ops))
	}
	if ops[0].Phase != PhaseDirect {
		t.Errorf("phase = %d, want PhaseDirect", ops[0].Phase)
	}
	if ops[0].From != "docs/old.md" || ops[0].To != "docs/new.md" {
		t.Errorf("op = %+v", ops[0])
	}
	if ops[0].PageID != "1" {
		t.Errorf("PageID = %q", ops[0].PageID)
	}
}

func TestPlanMoves_MultipleNonColliding(t *testing.T) {
	changes := []Change{
		{Type: RenamedInPlace, PageID: "1", OldPath: "docs/a.md", NewPath: "docs/b.md"},
		{Type: Moved, PageID: "2", OldPath: "docs/c.md", NewPath: "docs/sub/c.md"},
	}
	ops := PlanMoves(changes, "docs/")
	if len(ops) != 2 {
		t.Fatalf("len = %d, want 2", len(ops))
	}
	for _, op := range ops {
		if op.Phase != PhaseDirect {
			t.Errorf("expected direct phase, got %d", op.Phase)
		}
	}
}

func TestPlanMoves_TwoPhaseOnCollision(t *testing.T) {
	// A→B and B→C: B is both a source and a destination.
	changes := []Change{
		{Type: RenamedInPlace, PageID: "1", OldPath: "docs/a.md", NewPath: "docs/b.md"},
		{Type: RenamedInPlace, PageID: "2", OldPath: "docs/b.md", NewPath: "docs/c.md"},
	}
	ops := PlanMoves(changes, "docs/")

	// Should be 4 ops: 2 stash + 2 place.
	if len(ops) != 4 {
		t.Fatalf("len = %d, want 4", len(ops))
	}

	// First half: stash phase.
	for _, op := range ops[:2] {
		if op.Phase != PhaseStash {
			t.Errorf("expected stash phase: %+v", op)
		}
	}

	// Second half: place phase.
	for _, op := range ops[2:] {
		if op.Phase != PhasePlace {
			t.Errorf("expected place phase: %+v", op)
		}
	}

	// Final destinations should be correct.
	placeOps := ops[2:]
	found := map[string]string{}
	for _, op := range placeOps {
		found[op.PageID] = op.To
	}
	if found["1"] != "docs/b.md" {
		t.Errorf("page 1 dest = %q", found["1"])
	}
	if found["2"] != "docs/c.md" {
		t.Errorf("page 2 dest = %q", found["2"])
	}
}

func TestPlanMoves_SwapCollision(t *testing.T) {
	// A→B and B→A: a swap requires two-phase.
	changes := []Change{
		{Type: RenamedInPlace, PageID: "1", OldPath: "docs/a.md", NewPath: "docs/b.md"},
		{Type: RenamedInPlace, PageID: "2", OldPath: "docs/b.md", NewPath: "docs/a.md"},
	}
	ops := PlanMoves(changes, "docs/")
	if len(ops) != 4 {
		t.Fatalf("len = %d, want 4", len(ops))
	}

	stashCount := 0
	placeCount := 0
	for _, op := range ops {
		switch op.Phase {
		case PhaseStash:
			stashCount++
		case PhasePlace:
			placeCount++
		}
	}
	if stashCount != 2 || placeCount != 2 {
		t.Errorf("stash=%d place=%d", stashCount, placeCount)
	}
}

func TestPlanMoves_Promotion(t *testing.T) {
	changes := []Change{
		{Type: Promoted, PageID: "1", OldPath: "docs/arch.md", NewPath: "docs/arch/index.md"},
	}
	ops := PlanMoves(changes, "docs/")
	if len(ops) != 1 {
		t.Fatalf("len = %d", len(ops))
	}
	if ops[0].From != "docs/arch.md" || ops[0].To != "docs/arch/index.md" {
		t.Errorf("op = %+v", ops[0])
	}
}

func TestPlanMoves_Demotion(t *testing.T) {
	changes := []Change{
		{Type: Demoted, PageID: "1", OldPath: "docs/arch/index.md", NewPath: "docs/arch.md"},
	}
	ops := PlanMoves(changes, "docs/")
	if len(ops) != 1 {
		t.Fatalf("len = %d", len(ops))
	}
	if ops[0].From != "docs/arch/index.md" || ops[0].To != "docs/arch.md" {
		t.Errorf("op = %+v", ops[0])
	}
}

func TestPlanMoves_SamePath(t *testing.T) {
	// OldPath == NewPath means no actual move; should be filtered out.
	changes := []Change{
		{Type: RenamedInPlace, PageID: "1", OldPath: "docs/same.md", NewPath: "docs/same.md"},
	}
	ops := PlanMoves(changes, "docs/")
	if ops != nil {
		t.Errorf("expected nil for no-op rename, got %v", ops)
	}
}

func TestStagingPath(t *testing.T) {
	p := stagingPath("docs/.confluencer-staging", "12345", "docs/page.md")
	if p != "docs/.confluencer-staging/12345.md" {
		t.Errorf("staging path = %q", p)
	}
}
