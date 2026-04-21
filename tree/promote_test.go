package tree

import "testing"

func TestPromotionPath(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"docs/architecture.md", "docs/architecture/index.md"},
		{"docs/api-reference.md", "docs/api-reference/index.md"},
		{"docs/sub/page.md", "docs/sub/page/index.md"},
	}
	for _, c := range cases {
		got := PromotionPath(c.input)
		if got != c.want {
			t.Errorf("PromotionPath(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestDemotionPath(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"docs/architecture/index.md", "docs/architecture.md"},
		{"docs/sub/page/index.md", "docs/sub/page.md"},
		{"docs/onboarding/index.md", "docs/onboarding.md"},
	}
	for _, c := range cases {
		got := DemotionPath(c.input)
		if got != c.want {
			t.Errorf("DemotionPath(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestIsPromotable(t *testing.T) {
	if !IsPromotable("docs/architecture.md") {
		t.Error("flat .md should be promotable")
	}
	if IsPromotable("docs/architecture/index.md") {
		t.Error("index.md should not be promotable")
	}
	if IsPromotable("docs/config.json") {
		t.Error("non-md should not be promotable")
	}
}

func TestIsDemotable(t *testing.T) {
	if !IsDemotable("docs/architecture/index.md") {
		t.Error("index.md should be demotable")
	}
	if IsDemotable("docs/architecture.md") {
		t.Error("flat .md should not be demotable")
	}
}

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

func TestPromotionDemotionRoundTrip(t *testing.T) {
	// Promoting and then demoting should return to the original path.
	flat := "docs/architecture.md"
	promoted := PromotionPath(flat)
	if promoted != "docs/architecture/index.md" {
		t.Fatalf("promoted = %q", promoted)
	}
	demoted := DemotionPath(promoted)
	if demoted != flat {
		t.Errorf("round-trip: %q → %q → %q", flat, promoted, demoted)
	}
}

func TestAttachmentDir_SameForFlatAndPromoted(t *testing.T) {
	// Per CLAUDE.md: flat and promoted pages use the same attachment directory.
	flat := AttachmentDir("docs/architecture.md", "docs/", "docs/_attachments")
	promoted := AttachmentDir("docs/architecture/index.md", "docs/", "docs/_attachments")
	if flat != promoted {
		t.Errorf("flat=%q promoted=%q — should match", flat, promoted)
	}
}
