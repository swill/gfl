package lexer

import (
	"strings"
	"testing"
)

func TestEncodeBlockFence_Shape(t *testing.T) {
	// Sanity: the encoded fence is a single HTML block — opens with the v1/b64
	// marker line, ends with "-->", and has only base64 in between.
	xml := `<ac:structured-macro ac:name="jira"><ac:parameter ac:name="key">PROJ-1</ac:parameter></ac:structured-macro>`
	got := EncodeBlockFence(xml)
	lines := strings.Split(got, "\n")
	if len(lines) < 3 {
		t.Fatalf("fence should have at least 3 lines, got %d:\n%s", len(lines), got)
	}
	if lines[0] != "<!-- gfl:storage:block:v1:b64" {
		t.Errorf("first line = %q", lines[0])
	}
	if lines[len(lines)-1] != "-->" {
		t.Errorf("last line = %q", lines[len(lines)-1])
	}
	for i, ln := range lines[1 : len(lines)-1] {
		if len(ln) > fenceB64Width {
			t.Errorf("body line %d longer than wrap width: len=%d", i, len(ln))
		}
		// Every body byte must be in the base64 alphabet.
		for _, r := range ln {
			ok := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
				(r >= '0' && r <= '9') || r == '+' || r == '/' || r == '='
			if !ok {
				t.Errorf("non-base64 char %q in body line %d: %q", r, i, ln)
			}
		}
	}
}

func TestEncodeDecodeBlockFence_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		xml  string
	}{
		{"empty", ""},
		{"simple macro", `<ac:structured-macro ac:name="jira"/>`},
		{"with cdata", `<ac:plain-text-body><![CDATA[some <code> here]]></ac:plain-text-body>`},
		{"contains close-comment delim", `<![CDATA[has --> inside]]>`},
		{"contains backtick fence", "<p>```not a real fence```</p>"},
		{"unicode", `<p>café — résumé 日本語 🎉</p>`},
		{"multiline storage", "<ac:layout>\n  <ac:layout-section>\n    <ac:layout-cell><p>x</p></ac:layout-cell>\n  </ac:layout-section>\n</ac:layout>"},
		{"long payload", "<p>" + strings.Repeat("abcdefghij", 500) + "</p>"},
		{"single byte", "x"},
		{"only newlines", "\n\n\n"},
		{"binary-looking bytes in xml", "\x00\x01\x02\x7f"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fenced := EncodeBlockFence(c.xml)
			got, ok := DecodeBlockFence(fenced)
			if !ok {
				t.Fatalf("DecodeBlockFence rejected fence:\n%s", fenced)
			}
			if got != c.xml {
				t.Fatalf("round-trip mismatch:\n  in:  %q\n  out: %q", c.xml, got)
			}
		})
	}
}

func TestEncodeBlockFence_ByteStable(t *testing.T) {
	// Encoding the same payload twice produces the same bytes — the round trip
	// in the round-trip lexer test relies on this property.
	xml := `<ac:structured-macro ac:name="info"><ac:rich-text-body><p>hi</p></ac:rich-text-body></ac:structured-macro>`
	a := EncodeBlockFence(xml)
	b := EncodeBlockFence(xml)
	if a != b {
		t.Fatalf("non-deterministic encoding:\nA=%s\n\nB=%s", a, b)
	}
}

func TestDecodeBlockFence_RejectsNonFences(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"plain html comment", "<!-- just a regular comment -->"},
		{"html block, not a fence", "<div>hi</div>"},
		{"wrong version", "<!-- gfl:storage:block:v0:b64\nYWJj\n-->"},
		{"wrong encoding tag", "<!-- gfl:storage:block:v1:hex\nYWJj\n-->"},
		{"missing close", "<!-- gfl:storage:block:v1:b64\nYWJj"},
		{"missing open", "YWJj\n-->"},
		{"open with trailing junk on same line", "<!-- gfl:storage:block:v1:b64 EXTRA\nYWJj\n-->"},
		{"corrupt base64", "<!-- gfl:storage:block:v1:b64\n!!!notb64!!!\n-->"},
		{"empty", ""},
		{"single line opener", "<!-- gfl:storage:block:v1:b64 -->"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, ok := DecodeBlockFence(c.in); ok {
				t.Fatalf("expected rejection, got accepted: %q", c.in)
			}
		})
	}
}

func TestDecodeBlockFence_ToleratesWhitespace(t *testing.T) {
	// Editors sometimes add a trailing newline after "-->" or pad lines with
	// trailing spaces. Decoding must remain stable across those cosmetic edits.
	xml := `<p>hello</p>`
	canonical := EncodeBlockFence(xml)

	variants := []string{
		canonical + "\n",
		canonical + "\n\n",
		strings.ReplaceAll(canonical, "\n", "\n  "), // indent body lines
		strings.ReplaceAll(canonical, "-->", "  -->"),
		strings.ReplaceAll(canonical, fenceOpenLine, fenceOpenLine+"  "), // trailing space on open
	}
	for i, v := range variants {
		got, ok := DecodeBlockFence(v)
		if !ok {
			t.Errorf("variant %d rejected:\n%s", i, v)
			continue
		}
		if got != xml {
			t.Errorf("variant %d decoded to %q, want %q", i, got, xml)
		}
	}
}

func TestIsBlockFence(t *testing.T) {
	if !IsBlockFence(EncodeBlockFence("<p>x</p>")) {
		t.Error("real fence should be recognised")
	}
	if IsBlockFence("<!-- not a fence -->") {
		t.Error("plain comment misclassified as fence")
	}
	if IsBlockFence("") {
		t.Error("empty string misclassified as fence")
	}
	if IsBlockFence(fenceOpenLine) {
		// No newline after the marker — that's a degenerate single-line shape
		// that won't survive round-tripping; treat as non-fence.
		t.Error("opener-only string misclassified as fence")
	}
	if !IsBlockFence("  " + EncodeBlockFence("x")) {
		t.Error("leading indent should be tolerated")
	}
}

func TestEncodeBlockFence_NormaliseSurvives(t *testing.T) {
	// The fence is a single CommonMark HTML block, so Normalise must pass it
	// through unchanged. This is the key property that lets us avoid any
	// special placeholder/reinsert dance during normalisation.
	xml := `<ac:structured-macro ac:name="jira"><ac:parameter ac:name="key">PROJ-1</ac:parameter></ac:structured-macro>`
	fenced := EncodeBlockFence(xml)

	// Embed the fence in a small Markdown document so we exercise block
	// stitching and surrounding paragraphs.
	md := "Before the fence.\n\n" + fenced + "\n\nAfter the fence.\n"
	out := Normalise(md)

	if !strings.Contains(out, fenced) {
		t.Fatalf("Normalise mangled the fence.\nin:\n%s\nout:\n%s", md, out)
	}
	// And idempotent: a second pass must not change it.
	if Normalise(out) != out {
		t.Fatalf("Normalise not idempotent over fence content")
	}
	// Decoded payload still recoverable from the normalised output.
	idx := strings.Index(out, fenceOpenLine)
	if idx < 0 {
		t.Fatal("opening marker missing from normalised output")
	}
	end := strings.Index(out[idx:], fenceCloseTag)
	if end < 0 {
		t.Fatal("closing marker missing from normalised output")
	}
	got, ok := DecodeBlockFence(out[idx : idx+end+len(fenceCloseTag)])
	if !ok || got != xml {
		t.Fatalf("could not recover XML from normalised output:\nok=%v got=%q", ok, got)
	}
}

func TestEncodeBlockFence_MultipleInOneDoc(t *testing.T) {
	// Two fences in one document round-trip independently.
	a := `<ac:structured-macro ac:name="jira"/>`
	b := `<ac:structured-macro ac:name="info"><ac:rich-text-body><p>note</p></ac:rich-text-body></ac:structured-macro>`

	md := EncodeBlockFence(a) + "\n\nSome prose.\n\n" + EncodeBlockFence(b) + "\n"
	out := Normalise(md)

	if !strings.Contains(out, EncodeBlockFence(a)) {
		t.Errorf("first fence missing from normalised output")
	}
	if !strings.Contains(out, EncodeBlockFence(b)) {
		t.Errorf("second fence missing from normalised output")
	}
}
