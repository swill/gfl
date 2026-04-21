package tree

import "path"

// MovePhase indicates which phase of the two-phase rename protocol a
// MoveOp belongs to.
type MovePhase int

const (
	// PhaseDirect means no collision exists; the move can be executed immediately.
	PhaseDirect MovePhase = iota
	// PhaseStash is phase 1: move the file to the staging directory.
	PhaseStash
	// PhasePlace is phase 2: move the file from staging to its final path.
	PhasePlace
)

// MoveOp represents a single git mv operation within a planned rename set.
type MoveOp struct {
	Phase  MovePhase
	PageID string
	From   string
	To     string
}

// StagingDir is the directory name used for the two-phase rename stash.
// It is created and removed within a single sync; it never appears in a
// committed tree.
const StagingDir = ".confluencer-staging"

// plannedMove is an internal type for the move planner.
type plannedMove struct {
	pageID string
	from   string
	to     string
}

// PlanMoves takes the subset of changes that involve file path changes
// (RenamedInPlace, Moved, Promoted, Demoted) and returns an ordered
// sequence of git mv operations.
//
// If any move's destination matches another move's source (a collision),
// all moves use the two-phase stash-and-place protocol. Otherwise, each
// move is a single direct operation.
//
// Changes of other types are ignored.
func PlanMoves(changes []Change, localRoot string) []MoveOp {
	var moves []plannedMove
	for _, c := range changes {
		switch c.Type {
		case RenamedInPlace, Moved, Promoted, Demoted:
			if c.OldPath != "" && c.NewPath != "" && c.OldPath != c.NewPath {
				moves = append(moves, plannedMove{
					pageID: c.PageID,
					from:   c.OldPath,
					to:     c.NewPath,
				})
			}
		}
	}

	if len(moves) == 0 {
		return nil
	}

	if needsTwoPhase(moves) {
		return planTwoPhase(moves, localRoot)
	}
	return planDirect(moves)
}

// needsTwoPhase returns true if any move's destination path equals another
// move's source path, which would cause a collision if executed sequentially.
func needsTwoPhase(moves []plannedMove) bool {
	fromSet := make(map[string]bool, len(moves))
	for _, m := range moves {
		fromSet[m.from] = true
	}
	for _, m := range moves {
		if fromSet[m.to] {
			// This move's destination is another move's source.
			// Check it's not the same move (a no-op, already filtered out).
			for _, other := range moves {
				if other.from == m.to && other.pageID != m.pageID {
					return true
				}
			}
		}
	}
	return false
}

// planDirect returns one MoveOp per rename, all PhaseDirect.
func planDirect(moves []plannedMove) []MoveOp {
	ops := make([]MoveOp, len(moves))
	for i, m := range moves {
		ops[i] = MoveOp{
			Phase:  PhaseDirect,
			PageID: m.pageID,
			From:   m.from,
			To:     m.to,
		}
	}
	return ops
}

// planTwoPhase returns stash ops followed by place ops.
func planTwoPhase(moves []plannedMove, localRoot string) []MoveOp {
	staging := path.Join(localRoot, StagingDir)
	ops := make([]MoveOp, 0, len(moves)*2)

	// Phase 1: stash everything.
	for _, m := range moves {
		ops = append(ops, MoveOp{
			Phase:  PhaseStash,
			PageID: m.pageID,
			From:   m.from,
			To:     stagingPath(staging, m.pageID, m.from),
		})
	}

	// Phase 2: place from staging to final destination.
	for _, m := range moves {
		ops = append(ops, MoveOp{
			Phase:  PhasePlace,
			PageID: m.pageID,
			From:   stagingPath(staging, m.pageID, m.from),
			To:     m.to,
		})
	}

	return ops
}

// stagingPath returns the temporary path for a file during the stash phase.
// Uses the page ID as the filename to guarantee uniqueness.
func stagingPath(stagingDir, pageID, originalPath string) string {
	ext := path.Ext(originalPath)
	if ext == "" {
		ext = ".md"
	}
	return path.Join(stagingDir, pageID+ext)
}
