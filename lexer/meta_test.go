package lexer

import (
	"strings"
	"testing"
)

func TestEncodeMeta_Empty(t *testing.T) {
	if got := EncodeMeta(nil); got != "" {
		t.Errorf("nil map: got %q, want empty", got)
	}
	if got := EncodeMeta(map[string]string{}); got != "" {
		t.Errorf("empty map: got %q, want empty", got)
	}
}

func TestEncodeMeta_Shape(t *testing.T) {
	got := EncodeMeta(map[string]string{
		"ac:width":  "1006",
		"ac:layout": "center",
	})
	// Single line (must not trigger HTML block detection if it ever
	// landed at line start; even though we always emit it after a
	// construct, defence in depth).
	if strings.Contains(got, "\n") {
		t.Errorf("meta must be single line: %q", got)
	}
	// Deterministic: keys sorted ⇒ ac:layout before ac:width.
	want := `<!--gfl:meta ac:layout="center" ac:width="1006"-->`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEncodeMeta_DeterministicOrder(t *testing.T) {
	a := EncodeMeta(map[string]string{"b": "2", "a": "1", "c": "3"})
	b := EncodeMeta(map[string]string{"c": "3", "a": "1", "b": "2"})
	if a != b {
		t.Errorf("non-deterministic ordering:\n  a=%s\n  b=%s", a, b)
	}
}

func TestEncodeMeta_EscapesValues(t *testing.T) {
	got := EncodeMeta(map[string]string{
		"title": `She said "hi" & ran`,
	})
	want := `<!--gfl:meta title="She said &quot;hi&quot; &amp; ran"-->`
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDecodeMeta_RoundTrip(t *testing.T) {
	cases := []map[string]string{
		{"target": "_blank"},
		{"ac:width": "1006", "ac:layout": "center"},
		{"class": "btn btn-primary", "rel": "noopener noreferrer"},
		{"title": `She said "hi" & ran`},
	}
	for _, in := range cases {
		encoded := EncodeMeta(in)
		got, ok := DecodeMeta(encoded)
		if !ok {
			t.Errorf("decode rejected: %q", encoded)
			continue
		}
		if len(got) != len(in) {
			t.Errorf("attribute count differs: in=%v out=%v", in, got)
		}
		for k, v := range in {
			if got[k] != v {
				t.Errorf("key %q: got %q, want %q", k, got[k], v)
			}
		}
	}
}

func TestDecodeMeta_RejectsNonMeta(t *testing.T) {
	for _, in := range []string{
		"",
		`<!-- plain comment -->`,
		`<!--gfl:storage:inline:v1:b64 abc-->`, // inline fence, not meta
		`<gfl-meta target="_blank"/>`,          // wrong shape (custom element)
		`<!--gfl:meta -->`,                     // empty body
		`<!--gfl:meta no quotes-->`,            // unparseable
	} {
		if _, ok := DecodeMeta(in); ok {
			t.Errorf("expected rejection: %q", in)
		}
	}
}

func TestIsMeta(t *testing.T) {
	if !IsMeta(`<!--gfl:meta target="_blank"-->`) {
		t.Error("real meta should be recognised")
	}
	if IsMeta(`<!-- plain comment -->`) {
		t.Error("plain comment misclassified as meta")
	}
	if IsMeta("") {
		t.Error("empty string misclassified")
	}
	if !IsMeta(`  <!--gfl:meta x="y"-->  `) {
		t.Error("surrounding whitespace should be tolerated")
	}
}

// TestMeta_NormaliseSurvives — a meta sidecar embedded in a paragraph
// (after an image or link) must pass through Normalise unchanged so
// that pull→edit→push round-trips don't churn the metadata.
func TestMeta_NormaliseSurvives(t *testing.T) {
	meta := EncodeMeta(map[string]string{"ac:width": "1006"})
	md := "![alt](path.png)" + meta + "\n"
	out := Normalise(md)
	if !strings.Contains(out, meta) {
		t.Errorf("meta mangled by Normalise.\nin:  %q\nout: %q", md, out)
	}
	if Normalise(out) != out {
		t.Errorf("Normalise not idempotent over meta sidecar")
	}
}
