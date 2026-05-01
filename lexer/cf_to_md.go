package lexer

import (
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Confluence storage XML → Markdown.
//
// The conversion is implemented in three stages:
//
//  1. Parse the storage XML into a small typed node tree using encoding/xml.
//     Confluence storage is well-formed XML, so the standard parser is the
//     right tool — it preserves CDATA, handles namespaced elements (ac:, ri:),
//     and resolves HTML entities via xml.HTMLEntity.
//
//  2. Walk the tree emitting Markdown to a builder. Block-level dispatch
//     (paragraph, heading, list, table, etc.) and inline dispatch (strong,
//     em, link, code) are split so block boundaries get the right newline
//     separation and inline content stays single-line.
//
//  3. Run the result through Normalise so the output is canonical and ready
//     to write to disk. Anything we emit slightly off-shape (extra spaces,
//     missing trailing newline) is fixed by the normaliser, which keeps this
//     code free of cosmetic concerns.
//
// Anything not in the supported mapping table — primarily unknown
// ac:structured-macro instances and any other unrecognised non-HTML element —
// is preserved verbatim via EncodeBlockFence so that a subsequent push back to
// Confluence carries the original construct unchanged.

// PageResolver maps a Confluence page reference to a local file path. The
// returned path is what cf_to_md emits as the link target.
//
// Title-based matching is too loose: pages with the same title across
// different spaces would silently get redirected to the wrong local file.
// The id-based method is the discriminator — only the page-id is unique.
// ResolvePageByTitle is kept for fallback when storage XML lacks
// ri:content-id (rare; most Confluence storage includes it).
type PageResolver interface {
	ResolvePageByID(pageID string) (localPath string, ok bool)
	ResolvePageByTitle(title, spaceKey string) (localPath string, ok bool)
}

// AttachmentRefResolver maps an attachment reference to its Markdown image src.
// The returned path is used verbatim in `![alt](src)`. Returning an empty
// string causes the converter to fall back to the bare filename.
type AttachmentRefResolver interface {
	AttachmentSrc(filename string) string
}

// CfToMdOpts bundles optional resolvers. nil-safe — the converter falls back
// to the most informative placeholder available when a resolver is missing.
type CfToMdOpts struct {
	Pages       PageResolver
	Attachments AttachmentRefResolver
	// BaseURL is the Confluence wiki base (e.g. "https://yourorg.atlassian.net/wiki").
	// When an <ac:link><ri:page …/> can't be resolved against the local sync tree
	// (the page is in a different space, or hasn't been synced), the converter
	// falls back to a regular Markdown link pointing at the Confluence URL
	// derived from BaseURL plus the ri:content-id / ri:space-key it carries.
	// Empty BaseURL preserves the legacy "drop URL, emit plain text" behaviour.
	BaseURL string
}

// CfToMd converts a Confluence storage XML body to canonical Markdown. The
// returned string has been passed through Normalise.
//
// On a parse error (storage XML not well-formed XML), the function returns the
// error and an empty Markdown string — callers should surface the error rather
// than write garbage to disk.
func CfToMd(storage string, opts CfToMdOpts) (string, error) {
	root, err := parseStorage(storage)
	if err != nil {
		return "", err
	}
	r := &cfRenderer{opts: opts}
	r.renderBlocks(root.children)
	return Normalise(r.sb.String()), nil
}

// --- Internal node tree -----------------------------------------------------

type cfNodeKind int

const (
	cfElement cfNodeKind = iota
	cfText               // plain character data — may need entity-style escaping when emitted
	cfCData              // CDATA section — emit verbatim (used for code bodies)
)

type cfNode struct {
	kind     cfNodeKind
	name     string // "p", "ac:structured-macro", "ri:attachment", ...
	attrs    map[string]string
	text     string
	children []*cfNode
}

// attr returns the value of an attribute by full name (e.g. "ac:name", "href")
// or empty string if absent. Matching by full name keeps the call sites
// uniform across HTML attributes and Confluence-namespaced ones.
func (n *cfNode) attr(name string) string {
	if n == nil || n.attrs == nil {
		return ""
	}
	return n.attrs[name]
}

// firstElement returns the first immediate-child element with the given name,
// or nil. Useful for descending into well-known wrappers like
// <ac:plain-text-body> or <ac:rich-text-body>.
func (n *cfNode) firstElement(name string) *cfNode {
	for _, c := range n.children {
		if c.kind == cfElement && c.name == name {
			return c
		}
	}
	return nil
}

// childParam returns the text body of an <ac:parameter ac:name="<name>"> child,
// stripped of surrounding whitespace. Confluence stores macro parameters this
// way; e.g. <code> macros carry their language in
// <ac:parameter ac:name="language">go</ac:parameter>.
func (n *cfNode) childParam(name string) string {
	for _, c := range n.children {
		if c.kind == cfElement && c.name == "ac:parameter" && c.attr("ac:name") == name {
			return strings.TrimSpace(c.innerText())
		}
	}
	return ""
}

// innerText concatenates all descendant text and CDATA content.
func (n *cfNode) innerText() string {
	var sb strings.Builder
	var walk func(*cfNode)
	walk = func(x *cfNode) {
		if x == nil {
			return
		}
		switch x.kind {
		case cfText, cfCData:
			sb.WriteString(x.text)
		case cfElement:
			for _, c := range x.children {
				walk(c)
			}
		}
	}
	walk(n)
	return sb.String()
}

// parseStorage parses a Confluence storage XML body into a node tree. Because
// a storage body is a sequence of blocks rather than a single document
// element, the input is wrapped in a synthetic root before parsing. The
// wrapper itself never appears in the returned tree — only its children
// (i.e. the actual storage content) are exposed.
//
// The wrapper name uses an unlikely token rather than "root" so that an
// unwrap-by-name strategy can't be confused by user content that happens to
// also use a top-level <root> element.
func parseStorage(storage string) (*cfNode, error) {
	const wrapperName = "__cfl_doc__"
	wrapped := "<" + wrapperName + ">" + storage + "</" + wrapperName + ">"
	d := xml.NewDecoder(strings.NewReader(wrapped))
	d.Strict = false
	d.Entity = xml.HTMLEntity

	doc := &cfNode{kind: cfElement, name: wrapperName}
	stack := []*cfNode{doc}

	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("cf_to_md: parse storage XML: %w", err)
		}
		top := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			n := &cfNode{kind: cfElement, name: qname(t.Name)}
			if len(t.Attr) > 0 {
				n.attrs = make(map[string]string, len(t.Attr))
				for _, a := range t.Attr {
					n.attrs[qname(a.Name)] = a.Value
				}
			}
			top.children = append(top.children, n)
			stack = append(stack, n)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			top.children = append(top.children, &cfNode{kind: cfText, text: string(t)})
		case xml.Comment, xml.ProcInst, xml.Directive:
			// Strip — Confluence storage shouldn't contain these meaningfully.
		}
	}
	// Unwrap: the synthetic wrapper element holds the actual storage content
	// as its sole top-level group of children. Return a synthetic node whose
	// children are exactly those.
	if len(doc.children) == 1 && doc.children[0].kind == cfElement && doc.children[0].name == wrapperName {
		return doc.children[0], nil
	}
	return doc, nil
}

// qname returns "space:local" if there's a namespace, else just "local".
func qname(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}
	return n.Space + ":" + n.Local
}

// --- Rendering --------------------------------------------------------------

type cfRenderer struct {
	opts CfToMdOpts
	sb   strings.Builder
}

// renderBlocks emits a sequence of block-level nodes, separated by exactly one
// blank line (Normalise will tighten this further if needed). Whitespace-only
// text nodes between blocks are ignored — they are XML cosmetic separators
// rather than content.
func (r *cfRenderer) renderBlocks(nodes []*cfNode) {
	first := true
	for _, n := range nodes {
		if n.kind == cfText && strings.TrimSpace(n.text) == "" {
			continue
		}
		var s string
		switch n.kind {
		case cfElement:
			s = r.renderBlock(n)
		case cfText, cfCData:
			// Bare text at block level becomes a paragraph.
			s = strings.TrimSpace(n.text)
		}
		if s == "" {
			continue
		}
		if !first {
			r.sb.WriteString("\n\n")
		}
		r.sb.WriteString(s)
		first = false
	}
}

// renderBlock dispatches one block-level element. The returned string has no
// trailing newline; callers stitch blocks together with "\n\n".
func (r *cfRenderer) renderBlock(n *cfNode) string {
	switch n.name {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(n.name[1] - '0')
		return strings.Repeat("#", level) + " " + r.inline(n)
	case "p":
		return r.inline(n)
	case "ul", "ol":
		return r.renderList(n)
	case "blockquote":
		return r.renderBlockquote(n)
	case "pre":
		return "```\n" + strings.TrimRight(n.innerText(), "\n") + "\n```"
	case "hr":
		return "---"
	case "table":
		return r.renderTable(n)
	case "ac:structured-macro":
		return r.renderMacro(n)
	case "ac:task-list":
		return r.renderTaskList(n)
	case "ac:layout", "ac:layout-section", "ac:layout-cell":
		// Layouts wrap blocks but don't translate cleanly — render children
		// inline as a sequence of blocks. This is best-effort; if we ever need
		// fidelity here, switch to fence preservation for the outer layout.
		var inner cfRenderer
		inner.opts = r.opts
		inner.renderBlocks(n.children)
		return strings.TrimRight(inner.sb.String(), "\n")
	case "ac:image":
		// Confluence emits <ac:image> at the block level when the image
		// carries layout/sizing attributes (ac:align, ac:width, ac:layout,
		// etc.). Render it as a standalone Markdown image — the styling
		// attributes are intentionally dropped (Markdown can't represent
		// them) but the image content and its attachment link survive.
		var sb strings.Builder
		r.writeAcImage(&sb, n)
		return sb.String()
	case "ac:adf-extension":
		// Atlassian Cloud's newer ADF (Atlassian Document Format) wrapping.
		// Some constructs (panels, decisions, ...) migrated from the
		// classic <ac:structured-macro> shape to this extension form;
		// pages can mix both depending on when each construct was
		// authored. We translate the panel variant into the same GFM
		// admonition output the classic info/note/warning/tip macros
		// produce, so the user sees one consistent markdown shape
		// regardless of which storage form Confluence chose.
		if label, body, ok := adfPanelToAdmonition(n); ok {
			return renderGFMAdmonitionFromBlocks(label, body, nil, r.opts)
		}
		// Decision lists are also stored as ADF extensions. Each item
		// becomes its own [!DECISION] admonition so the body content
		// stays editable; the state (DECIDED / UNDECIDED) round-trips
		// via the meta sidecar.
		if items, ok := adfDecisionListToItems(n); ok {
			return renderDecisionItems(items, r.opts)
		}
		// Other ADF nodes (custom panels with explicit colours, future
		// node types) fence-preserve.
		return EncodeBlockFence(serializeXML(n))
	default:
		// Anything else block-level — including unknown HTML elements that
		// don't fit the supported set — gets fence-preserved so the round trip
		// keeps it intact.
		return EncodeBlockFence(serializeXML(n))
	}
}

// adfPanelToAdmonition recognises the modern Atlassian ADF panel shape:
//
//	<ac:adf-extension>
//	  <ac:adf-node type="panel">
//	    <ac:adf-attribute key="panel-type">note</ac:adf-attribute>
//	    <ac:adf-content>...body blocks...</ac:adf-content>
//	  </ac:adf-node>
//	  <ac:adf-fallback>...</ac:adf-fallback>  (ignored)
//	</ac:adf-extension>
//
// On a successful match, returns the admonition label (info/note/
// warning/tip) and the body's child block nodes. Returns ok=false for
// non-panel ADF extensions, panels with custom colours, or panel-types
// outside the small set we can losslessly map to a GFM admonition.
func adfPanelToAdmonition(ext *cfNode) (label string, body []*cfNode, ok bool) {
	node := ext.firstElement("ac:adf-node")
	if node == nil || node.attr("type") != "panel" {
		return "", nil, false
	}
	var panelType string
	for _, c := range node.children {
		if c.kind == cfElement && c.name == "ac:adf-attribute" && c.attr("key") == "panel-type" {
			panelType = strings.TrimSpace(c.innerText())
			break
		}
	}
	label = adfPanelTypeToLabel(panelType)
	if label == "" {
		return "", nil, false
	}
	if c := node.firstElement("ac:adf-content"); c != nil {
		body = c.children
	}
	return label, body, true
}

// classicMacroLabel maps a classic <ac:structured-macro ac:name="..."> to
// the user-facing markdown admonition label. Confluence's storage names
// are LEGACY: the editor was redesigned to call panels info/note/
// success/warning/error in the UI, but the storage XML kept the
// original four names — and they don't line up. `ac:name="note"` is
// today's yellow *warning* panel; `ac:name="warning"` is today's red
// *error* panel; `ac:name="tip"` is today's green *success* panel. The
// UI never had a classic macro for the purple *note* panel — that one
// only exists as ADF.
//
// We use UI-aligned labels on the markdown side so authors writing
// `[!WARNING]` get the yellow warning panel they see in Confluence
// (not the red error one).
var classicMacroLabel = map[string]string{
	"info":    "info",
	"tip":     "success", // green
	"note":    "warning", // yellow
	"warning": "error",   // red
	"panel":   "panel",
	"expand":  "expand",
}

// adfPanelTypeToLabel maps an ADF panel-type to the user-facing markdown
// admonition label. ADF's naming is sane (it matches the UI), so the
// mapping is a straight pass-through for all five well-known types.
// Unmapped types ("custom" with explicit colours, anything new) return
// "" so the caller fence-preserves.
func adfPanelTypeToLabel(panelType string) string {
	switch panelType {
	case "info":
		return "info"
	case "note":
		return "note" // purple — only available via ADF
	case "success":
		return "success"
	case "warning":
		return "warning"
	case "error":
		return "error"
	}
	return ""
}

// decisionItem is one entry in an ADF decision-list — a single row in
// Confluence's "Decisions" feature. Each item carries its own state
// (DECIDED, UNDECIDED) and inline content.
type decisionItem struct {
	state   string
	content []*cfNode
}

// adfDecisionListToItems extracts the decision items from an
// <ac:adf-extension> wrapping <ac:adf-node type="decision-list">.
// Returns ok=false for any other ADF extension.
func adfDecisionListToItems(ext *cfNode) ([]decisionItem, bool) {
	list := ext.firstElement("ac:adf-node")
	if list == nil || list.attr("type") != "decision-list" {
		return nil, false
	}
	var items []decisionItem
	for _, c := range list.children {
		if c.kind != cfElement || c.name != "ac:adf-node" || c.attr("type") != "decision-item" {
			continue
		}
		var item decisionItem
		for _, cc := range c.children {
			if cc.kind != cfElement {
				continue
			}
			switch cc.name {
			case "ac:adf-attribute":
				if cc.attr("key") == "state" {
					item.state = strings.TrimSpace(cc.innerText())
				}
			case "ac:adf-content":
				item.content = cc.children
			}
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil, false
	}
	return items, true
}

// renderDecisionItems emits one [!DECISION] admonition per decision
// item. The state travels via the meta sidecar (omitted when DECIDED,
// the default).
//
// A single Confluence decision-list with N items becomes N adjacent
// blockquotes in markdown. On push, each becomes its own
// <ac:adf-extension>; Confluence's editor may re-merge them into a
// single list on the next save. The visual rendering matches in either
// shape.
func renderDecisionItems(items []decisionItem, opts CfToMdOpts) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		var meta map[string]string
		if item.state != "" && item.state != "DECIDED" {
			meta = map[string]string{"state": item.state}
		}
		parts = append(parts, renderGFMAdmonitionFromBlocks("decision", item.content, meta, opts))
	}
	return strings.Join(parts, "\n\n")
}

// renderMacro handles known ac:structured-macro variants. Unknown macros fall
// through to fence preservation.
func (r *cfRenderer) renderMacro(n *cfNode) string {
	switch n.attr("ac:name") {
	case "code":
		lang := strings.ToLower(strings.TrimSpace(n.childParam("language")))
		body := ""
		if b := n.firstElement("ac:plain-text-body"); b != nil {
			body = b.innerText()
		}
		return "```" + lang + "\n" + strings.TrimRight(body, "\n") + "\n```"
	case "toc":
		// Per CLAUDE.md mapping: TOC macro is omitted entirely.
		return ""
	case "info", "note", "warning", "tip", "panel":
		// Render as a GitHub-flavoured admonition. Any <ac:parameter>
		// children (icon, bgColor, panelIcon, panelIconId,
		// panelIconText, ...) round-trip via the meta sidecar after the
		// marker. A complex parameter (one whose value is nested
		// elements rather than simple text) can't be encoded in the
		// flat sidecar — fall back to fence preservation so the
		// parameter doesn't get truncated.
		//
		// "panel" is Confluence's generic themable panel macro,
		// rendered as `[!PANEL]`. Authors get the same editable body
		// as info/note/warning/tip, with whatever colour / icon /
		// border parameters Confluence emitted preserved verbatim.
		if meta, ok := extractMacroMeta(n); ok {
			return renderGFMAdmonitionFromMacro(n, meta, r.opts)
		}
	case "expand":
		// Confluence's expand macro is a labelled body with a title:
		// the same shape as an admonition. Render as `[!EXPAND]` with
		// the title and other parameters in the meta sidecar so the
		// body stays editable in markdown.
		if meta, ok := extractMacroMeta(n); ok {
			return renderGFMAdmonitionFromMacro(n, meta, r.opts)
		}
	}
	// Anything else — admonition with complex parameters, status, jira,
	// view-file, layout, ... — fence-preserve so the original element
	// round-trips byte-for-byte.
	return EncodeBlockFence(serializeXML(n))
}

// extractMacroMeta collects round-trippable metadata from a structured
// macro: every <ac:parameter ac:name="X">value</ac:parameter> child as
// a key/value pair, plus any data-* XML attributes on the macro element
// itself (Confluence emits things like data-layout="wide" on expand).
//
// Returns ok=false when any parameter has structured (non-text-only)
// content that can't be represented in the flat key/value sidecar — the
// caller should fence-preserve in that case so the parameter isn't
// silently truncated. Auto-generated attributes (ac:macro-id,
// ac:local-id, ac:schema-version) are deliberately dropped — Confluence
// regenerates them on every save, so preserving them only adds churn.
func extractMacroMeta(n *cfNode) (map[string]string, bool) {
	out := make(map[string]string)

	// data-* XML attributes on the macro element. Other namespaced
	// attributes (ac:name, ac:macro-id, ac:schema-version, ac:local-id)
	// are either the macro identity itself or auto-generated, so we
	// skip them.
	for k, v := range n.attrs {
		if strings.HasPrefix(k, "data-") {
			out[k] = v
		}
	}

	// <ac:parameter> children — text-only values only.
	for _, c := range n.children {
		if c.kind != cfElement || c.name != "ac:parameter" {
			continue
		}
		name := c.attr("ac:name")
		if name == "" {
			return nil, false
		}
		for _, cc := range c.children {
			if cc.kind == cfElement {
				return nil, false
			}
		}
		out[name] = strings.TrimSpace(c.innerText())
	}

	return out, true
}

// renderGFMAdmonitionFromMacro is the shared rendering path for classic
// <ac:structured-macro> admonitions and expand macros. The label is
// translated via classicMacroLabel so the markdown side uses UI-aligned
// names rather than Confluence's legacy storage names.
func renderGFMAdmonitionFromMacro(n *cfNode, meta map[string]string, opts CfToMdOpts) string {
	var body []*cfNode
	if b := n.firstElement("ac:rich-text-body"); b != nil {
		body = b.children
	}
	storageName := n.attr("ac:name")
	label := classicMacroLabel[storageName]
	if label == "" {
		// Defensive fallback: if a future macro name slips through the
		// renderMacro switch, use the storage name verbatim rather than
		// returning an empty marker.
		label = storageName
	}
	return renderGFMAdmonitionFromBlocks(label, body, meta, opts)
}

// renderGFMAdmonitionFromBlocks builds the GFM admonition output given
// an already-known label, a slice of body block nodes, and an optional
// meta sidecar. The meta is encoded as a `<!--gfl:meta key="..."-->`
// comment immediately after the marker; on push, md_to_cf decodes it
// back into <ac:parameter> children (and data-* XML attributes).
//
// This helper is the convergence point for every storage shape that
// produces a GFM admonition: classic <ac:structured-macro> info/note/
// warning/tip, classic expand, modern ADF-extension panels, and ADF
// decision-list items all flow through here.
func renderGFMAdmonitionFromBlocks(label string, body []*cfNode, meta map[string]string, opts CfToMdOpts) string {
	upper := strings.ToUpper(label)
	metaStr := EncodeMeta(meta)
	bodyStr := ""
	if len(body) > 0 {
		var inner cfRenderer
		inner.opts = opts
		inner.renderBlocks(body)
		bodyStr = strings.TrimRight(inner.sb.String(), "\n")
	}
	if bodyStr == "" {
		return "> [!" + upper + "]" + metaStr
	}
	var sb strings.Builder
	sb.WriteString("> [!" + upper + "]")
	sb.WriteString(metaStr)
	sb.WriteByte('\n')
	for _, line := range strings.Split(bodyStr, "\n") {
		if line == "" {
			sb.WriteString(">\n")
		} else {
			sb.WriteString("> ")
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// renderTaskList renders a Confluence task macro as a GFM checkbox list. Each
// <ac:task> contains <ac:task-status> (complete|incomplete) and
// <ac:task-body>. The body's inline content becomes the item text.
func (r *cfRenderer) renderTaskList(n *cfNode) string {
	var lines []string
	for _, c := range n.children {
		if c.kind != cfElement || c.name != "ac:task" {
			continue
		}
		checked := strings.TrimSpace(c.firstElement("ac:task-status").innerText()) == "complete"
		body := ""
		if b := c.firstElement("ac:task-body"); b != nil {
			body = r.inline(b)
		}
		mark := "[ ]"
		if checked {
			mark = "[x]"
		}
		lines = append(lines, "- "+mark+" "+body)
	}
	return strings.Join(lines, "\n")
}

func (r *cfRenderer) renderList(n *cfNode) string {
	ordered := n.name == "ol"
	var items []string
	for _, c := range n.children {
		if c.kind != cfElement || c.name != "li" {
			continue
		}
		items = append(items, r.renderListItem(c, ordered))
	}
	return strings.Join(items, "\n")
}

func (r *cfRenderer) renderListItem(li *cfNode, ordered bool) string {
	marker := "- "
	if ordered {
		// Normalisation rule 8: every ordered item starts with "1.".
		marker = "1. "
	}
	// A list item's content is split into an inline part (the text/inline
	// elements that appear before any block child) and zero or more block
	// children (typically nested lists). Confluence often emits inline-only
	// items, so the common case is a single inline render.
	var inlineParts []*cfNode
	var blockParts []*cfNode
	for _, c := range li.children {
		if c.kind == cfElement && isBlockElement(c.name) {
			blockParts = append(blockParts, c)
			continue
		}
		inlineParts = append(inlineParts, c)
	}

	body := r.inlineNodes(inlineParts)
	if len(blockParts) > 0 {
		var sb strings.Builder
		sb.WriteString(body)
		for _, b := range blockParts {
			sb.WriteByte('\n')
			rendered := r.renderBlock(b)
			// Indent continuation lines by the marker width (2 spaces).
			rendered = indentAfterFirst(rendered, "  ")
			sb.WriteString("  " + rendered)
		}
		body = sb.String()
	}
	return indentAfterFirst(marker+body, strings.Repeat(" ", len(marker)))
}

// isBlockElement reports whether an element is rendered as its own block when
// encountered inside a list item (or other inline context). Anything not in
// this set is treated as inline by the list-item renderer.
func isBlockElement(name string) bool {
	switch name {
	case "p", "ul", "ol", "blockquote", "pre", "table", "hr",
		"h1", "h2", "h3", "h4", "h5", "h6",
		"ac:structured-macro", "ac:task-list":
		return true
	}
	return false
}

func (r *cfRenderer) renderBlockquote(n *cfNode) string {
	var inner cfRenderer
	inner.opts = r.opts
	inner.renderBlocks(n.children)
	return prefixBlockquote(strings.TrimRight(inner.sb.String(), "\n"))
}

func (r *cfRenderer) renderTable(n *cfNode) string {
	// Confluence storage tables nest <tbody>; some omit it. Find all <tr>
	// descendants regardless of where they sit. <thead> rows come first.
	var headRows, bodyRows []*cfNode
	var collect func(parent *cfNode, head bool)
	collect = func(parent *cfNode, head bool) {
		for _, c := range parent.children {
			if c.kind != cfElement {
				continue
			}
			switch c.name {
			case "thead":
				collect(c, true)
			case "tbody", "tfoot":
				collect(c, false)
			case "tr":
				if head {
					headRows = append(headRows, c)
				} else {
					bodyRows = append(bodyRows, c)
				}
			}
		}
	}
	collect(n, false)

	rows := append([]*cfNode{}, headRows...)
	rows = append(rows, bodyRows...)
	if len(rows) == 0 {
		return ""
	}

	headerCells, headerAligns := r.renderTableRowWithAlign(rows[0])
	cols := len(headerCells)
	if cols == 0 {
		return ""
	}

	var lines []string
	lines = append(lines, "| "+strings.Join(headerCells, " | ")+" |")

	seps := make([]string, cols)
	for i := range seps {
		align := ""
		if i < len(headerAligns) {
			align = headerAligns[i]
		}
		switch align {
		case "center":
			seps[i] = ":---:"
		case "right":
			seps[i] = "---:"
		case "left":
			seps[i] = ":---"
		default:
			seps[i] = "---"
		}
	}
	lines = append(lines, "| "+strings.Join(seps, " | ")+" |")

	for _, row := range rows[1:] {
		cells := r.renderTableRow(row)
		// Pad short rows to match column count.
		for len(cells) < cols {
			cells = append(cells, "")
		}
		if len(cells) > cols {
			cells = cells[:cols]
		}
		lines = append(lines, "| "+strings.Join(cells, " | ")+" |")
	}
	return strings.Join(lines, "\n")
}

func (r *cfRenderer) renderTableRow(tr *cfNode) []string {
	cells, _ := r.renderTableRowWithAlign(tr)
	return cells
}

func (r *cfRenderer) renderTableRowWithAlign(tr *cfNode) (cells []string, aligns []string) {
	for _, c := range tr.children {
		if c.kind != cfElement {
			continue
		}
		if c.name == "th" || c.name == "td" {
			cells = append(cells, r.inline(c))
			aligns = append(aligns, cellAlignment(c))
		}
	}
	return
}

// cellAlignment extracts text-align from a <th>/<td> style attribute.
func cellAlignment(n *cfNode) string {
	style := n.attr("style")
	if style == "" {
		return ""
	}
	for _, part := range strings.Split(style, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "text-align:") {
			return strings.TrimSpace(strings.TrimPrefix(part, "text-align:"))
		}
	}
	return ""
}

// --- Inline rendering -------------------------------------------------------

// inline renders the inline children of a block-level element as a single line
// of Markdown. Newlines in source text become spaces — Markdown paragraphs are
// single logical lines per Normalise rule 16.
func (r *cfRenderer) inline(n *cfNode) string {
	return r.inlineNodes(n.children)
}

func (r *cfRenderer) inlineNodes(nodes []*cfNode) string {
	var sb strings.Builder
	for _, c := range nodes {
		r.writeInline(&sb, c)
	}
	return collapseWhitespace(sb.String())
}

func (r *cfRenderer) writeInline(sb *strings.Builder, n *cfNode) {
	switch n.kind {
	case cfText, cfCData:
		sb.WriteString(escapeInlineText(n.text))
		return
	case cfElement:
		// fall through
	}

	switch n.name {
	case "strong", "b":
		sb.WriteString("**")
		r.writeInlineChildren(sb, n)
		sb.WriteString("**")
	case "em", "i":
		sb.WriteString("*")
		r.writeInlineChildren(sb, n)
		sb.WriteString("*")
	case "s", "del", "strike":
		sb.WriteString("~~")
		r.writeInlineChildren(sb, n)
		sb.WriteString("~~")
	case "code":
		sb.WriteByte('`')
		sb.WriteString(n.innerText())
		sb.WriteByte('`')
	case "br":
		// Hard line break — backslash form, per Normalise rule 3.
		sb.WriteString("\\\n")
	case "a":
		text := r.inline(n)
		href := n.attr("href")
		if href == "" {
			sb.WriteString(text)
			return
		}
		fmt.Fprintf(sb, "[%s](%s)", text, href)
		// Preserve any additional <a> attributes (target="_blank",
		// rel="noopener", class, ...) via the inline metadata sidecar so
		// they survive the round trip.
		sb.WriteString(EncodeMeta(extractLinkMeta(n)))
	case "ac:link":
		r.writeAcLink(sb, n)
	case "ac:image":
		r.writeAcImage(sb, n)
	case "ac:emoticon":
		// Confluence emoticons (heart, smile, tick, ...) have no Markdown
		// equivalent. Preserve the original element via the inline fence so
		// a push round-trip yields exactly the same emoticon Confluence
		// rendered in the first place — rather than converting to a
		// `:name:` shortcode that would push back as literal text.
		sb.WriteString(EncodeInlineFence(serializeXML(n)))
	case "ac:structured-macro":
		// Inline structured macros (status, jira issue link, mention, etc.)
		// have arbitrary parameter shapes. Preserving the original element
		// verbatim is the only safe round-trip; falling through to the
		// recurse-into-children default would concatenate parameter text
		// into the surrounding paragraph (e.g. "[I AM A STATUS]" + "Blue"
		// from a status macro's title and colour parameters).
		sb.WriteString(EncodeInlineFence(serializeXML(n)))
	default:
		// Unknown inline element — recurse to keep child text intact.
		r.writeInlineChildren(sb, n)
	}
}

func (r *cfRenderer) writeInlineChildren(sb *strings.Builder, n *cfNode) {
	for _, c := range n.children {
		r.writeInline(sb, c)
	}
}

// hasNonPageRiChild reports whether n contains any direct child element in
// the `ri:` namespace other than ri:page. Used to detect ac:link variants
// (user mentions, attachment links, space links, ...) that have no
// Markdown shape and must be fence-preserved.
func hasNonPageRiChild(n *cfNode) bool {
	for _, c := range n.children {
		if c.kind != cfElement {
			continue
		}
		if strings.HasPrefix(c.name, "ri:") && c.name != "ri:page" {
			return true
		}
	}
	return false
}

// writeAcLink renders <ac:link><ri:page .../><ac:plain-text-link-body>...
// Page references with a registered local path turn into Markdown links to
// that path; unresolvable references render as plain text using the link body
// (or, if absent, the referenced title).
//
// Non-page resource references (ri:user for @mentions, ri:attachment for
// attachment download links, ri:space, ri:blog-post, ...) have no Markdown
// equivalent. They are fence-preserved so push round-trips them back to
// Confluence intact rather than silently dropping the element.
func (r *cfRenderer) writeAcLink(sb *strings.Builder, n *cfNode) {
	page := n.firstElement("ri:page")
	body := n.firstElement("ac:plain-text-link-body")
	bodyText := ""
	if body != nil {
		bodyText = strings.TrimSpace(body.innerText())
	} else {
		bodyText = strings.TrimSpace(n.innerText())
	}

	if page == nil {
		// Non-page resource (ri:user, ri:attachment, ri:space, ...) — no
		// Markdown shape exists. Fence-preserve so push restores it
		// verbatim. Pre-fix, user mentions silently disappeared because
		// they have no plain-text-link-body and no inner text.
		if hasNonPageRiChild(n) {
			sb.WriteString(EncodeInlineFence(serializeXML(n)))
			return
		}
		// Pathological link with no resource at all — emit body text so
		// nothing visible is lost.
		if bodyText != "" {
			sb.WriteString(escapeInlineText(bodyText))
		}
		return
	}

	title := page.attr("ri:content-title")
	space := page.attr("ri:space-key")
	if bodyText == "" {
		bodyText = title
	}

	contentID := page.attr("ri:content-id")

	if r.opts.Pages != nil {
		// Prefer id-based resolution: only the page-id is unique. A page
		// outside our local tree might share a title with one inside it
		// — title-based matching would silently mis-route the link.
		if contentID != "" {
			if path, ok := r.opts.Pages.ResolvePageByID(contentID); ok {
				fmt.Fprintf(sb, "[%s](%s)", bodyText, path)
				return
			}
		} else {
			// Storage didn't include an id (older Confluence, or our own
			// title-only writes). Fall back to title resolution; it can
			// mis-route on title collisions but it's better than treating
			// every title-only ac:link as external.
			if path, ok := r.opts.Pages.ResolvePageByTitle(title, space); ok {
				fmt.Fprintf(sb, "[%s](%s)", bodyText, path)
				return
			}
		}
	}

	// Either the id-bearing link points outside our tree, or there was no
	// id and title resolution missed. Either way, the link refers to a page
	// we don't track locally — emit a Confluence-side URL so the link stays
	// clickable. Strict invariant: never silently drop the URL.
	if url := buildConfluencePageURL(r.opts.BaseURL, page); url != "" {
		fmt.Fprintf(sb, "[%s](%s)", bodyText, url)
		return
	}

	// No identifying attributes at all (no id, no space, no title) —
	// nothing to construct a URL from. Emit the body as text so the
	// reader at least sees what was there.
	if bodyText != "" {
		sb.WriteString(escapeInlineText(bodyText))
	}
}

// buildConfluencePageURL constructs a URL for an <ri:page> the local sync
// tree didn't recognise. With a base URL it produces a clickable link
// (preferring id-based paths because page IDs survive renames); without
// one it falls back to the bare path so the URL is preserved and visibly
// signals that the base URL wasn't configured at conversion time.
//
// Returns "" only if the <ri:page> has no usable attributes at all (no id,
// no space-key, no title) — at which point there's nothing to link to.
func buildConfluencePageURL(baseURL string, page *cfNode) string {
	base := strings.TrimRight(baseURL, "/")
	contentID := page.attr("ri:content-id")
	spaceKey := page.attr("ri:space-key")
	title := page.attr("ri:content-title")

	var path string
	switch {
	case contentID != "" && spaceKey != "":
		path = fmt.Sprintf("/spaces/%s/pages/%s", spaceKey, contentID)
	case contentID != "":
		path = fmt.Sprintf("/pages/viewpage.action?pageId=%s", contentID)
	case title != "":
		// Last resort: a search URL so the reader can still find the page.
		path = fmt.Sprintf("/search?text=%s", urlQueryEscape(title))
	default:
		return ""
	}
	return base + path
}

func urlQueryEscape(s string) string {
	// Lightweight percent-encoding for non-alphanumerics. We avoid pulling
	// net/url into the lexer to keep the import surface small; the spec
	// only needs to encode the few characters Confluence titles produce.
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == '~':
			sb.WriteRune(r)
		case r == ' ':
			sb.WriteByte('+')
		default:
			b := []byte(string(r))
			for _, x := range b {
				fmt.Fprintf(&sb, "%%%02X", x)
			}
		}
	}
	return sb.String()
}

// writeAcImage renders <ac:image> wrapping either <ri:attachment ri:filename>
// or <ri:url ri:value>. For attachments, the Markdown alt text defaults to the
// leaf filename without extension if none is provided in the source.
//
// Any non-trivial Confluence attributes (ac:width, ac:layout, ac:align, ...)
// are preserved via the inline metadata sidecar so they round-trip.
func (r *cfRenderer) writeAcImage(sb *strings.Builder, n *cfNode) {
	alt := n.attr("ac:alt")
	if att := n.firstElement("ri:attachment"); att != nil {
		filename := att.attr("ri:filename")
		src := filename
		if r.opts.Attachments != nil {
			if s := r.opts.Attachments.AttachmentSrc(filename); s != "" {
				src = s
			}
		}
		if alt == "" {
			alt = stripExt(filename)
		}
		fmt.Fprintf(sb, "![%s](%s)", alt, src)
		sb.WriteString(EncodeMeta(extractImageMeta(n)))
		return
	}
	if u := n.firstElement("ri:url"); u != nil {
		src := u.attr("ri:value")
		fmt.Fprintf(sb, "![%s](%s)", alt, src)
		sb.WriteString(EncodeMeta(extractImageMeta(n)))
		return
	}
}

// extractImageMeta returns the round-trippable attributes of an
// <ac:image> element — every attribute except ones already represented
// in the markdown (ac:alt) or generated by Confluence on save (macro-id,
// local-id).
func extractImageMeta(n *cfNode) map[string]string {
	skip := map[string]bool{
		"ac:alt":      true, // already encoded as the markdown alt text
		"ac:macro-id": true, // regenerated by Confluence
		"ac:local-id": true, // regenerated by Confluence
	}
	out := make(map[string]string, len(n.attrs))
	for k, v := range n.attrs {
		if skip[k] {
			continue
		}
		out[k] = v
	}
	return out
}

// extractLinkMeta returns the round-trippable attributes of an external
// <a> element — every attribute except href (already encoded as the
// markdown link target).
func extractLinkMeta(n *cfNode) map[string]string {
	out := make(map[string]string, len(n.attrs))
	for k, v := range n.attrs {
		if k == "href" {
			continue
		}
		out[k] = v
	}
	return out
}

func stripExt(filename string) string {
	if i := strings.LastIndexByte(filename, '.'); i > 0 {
		return filename[:i]
	}
	return filename
}

// --- Helpers ----------------------------------------------------------------

// collapseWhitespace turns any run of whitespace (spaces, tabs, newlines)
// into a single space and trims the ends. Markdown paragraphs are single
// logical lines, so embedded source-level newlines from XML pretty-printing
// must not survive into the output.
func collapseWhitespace(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !inSpace {
				sb.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		// Hard breaks ("\\\n") survive collapsing because we never hit the
		// '\n' branch — the backslash is a regular rune and the newline is
		// adjacent to it. To preserve them, undo the collapsing for the
		// specific "\ " sequence: rewrite to "\\\n". This keeps the few hard
		// breaks we emit intact while still flattening all other runs.
		if r == '\\' {
			sb.WriteRune(r)
			inSpace = false
			continue
		}
		inSpace = false
		sb.WriteRune(r)
	}
	out := strings.TrimSpace(sb.String())
	// Restore "\\ " (which would have been "\\\n" before collapsing) to
	// "\\\n" so hard breaks survive.
	return strings.ReplaceAll(out, "\\ ", "\\\n")
}

// escapeInlineText escapes the small set of characters that would otherwise
// take on Markdown meaning in inline context. We deliberately escape only
// characters that are syntactically dangerous (would re-parse as something
// else); cosmetic noise like "(" or ":" is left alone.
func escapeInlineText(s string) string {
	if s == "" {
		return ""
	}
	// Most Confluence content has no special chars; fast path skips allocation.
	if !strings.ContainsAny(s, "\\`*_[]<>") {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s) + 4)
	for _, r := range s {
		switch r {
		case '\\', '`', '*', '_', '[', ']', '<', '>':
			sb.WriteByte('\\')
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

// prefixBlockquote prepends "> " to every line (and ">" to blank lines) so the
// resulting block reads as a single CommonMark blockquote.
func prefixBlockquote(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if ln == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + ln
		}
	}
	return strings.Join(lines, "\n")
}

// --- XML re-serialisation for fence preservation ----------------------------

// serializeXML renders a node tree back to canonical XML for embedding in a
// Confluence-native fence. The output is deterministic (attributes sorted,
// CDATA preserved as escaped text) so two renders of the same input produce
// identical bytes — which is what the round-trip property requires.
//
// We do not attempt byte-for-byte preservation of the original input. The
// round trip is "Confluence accepts the re-emitted form and yields the same
// effect"; that property is satisfied as long as element names, attributes,
// and text content survive intact.
func serializeXML(n *cfNode) string {
	var sb strings.Builder
	writeXML(&sb, n)
	return sb.String()
}

func writeXML(sb *strings.Builder, n *cfNode) {
	switch n.kind {
	case cfText, cfCData:
		sb.WriteString(xmlEscape(n.text))
		return
	case cfElement:
		// fall through
	}
	sb.WriteByte('<')
	sb.WriteString(n.name)
	if len(n.attrs) > 0 {
		keys := make([]string, 0, len(n.attrs))
		for k := range n.attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sb.WriteByte(' ')
			sb.WriteString(k)
			sb.WriteString(`="`)
			sb.WriteString(xmlAttrEscape(n.attrs[k]))
			sb.WriteByte('"')
		}
	}
	if len(n.children) == 0 {
		sb.WriteString("/>")
		return
	}
	sb.WriteByte('>')
	for _, c := range n.children {
		writeXML(sb, c)
	}
	sb.WriteString("</")
	sb.WriteString(n.name)
	sb.WriteByte('>')
}

func xmlEscape(s string) string {
	if !strings.ContainsAny(s, "<>&") {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case '<':
			sb.WriteString("&lt;")
		case '>':
			sb.WriteString("&gt;")
		case '&':
			sb.WriteString("&amp;")
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func xmlAttrEscape(s string) string {
	if !strings.ContainsAny(s, `<>&"`) {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case '<':
			sb.WriteString("&lt;")
		case '>':
			sb.WriteString("&gt;")
		case '&':
			sb.WriteString("&amp;")
		case '"':
			sb.WriteString("&quot;")
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
