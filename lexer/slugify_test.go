package lexer

import (
	"strings"
	"testing"
)

func TestSlugify_Basic(t *testing.T) {
	cases := []struct {
		name  string
		title string
		want  string
	}{
		{"simple", "Architecture", "architecture"},
		{"two words", "Database Design", "database-design"},
		{"mixed case", "API Reference", "api-reference"},
		{"multiple spaces", "Hello    World", "hello-world"},
		{"tabs and newlines", "Hello\tWorld\nAgain", "hello-world-again"},
		{"leading trailing space", "  padded  ", "padded"},
		{"punctuation stripped", "What's new?!", "whats-new"},
		{"slash and parens", "Notes (draft) / v2", "notes-draft-v2"},
		{"colon and semicolon", "Scope: goals; non-goals", "scope-goals-non-goals"},
		{"underscores become hyphens", "snake_case_name", "snake-case-name"},
		{"existing hyphens preserved", "already-hyphenated", "already-hyphenated"},
		{"collapse consecutive hyphens from input", "a---b", "a-b"},
		{"strip leading and trailing hyphens", "---hello---", "hello"},
		{"digits are kept", "Release 2026", "release-2026"},
		{"digits-only", "2026", "2026"},
		{"emoji dropped", "Launch 🚀 Plan", "launch-plan"},
		{"punctuation around words", "!!!Boom!!!", "boom"},
		{"slash between words", "Docs / Ops", "docs-ops"},
		{"hyphens and spaces mixed", "foo - bar", "foo-bar"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Slugify(c.title, "0"); got != c.want {
				t.Fatalf("Slugify(%q) = %q, want %q", c.title, got, c.want)
			}
		})
	}
}

func TestSlugify_EmptyFallback(t *testing.T) {
	// All non-latin title that gets fully stripped falls back to page-<id>.
	if got := Slugify("日本語", "123456"); got != "page-123456" {
		t.Fatalf("non-latin fallback: got %q, want %q", got, "page-123456")
	}
	if got := Slugify("!!!", "42"); got != "page-42" {
		t.Fatalf("punctuation-only fallback: got %q, want %q", got, "page-42")
	}
	// No page ID and empty result → empty string (caller must handle).
	if got := Slugify("!!!", ""); got != "" {
		t.Fatalf("no pageID empty fallback: got %q, want empty", got)
	}
}

func TestSlugify_Idempotent(t *testing.T) {
	// Slugify(Slugify(x)) == Slugify(x) — the slug form is a fixed point.
	inputs := []string{
		"Architecture",
		"Database Design",
		"What's new?",
		"   Leading trailing   ",
		"a---b---c",
		"___already_snake___",
	}
	for _, in := range inputs {
		once := Slugify(in, "1")
		twice := Slugify(once, "1")
		if once != twice {
			t.Errorf("not idempotent for %q: once=%q twice=%q", in, once, twice)
		}
	}
}

func TestReverseSlugify(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"simple .md", "architecture.md", "Architecture"},
		{"two words .md", "database-design.md", "Database Design"},
		{"no .md", "api-reference", "Api Reference"},
		{"single word", "notes", "Notes"},
		{"numbers kept", "release-2026", "Release 2026"},
		{"with collision suffix", "database-design-100042.md", "Database Design"},
		{"suffix too short — not stripped", "foo-12345.md", "Foo 12345"},
		{"suffix too long — not stripped", "foo-1234567.md", "Foo 1234567"},
		{"suffix non-digit — not stripped", "foo-12a456.md", "Foo 12a456"},
		{"underscores become spaces", "snake_case.md", "Snake Case"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ReverseSlugify(c.in); got != c.want {
				t.Fatalf("ReverseSlugify(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestSlugifyReverse_SlugFixedPoint(t *testing.T) {
	// Slugify(ReverseSlugify(Slugify(x))) == Slugify(x). In other words, a
	// slug survives a round trip through its reverse form. This is the actual
	// guarantee the push path relies on.
	inputs := []string{
		"Architecture",
		"Database Design",
		"API Reference",
		"notes (draft)",
		"Scope: goals; non-goals",
		"Release 2026",
	}
	for _, in := range inputs {
		slug := Slugify(in, "1")
		reversed := ReverseSlugify(slug)
		again := Slugify(reversed, "1")
		if slug != again {
			t.Errorf("slug round trip mismatch: in=%q slug=%q reversed=%q again=%q", in, slug, reversed, again)
		}
	}
}

func TestDisambiguateSiblings_NoCollisions(t *testing.T) {
	sibs := []PageRef{
		{"100", "Architecture"},
		{"101", "Onboarding"},
		{"102", "API Reference"},
	}
	got := DisambiguateSiblings(sibs)
	want := map[string]string{
		"100": "architecture",
		"101": "onboarding",
		"102": "api-reference",
	}
	assertStringMapEqual(t, got, want)
}

func TestDisambiguateSiblings_TwoWayCollision(t *testing.T) {
	// "Database Design" and "Database-Design" both slugify to database-design.
	// Lower page ID (100000) wins the plain slug; higher (100042) gets suffixed.
	sibs := []PageRef{
		{"100042", "Database-Design"},
		{"100000", "Database Design"},
	}
	got := DisambiguateSiblings(sibs)
	want := map[string]string{
		"100000": "database-design",
		"100042": "database-design-100042",
	}
	assertStringMapEqual(t, got, want)
}

func TestDisambiguateSiblings_ManyWayCollision(t *testing.T) {
	// Four siblings whose titles all slugify to "foo".
	sibs := []PageRef{
		{"5", "Foo"},
		{"3", "foo"},
		{"7", "FOO"},
		{"9", "  Foo  "}, // leading/trailing whitespace — still slugifies to "foo"
	}
	got := DisambiguateSiblings(sibs)
	// Canonical winner: lowest numeric page ID = "3".
	if got["3"] != "foo" {
		t.Fatalf("canonical winner got %q, want %q", got["3"], "foo")
	}
	// All others get the -<last-6-digits> suffix.
	for _, id := range []string{"5", "7", "9"} {
		want := "foo-00000" + id
		if got[id] != want {
			t.Errorf("sibling %s got %q, want %q", id, got[id], want)
		}
	}
}

func TestDisambiguateSiblings_StableAcrossSiblingRename(t *testing.T) {
	// If sibling 100042 gets renamed such that it no longer collides, the
	// canonical winner's slug is unchanged — stability is the whole point of
	// the lowest-ID-wins rule.
	before := DisambiguateSiblings([]PageRef{
		{"100000", "Database Design"},
		{"100042", "Database-Design"},
	})
	after := DisambiguateSiblings([]PageRef{
		{"100000", "Database Design"},
		{"100042", "Query Language"},
	})
	if before["100000"] != after["100000"] {
		t.Fatalf("winner slug changed after sibling rename: before=%q after=%q",
			before["100000"], after["100000"])
	}
}

func TestDisambiguateSiblings_ShortPageIDPadding(t *testing.T) {
	// A colliding sibling with a page ID shorter than the suffix length must
	// still receive a 6-digit suffix (zero-padded).
	sibs := []PageRef{
		{"1", "Foo"},
		{"42", "foo"},
	}
	got := DisambiguateSiblings(sibs)
	if got["1"] != "foo" {
		t.Fatalf("canonical = %q, want %q", got["1"], "foo")
	}
	if got["42"] != "foo-000042" {
		t.Fatalf("short-ID sibling = %q, want %q", got["42"], "foo-000042")
	}
}

func TestDisambiguateSiblings_EmptyAndSingleton(t *testing.T) {
	if got := DisambiguateSiblings(nil); len(got) != 0 {
		t.Fatalf("nil input should produce empty map, got %v", got)
	}
	single := DisambiguateSiblings([]PageRef{{"7", "Notes"}})
	if single["7"] != "notes" {
		t.Fatalf("singleton: got %q, want %q", single["7"], "notes")
	}
}

func TestDisambiguateSiblings_NonNumericPageID(t *testing.T) {
	// Cloud APIs always return numeric page IDs, but the function must still
	// produce deterministic output for unexpected shapes.
	sibs := []PageRef{
		{"page-abc", "Foo"},
		{"page-xyz", "foo"},
	}
	got := DisambiguateSiblings(sibs)
	// Both IDs present, both distinct, neither is an empty string.
	if len(got) != 2 {
		t.Fatalf("non-numeric: got %v, want 2 entries", got)
	}
	if got["page-abc"] == "" || got["page-xyz"] == "" {
		t.Fatalf("non-numeric: empty slug produced: %v", got)
	}
	if got["page-abc"] == got["page-xyz"] {
		t.Fatalf("non-numeric: collisions not disambiguated: %v", got)
	}
	// Calling again yields the same answer (determinism).
	again := DisambiguateSiblings(sibs)
	assertStringMapEqual(t, again, got)
}

func TestTitleSlugsMatch(t *testing.T) {
	cases := []struct {
		name         string
		indexTitle   string
		filenameSlug string
		want         bool
	}{
		{"identical after slug", "API Design", "api-design", true},
		{"capitalisation only differs", "API DESIGN", "api-design", true},
		{"real rename", "API Design", "rest-api-design", false},
		{"punctuation only differs", "What's new?", "whats-new", true},
		{"unrelated titles", "Onboarding", "architecture", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TitleSlugsMatch(c.indexTitle, c.filenameSlug); got != c.want {
				t.Fatalf("TitleSlugsMatch(%q, %q) = %v, want %v",
					c.indexTitle, c.filenameSlug, got, c.want)
			}
		})
	}
}

func TestStripCollisionSuffix_Boundaries(t *testing.T) {
	// Exact boundary cases around the 6-digit rule.
	cases := []struct {
		in, want string
	}{
		{"foo-123456", "foo"},
		{"foo-000001", "foo"},
		{"foo-12345", "foo-12345"},     // 5 digits — not stripped
		{"foo-1234567", "foo-1234567"}, // 7 digits — not stripped
		{"foo-abcdef", "foo-abcdef"},   // not digits
		{"foo", "foo"},                 // no suffix at all
		{"123456", "123456"},           // too short to have "word-DDDDDD"
		{"-123456", "-123456"},         // just-suffix doesn't count (empty base)
	}
	for _, c := range cases {
		if got := stripCollisionSuffix(c.in); got != c.want {
			t.Errorf("stripCollisionSuffix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSlugify_NeverProducesInvalidFilenameChars(t *testing.T) {
	// Property: for any input, every rune in the slug is a-z, 0-9, '-', or '_',
	// and the slug has no leading or trailing hyphen.
	inputs := []string{
		"Hello, World!",
		"Café résumé",
		"Line1\nLine2",
		"🎉 Party Time 🎊",
		"/foo/bar/baz",
		"<script>",
		"Mix  of\t whitespace",
		"-leading-and-trailing-",
		strings.Repeat("-", 50) + "middle" + strings.Repeat("-", 50),
	}
	for _, in := range inputs {
		got := Slugify(in, "1")
		for i, r := range got {
			ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
			if !ok {
				t.Errorf("invalid char %q at position %d in slug %q (input %q)", r, i, got, in)
			}
		}
		if strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") {
			t.Errorf("slug has leading/trailing hyphen: %q (input %q)", got, in)
		}
		if strings.Contains(got, "--") {
			t.Errorf("slug has consecutive hyphens: %q (input %q)", got, in)
		}
	}
}

func assertStringMapEqual(t *testing.T, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("map size: got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("key %q: got %q, want %q", k, got[k], v)
		}
	}
}
