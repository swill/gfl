package tree

import "testing"

func TestAttachmentDir(t *testing.T) {
	cases := []struct {
		name           string
		localPath      string
		localRoot      string
		attachmentsDir string
		want           string
	}{
		{
			"root page",
			"docs/index.md",
			"docs/",
			"docs/_attachments",
			"docs/_attachments",
		},
		{
			"directory page",
			"docs/architecture/index.md",
			"docs/",
			"docs/_attachments",
			"docs/_attachments/architecture",
		},
		{
			"flat page",
			"docs/architecture/database-design.md",
			"docs/",
			"docs/_attachments",
			"docs/_attachments/architecture/database-design",
		},
		{
			"flat sibling",
			"docs/api-reference.md",
			"docs/",
			"docs/_attachments",
			"docs/_attachments/api-reference",
		},
		{
			"nested directory page",
			"docs/architecture/backend/index.md",
			"docs/",
			"docs/_attachments",
			"docs/_attachments/architecture/backend",
		},
		{
			"nested flat page",
			"docs/architecture/backend/auth.md",
			"docs/",
			"docs/_attachments",
			"docs/_attachments/architecture/backend/auth",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := AttachmentDir(c.localPath, c.localRoot, c.attachmentsDir)
			if got != c.want {
				t.Errorf("AttachmentDir(%q) = %q, want %q", c.localPath, got, c.want)
			}
		})
	}
}

func TestAttachmentDir_SameForFlatAndPromoted(t *testing.T) {
	// Per design: flat and promoted forms of a page use the same attachment
	// directory, so transitioning between them never moves attachments.
	flat := AttachmentDir("docs/architecture.md", "docs/", "docs/_attachments")
	promoted := AttachmentDir("docs/architecture/index.md", "docs/", "docs/_attachments")
	if flat != promoted {
		t.Errorf("flat=%q promoted=%q — should match", flat, promoted)
	}
}
