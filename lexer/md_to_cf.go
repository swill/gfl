package lexer

import (
	"fmt"
	"sort"
	"strings"

	"github.com/yuin/goldmark/ast"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

// Markdown → Confluence storage XML.
//
// This is the push-direction counterpart of CfToMd. The input is normalised
// Markdown (see Normalise); the output is well-formed Confluence storage XML
// suitable for body.storage.value on a PUT/POST to /content.
//
// The conversion:
//
//  1. Parse the Markdown through the same goldmark pipeline used by Normalise
//     (GFM extension enabled) so tables, strikethrough, task lists, and
//     autolinks are recognised.
//  2. Walk the AST, emitting storage XML per the mapping table in CLAUDE.md.
//     Block-level and inline dispatch are split so paragraph boundaries and
//     inline runs each get the right wrapping.
//  3. HTML blocks that match the v1/b64 Confluence-native fence are decoded
//     and the original storage XML is spliced back verbatim. Any other HTML
//     block is wrapped in <p> with its contents escaped as plain text —
//     CLAUDE.md explicitly disallows emitting <ac:structured-macro
//     ac:name="html"> since many Confluence Cloud instances disable it.

// MdPageResolver maps a local Markdown link target (a path like
// "../architecture.md") to the Confluence page reference that should appear
// in <ri:page>. Returning ok=false falls back to an external <a href> with
// the original target. The target is already resolved relative to the source
// document's directory by the time the resolver sees it — resolvers never
// need to know which file the link came from.
type MdPageResolver interface {
	ResolveLink(target string) (title, space string, ok bool)
}

// MdAttachmentResolver maps a Markdown image src (a path like
// "../_attachments/architecture/diagram.png") to a Confluence attachment
// filename. Returning ok=false causes the converter to emit <ri:url> with the
// original src — appropriate for external image URLs.
type MdAttachmentResolver interface {
	ResolveImage(src string) (filename string, ok bool)
}

// MdToCfOpts bundles optional resolvers. Both are nil-safe; missing resolvers
// cause the converter to fall back to external-link / external-image output
// so nothing is silently dropped.
type MdToCfOpts struct {
	Pages       MdPageResolver
	Attachments MdAttachmentResolver
}

// MdToCf converts a canonical-form Markdown string to Confluence storage XML.
// The returned string is ready to place in body.storage.value.
//
// The function does not normalise input; callers that need round-trip
// stability should run Normalise first (in practice the push path reads the
// file directly, and the file is already canonical because every write goes
// through Normalise).
func MdToCf(md string, opts MdToCfOpts) (string, error) {
	src := []byte(md)
	reader := text.NewReader(src)
	doc := normaliserMD.Parser().Parse(reader)

	w := &mdToCfWriter{source: src, opts: opts}
	w.writeBlocks(doc)
	return w.sb.String(), nil
}

// mdToCfWriter holds converter state. It's a fresh instance per MdToCf call.
type mdToCfWriter struct {
	sb     strings.Builder
	source []byte
	opts   MdToCfOpts
}

// writeBlocks walks the immediate children of a block-container node and
// emits each as storage XML. Storage XML does not require inter-block
// separators (it's a DOM, not a line-based format), so blocks are written
// flush against each other with no whitespace between them — deterministic
// output is the goal, not diffability.
func (w *mdToCfWriter) writeBlocks(parent ast.Node) {
	for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
		if _, ok := c.(*ast.LinkReferenceDefinition); ok {
			// Link reference definitions are metadata; they've already been
			// applied to Link nodes by goldmark. Don't emit them.
			continue
		}
		w.writeBlock(c)
	}
}

func (w *mdToCfWriter) writeBlock(n ast.Node) {
	switch node := n.(type) {
	case *ast.Heading:
		fmt.Fprintf(&w.sb, "<h%d>", node.Level)
		w.writeInline(node)
		fmt.Fprintf(&w.sb, "</h%d>", node.Level)
	case *ast.Paragraph:
		w.sb.WriteString("<p>")
		w.writeInline(node)
		w.sb.WriteString("</p>")
	case *ast.TextBlock:
		// TextBlock is the inline container inside a loose list item — it
		// doesn't wrap itself in <p>. Callers that expect paragraph wrapping
		// should use Paragraph instead.
		w.writeInline(node)
	case *ast.List:
		w.writeList(node)
	case *ast.FencedCodeBlock:
		w.writeFencedCode(node)
	case *ast.CodeBlock:
		w.writeIndentedCode(node)
	case *ast.Blockquote:
		if label, bodyStart, meta, ok := admonitionFromBlockquote(node, w.source); ok {
			w.writeAdmonition(label, bodyStart, node, meta)
			return
		}
		w.sb.WriteString("<blockquote>")
		w.writeBlocks(node)
		w.sb.WriteString("</blockquote>")
	case *ast.ThematicBreak:
		w.sb.WriteString("<hr/>")
	case *ast.HTMLBlock:
		w.writeHTMLBlock(node)
	case *extast.Table:
		w.writeTable(node)
	default:
		// Unknown block — fall back to rendering children inline so nothing
		// is lost. This path is hit for extension nodes we haven't mapped.
		w.writeInline(n)
	}
}

// writeList emits <ul> or <ol>. Task-list items (GFM) render as a Confluence
// ac:task-list rather than a bullet list — Confluence has a native task macro
// and that's what a pull would produce, so a round trip should preserve it.
func (w *mdToCfWriter) writeList(n *ast.List) {
	if listIsTaskList(n) {
		w.writeTaskList(n)
		return
	}
	tag := "ul"
	if n.IsOrdered() {
		tag = "ol"
	}
	fmt.Fprintf(&w.sb, "<%s>", tag)
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		li, ok := c.(*ast.ListItem)
		if !ok {
			continue
		}
		w.sb.WriteString("<li>")
		w.writeListItemContent(li, n.IsTight)
		w.sb.WriteString("</li>")
	}
	fmt.Fprintf(&w.sb, "</%s>", tag)
}

// listIsTaskList reports whether every item in n begins with a GFM task
// checkbox. A mixed list (checkbox + plain items) is treated as a bullet list
// with rendered checkboxes inline, not a Confluence task macro.
func listIsTaskList(n *ast.List) bool {
	any := false
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		li, ok := c.(*ast.ListItem)
		if !ok {
			continue
		}
		if !listItemHasTaskCheckbox(li) {
			return false
		}
		any = true
	}
	return any
}

// listItemHasTaskCheckbox reports whether li's first inline child is a GFM
// task checkbox. Only the very first inline position matters — that's where
// parsers emit the checkbox for "- [ ] ..." items.
func listItemHasTaskCheckbox(li *ast.ListItem) bool {
	c := li.FirstChild()
	if c == nil {
		return false
	}
	first := c.FirstChild()
	if first == nil {
		return false
	}
	_, ok := first.(*extast.TaskCheckBox)
	return ok
}

// writeTaskList renders a list whose every item starts with a checkbox as a
// Confluence ac:task-list macro.
func (w *mdToCfWriter) writeTaskList(n *ast.List) {
	w.sb.WriteString("<ac:task-list>")
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		li, ok := c.(*ast.ListItem)
		if !ok {
			continue
		}
		w.writeTask(li)
	}
	w.sb.WriteString("</ac:task-list>")
}

func (w *mdToCfWriter) writeTask(li *ast.ListItem) {
	checked := false
	// Walk the first inline container and find the checkbox; emit the rest
	// of the inline content as the task body.
	var bodyStart ast.Node
	if c := li.FirstChild(); c != nil {
		if box, ok := c.FirstChild().(*extast.TaskCheckBox); ok {
			checked = box.IsChecked
			bodyStart = c
		}
	}
	status := "incomplete"
	if checked {
		status = "complete"
	}
	w.sb.WriteString("<ac:task>")
	fmt.Fprintf(&w.sb, "<ac:task-status>%s</ac:task-status>", status)
	w.sb.WriteString("<ac:task-body>")
	if bodyStart != nil {
		// Skip the TaskCheckBox inline child; render the rest.
		for c := bodyStart.FirstChild(); c != nil; c = c.NextSibling() {
			if _, ok := c.(*extast.TaskCheckBox); ok {
				continue
			}
			w.writeInlineNode(c)
		}
	}
	w.sb.WriteString("</ac:task-body>")
	w.sb.WriteString("</ac:task>")
}

// writeListItemContent emits the contents of an <li>. Tight list items wrap
// a single TextBlock (emit inline content directly); loose items wrap one or
// more block children (including Paragraph) and we emit each as a block. The
// tight-vs-loose parse comes from goldmark; storage XML doesn't carry the
// distinction explicitly, but loose items surrounded by <p> visibly space
// themselves in the rendered page, which matches the Markdown intent.
func (w *mdToCfWriter) writeListItemContent(li *ast.ListItem, tight bool) {
	for c := li.FirstChild(); c != nil; c = c.NextSibling() {
		if tight {
			if _, ok := c.(*ast.TextBlock); ok {
				w.writeInline(c)
				continue
			}
		}
		w.writeBlock(c)
	}
}

func (w *mdToCfWriter) writeFencedCode(n *ast.FencedCodeBlock) {
	lang := ""
	if l := n.Language(w.source); len(l) > 0 {
		lang = strings.ToLower(string(l))
	}
	// goldmark's Lines() includes the source's trailing newline on the last
	// line of the code body. Confluence preserves the CDATA contents
	// byte-for-byte, so leaving that newline in place adds a phantom blank
	// line at the end of every code block on push. cf_to_md already strips
	// trailing newlines on the way out (via TrimRight); we have to mirror
	// that on the way in to keep the round trip clean.
	body := strings.TrimRight(readLines(n, w.source), "\n")
	w.sb.WriteString(`<ac:structured-macro ac:name="code">`)
	if lang != "" {
		fmt.Fprintf(&w.sb, `<ac:parameter ac:name="language">%s</ac:parameter>`, xmlEscape(lang))
	}
	w.sb.WriteString(`<ac:plain-text-body>`)
	w.sb.WriteString(wrapCDATA(body))
	w.sb.WriteString(`</ac:plain-text-body>`)
	w.sb.WriteString(`</ac:structured-macro>`)
}

func (w *mdToCfWriter) writeIndentedCode(n *ast.CodeBlock) {
	body := strings.TrimRight(readLines(n, w.source), "\n")
	w.sb.WriteString(`<ac:structured-macro ac:name="code">`)
	w.sb.WriteString(`<ac:plain-text-body>`)
	w.sb.WriteString(wrapCDATA(body))
	w.sb.WriteString(`</ac:plain-text-body>`)
	w.sb.WriteString(`</ac:structured-macro>`)
}

func (w *mdToCfWriter) writeHTMLBlock(n *ast.HTMLBlock) {
	raw := readLines(n, w.source)
	if n.HasClosure() {
		raw += string(n.ClosureLine.Value(w.source))
	}
	// Is this one of our Confluence-native fences? If so, splice the original
	// storage XML back in verbatim — this is the whole point of the fence.
	if xml, ok := DecodeBlockFence(raw); ok {
		w.sb.WriteString(xml)
		return
	}
	// An inline fence alone on its own line is parsed as an HTML block
	// (CommonMark Type 7: a complete custom-element tag followed only by
	// whitespace). Wrap the spliced XML in <p> since at block level the
	// natural Confluence shape is a one-element paragraph.
	if xml, ok := DecodeInlineFence(raw); ok {
		w.sb.WriteString("<p>")
		w.sb.WriteString(xml)
		w.sb.WriteString("</p>")
		return
	}
	// Plain HTML — Confluence storage doesn't tolerate arbitrary HTML (and
	// ac:structured-macro ac:name="html" is disabled on many Cloud sites per
	// CLAUDE.md), so wrap it in a paragraph with its contents escaped as
	// plain text. The user sees the HTML tags literally; nothing is lost.
	w.sb.WriteString("<p>")
	w.sb.WriteString(xmlEscape(strings.TrimRight(raw, "\n")))
	w.sb.WriteString("</p>")
}

// writeTable emits a GFM pipe table as <table><tbody> with <tr><th>/<tr><td>.
// Confluence's storage uses <tbody> even for header rows (it derives "header"
// from the cell tag, not the row grouping), so we don't emit <thead>.
//
// Per-column alignment from the GFM `:---:` / `---:` / `:---` syntax is
// emitted as `style="text-align: …"` on each cell — both the header row's
// <th>s and every data row's <td>s. Confluence storage doesn't have a
// `<colgroup>` equivalent, so the alignment must travel on the cells.
func (w *mdToCfWriter) writeTable(n *extast.Table) {
	w.sb.WriteString("<table><tbody>")
	alignments := n.Alignments
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		_, isHeader := c.(*extast.TableHeader)
		w.sb.WriteString("<tr>")
		col := 0
		for cell := c.FirstChild(); cell != nil; cell = cell.NextSibling() {
			tc, ok := cell.(*extast.TableCell)
			if !ok {
				continue
			}
			tag := "td"
			if isHeader {
				tag = "th"
			}
			styleAttr := ""
			if col < len(alignments) {
				if css := tableAlignToCSS(alignments[col]); css != "" {
					styleAttr = fmt.Sprintf(` style="text-align: %s;"`, css)
				}
			}
			fmt.Fprintf(&w.sb, "<%s%s>", tag, styleAttr)
			w.writeInline(tc)
			fmt.Fprintf(&w.sb, "</%s>", tag)
			col++
		}
		w.sb.WriteString("</tr>")
	}
	w.sb.WriteString("</tbody></table>")
}

// tableAlignToCSS maps goldmark's per-column alignment enum to the
// CSS text-align values cf_to_md reads back. AlignNone returns "" so we
// don't emit a redundant attribute when no alignment was specified.
func tableAlignToCSS(a extast.Alignment) string {
	switch a {
	case extast.AlignLeft:
		return "left"
	case extast.AlignCenter:
		return "center"
	case extast.AlignRight:
		return "right"
	}
	return ""
}

// --- Inline -----------------------------------------------------------------

func (w *mdToCfWriter) writeInline(parent ast.Node) {
	var skipNext ast.Node
	for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
		if c == skipNext {
			// Already consumed as a metadata sidecar of the previous node.
			skipNext = nil
			continue
		}
		// Peek for a gfl:meta sidecar immediately after this node and,
		// if it decorates a construct that supports metadata (image,
		// external link), apply it and arrange to skip the meta node on
		// the next iteration.
		if meta, metaNode := readAdjacentMeta(c, w.source); meta != nil {
			switch node := c.(type) {
			case *ast.Image:
				w.writeImage(node, meta)
				skipNext = metaNode
				continue
			case *ast.Link:
				w.writeLink(node, meta)
				skipNext = metaNode
				continue
			}
			// c isn't a meta-supporting construct — fall through and
			// emit normally; the meta will be handled by writeInlineNode's
			// stray-meta path below.
		}
		w.writeInlineNode(c)
	}
}

// readAdjacentMeta inspects c.NextSibling() and, if it's a gfl:meta
// inline raw HTML comment, returns the decoded attributes and the node
// itself (so the caller can mark it for skipping). Returns (nil, nil)
// when there's no adjacent metadata.
func readAdjacentMeta(c ast.Node, source []byte) (map[string]string, ast.Node) {
	if c == nil {
		return nil, nil
	}
	next, ok := c.NextSibling().(*ast.RawHTML)
	if !ok || next == nil {
		return nil, nil
	}
	var raw strings.Builder
	for i := 0; i < next.Segments.Len(); i++ {
		seg := next.Segments.At(i)
		raw.Write(seg.Value(source))
	}
	meta, ok := DecodeMeta(raw.String())
	if !ok {
		return nil, nil
	}
	return meta, next
}

func (w *mdToCfWriter) writeInlineNode(n ast.Node) {
	switch node := n.(type) {
	case *ast.Text:
		seg := node.Segment
		w.sb.WriteString(xmlEscape(stripBackslashEscapes(string(seg.Value(w.source)))))
		switch {
		case node.HardLineBreak():
			// Confluence treats <br/> inside a paragraph as a hard break; this
			// matches what Markdown intended with trailing "\\\n" / "  \n".
			w.sb.WriteString("<br/>")
		case node.SoftLineBreak():
			// Soft breaks are treated as significant line breaks (matching the
			// normaliser's policy) because Confluence content relies on line
			// breaks for layout.
			w.sb.WriteString("<br/>")
		}
	case *ast.String:
		w.sb.WriteString(xmlEscape(string(node.Value)))
	case *ast.Emphasis:
		tag := "em"
		if node.Level == 2 {
			tag = "strong"
		}
		fmt.Fprintf(&w.sb, "<%s>", tag)
		w.writeInline(node)
		fmt.Fprintf(&w.sb, "</%s>", tag)
	case *ast.CodeSpan:
		w.sb.WriteString("<code>")
		// CodeSpan contents are literal — no further Markdown processing, but
		// XML-escape for safety.
		var buf strings.Builder
		for c := node.FirstChild(); c != nil; c = c.NextSibling() {
			if t, ok := c.(*ast.Text); ok {
				buf.Write(t.Segment.Value(w.source))
			}
		}
		w.sb.WriteString(xmlEscape(buf.String()))
		w.sb.WriteString("</code>")
	case *ast.Link:
		w.writeLink(node, nil)
	case *ast.AutoLink:
		url := string(node.URL(w.source))
		fmt.Fprintf(&w.sb, `<a href="%s">%s</a>`, xmlAttrEscape(url), xmlEscape(url))
	case *ast.Image:
		w.writeImage(node, nil)
	case *ast.RawHTML:
		// Concatenate all segments first; an inline fence is one comment
		// but goldmark may split a long line across multiple segments.
		var raw strings.Builder
		for i := 0; i < node.Segments.Len(); i++ {
			seg := node.Segments.At(i)
			raw.Write(seg.Value(w.source))
		}
		rawStr := raw.String()
		// Inline fence: splice the original storage XML back verbatim.
		// This is the inline counterpart to the HTMLBlock fence path —
		// preserves <ac:emoticon>, inline <ac:structured-macro>, and any
		// other inline construct that has no Markdown shape.
		if xml, ok := DecodeInlineFence(rawStr); ok {
			w.sb.WriteString(xml)
			break
		}
		// Stray gfl:meta comment — was not consumed by an adjacent
		// construct (probably because the user moved or deleted it, or
		// inserted whitespace between it and the construct). Drop
		// silently so it doesn't surface as escaped HTML on the
		// Confluence side.
		if IsMeta(rawStr) {
			break
		}
		// Plain inline HTML — Confluence storage doesn't tolerate it, so
		// escape so the author sees the source literally rather than
		// having it silently dropped by a Confluence sanitiser.
		w.sb.WriteString(xmlEscape(rawStr))
	case *extast.Strikethrough:
		w.sb.WriteString("<s>")
		w.writeInline(node)
		w.sb.WriteString("</s>")
	case *extast.TaskCheckBox:
		// Encountered outside a task-list render (e.g. a stray checkbox in
		// regular prose). Fall back to literal "[x]"/"[ ]" so the author
		// still sees what they wrote.
		if node.IsChecked {
			w.sb.WriteString("[x]")
		} else {
			w.sb.WriteString("[ ]")
		}
	default:
		// Unknown inline — recurse to keep any child text intact.
		w.writeInline(n)
	}
}

func (w *mdToCfWriter) writeLink(n *ast.Link, meta map[string]string) {
	dest := string(n.Destination)
	// Is this a reference to another page in the sync tree?
	if w.opts.Pages != nil {
		if title, space, ok := w.opts.Pages.ResolveLink(dest); ok {
			// ac:link is a Confluence-internal page reference. Arbitrary
			// HTML attributes (target, rel, ...) don't apply, so any
			// adjacent meta is silently dropped here.
			w.sb.WriteString("<ac:link>")
			if space != "" {
				fmt.Fprintf(&w.sb, `<ri:page ri:content-title="%s" ri:space-key="%s"/>`,
					xmlAttrEscape(title), xmlAttrEscape(space))
			} else {
				fmt.Fprintf(&w.sb, `<ri:page ri:content-title="%s"/>`, xmlAttrEscape(title))
			}
			w.sb.WriteString("<ac:plain-text-link-body>")
			w.sb.WriteString(wrapCDATA(linkInlineText(n, w.source)))
			w.sb.WriteString("</ac:plain-text-link-body>")
			w.sb.WriteString("</ac:link>")
			return
		}
	}
	// External link — standard <a href> with any sidecar meta applied
	// as additional attributes (target, rel, class, ...).
	fmt.Fprintf(&w.sb, `<a href="%s"`, xmlAttrEscape(dest))
	w.writeMetaAttrs(meta)
	w.sb.WriteByte('>')
	w.writeInline(n)
	w.sb.WriteString("</a>")
}

// linkInlineText flattens a link's inline children to plain text (no markup).
// Confluence's <ac:plain-text-link-body> is CDATA; inline emphasis inside a
// page-link body is not representable in storage XML — the Confluence editor
// strips it too. We match that behaviour.
func linkInlineText(n *ast.Link, source []byte) string {
	var sb strings.Builder
	var walk func(ast.Node)
	walk = func(x ast.Node) {
		for c := x.FirstChild(); c != nil; c = c.NextSibling() {
			switch cn := c.(type) {
			case *ast.Text:
				sb.Write(cn.Segment.Value(source))
				if cn.SoftLineBreak() {
					sb.WriteByte(' ')
				}
			case *ast.String:
				sb.Write(cn.Value)
			default:
				walk(c)
			}
		}
	}
	walk(n)
	return sb.String()
}

func (w *mdToCfWriter) writeImage(n *ast.Image, meta map[string]string) {
	dest := string(n.Destination)
	alt := imageAltText(n, w.source)

	if w.opts.Attachments != nil {
		if filename, ok := w.opts.Attachments.ResolveImage(dest); ok {
			w.sb.WriteString("<ac:image")
			if alt != "" {
				fmt.Fprintf(&w.sb, ` ac:alt="%s"`, xmlAttrEscape(alt))
			}
			w.writeMetaAttrs(meta)
			w.sb.WriteByte('>')
			fmt.Fprintf(&w.sb, `<ri:attachment ri:filename="%s"/>`, xmlAttrEscape(filename))
			w.sb.WriteString("</ac:image>")
			return
		}
	}
	// External image URL.
	w.sb.WriteString("<ac:image")
	if alt != "" {
		fmt.Fprintf(&w.sb, ` ac:alt="%s"`, xmlAttrEscape(alt))
	}
	w.writeMetaAttrs(meta)
	w.sb.WriteByte('>')
	fmt.Fprintf(&w.sb, `<ri:url ri:value="%s"/>`, xmlAttrEscape(dest))
	w.sb.WriteString("</ac:image>")
}

// writeMetaAttrs applies a meta-sidecar map as additional XML attributes
// on the currently-open element. Keys are emitted in sorted order for
// deterministic output. A nil/empty map writes nothing.
func (w *mdToCfWriter) writeMetaAttrs(meta map[string]string) {
	if len(meta) == 0 {
		return
	}
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&w.sb, ` %s="%s"`, k, xmlAttrEscape(meta[k]))
	}
}

// imageAltText collects the plain-text alt attribute from an image node's
// children. Markdown images `![alt](src)` parse with "alt" as the children of
// the Image node, but those children may themselves be emphasis or code spans
// — storage XML's ac:alt is a flat string, so we flatten.
func imageAltText(n *ast.Image, source []byte) string {
	var sb strings.Builder
	var walk func(ast.Node)
	walk = func(x ast.Node) {
		for c := x.FirstChild(); c != nil; c = c.NextSibling() {
			switch cn := c.(type) {
			case *ast.Text:
				sb.Write(cn.Segment.Value(source))
			case *ast.String:
				sb.Write(cn.Value)
			default:
				walk(c)
			}
		}
	}
	walk(n)
	return sb.String()
}

// --- Admonition detection (GFM) --------------------------------------------

// admonitionFromBlockquote inspects a Blockquote AST and reports whether it
// matches the GitHub-flavoured admonition shape:
//
//	> [!INFO]
//	> body
//
//	> [!INFO]<!--gfl:meta icon="true"-->
//	> body
//
// On a match, label is the lowercase macro name (info/note/warning/tip/
// expand/decision), bodyStart is the first inline child of the first
// paragraph that follows the marker (and any sidecar) — or nil when no
// inline content follows — and meta is the decoded sidecar attributes
// (nil if there is no sidecar).
//
// Only the canonical "marker on its own line" form is recognised — the
// form GitHub officially documents and that goldmark splits cleanly into
// three Text tokens (`[`, `!LABEL`, `]`) with the closing bracket
// carrying the soft line break. The optional gfl:meta sidecar must sit
// on the same line, immediately after `]`, with no intervening
// whitespace.
func admonitionFromBlockquote(bq *ast.Blockquote, source []byte) (label string, bodyStart ast.Node, meta map[string]string, ok bool) {
	first, isPara := bq.FirstChild().(*ast.Paragraph)
	if !isPara || first == nil {
		return "", nil, nil, false
	}
	t1, ok := first.FirstChild().(*ast.Text)
	if !ok || string(t1.Segment.Value(source)) != "[" {
		return "", nil, nil, false
	}
	t2, ok := t1.NextSibling().(*ast.Text)
	if !ok {
		return "", nil, nil, false
	}
	middle := string(t2.Segment.Value(source))
	if len(middle) < 2 || middle[0] != '!' {
		return "", nil, nil, false
	}

	// Goldmark splits the marker tokens differently depending on what
	// follows the closing bracket on the same line:
	//
	//   `> [!INFO]\n> body`            → three tokens: [, !INFO, ]
	//   `> [!INFO]<!--gfl:meta-->...`  → two tokens:   [, !INFO]
	//
	// In the merged-bracket case, the closing `]` is the last byte of t2.
	// Accept both shapes.
	var labelText string
	var lastMarker ast.Node
	if strings.HasSuffix(middle, "]") {
		// Two-token form: !LABEL] merged.
		labelText = middle[1 : len(middle)-1]
		lastMarker = t2
	} else {
		// Three-token form: separate ] follows.
		t3, ok := t2.NextSibling().(*ast.Text)
		if !ok || string(t3.Segment.Value(source)) != "]" {
			return "", nil, nil, false
		}
		labelText = middle[1:]
		lastMarker = t3
	}
	label = strings.ToLower(labelText)
	switch label {
	case
		// UI-aligned canonical labels (cf_to_md emits these on pull).
		"info", "note", "success", "warning", "error", "panel",
		// Other supported constructs.
		"expand", "decision",
		// Backward-compat / GH-spec aliases — accepted on push but
		// never emitted on pull. Resolved to the canonical equivalent
		// by labelToStorage when emitting.
		"tip", "important", "caution":
		// supported
	default:
		return "", nil, nil, false
	}

	afterMarker := lastMarker.NextSibling()

	// Optional gfl:meta sidecar immediately after `]`.
	if rh, isRaw := afterMarker.(*ast.RawHTML); isRaw {
		var raw strings.Builder
		for i := 0; i < rh.Segments.Len(); i++ {
			seg := rh.Segments.At(i)
			raw.Write(seg.Value(source))
		}
		if m, mok := DecodeMeta(raw.String()); mok {
			meta = m
			afterMarker = rh.NextSibling()
		}
	}

	// In the meta-sidecar case the soft line break between the marker
	// line and the body content shows up as an empty Text node (or one
	// that consists entirely of whitespace) carrying SoftLineBreak=true
	// — an artifact of how goldmark splits the inline run. Skip it so
	// the body iteration starts on real content.
	for {
		t, isText := afterMarker.(*ast.Text)
		if !isText {
			break
		}
		v := string(t.Segment.Value(source))
		if strings.TrimSpace(v) != "" {
			break
		}
		afterMarker = t.NextSibling()
	}

	return label, afterMarker, meta, true
}

// writeAdmonition emits the Confluence storage shape for an admonition
// blockquote, dispatching on label. Confluence's storage uses TWO
// shapes for panels (classic <ac:structured-macro> and modern ADF
// <ac:adf-extension><ac:adf-node type="panel">), and the markdown
// labels don't always have a 1:1 mapping to either:
//
//   - info/success/warning/error/panel/expand → classic structured-
//     macro (the storage name from classicMarkdownToMacro maps the
//     UI-aligned label to the legacy storage name);
//   - note → ADF panel-type="note" (no classic equivalent for the
//     purple note panel — it only exists as ADF);
//   - decision → ADF decision-list with one decision-item;
//   - tip/important/caution → resolved as aliases for success/note/
//     error respectively.
func (w *mdToCfWriter) writeAdmonition(label string, firstParaBodyStart ast.Node, bq *ast.Blockquote, meta map[string]string) {
	switch label {
	case "decision":
		w.writeDecisionAdmonition(firstParaBodyStart, meta)
	case "note", "important":
		// No classic structured-macro produces the purple note panel.
		// `important` is the GH-style alias.
		w.writeAdfPanelAdmonition("note", firstParaBodyStart, bq, meta)
	default:
		// All other supported labels resolve to a classic structured-
		// macro. classicMarkdownToMacro maps the UI-aligned markdown
		// label back to the legacy storage `ac:name`.
		macroName := classicMarkdownToMacro[label]
		if macroName == "" {
			macroName = label // expand, panel — same name on both sides
		}
		w.writeStructuredAdmonition(macroName, firstParaBodyStart, bq, meta)
	}
}

// classicMarkdownToMacro is the inverse of classicMacroLabel — it maps
// a markdown admonition label (UI-aligned name) back to the storage
// `ac:name` Confluence expects on push. Includes back-compat aliases
// that accept GH-style labels (tip / caution) and the older `tip` name
// users may have learned for the green panel.
var classicMarkdownToMacro = map[string]string{
	"info":    "info",
	"success": "tip",     // green panel — legacy storage name "tip"
	"warning": "note",    // yellow panel — legacy storage name "note"
	"error":   "warning", // red panel — legacy storage name "warning"
	"panel":   "panel",
	"expand":  "expand",
	// aliases:
	"tip":     "tip",     // GH spec & legacy gfl name for green
	"caution": "warning", // GH spec name for red
}

// writeAdfPanelAdmonition emits an <ac:adf-extension> wrapping an
// <ac:adf-node type="panel"> with the given panel-type. Used for the
// purple "note" panel (which has no classic structured-macro form).
// Any meta sidecar attributes are emitted as additional
// <ac:adf-attribute key="…"> children — the wire shape Confluence uses
// for ADF panel parameters.
func (w *mdToCfWriter) writeAdfPanelAdmonition(panelType string, firstParaBodyStart ast.Node, bq *ast.Blockquote, meta map[string]string) {
	w.sb.WriteString(`<ac:adf-extension>`)
	w.sb.WriteString(`<ac:adf-node type="panel">`)
	fmt.Fprintf(&w.sb, `<ac:adf-attribute key="panel-type">%s</ac:adf-attribute>`, xmlEscape(panelType))

	// Additional meta keys travel as further <ac:adf-attribute> children
	// (sorted for deterministic output).
	keys := make([]string, 0, len(meta))
	for k := range meta {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&w.sb, `<ac:adf-attribute key="%s">%s</ac:adf-attribute>`,
			xmlAttrEscape(k), xmlEscape(meta[k]))
	}

	w.sb.WriteString(`<ac:adf-content>`)
	w.writeAdmonitionBody(firstParaBodyStart, bq)
	w.sb.WriteString(`</ac:adf-content>`)
	w.sb.WriteString(`</ac:adf-node>`)
	w.sb.WriteString(`</ac:adf-extension>`)
}

// writeStructuredAdmonition emits <ac:structured-macro ac:name="…"> with
// any data-* meta keys as XML attributes on the macro element and any
// other meta keys as <ac:parameter> children, wrapping the body in
// <ac:rich-text-body>.
func (w *mdToCfWriter) writeStructuredAdmonition(macroName string, firstParaBodyStart ast.Node, bq *ast.Blockquote, meta map[string]string) {
	// Split meta keys into XML attributes (data-*) and parameters
	// (everything else). Sort each list for deterministic output.
	var attrKeys, paramKeys []string
	for k := range meta {
		if strings.HasPrefix(k, "data-") {
			attrKeys = append(attrKeys, k)
		} else {
			paramKeys = append(paramKeys, k)
		}
	}
	sort.Strings(attrKeys)
	sort.Strings(paramKeys)

	fmt.Fprintf(&w.sb, `<ac:structured-macro ac:name="%s"`, xmlAttrEscape(macroName))
	for _, k := range attrKeys {
		fmt.Fprintf(&w.sb, ` %s="%s"`, k, xmlAttrEscape(meta[k]))
	}
	w.sb.WriteByte('>')

	for _, k := range paramKeys {
		fmt.Fprintf(&w.sb, `<ac:parameter ac:name="%s">%s</ac:parameter>`,
			xmlAttrEscape(k), xmlEscape(meta[k]))
	}

	w.sb.WriteString(`<ac:rich-text-body>`)
	w.writeAdmonitionBody(firstParaBodyStart, bq)
	w.sb.WriteString(`</ac:rich-text-body>`)
	w.sb.WriteString(`</ac:structured-macro>`)
}

// writeAdmonitionBody fills the body wrapper of any structured-macro
// admonition. The first paragraph's inline content (after the marker
// and any sidecar) becomes one <p>; subsequent block children of the
// blockquote (paragraphs, lists, ...) render normally.
func (w *mdToCfWriter) writeAdmonitionBody(firstParaBodyStart ast.Node, bq *ast.Blockquote) {
	if firstParaBodyStart != nil {
		w.sb.WriteString("<p>")
		for c := firstParaBodyStart; c != nil; c = c.NextSibling() {
			w.writeInlineNode(c)
		}
		w.sb.WriteString("</p>")
	}
	for c := bq.FirstChild().NextSibling(); c != nil; c = c.NextSibling() {
		w.writeBlock(c)
	}
}

// writeDecisionAdmonition emits a single decision-item wrapped in an
// <ac:adf-extension><ac:adf-node type="decision-list"> envelope. The
// state defaults to DECIDED if the meta sidecar doesn't specify one.
//
// Decision content in storage is plain inline text (no <p> wrapping,
// no nested blocks). We render only the inline content of the first
// paragraph; any subsequent block children of the blockquote are
// dropped because <ac:adf-content> for a decision-item can't carry
// them. This is a documented limitation — multi-paragraph decisions
// need to be authored in Confluence.
func (w *mdToCfWriter) writeDecisionAdmonition(firstParaBodyStart ast.Node, meta map[string]string) {
	state := meta["state"]
	if state == "" {
		state = "DECIDED"
	}
	w.sb.WriteString(`<ac:adf-extension>`)
	w.sb.WriteString(`<ac:adf-node type="decision-list">`)
	w.sb.WriteString(`<ac:adf-node type="decision-item">`)
	fmt.Fprintf(&w.sb, `<ac:adf-attribute key="state">%s</ac:adf-attribute>`, xmlEscape(state))
	w.sb.WriteString(`<ac:adf-content>`)
	if firstParaBodyStart != nil {
		for c := firstParaBodyStart; c != nil; c = c.NextSibling() {
			w.writeInlineNode(c)
		}
	}
	w.sb.WriteString(`</ac:adf-content>`)
	w.sb.WriteString(`</ac:adf-node>`)
	w.sb.WriteString(`</ac:adf-node>`)
	w.sb.WriteString(`</ac:adf-extension>`)
}

// --- Helpers ----------------------------------------------------------------

// readLines concatenates the backing source lines of a goldmark block node.
// Used for fenced code and HTML blocks whose contents must be emitted
// verbatim.
func readLines(n ast.Node, source []byte) string {
	lines := n.Lines()
	if lines == nil || lines.Len() == 0 {
		return ""
	}
	var sb strings.Builder
	for i := 0; i < lines.Len(); i++ {
		seg := lines.At(i)
		sb.Write(seg.Value(source))
	}
	return sb.String()
}

// wrapCDATA produces a <![CDATA[...]]> section containing s. If s contains
// the sequence "]]>", it is split across sections so that the close delimiter
// never appears inside a single section — the standard XML idiom.
//
// The escape works by splitting at each "]]>" and emitting, between
// consecutive pieces, the byte sequence "]]]]><![CDATA[>". Read carefully:
// "]]" is content (two ] characters), "]]>" closes the current CDATA,
// "<![CDATA[" opens a fresh one, and ">" is content. The decoder reassembles
// the original "]]>" by concatenating "]]" + ">" from adjacent sections.
// stripBackslashEscapes resolves CommonMark backslash escapes in raw text
// segments. Goldmark's Segment.Value returns the source bytes verbatim, so
// `\*` appears as two bytes. In Confluence storage XML the backslash has no
// special meaning, so we strip it here to prevent escape accumulation on
// round trips.
func stripBackslashEscapes(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			next := s[i+1]
			if strings.ContainsRune("\\`*_{}[]()#+-.!|~>", rune(next)) {
				sb.WriteByte(next)
				i++
				continue
			}
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

func wrapCDATA(s string) string {
	if !strings.Contains(s, "]]>") {
		return "<![CDATA[" + s + "]]>"
	}
	parts := strings.Split(s, "]]>")
	var sb strings.Builder
	sb.WriteString("<![CDATA[")
	for i, p := range parts {
		sb.WriteString(p)
		if i < len(parts)-1 {
			sb.WriteString("]]]]><![CDATA[>")
		}
	}
	sb.WriteString("]]>")
	return sb.String()
}
