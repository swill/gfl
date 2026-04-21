package tree

import (
	"path"
	"strings"
)

// PromotionPath returns the index.md path that a flat .md file should be
// promoted to when it gains children.
// Example: "docs/architecture.md" → "docs/architecture/index.md"
func PromotionPath(flatPath string) string {
	base := strings.TrimSuffix(flatPath, path.Ext(flatPath))
	return path.Join(base, "index.md")
}

// DemotionPath returns the flat file path that a directory's index.md
// should be demoted to when all children are removed.
// Example: "docs/architecture/index.md" → "docs/architecture.md"
func DemotionPath(indexPath string) string {
	dir := path.Dir(indexPath) // "docs/architecture"
	parent := path.Dir(dir)    // "docs"
	slug := path.Base(dir)     // "architecture"
	return path.Join(parent, slug+".md")
}

// IsPromotable returns true if the path represents a flat .md file
// (not an index.md) that could be promoted to a directory.
func IsPromotable(localPath string) bool {
	return path.Ext(localPath) == ".md" && path.Base(localPath) != "index.md"
}

// IsDemotable returns true if the path is an index.md that could be
// demoted to a flat file. The root index.md (directly under localRoot)
// is never demotable.
func IsDemotable(localPath string) bool {
	return path.Base(localPath) == "index.md"
}

// AttachmentDir returns the attachment directory for a page at localPath.
// Both flat files and promoted files (index.md) resolve to the same
// attachment directory, so promotion does not move attachments.
//
// Examples (localRoot="docs/", attachmentsDir="docs/_attachments"):
//
//	"docs/index.md"                        → "docs/_attachments"
//	"docs/architecture/index.md"           → "docs/_attachments/architecture"
//	"docs/architecture/database-design.md" → "docs/_attachments/architecture/database-design"
func AttachmentDir(localPath, localRoot, attachmentsDir string) string {
	rel := strings.TrimPrefix(localPath, localRoot)

	if path.Base(rel) == "index.md" {
		rel = path.Dir(rel)
	} else {
		rel = strings.TrimSuffix(rel, ".md")
	}

	if rel == "." || rel == "" {
		return attachmentsDir
	}
	return path.Join(attachmentsDir, rel)
}
