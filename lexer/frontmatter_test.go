package lexer

import (
	"strings"
	"testing"
)

func TestHasFrontMatter(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"---\n", true},
		{"---\nfoo: bar\n---\n", true},
		{"---\r\nfoo: bar\r\n---\r\n", true},
		{"# Heading\n", false},
		{"", false},
		{"--- not actually a fence", false},
		{"----\n", false},
	}
	for _, tc := range cases {
		if got := HasFrontMatter(tc.in); got != tc.want {
			t.Errorf("HasFrontMatter(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestExtractFrontMatter_NoneReturnsBodyAsIs(t *testing.T) {
	in := "# Heading\n\nBody.\n"
	fm, body, err := ExtractFrontMatter(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !fm.IsEmpty() {
		t.Errorf("expected empty fm, got %+v", fm)
	}
	if body != in {
		t.Errorf("body changed: got %q, want %q", body, in)
	}
}

func TestExtractFrontMatter_KnownFields(t *testing.T) {
	in := "---\nconfluence_page_id: \"5233836047\"\nconfluence_version: 12\n---\n\n# Heading\n"
	fm, body, err := ExtractFrontMatter(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if fm.PageID != "5233836047" {
		t.Errorf("PageID: got %q, want %q", fm.PageID, "5233836047")
	}
	if fm.Version != 12 {
		t.Errorf("Version: got %d, want 12", fm.Version)
	}
	if len(fm.Extra) != 0 {
		t.Errorf("Extra: got %v, want empty", fm.Extra)
	}
	if body != "# Heading\n" {
		t.Errorf("body: got %q, want %q", body, "# Heading\n")
	}
}

func TestExtractFrontMatter_BarewordPageID(t *testing.T) {
	// Forward-compat: bareword strings (no quotes) are tolerated on input.
	in := "---\nconfluence_page_id: 5233836047\n---\n\nbody\n"
	fm, _, err := ExtractFrontMatter(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if fm.PageID != "5233836047" {
		t.Errorf("PageID: got %q, want %q", fm.PageID, "5233836047")
	}
}

func TestExtractFrontMatter_PreservesUnknownFields(t *testing.T) {
	in := "---\nauthor: alice\nconfluence_page_id: \"123\"\ncategory: ops\n---\n\nbody\n"
	fm, _, err := ExtractFrontMatter(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if fm.PageID != "123" {
		t.Errorf("PageID: got %q", fm.PageID)
	}
	want := []string{"author: alice", "category: ops"}
	if len(fm.Extra) != 2 || fm.Extra[0] != want[0] || fm.Extra[1] != want[1] {
		t.Errorf("Extra: got %v, want %v", fm.Extra, want)
	}
}

func TestExtractFrontMatter_HandlesCRLF(t *testing.T) {
	in := "---\r\nconfluence_page_id: \"99\"\r\n---\r\n\r\nbody\r\n"
	fm, body, err := ExtractFrontMatter(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if fm.PageID != "99" {
		t.Errorf("PageID: got %q", fm.PageID)
	}
	if !strings.Contains(body, "\n") || strings.Contains(body, "\r") {
		t.Errorf("body should be LF-only: %q", body)
	}
}

func TestExtractFrontMatter_HandlesBOM(t *testing.T) {
	in := "\ufeff---\nconfluence_page_id: \"99\"\n---\n\nbody\n"
	fm, _, err := ExtractFrontMatter(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if fm.PageID != "99" {
		t.Errorf("PageID: got %q", fm.PageID)
	}
}

func TestExtractFrontMatter_UnclosedReturnsError(t *testing.T) {
	in := "---\nconfluence_page_id: \"99\"\n# never closed\n"
	_, _, err := ExtractFrontMatter(in)
	if err == nil {
		t.Fatal("expected error for unclosed front-matter")
	}
}

func TestExtractFrontMatter_EmptyBlock(t *testing.T) {
	in := "---\n---\n\nbody\n"
	fm, body, err := ExtractFrontMatter(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !fm.IsEmpty() {
		t.Errorf("expected empty fm, got %+v", fm)
	}
	if body != "body\n" {
		t.Errorf("body: got %q", body)
	}
}

func TestExtractFrontMatter_FrontMatterOnly(t *testing.T) {
	// File with no body at all (Confluence page exists but body is empty).
	in := "---\nconfluence_page_id: \"42\"\n---\n"
	fm, body, err := ExtractFrontMatter(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if fm.PageID != "42" {
		t.Errorf("PageID: got %q", fm.PageID)
	}
	if body != "" {
		t.Errorf("body: got %q, want empty", body)
	}
}

func TestExtractFrontMatter_VersionParseError(t *testing.T) {
	in := "---\nconfluence_version: not-a-number\n---\n"
	_, _, err := ExtractFrontMatter(in)
	if err == nil {
		t.Fatal("expected error for non-numeric version")
	}
}

func TestApplyFrontMatter_EmptyReturnsBodyUnchanged(t *testing.T) {
	body := "# Heading\n"
	got := ApplyFrontMatter(FrontMatter{}, body)
	if got != body {
		t.Errorf("got %q, want %q", got, body)
	}
}

func TestApplyFrontMatter_KnownFieldsCanonicalOrder(t *testing.T) {
	// Even though Version is set first in the literal here, canonical
	// emission puts confluence_page_id before confluence_version.
	fm := FrontMatter{Version: 7, PageID: "123"}
	got := ApplyFrontMatter(fm, "body\n")
	want := "---\nconfluence_page_id: \"123\"\nconfluence_version: 7\n---\n\nbody\n"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestApplyFrontMatter_EmptyBody(t *testing.T) {
	fm := FrontMatter{PageID: "123"}
	got := ApplyFrontMatter(fm, "")
	want := "---\nconfluence_page_id: \"123\"\n---\n"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestApplyFrontMatter_PreservesExtra(t *testing.T) {
	fm := FrontMatter{PageID: "1", Extra: []string{"author: alice", "tag: ops"}}
	got := ApplyFrontMatter(fm, "x\n")
	want := "---\nconfluence_page_id: \"1\"\nauthor: alice\ntag: ops\n---\n\nx\n"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestExtractApply_RoundTrip(t *testing.T) {
	cases := []FrontMatter{
		{},
		{PageID: "1"},
		{PageID: "5233836047", Version: 12},
		{PageID: "1", Extra: []string{"author: alice"}},
		{Version: 5, Extra: []string{"category: ops", "tag: a"}},
	}
	for _, fm := range cases {
		body := "# Heading\n\nBody.\n"
		serialised := ApplyFrontMatter(fm, body)
		fm2, body2, err := ExtractFrontMatter(serialised)
		if err != nil {
			t.Errorf("ExtractFrontMatter(%+v): %v", fm, err)
			continue
		}
		if fm2.PageID != fm.PageID || fm2.Version != fm.Version {
			t.Errorf("round-trip mismatch: %+v -> %+v", fm, fm2)
		}
		if len(fm2.Extra) != len(fm.Extra) {
			t.Errorf("Extra length mismatch: %v -> %v", fm.Extra, fm2.Extra)
			continue
		}
		for i := range fm.Extra {
			if fm.Extra[i] != fm2.Extra[i] {
				t.Errorf("Extra[%d]: %q -> %q", i, fm.Extra[i], fm2.Extra[i])
			}
		}
		// Body should round-trip identically when the front-matter is non-empty.
		// When fm is empty, ApplyFrontMatter returns body unchanged so this also holds.
		if body2 != body {
			t.Errorf("body changed: %q -> %q", body, body2)
		}
	}
}

func TestNormalise_PreservesFrontMatter(t *testing.T) {
	in := "---\nconfluence_page_id: \"123\"\nconfluence_version: 5\n---\n\n#  Heading  \n\n\nBody.\n"
	got := Normalise(in)
	// Front-matter preserved verbatim; body normalised (extra blank line collapsed,
	// trailing whitespace trimmed in heading).
	want := "---\nconfluence_page_id: \"123\"\nconfluence_version: 5\n---\n\n# Heading\n\nBody.\n"
	if got != want {
		t.Errorf("Normalise output mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestNormalise_IsIdempotentWithFrontMatter(t *testing.T) {
	in := "---\nconfluence_page_id: \"123\"\nconfluence_version: 5\n---\n\n# Heading\n\nBody.\n"
	once := Normalise(in)
	twice := Normalise(once)
	if once != twice {
		t.Errorf("not idempotent:\n once: %q\ntwice: %q", once, twice)
	}
}

func TestNormalise_FrontMatterOnly(t *testing.T) {
	in := "---\nconfluence_page_id: \"42\"\n---\n"
	got := Normalise(in)
	if got != in {
		t.Errorf("got %q, want %q", got, in)
	}
}

func TestNormalise_ReorderedFieldsCanonicalised(t *testing.T) {
	// User wrote version before page_id; canonical form swaps them.
	in := "---\nconfluence_version: 5\nconfluence_page_id: \"123\"\n---\n\nbody\n"
	got := Normalise(in)
	want := "---\nconfluence_page_id: \"123\"\nconfluence_version: 5\n---\n\nbody\n"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestNormalise_MalformedFrontMatterPassesThrough(t *testing.T) {
	// Opening --- without closing --- looks like a thematic break to goldmark.
	// Normalise should not throw; the input becomes body (best-effort).
	in := "---\nconfluence_page_id: \"123\"\n# Heading\n"
	got := Normalise(in)
	// We don't strictly require any specific output here, just that it doesn't
	// blow up and produces some deterministic result.
	if got == "" {
		t.Error("Normalise returned empty string for malformed input")
	}
	// And it should be idempotent.
	if Normalise(got) != got {
		t.Errorf("not idempotent on malformed input: %q -> %q", got, Normalise(got))
	}
}
