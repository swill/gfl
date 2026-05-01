package lexer

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// Normalise returns md in the canonical Markdown form defined in CLAUDE.md
// under "Markdown Normalisation". It is idempotent:
//
//	Normalise(Normalise(x)) == Normalise(x)
//
// for any input x.
//
// Two stages run in sequence:
//
//  1. Text-level pre-processing: strip BOM, normalise line endings to LF,
//     strip trailing whitespace on every line. Done before the parser sees
//     the input so that line-sensitive constructs (fences, list items) are
//     parsed from a clean source.
//  2. AST parse via goldmark (with the GFM extension), then a deterministic
//     Markdown renderer walks the AST and emits the canonical form: ATX-only
//     headings, `*`/`**` emphasis, `-` bullets, `1.` ordered items, triple-
//     backtick code fences with lowercased language tags, inline links, GFM
//     pipe tables, `> ` blockquotes, `---` thematic breaks, no hard wrap.
//
// HTML blocks (including the Confluence-native fences from lexer/fence.go)
// pass through verbatim so that unsupported constructs are preserved.
func Normalise(md string) string {
	src := preNormalise([]byte(md))

	// Split off any front-matter so the body parses cleanly through goldmark
	// (otherwise the leading `---` would parse as a thematic break followed by
	// stray "key: value" paragraphs). On malformed front-matter, fall back to
	// treating the whole input as body — preserves user content; the surfaced
	// error from ExtractFrontMatter is intentionally swallowed here so that
	// Normalise stays total. Callers that need to validate front-matter should
	// call ExtractFrontMatter directly.
	fm, body, err := ExtractFrontMatter(string(src))
	if err != nil {
		fm = FrontMatter{}
		body = string(src)
	}
	bodyBytes := []byte(body)

	reader := text.NewReader(bodyBytes)
	doc := normaliserMD.Parser().Parse(reader)

	r := &mdRenderer{source: bodyBytes}
	out := r.renderDocument(doc)

	// Ensure exactly one trailing newline on the body, except when the body
	// is empty (front-matter-only file).
	out = strings.TrimRight(out, "\n")
	var bodyOut string
	if out != "" {
		bodyOut = out + "\n"
	}

	if fm.IsEmpty() {
		return bodyOut
	}
	return ApplyFrontMatter(fm, bodyOut)
}

// NormaliseBytes is the []byte convenience for Normalise.
func NormaliseBytes(md []byte) []byte {
	return []byte(Normalise(string(md)))
}

var normaliserMD = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
)

// preNormalise strips the UTF-8 BOM, converts CRLF and standalone CR to LF,
// and removes trailing whitespace on every line. The result has no trailing
// newlines; trailing-newline enforcement is the final step of Normalise.
func preNormalise(src []byte) []byte {
	// Strip UTF-8 BOM if present.
	if len(src) >= 3 && src[0] == 0xEF && src[1] == 0xBB && src[2] == 0xBF {
		src = src[3:]
	}
	// CRLF → LF; lone CR → LF.
	out := make([]byte, 0, len(src))
	for i := 0; i < len(src); i++ {
		if src[i] == '\r' {
			out = append(out, '\n')
			if i+1 < len(src) && src[i+1] == '\n' {
				i++
			}
			continue
		}
		out = append(out, src[i])
	}
	// Strip trailing whitespace on every line.
	lines := bytes.Split(out, []byte{'\n'})
	for i, line := range lines {
		lines[i] = bytes.TrimRight(line, " \t\v\f")
	}
	return bytes.Join(lines, []byte{'\n'})
}

// mdRenderer walks a goldmark AST and emits canonical Markdown. Every block
// render function returns the content for that block without a trailing
// newline; the caller stitches blocks together with exactly one blank line
// between them (per rule 5 in the Markdown Normalisation section).
type mdRenderer struct {
	source []byte
}

func (r *mdRenderer) renderDocument(doc ast.Node) string {
	var parts []string
	for c := doc.FirstChild(); c != nil; c = c.NextSibling() {
		// Link reference definitions are consumed during inlining (all
		// reference-style links have already been resolved to inline form).
		// Skip them so they don't appear as stray blocks in the output.
		if _, ok := c.(*ast.LinkReferenceDefinition); ok {
			continue
		}
		if p := r.renderBlock(c); p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "\n\n")
}

// renderBlock dispatches on block-level node types. Unknown blocks are best-
// effort rendered from their source segments.
func (r *mdRenderer) renderBlock(n ast.Node) string {
	switch node := n.(type) {
	case *ast.Heading:
		return r.renderHeading(node)
	case *ast.Paragraph:
		return r.renderParagraph(node)
	case *ast.TextBlock:
		// Loose list items wrap their text in TextBlock; render same as paragraph.
		return r.renderInline(node)
	case *ast.List:
		return r.renderList(node)
	case *ast.ListItem:
		// Not expected at top-level; assume tight when encountered bare.
		return r.renderListItemContent(node, true)
	case *ast.FencedCodeBlock:
		return r.renderFencedCode(node)
	case *ast.CodeBlock:
		return r.renderIndentedCodeAsFence(node)
	case *ast.Blockquote:
		return r.renderBlockquote(node)
	case *ast.HTMLBlock:
		return r.renderHTMLBlock(node)
	case *ast.ThematicBreak:
		return "---"
	case *extast.Table:
		return r.renderTable(node)
	default:
		// Fall back to raw source segments.
		return r.rawSource(n)
	}
}

func (r *mdRenderer) renderHeading(n *ast.Heading) string {
	hashes := strings.Repeat("#", n.Level)
	return hashes + " " + r.renderInline(n)
}

func (r *mdRenderer) renderParagraph(n *ast.Paragraph) string {
	return r.renderInline(n)
}

func (r *mdRenderer) renderList(n *ast.List) string {
	ordered := n.IsOrdered()
	tight := n.IsTight
	var items []string
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		li, ok := c.(*ast.ListItem)
		if !ok {
			continue
		}
		marker := "- "
		if ordered {
			// Normalisation rule: every ordered item starts with "1.".
			marker = "1. "
		}
		itemBody := r.renderListItemContent(li, tight)
		items = append(items, indentAfterFirst(marker+itemBody, strings.Repeat(" ", len(marker))))
	}
	sep := "\n"
	if !tight {
		sep = "\n\n"
	}
	return strings.Join(items, sep)
}

// renderListItemContent renders the children of a list item. Tight lists
// separate item-internal blocks with a single newline; loose lists use a blank
// line (per CommonMark tight/loose distinction). The returned string has no
// leading marker; the caller prepends it.
func (r *mdRenderer) renderListItemContent(li *ast.ListItem, tight bool) string {
	sep := "\n\n"
	if tight {
		sep = "\n"
	}
	var parts []string
	for c := li.FirstChild(); c != nil; c = c.NextSibling() {
		parts = append(parts, r.renderBlock(c))
	}
	return strings.Join(parts, sep)
}

// indentAfterFirst prepends `indent` to every line of s except the first.
// Used to shift the continuation lines of a list item so they align under the
// marker.
func indentAfterFirst(s, indent string) string {
	if !strings.Contains(s, "\n") {
		return s
	}
	lines := strings.Split(s, "\n")
	for i := 1; i < len(lines); i++ {
		if lines[i] == "" {
			continue // keep blank separators empty, not indented
		}
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
}

func (r *mdRenderer) renderFencedCode(n *ast.FencedCodeBlock) string {
	lang := ""
	if l := n.Language(r.source); len(l) > 0 {
		lang = strings.ToLower(string(l))
	}
	body := r.rawLines(n)
	// Drop any existing trailing newline on the body; we add one explicitly.
	body = strings.TrimRight(body, "\n")
	return "```" + lang + "\n" + body + "\n```"
}

func (r *mdRenderer) renderIndentedCodeAsFence(n *ast.CodeBlock) string {
	body := strings.TrimRight(r.rawLines(n), "\n")
	return "```\n" + body + "\n```"
}

func (r *mdRenderer) renderBlockquote(n *ast.Blockquote) string {
	if label, bodyStart, meta, ok := admonitionFromBlockquote(n, r.source); ok {
		return r.renderAdmonition(label, bodyStart, n, meta)
	}
	var parts []string
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		parts = append(parts, r.renderBlock(c))
	}
	inner := strings.Join(parts, "\n\n")
	// Prefix every line with "> ". Empty separator lines become just ">" so
	// they don't carry a trailing space (rule 3).
	lines := strings.Split(inner, "\n")
	for i, line := range lines {
		if line == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + line
		}
	}
	return strings.Join(lines, "\n")
}

// renderAdmonition emits a blockquote that's been recognised as a GFM
// admonition in the canonical form:
//
//	> [!LABEL]<optional gfl:meta sidecar>
//	> body line
//	> body line
//
// Without this special case, the default blockquote renderer emits a
// hard-break backslash between the marker line and the body content
// (because goldmark parses both as one paragraph with a soft line break,
// and the standard inline writer turns soft breaks into "\\\n"). The
// canonical form lets pull→edit→push round-trip cleanly through Normalise
// without churning the marker shape.
func (r *mdRenderer) renderAdmonition(label string, firstParaBodyStart ast.Node, bq *ast.Blockquote, meta map[string]string) string {
	var sb strings.Builder
	sb.WriteString("> [!")
	sb.WriteString(strings.ToUpper(label))
	sb.WriteByte(']')
	sb.WriteString(EncodeMeta(meta))

	var bodyParts []string

	// Inline content following the marker on subsequent lines forms the
	// first body paragraph. The default writer emits soft breaks as
	// "\\\n" — fine for ordinary paragraphs, but inside an admonition
	// body it produces a stair-step of escaped backslashes. Replace them
	// with natural newlines so the body reads as plain blockquote prose.
	if firstParaBodyStart != nil {
		var inlineSb strings.Builder
		for c := firstParaBodyStart; c != nil; c = c.NextSibling() {
			r.writeInlineNode(&inlineSb, c)
		}
		body := strings.ReplaceAll(strings.TrimRight(inlineSb.String(), "\n"), "\\\n", "\n")
		if body != "" {
			bodyParts = append(bodyParts, body)
		}
	}

	// Subsequent block children of the blockquote (extra paragraphs,
	// lists, code, ...) render normally.
	para := bq.FirstChild()
	for c := para.NextSibling(); c != nil; c = c.NextSibling() {
		if rendered := r.renderBlock(c); rendered != "" {
			bodyParts = append(bodyParts, rendered)
		}
	}

	if len(bodyParts) == 0 {
		return sb.String()
	}

	body := strings.Join(bodyParts, "\n\n")
	sb.WriteByte('\n')
	for i, line := range strings.Split(body, "\n") {
		if i > 0 {
			sb.WriteByte('\n')
		}
		if line == "" {
			sb.WriteByte('>')
		} else {
			sb.WriteString("> ")
			sb.WriteString(line)
		}
	}
	return sb.String()
}

func (r *mdRenderer) renderHTMLBlock(n *ast.HTMLBlock) string {
	// Verbatim passthrough; this is how our Confluence-native fences survive.
	// goldmark stores the line that closes the block (e.g. the line containing
	// "-->" for a type-2 comment block) in ClosureLine, separate from Lines();
	// both must be emitted to round-trip the block intact.
	body := r.rawLines(n)
	if n.HasClosure() {
		body += string(n.ClosureLine.Value(r.source))
	}
	return strings.TrimRight(body, "\n")
}

func (r *mdRenderer) renderTable(n *extast.Table) string {
	var rows [][]string
	var alignments []extast.Alignment

	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		_, isHeader := c.(*extast.TableHeader)
		var cells []string
		for cell := c.FirstChild(); cell != nil; cell = cell.NextSibling() {
			tc, ok := cell.(*extast.TableCell)
			if !ok {
				continue
			}
			cells = append(cells, r.renderInline(tc))
			if isHeader {
				alignments = append(alignments, tc.Alignment)
			}
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 || len(alignments) == 0 {
		return ""
	}

	var lines []string
	lines = append(lines, renderTableRow(rows[0], len(alignments)))

	seps := make([]string, len(alignments))
	for i, a := range alignments {
		switch a {
		case extast.AlignLeft:
			seps[i] = ":---"
		case extast.AlignRight:
			seps[i] = "---:"
		case extast.AlignCenter:
			seps[i] = ":---:"
		default:
			seps[i] = "---"
		}
	}
	lines = append(lines, renderTableRow(seps, len(alignments)))

	for _, row := range rows[1:] {
		lines = append(lines, renderTableRow(row, len(alignments)))
	}
	return strings.Join(lines, "\n")
}

// renderTableRow joins cells with " | ", wraps with "| ... |", and pads/truncates
// to match the column count. Cells are not padded to column width (per rule).
func renderTableRow(cells []string, cols int) string {
	out := make([]string, cols)
	for i := 0; i < cols; i++ {
		if i < len(cells) {
			out[i] = cells[i]
		} else {
			out[i] = ""
		}
	}
	return "| " + strings.Join(out, " | ") + " |"
}

// renderInline renders all inline children of a node. Hard line breaks and
// soft line breaks both emit "\\\n" — the backslash form preserves line
// breaks without trailing whitespace. This is intentionally more conservative
// than CommonMark (which treats soft breaks as spaces) because Confluence
// content relies on line breaks for layout, and collapsing them destroys
// the author's intent.
func (r *mdRenderer) renderInline(n ast.Node) string {
	var sb strings.Builder
	r.writeInline(&sb, n)
	return sb.String()
}

func (r *mdRenderer) writeInline(sb *strings.Builder, parent ast.Node) {
	for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
		r.writeInlineNode(sb, c)
	}
}

// writeInlineNode emits a single inline AST node. Extracted from
// writeInline so the admonition renderer (and any future caller that
// needs to iterate from a non-first sibling) can emit a partial inline
// run without rebuilding a parent.
func (r *mdRenderer) writeInlineNode(sb *strings.Builder, c ast.Node) {
	switch node := c.(type) {
	case *ast.Text:
		seg := node.Segment
		sb.Write(seg.Value(r.source))
		if node.HardLineBreak() || node.SoftLineBreak() {
			sb.WriteString("\\\n")
		}
	case *ast.String:
		sb.Write(node.Value)
	case *ast.Emphasis:
		delim := "*"
		if node.Level == 2 {
			delim = "**"
		}
		sb.WriteString(delim)
		r.writeInline(sb, node)
		sb.WriteString(delim)
	case *ast.CodeSpan:
		// Inline code content is the concatenation of child text nodes.
		sb.WriteByte('`')
		r.writeInline(sb, node)
		sb.WriteByte('`')
	case *ast.Link:
		sb.WriteByte('[')
		r.writeInline(sb, node)
		sb.WriteString("](")
		sb.Write(node.Destination)
		if len(node.Title) > 0 {
			sb.WriteString(` "`)
			sb.Write(node.Title)
			sb.WriteByte('"')
		}
		sb.WriteByte(')')
	case *ast.AutoLink:
		sb.WriteByte('<')
		sb.Write(node.URL(r.source))
		sb.WriteByte('>')
	case *ast.Image:
		sb.WriteString("![")
		r.writeInline(sb, node)
		sb.WriteString("](")
		sb.Write(node.Destination)
		if len(node.Title) > 0 {
			sb.WriteString(` "`)
			sb.Write(node.Title)
			sb.WriteByte('"')
		}
		sb.WriteByte(')')
	case *ast.RawHTML:
		for i := 0; i < node.Segments.Len(); i++ {
			seg := node.Segments.At(i)
			sb.Write(seg.Value(r.source))
		}
	case *extast.Strikethrough:
		sb.WriteString("~~")
		r.writeInline(sb, node)
		sb.WriteString("~~")
	case *extast.TaskCheckBox:
		if node.IsChecked {
			sb.WriteString("[x] ")
		} else {
			sb.WriteString("[ ] ")
		}
	default:
		// Unknown inline node — recurse to keep any child text.
		r.writeInline(sb, node)
	}
}

// rawLines collects a block's backing source lines verbatim.
func (r *mdRenderer) rawLines(n ast.Node) string {
	lines := n.Lines()
	if lines == nil || lines.Len() == 0 {
		return ""
	}
	var sb strings.Builder
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		sb.Write(seg.Value(r.source))
	}
	return sb.String()
}

// rawSource is a last-resort renderer for unrecognised block node types. It
// emits any attached source lines; if there are none, it returns a debug
// comment so the caller can see something is missing in tests.
func (r *mdRenderer) rawSource(n ast.Node) string {
	if s := r.rawLines(n); s != "" {
		return strings.TrimRight(s, "\n")
	}
	return fmt.Sprintf("<!-- gfl-normaliser: unhandled node %T -->", n)
}
