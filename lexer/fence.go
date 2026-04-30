package lexer

import (
	"encoding/base64"
	"strings"
)

// Confluence-native fence format (see CLAUDE.md → Confluence-Native Fence
// Preservation). The fence is a single CommonMark HTML block whose body is the
// storage XML, base64-encoded and line-wrapped. Choosing base64 over a readable
// inner comment sidesteps the problem that storage XML can legitimately contain
// "-->" (e.g. inside a CDATA section), which an HTML comment cannot.
//
// The on-disk shape, exactly:
//
//	<!-- gfl:storage:block:v1:b64
//	<base64 wrapped at 76 cols>
//	-->
//
// goldmark parses this as a single HTMLBlock (HTML block start condition 2)
// and the canonical Markdown renderer emits it verbatim, so no special path is
// needed in Normalise. md_to_cf hands every HTMLBlock to DecodeBlockFence; on
// a match, the decoded XML is spliced back into the storage output unchanged.
const (
	fenceOpenLine = "<!-- gfl:storage:block:v1:b64"
	fenceCloseTag = "-->"
	// Base64 line width inside the fence. 76 matches the historical MIME/PEM
	// convention; the exact value is not load-bearing as long as Encode and
	// Decode agree (Decode is whitespace-tolerant).
	fenceB64Width = 76
)

// EncodeBlockFence wraps a verbatim Confluence storage XML payload in the
// v1/b64 block fence. The result is always a single HTML block: one opening
// comment line, one or more base64 body lines, and a closing "-->". An empty
// payload is encoded as a fence with no body lines so that the round trip
// through DecodeBlockFence returns the empty string unchanged.
func EncodeBlockFence(storageXML string) string {
	encoded := base64.StdEncoding.EncodeToString([]byte(storageXML))
	var sb strings.Builder
	sb.WriteString(fenceOpenLine)
	sb.WriteByte('\n')
	for i := 0; i < len(encoded); i += fenceB64Width {
		end := min(i+fenceB64Width, len(encoded))
		sb.WriteString(encoded[i:end])
		sb.WriteByte('\n')
	}
	sb.WriteString(fenceCloseTag)
	return sb.String()
}

// DecodeBlockFence inspects a candidate HTML block and, if it has the v1/b64
// fence shape, returns the original storage XML. ok=false means the block is
// some other HTML — the caller should treat it as a regular HTML block.
//
// Recognition is lenient about surrounding whitespace and trailing newlines so
// that fences which have passed through editors that normalise line endings
// or strip trailing blank lines still round-trip.
func DecodeBlockFence(htmlBlock string) (string, bool) {
	s := strings.TrimRight(htmlBlock, "\n")
	lines := strings.Split(s, "\n")
	if len(lines) < 2 {
		return "", false
	}
	if strings.TrimRight(lines[0], " \t") != fenceOpenLine {
		return "", false
	}
	if strings.TrimSpace(lines[len(lines)-1]) != fenceCloseTag {
		return "", false
	}
	// Concatenate body lines and strip all whitespace; base64 decoders accept
	// lines of any width but not interior whitespace.
	var body strings.Builder
	for _, ln := range lines[1 : len(lines)-1] {
		for _, r := range ln {
			if r == ' ' || r == '\t' || r == '\r' {
				continue
			}
			body.WriteRune(r)
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(body.String())
	if err != nil {
		return "", false
	}
	return string(decoded), true
}

// IsBlockFence reports whether s begins with the v1/b64 fence opening line.
// Useful for cheap classification when walking an AST without paying the cost
// of a full base64 decode.
func IsBlockFence(s string) bool {
	s = strings.TrimLeft(s, " \t")
	if !strings.HasPrefix(s, fenceOpenLine) {
		return false
	}
	// The opening token must be the entire first line (no trailing junk on the
	// same line that would change the meaning).
	first, _, ok := strings.Cut(s[len(fenceOpenLine):], "\n")
	if !ok {
		return false
	}
	return strings.TrimRight(first, " \t") == ""
}

// Inline fence — a single self-closing HTML element used for storage
// constructs that appear inside a paragraph (e.g. <ac:emoticon>, inline
// <ac:structured-macro>).
//
// Shape:
//
//	<gfl-fence data-v1-b64="BASE64DATA"/>
//
// The element form is chosen specifically to avoid CommonMark's HTML block
// detection. The earlier comment form `<!--gfl:...-->` matched HTML block
// Type 2 (any line starting with `<!--`), which silently swallowed any
// paragraph that began with a fence — including the rest of that line and
// any later fences — and the block fell to the HTML-block escape path on
// push.
//
// `gfl-fence` is a custom element name (ASCII letter + hyphen-allowed name
// per the CommonMark/HTML5 tag-name rule). It is:
//
//   - Not in the Type 6 HTML-block tag-name list (those are well-known
//     block elements like div, p, table); and
//   - Not eligible for Type 7 at start-of-line unless the tag is followed
//     only by whitespace or end-of-line — so a paragraph starting with a
//     fence followed by more text/fences stays a paragraph.
//
// Base64 alphabet (`A-Z a-z 0-9 + / =`) is safe inside an HTML attribute
// value quoted with `"` — none of the alphabet characters require
// escaping, and there is no `"` to terminate the attribute prematurely.
const (
	inlineFencePrefix = `<gfl-fence data-v1-b64="`
	inlineFenceSuffix = `"/>`
)

// EncodeInlineFence wraps a verbatim Confluence storage XML payload in the
// v1/b64 inline fence — a single self-closing custom-element tag. The
// result is always one line and contains no characters that would
// prematurely close the surrounding paragraph or trigger HTML-block
// detection at line start.
func EncodeInlineFence(storageXML string) string {
	return inlineFencePrefix + base64.StdEncoding.EncodeToString([]byte(storageXML)) + inlineFenceSuffix
}

// DecodeInlineFence inspects a candidate raw-inline-HTML segment and, if it
// matches the inline fence shape, returns the original storage XML.
// ok=false means the segment is some other inline HTML — the caller
// should treat it as ordinary inline raw HTML.
func DecodeInlineFence(htmlSegment string) (string, bool) {
	s := strings.TrimSpace(htmlSegment)
	if !strings.HasPrefix(s, inlineFencePrefix) || !strings.HasSuffix(s, inlineFenceSuffix) {
		return "", false
	}
	body := strings.TrimSpace(s[len(inlineFencePrefix) : len(s)-len(inlineFenceSuffix)])
	decoded, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return "", false
	}
	return string(decoded), true
}

// IsInlineFence reports whether s looks like an inline fence element.
// A cheap classifier for callers that don't need to decode the body yet.
func IsInlineFence(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, inlineFencePrefix) && strings.HasSuffix(s, inlineFenceSuffix)
}
