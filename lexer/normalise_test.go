package lexer

import (
	"strings"
	"testing"
)

// caseT is a focused before/after normalisation fixture.
type caseT struct {
	name string
	in   string
	want string
}

func runCases(t *testing.T, cases []caseT) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Normalise(c.in)
			if got != c.want {
				t.Fatalf("Normalise mismatch\ninput:    %q\nexpected: %q\ngot:      %q", c.in, c.want, got)
			}
			// Every case must also be idempotent.
			if twice := Normalise(got); twice != got {
				t.Fatalf("not idempotent:\nonce:  %q\ntwice: %q", got, twice)
			}
		})
	}
}

func TestNormalise_Empty(t *testing.T) {
	if got := Normalise(""); got != "" {
		t.Fatalf("empty input: got %q, want empty string", got)
	}
	if got := Normalise("\n\n\n"); got != "" {
		t.Fatalf("blank-only input: got %q, want empty string", got)
	}
}

func TestNormalise_LineEndings(t *testing.T) {
	runCases(t, []caseT{
		{"crlf to lf", "hello\r\nworld\r\n", "hello\\\nworld\n"},
		{"cr to lf", "hello\rworld\r", "hello\\\nworld\n"},
		{"mixed", "a\r\nb\rc\n", "a\\\nb\\\nc\n"},
	})
}

func TestNormalise_BOMStripped(t *testing.T) {
	// UTF-8 BOM: 0xEF 0xBB 0xBF
	in := string([]byte{0xEF, 0xBB, 0xBF}) + "hello\n"
	if got := Normalise(in); got != "hello\n" {
		t.Fatalf("BOM not stripped: got %q", got)
	}
}

func TestNormalise_TrailingWhitespace(t *testing.T) {
	runCases(t, []caseT{
		{"spaces at eol", "hello   \nworld   \n", "hello\\\nworld\n"},
		{"tabs at eol", "hello\t\t\nworld\t\n", "hello\\\nworld\n"},
		{"mixed", "hello \t \nworld\n", "hello\\\nworld\n"},
	})
}

func TestNormalise_EOFNewline(t *testing.T) {
	runCases(t, []caseT{
		{"no trailing newline", "hello", "hello\n"},
		{"one trailing newline", "hello\n", "hello\n"},
		{"multiple trailing newlines", "hello\n\n\n\n", "hello\n"},
	})
}

func TestNormalise_BlockSeparation(t *testing.T) {
	runCases(t, []caseT{
		{
			"multiple blank lines collapse to one",
			"# A\n\n\n\nparagraph\n",
			"# A\n\nparagraph\n",
		},
		{
			"no blank line between blocks becomes one blank",
			"# A\nparagraph\n",
			// goldmark will treat "paragraph" as part of a setext-style
			// heading continuation if unseparated — check what actually
			// happens. Expect it to be two blocks with one blank between.
			"# A\n\nparagraph\n",
		},
	})
}

func TestNormalise_Headings(t *testing.T) {
	runCases(t, []caseT{
		{"atx h1", "# Hello\n", "# Hello\n"},
		{"atx h3", "### Hello\n", "### Hello\n"},
		{"atx with closing hashes", "# Hello ###\n", "# Hello\n"},
		{"atx with extra leading spaces after hashes", "#   Hello\n", "# Hello\n"},
		{"setext h1 with =", "Hello\n=====\n", "# Hello\n"},
		{"setext h2 with -", "Hello\n-----\n", "## Hello\n"},
		{"heading with emphasis", "# *Hello* there\n", "# *Hello* there\n"},
	})
}

func TestNormalise_Emphasis(t *testing.T) {
	runCases(t, []caseT{
		{"underscore em to asterisk", "_hello_", "*hello*\n"},
		{"underscore strong to double asterisk", "__hello__", "**hello**\n"},
		{"asterisk em passthrough", "*hello*", "*hello*\n"},
		{"asterisk strong passthrough", "**hello**", "**hello**\n"},
		{"mixed in paragraph", "This _is_ **bold** text.", "This *is* **bold** text.\n"},
	})
}

func TestNormalise_Lists(t *testing.T) {
	runCases(t, []caseT{
		{
			"asterisk bullets to hyphen",
			"* one\n* two\n* three\n",
			"- one\n- two\n- three\n",
		},
		{
			"plus bullets to hyphen",
			"+ one\n+ two\n",
			"- one\n- two\n",
		},
		{
			"hyphen bullets passthrough",
			"- one\n- two\n",
			"- one\n- two\n",
		},
		{
			"ordered list renumbered to all 1.",
			"1. one\n2. two\n3. three\n",
			"1. one\n1. two\n1. three\n",
		},
		{
			"ordered list starting at non-1 still becomes 1.",
			"5. five\n6. six\n7. seven\n",
			"1. five\n1. six\n1. seven\n",
		},
	})
}

func TestNormalise_NestedLists(t *testing.T) {
	// Nested lists indent by 2 spaces per level. goldmark's default indent for
	// sublists is 2 spaces after a "- " marker.
	in := "- one\n  - one-a\n  - one-b\n- two\n"
	want := "- one\n  - one-a\n  - one-b\n- two\n"
	if got := Normalise(in); got != want {
		t.Fatalf("nested list:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestNormalise_FencedCode(t *testing.T) {
	runCases(t, []caseT{
		{
			"tilde fence to backtick",
			"~~~\nfoo\n~~~\n",
			"```\nfoo\n```\n",
		},
		{
			"language tag lowercased",
			"```Go\npackage main\n```\n",
			"```go\npackage main\n```\n",
		},
		{
			"preserve content exactly",
			"```go\nfunc main() {\n\tprintln(\"hi\")\n}\n```\n",
			"```go\nfunc main() {\n\tprintln(\"hi\")\n}\n```\n",
		},
	})
}

func TestNormalise_IndentedCodeBecomesFence(t *testing.T) {
	in := "    foo\n    bar\n"
	want := "```\nfoo\nbar\n```\n"
	if got := Normalise(in); got != want {
		t.Fatalf("indented code:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestNormalise_Links(t *testing.T) {
	runCases(t, []caseT{
		{
			"inline link passthrough",
			"[text](https://example.com)",
			"[text](https://example.com)\n",
		},
		{
			"reference link becomes inline",
			"[text][ref]\n\n[ref]: https://example.com\n",
			"[text](https://example.com)\n",
		},
		{
			"link with title",
			`[text](https://example.com "A title")`,
			"[text](https://example.com \"A title\")\n",
		},
	})
}

func TestNormalise_Images(t *testing.T) {
	runCases(t, []caseT{
		{
			"inline image",
			"![alt](image.png)",
			"![alt](image.png)\n",
		},
		{
			"image in paragraph",
			"See ![logo](logo.png) here.",
			"See ![logo](logo.png) here.\n",
		},
	})
}

func TestNormalise_Blockquote(t *testing.T) {
	runCases(t, []caseT{
		{
			"simple blockquote",
			">hello\n",
			"> hello\n",
		},
		{
			"multiline blockquote preserves line breaks",
			"> line one\n> line two\n",
			"> line one\\\n> line two\n",
		},
		{
			"multi-block blockquote keeps blank-line separator",
			"> Paragraph one.\n>\n> Paragraph two.\n",
			"> Paragraph one.\n>\n> Paragraph two.\n",
		},
	})
}

func TestNormalise_ThematicBreak(t *testing.T) {
	runCases(t, []caseT{
		{"three hyphens", "---\n", "---\n"},
		{"three asterisks", "***\n", "---\n"},
		{"three underscores", "___\n", "---\n"},
		{"spaced", "- - -\n", "---\n"},
	})
}

func TestNormalise_Strikethrough(t *testing.T) {
	runCases(t, []caseT{
		{
			"gfm strikethrough",
			"This is ~~wrong~~ text.",
			"This is ~~wrong~~ text.\n",
		},
	})
}

func TestNormalise_Tables(t *testing.T) {
	// GFM pipe table, no alignment, normal content.
	in := "| A | B |\n|---|---|\n| 1 | 2 |\n| 3 | 4 |\n"
	want := "| A | B |\n| --- | --- |\n| 1 | 2 |\n| 3 | 4 |\n"
	if got := Normalise(in); got != want {
		t.Fatalf("plain table:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestNormalise_TableAlignment(t *testing.T) {
	in := "| L | C | R |\n| :--- | :---: | ---: |\n| a | b | c |\n"
	want := "| L | C | R |\n| :--- | :---: | ---: |\n| a | b | c |\n"
	if got := Normalise(in); got != want {
		t.Fatalf("aligned table:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestNormalise_HTMLBlockPreserved(t *testing.T) {
	// The Confluence-native fence (HTML comment block) must survive verbatim.
	in := `<!-- confluencer:storage:block:v1 -->
<!--
<ac:structured-macro ac:name="jira" ac:schema-version="1">
  <ac:parameter ac:name="key">PROJ-123</ac:parameter>
</ac:structured-macro>
-->
<!-- /confluencer:storage:block -->
`
	got := Normalise(in)
	// The block should appear verbatim in the output (with the canonical
	// trailing newline).
	for _, needle := range []string{
		"<!-- confluencer:storage:block:v1 -->",
		`<ac:structured-macro ac:name="jira" ac:schema-version="1">`,
		`<ac:parameter ac:name="key">PROJ-123</ac:parameter>`,
		"</ac:structured-macro>",
		"<!-- /confluencer:storage:block -->",
	} {
		if !strings.Contains(got, needle) {
			t.Errorf("HTML block dropped %q from output:\n%s", needle, got)
		}
	}
	if !strings.HasSuffix(got, "\n") {
		t.Errorf("missing trailing newline")
	}
}

func TestNormalise_ParagraphPreservesLineBreaks(t *testing.T) {
	in := "First line.\nSecond line.\nThird line.\n"
	want := "First line.\\\nSecond line.\\\nThird line.\n"
	if got := Normalise(in); got != want {
		t.Fatalf("paragraph line breaks:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestNormalise_HardBreakBecomesBackslash(t *testing.T) {
	// "foo  \nbar" carries a hard break via two trailing spaces. After
	// preNormalise strips trailing whitespace, the hard break is dropped and
	// becomes a soft break (→ space). So this is effectively covered by
	// paragraph-join. The backslash form, however, survives:
	in := "foo\\\nbar\n"
	want := "foo\\\nbar\n"
	if got := Normalise(in); got != want {
		t.Fatalf("backslash hard break:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestNormalise_CodeSpan(t *testing.T) {
	runCases(t, []caseT{
		{
			"simple",
			"use `fmt.Sprintf` here",
			"use `fmt.Sprintf` here\n",
		},
	})
}

func TestNormalise_MixedDocument(t *testing.T) {
	// Kitchen-sink: heading + paragraph + list + code + quote + table + HR.
	in := "# Title\n\n" +
		"A paragraph with _em_ and __strong__ and `code`.\n\n" +
		"* one\n* two\n\n" +
		"```Python\nprint('hi')\n```\n\n" +
		">cite me\n\n" +
		"| A | B |\n|---|---|\n| 1 | 2 |\n\n" +
		"---\n"
	want := "# Title\n\n" +
		"A paragraph with *em* and **strong** and `code`.\n\n" +
		"- one\n- two\n\n" +
		"```python\nprint('hi')\n```\n\n" +
		"> cite me\n\n" +
		"| A | B |\n| --- | --- |\n| 1 | 2 |\n\n" +
		"---\n"
	got := Normalise(in)
	if got != want {
		t.Fatalf("mixed doc:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestNormaliseBytes(t *testing.T) {
	got := NormaliseBytes([]byte("hello\r\nworld\r\n"))
	if string(got) != "hello\\\nworld\n" {
		t.Fatalf("NormaliseBytes: got %q", string(got))
	}
}

func TestNormalise_AutoLink(t *testing.T) {
	// GFM autolinking: bare URLs → <url> angle-bracket form.
	got := Normalise("see <https://example.com> here\n")
	if !strings.Contains(got, "<https://example.com>") {
		t.Fatalf("autolink dropped: %q", got)
	}
}

func TestNormalise_TaskList(t *testing.T) {
	in := "- [ ] todo\n- [x] done\n"
	got := Normalise(in)
	if !strings.Contains(got, "[ ] todo") || !strings.Contains(got, "[x] done") {
		t.Fatalf("task list dropped: %q", got)
	}
}

func TestNormalise_LooseList(t *testing.T) {
	// Blank line between items forces loose parsing; items become paragraph
	// blocks and the output preserves the blank line between them.
	in := "- one\n\n- two\n"
	got := Normalise(in)
	want := "- one\n\n- two\n"
	if got != want {
		t.Fatalf("loose list:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestNormalise_ImageWithTitle(t *testing.T) {
	in := `![alt](img.png "a caption")`
	got := Normalise(in)
	want := "![alt](img.png \"a caption\")\n"
	if got != want {
		t.Fatalf("image title:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestNormalise_RawHTMLInline(t *testing.T) {
	in := "some <span>raw</span> html inline\n"
	got := Normalise(in)
	if !strings.Contains(got, "<span>raw</span>") {
		t.Fatalf("raw html inline dropped: %q", got)
	}
}

func TestNormalise_TableRowWithFewerCells(t *testing.T) {
	// A row missing a trailing cell should be padded to the column count.
	in := "| A | B |\n|---|---|\n| 1 |\n"
	got := Normalise(in)
	want := "| A | B |\n| --- | --- |\n| 1 |  |\n"
	if got != want {
		t.Fatalf("short row:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestNormalise_FixedPoint_Corpus(t *testing.T) {
	// Property: Normalise(Normalise(x)) == Normalise(x) for every input in the
	// corpus. The corpus deliberately includes denormalised inputs.
	corpus := []string{
		"",
		"hello",
		"# heading\n\nparagraph\n",
		"Setext\n======\n",
		"Setext2\n------\n",
		"1. a\n2. b\n3. c\n",
		"* x\n* y\n",
		"- top\n  - nested\n    - deeper\n",
		"~~~go\nfunc(){}\n~~~\n",
		"    indented\n    code\n",
		"> a quote\n> continued\n",
		"| a | b |\n|---|---|\n| 1 | 2 |\n",
		"Some _em_ and __strong__.\n",
		"A paragraph\nwith a soft break.\n",
		"![image](x.png)\n",
		"[link](https://ex.com)\n",
		"This is ~~wrong~~.\n",
		"***\n",
		"<!-- confluencer:storage:block:v1 -->\n<!-- raw -->\n<!-- /confluencer:storage:block -->\n",
	}
	for _, in := range corpus {
		once := Normalise(in)
		twice := Normalise(once)
		if once != twice {
			t.Errorf("not a fixed point\ninput: %q\nonce:  %q\ntwice: %q", in, once, twice)
		}
	}
}
