package tree

import (
	"path"
	"strings"
)

// AttachmentDir returns the per-page attachment subdirectory for a page at
// localPath. Both flat and "promoted" (index.md) forms of a page resolve to
// the same attachment directory, so converting between flat and directory
// shapes never moves attachments.
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
