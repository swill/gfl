package tree

import "path"

// ChangeType classifies a single difference between the Confluence tree
// and the local index. The types map 1:1 to the typed change set table
// in CLAUDE.md.
type ChangeType int

const (
	ContentChanged  ChangeType = iota // body differs, path unchanged
	RenamedInPlace                    // title or slug changed, same parent
	Moved                             // parent page changed
	AncestorRenamed                   // path changed due to ancestor rename
	Promoted                          // flat file → directory/index.md (gained children)
	Demoted                           // directory/index.md → flat file (lost all children)
	Created                           // new page not in index
	Deleted                           // page removed from Confluence (404)
	Orphaned                          // page moved outside sync scope
	MissingUnknown                    // page status could not be determined (network/5xx)
)

// String returns a human-readable name for the change type.
func (ct ChangeType) String() string {
	switch ct {
	case ContentChanged:
		return "ContentChanged"
	case RenamedInPlace:
		return "RenamedInPlace"
	case Moved:
		return "Moved"
	case AncestorRenamed:
		return "AncestorRenamed"
	case Promoted:
		return "Promoted"
	case Demoted:
		return "Demoted"
	case Created:
		return "Created"
	case Deleted:
		return "Deleted"
	case Orphaned:
		return "Orphaned"
	case MissingUnknown:
		return "MissingUnknown"
	default:
		return "unknown"
	}
}

// Change describes a single difference between the Confluence tree and
// the local index. Every page that differs produces exactly one Change.
type Change struct {
	Type            ChangeType
	PageID          string
	Title           string // Confluence title (or index title for Deleted/Orphaned/MissingUnknown)
	OldPath         string // from index; empty for Created
	NewPath         string // expected path from PathMap; empty for Deleted/Orphaned/MissingUnknown
	OldParentPageID string
	NewParentPageID string
}

// IndexEntry holds the subset of a page's index record that Diff needs.
// The index package produces these; the tree package consumes them.
type IndexEntry struct {
	PageID       string
	Title        string
	LocalPath    string
	ParentPageID string
}

// MissingStatus describes why an indexed page is absent from the
// Confluence tree.
type MissingStatus int

const (
	StatusDeleted  MissingStatus = iota // 404 — page was deleted
	StatusOrphaned                      // page exists but ancestry is outside sync scope
	StatusUnknown                       // network or 5xx error
)

// MissingPage pairs a page ID with its resolved status. The orchestrator
// resolves these by issuing direct GET /content/{id} for each page that
// appears in the index but not in the fetched tree.
type MissingPage struct {
	PageID string
	Status MissingStatus
}

// ContentChecker determines whether a page's Confluence body matches its
// current local file content. The orchestrator implements this by running
// cf_to_md on the storage XML and comparing with the local file. The
// tree package never imports the lexer for conversion — that coupling
// stays in the orchestrator.
type ContentChecker interface {
	ContentEqual(localPath string, storageXML string) (bool, error)
}

// DiffInput bundles the inputs for Diff.
type DiffInput struct {
	Tree    *CfTree        // current Confluence page hierarchy
	Paths   *PathMap       // expected local paths computed from Tree
	Index   []IndexEntry   // last-synced index records
	Missing []MissingPage  // pages in index but absent from Tree, with resolved status
	Content ContentChecker // optional; nil skips content comparison
}

// Diff compares the current Confluence tree against the local index and
// returns a typed change set. Each page that differs produces exactly one
// Change entry. Pages that match on path and content produce no entry.
//
// The algorithm:
//  1. Walk every page in the Confluence tree. For each page:
//     - Not in index → Created.
//     - In index, path changed → classify (Moved, Promoted, Demoted,
//     RenamedInPlace, or AncestorRenamed).
//     - In index, path unchanged → check content via ContentChecker;
//     if different → ContentChanged.
//  2. Process Missing entries → Deleted, Orphaned, or MissingUnknown.
func Diff(input DiffInput) []Change {
	indexByID := make(map[string]*IndexEntry, len(input.Index))
	for i := range input.Index {
		indexByID[input.Index[i].PageID] = &input.Index[i]
	}

	var changes []Change

	// Phase 1: walk every page in the Confluence tree.
	if input.Tree != nil {
		input.Tree.Walk(func(node *CfNode) {
			ie, inIndex := indexByID[node.PageID]
			if !inIndex {
				newPath, _ := input.Paths.Path(node.PageID)
				changes = append(changes, Change{
					Type:            Created,
					PageID:          node.PageID,
					Title:           node.Title,
					NewPath:         newPath,
					NewParentPageID: node.ParentPageID,
				})
				return
			}

			expectedPath, _ := input.Paths.Path(node.PageID)

			if ie.LocalPath != expectedPath {
				changes = append(changes, classifyPathChange(node, ie, expectedPath, input.Tree))
				return
			}

			// Path unchanged — check content if a checker is provided.
			if input.Content != nil {
				equal, err := input.Content.ContentEqual(ie.LocalPath, node.Body)
				if err == nil && !equal {
					changes = append(changes, Change{
						Type:    ContentChanged,
						PageID:  node.PageID,
						Title:   node.Title,
						OldPath: ie.LocalPath,
						NewPath: expectedPath,
					})
				}
			}
		})
	}

	// Phase 2: pages in the index but absent from the tree.
	for _, mp := range input.Missing {
		ie, ok := indexByID[mp.PageID]
		if !ok {
			continue
		}
		// Guard: if the page is also in the tree, the walk already handled
		// it — skip the missing entry.
		if input.Tree != nil && input.Tree.Contains(mp.PageID) {
			continue
		}
		var ct ChangeType
		switch mp.Status {
		case StatusDeleted:
			ct = Deleted
		case StatusOrphaned:
			ct = Orphaned
		default:
			ct = MissingUnknown
		}
		changes = append(changes, Change{
			Type:    ct,
			PageID:  mp.PageID,
			Title:   ie.Title,
			OldPath: ie.LocalPath,
		})
	}

	return changes
}

// classifyPathChange determines why a page's expected path differs from
// its indexed path. The priority order prevents ambiguity when multiple
// conditions are true simultaneously (e.g. a page that moves AND gains
// children is classified as Moved, not Promoted).
func classifyPathChange(node *CfNode, ie *IndexEntry, expectedPath string, ct *CfTree) Change {
	ch := Change{
		PageID:          node.PageID,
		Title:           node.Title,
		OldPath:         ie.LocalPath,
		NewPath:         expectedPath,
		OldParentPageID: ie.ParentPageID,
		NewParentPageID: node.ParentPageID,
	}

	// 1. Parent changed → Moved (highest priority — cross-directory move,
	//    requires updating Confluence ancestors).
	if ie.ParentPageID != node.ParentPageID {
		ch.Type = Moved
		return ch
	}

	// 2. Promotion/demotion (non-root pages only — root is always index.md).
	if node.ParentPageID != "" {
		wasFlat := !isIndexMd(ie.LocalPath)
		hasChildren := ct.HasChildren(node.PageID)

		if wasFlat && hasChildren {
			ch.Type = Promoted
			return ch
		}
		if isIndexMd(ie.LocalPath) && !hasChildren {
			ch.Type = Demoted
			return ch
		}
	}

	// 3. Title changed (same parent, no structural change) → RenamedInPlace.
	if ie.Title != node.Title {
		ch.Type = RenamedInPlace
		return ch
	}

	// 4. Title and parent both unchanged but path still differs.
	//    If the parent directory changed → an ancestor was renamed.
	//    If only the filename changed → sibling collision shift (operationally
	//    a rename in place).
	if path.Dir(ie.LocalPath) != path.Dir(expectedPath) {
		ch.Type = AncestorRenamed
		return ch
	}
	ch.Type = RenamedInPlace
	return ch
}

// isIndexMd returns true if the path's filename is "index.md".
func isIndexMd(p string) bool {
	return path.Base(p) == "index.md"
}
