package lexer

import (
	"fmt"
	"strconv"
	"strings"
)

// Front-matter is a YAML-subset metadata block that may appear as the very
// first element of a managed Markdown file. It carries the Confluence page
// identity (page_id, stable across renames and moves) and the last-seen
// Confluence version (used to detect conflicts on write and to skip body
// fetches for unchanged pages on read).
//
// The block is delimited by `---` lines:
//
//	---
//	confluence_page_id: "5233836047"
//	confluence_version: 12
//	---
//
//	# Body starts here
//
// Canonical serialisation rules:
//   - Known keys appear first, in a fixed order: confluence_page_id, then
//     confluence_version. Unknown keys are preserved verbatim after the known
//     keys, in their original relative order (forward-compat for fields this
//     release doesn't understand).
//   - String values are always double-quoted so page IDs that look numeric
//     (they often are) survive a round-trip as strings.
//   - The closing `---` is followed by one blank line before the body.
//
// ExtractFrontMatter / ApplyFrontMatter are inverses for round-trips that go
// through canonical form: `ApplyFrontMatter(ExtractFrontMatter(x)) == Normalise(x)`
// when x is itself canonical.
const (
	FrontMatterKeyPageID  = "confluence_page_id"
	FrontMatterKeyVersion = "confluence_version"
)

// FrontMatter is the parsed representation of a front-matter block.
// Zero value means "no front-matter".
type FrontMatter struct {
	PageID  string   // "" if absent
	Version int      // 0 if absent; Confluence versions start at 1
	Extra   []string // verbatim "key: value" lines for unknown fields, in original order
}

// IsEmpty reports whether fm carries no information at all.
func (fm FrontMatter) IsEmpty() bool {
	return fm.PageID == "" && fm.Version == 0 && len(fm.Extra) == 0
}

// HasFrontMatter reports whether md begins with a front-matter opener. This
// is a cheap prefix check; a `true` result does not guarantee the block is
// well-formed (use ExtractFrontMatter to validate).
func HasFrontMatter(md string) bool {
	return strings.HasPrefix(md, "---\n") || strings.HasPrefix(md, "---\r\n")
}

// ExtractFrontMatter splits md into its front-matter (if any) and the body
// that follows. A leading UTF-8 BOM is tolerated. CRLF line endings are
// handled; the returned body is LF-only.
//
// If md does not begin with a front-matter block, returns (zero, md-normalised, nil).
// If md begins with `---` but the block is not closed, returns an error and
// leaves md as body.
func ExtractFrontMatter(md string) (FrontMatter, string, error) {
	// Strip BOM and normalise line endings so scanning is simple.
	md = strings.TrimPrefix(md, "\ufeff")
	md = strings.ReplaceAll(md, "\r\n", "\n")
	md = strings.ReplaceAll(md, "\r", "\n")

	if !strings.HasPrefix(md, "---\n") {
		return FrontMatter{}, md, nil
	}
	rest := md[len("---\n"):]
	closeIdx := findFrontMatterClose(rest)
	if closeIdx < 0 {
		return FrontMatter{}, md, fmt.Errorf("front-matter: opening --- without closing ---")
	}
	inner := rest[:closeIdx]
	after := rest[closeIdx+len("---"):]
	// Consume the newline after the closing `---`, if any.
	after = strings.TrimPrefix(after, "\n")
	// Canonical form has exactly one blank line between closing `---` and body.
	// Strip any additional leading blank lines so round-trips stabilise.
	for strings.HasPrefix(after, "\n") {
		after = after[1:]
	}

	fm, err := parseFrontMatter(inner)
	if err != nil {
		return FrontMatter{}, md, err
	}
	return fm, after, nil
}

// ApplyFrontMatter prepends fm to body in canonical form. If fm is empty,
// body is returned unchanged (no `---` block is emitted for a zero FM).
//
// body should end with a single trailing newline if it is non-empty; ApplyFrontMatter
// does not add or remove trailing newlines on the body.
func ApplyFrontMatter(fm FrontMatter, body string) string {
	if fm.IsEmpty() {
		return body
	}
	var sb strings.Builder
	sb.WriteString("---\n")
	if fm.PageID != "" {
		sb.WriteString(FrontMatterKeyPageID)
		sb.WriteString(": ")
		sb.WriteString(strconv.Quote(fm.PageID))
		sb.WriteByte('\n')
	}
	if fm.Version != 0 {
		sb.WriteString(FrontMatterKeyVersion)
		sb.WriteString(": ")
		sb.WriteString(strconv.Itoa(fm.Version))
		sb.WriteByte('\n')
	}
	for _, line := range fm.Extra {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	sb.WriteString("---\n")
	if body != "" {
		sb.WriteByte('\n')
		sb.WriteString(body)
	}
	return sb.String()
}

// findFrontMatterClose returns the byte offset in s of the closing `---`
// delimiter line, or -1 if no such line exists. A delimiter line is a line
// whose entire content is exactly `---`.
func findFrontMatterClose(s string) int {
	offset := 0
	for offset < len(s) {
		nl := strings.IndexByte(s[offset:], '\n')
		var line string
		var next int
		if nl < 0 {
			line = s[offset:]
			next = len(s)
		} else {
			line = s[offset : offset+nl]
			next = offset + nl + 1
		}
		if line == "---" {
			return offset
		}
		offset = next
	}
	return -1
}

// parseFrontMatter reads the inside of a front-matter block (the text between
// the opening and closing `---` lines) into a FrontMatter. Unrecognised keys
// are preserved verbatim in Extra.
func parseFrontMatter(inner string) (FrontMatter, error) {
	var fm FrontMatter
	if inner == "" {
		return fm, nil
	}
	// Trim trailing newlines so the final split doesn't produce a spurious
	// empty line at the end.
	for strings.HasSuffix(inner, "\n") {
		inner = inner[:len(inner)-1]
	}
	for _, line := range strings.Split(inner, "\n") {
		if line == "" {
			fm.Extra = append(fm.Extra, "")
			continue
		}
		key, rawValue, ok := splitFrontMatterKV(line)
		if !ok {
			fm.Extra = append(fm.Extra, line)
			continue
		}
		switch key {
		case FrontMatterKeyPageID:
			s, err := unquoteFrontMatterString(rawValue)
			if err != nil {
				return FrontMatter{}, fmt.Errorf("front-matter %s: %w", key, err)
			}
			fm.PageID = s
		case FrontMatterKeyVersion:
			n, err := strconv.Atoi(strings.TrimSpace(rawValue))
			if err != nil {
				return FrontMatter{}, fmt.Errorf("front-matter %s: %w", key, err)
			}
			fm.Version = n
		default:
			fm.Extra = append(fm.Extra, line)
		}
	}
	return fm, nil
}

// splitFrontMatterKV splits a "key: value" line on the first colon.
// Returns ok=false if there's no colon or the key is empty.
func splitFrontMatterKV(line string) (key, value string, ok bool) {
	idx := strings.IndexByte(line, ':')
	if idx < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	if key == "" {
		return "", "", false
	}
	return key, line[idx+1:], true
}

// unquoteFrontMatterString accepts either a double-quoted string (our canonical
// emission) or a bareword (tolerated on input). Returns the unquoted value.
func unquoteFrontMatterString(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return strconv.Unquote(s)
	}
	return s, nil
}
