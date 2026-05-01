package lexer

import (
	"regexp"
	"sort"
	"strings"
)

// Inline metadata sidecar — a small HTML comment that travels immediately
// after an inline construct (image, external link) to carry attributes
// that have no native Markdown shape. Without it, Confluence's image
// width/layout, link target/rel, and similar attributes would be silently
// dropped on the first edit-and-push cycle.
//
// Shape (one line, sits flush against the construct it decorates):
//
//	![alt](src)<!--gfl:meta ac:width="1006" ac:layout="center"-->
//	[text](https://example.com)<!--gfl:meta target="_blank" rel="noopener"-->
//
// Adjacency is strict: the comment must be the immediate next inline
// sibling of the construct (no spaces, no newline). The pull-direction
// emission always emits them strictly adjacent; manual edits that
// insert whitespace will detach the metadata, which is then treated as
// stray and dropped on push.
//
// Why a comment rather than a custom element: any decent text editor and
// markdown viewer treats HTML comments as invisible. A custom element
// would render as nothing in most viewers but might trip linters or
// editor warnings; the comment is universally inert.
const (
	metaPrefix = "<!--gfl:meta "
	metaSuffix = "-->"
)

// EncodeMeta serialises a map of attributes into the gfl:meta comment.
// Keys are sorted so the output is deterministic. Returns "" if attrs is
// empty so callers can unconditionally append the result.
//
// Values are escaped for the HTML-attribute-style (`key="value"`) syntax:
// `&` and `"` are the only characters that need escaping inside a
// double-quoted attribute value.
func EncodeMeta(attrs map[string]string) string {
	if len(attrs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString(metaPrefix)
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(k)
		sb.WriteString(`="`)
		sb.WriteString(escapeMetaValue(attrs[k]))
		sb.WriteByte('"')
	}
	sb.WriteString(metaSuffix)
	return sb.String()
}

// DecodeMeta parses a gfl:meta comment into its attribute map. ok=false
// means the input isn't a meta comment shape — the caller should treat
// it as ordinary inline raw HTML.
func DecodeMeta(s string) (map[string]string, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, metaPrefix) || !strings.HasSuffix(s, metaSuffix) {
		return nil, false
	}
	body := strings.TrimSpace(s[len(metaPrefix) : len(s)-len(metaSuffix)])
	out := make(map[string]string)
	for _, m := range metaAttrPattern.FindAllStringSubmatch(body, -1) {
		out[m[1]] = unescapeMetaValue(m[2])
	}
	if len(out) == 0 {
		// A meta comment with the right shell but no attributes is
		// degenerate — treat it as not-a-meta so the caller can decide
		// what to do (dropping it silently in our case).
		return nil, false
	}
	return out, true
}

// IsMeta reports whether s looks like a gfl:meta comment. A cheap
// classifier for callers (chiefly md_to_cf's RawHTML branch) that need
// to know whether to drop a stray comment.
func IsMeta(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, metaPrefix) && strings.HasSuffix(s, metaSuffix)
}

// metaAttrPattern matches `key="value"` pairs. The key is an XML-attribute-
// shaped name: starts with a letter/underscore, then letters, digits,
// underscores, colons (for namespaced names like ac:width), dots, or
// hyphens. The value is anything not containing `"`.
var metaAttrPattern = regexp.MustCompile(`([A-Za-z_][\w:.-]*)="([^"]*)"`)

func escapeMetaValue(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

func unescapeMetaValue(s string) string {
	s = strings.ReplaceAll(s, "&quot;", `"`)
	s = strings.ReplaceAll(s, "&amp;", "&")
	return s
}
