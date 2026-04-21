// Package lexer contains the pure text transforms that move content between
// Confluence storage XML, Markdown, and the local file tree. Everything in this
// package is side-effect free — no network, filesystem, or index access — so
// round-trip tests can run entirely in memory.
package lexer

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// collisionSuffixLen is the number of trailing page-ID digits appended to a
// sibling's slug when two or more siblings collide. Six digits gives effectively
// zero chance of a secondary collision among real Confluence page IDs (which
// are large integers) while keeping filenames short.
const collisionSuffixLen = 6

// Slugify converts a Confluence page title to a filesystem-safe slug. It
// applies the five-step rule specified in CLAUDE.md (lowercase, spaces →
// hyphens, strip non-alphanumeric-hyphen-underscore, collapse hyphens, trim).
//
// If the resulting slug is empty (e.g. a title composed entirely of characters
// that get stripped), it falls back to page-<pageID> so every page still has a
// stable, filesystem-safe representation. pageID may be empty for contexts
// where the fallback is impossible; in that case an empty string is returned.
func Slugify(title, pageID string) string {
	s := strings.ToLower(title)

	// Step 2: collapse whitespace sequences to a single hyphen.
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !inSpace {
				b.WriteByte('-')
				inSpace = true
			}
			continue
		}
		inSpace = false
		b.WriteRune(r)
	}
	s = b.String()

	// Step 3: drop anything that isn't [a-z0-9_-].
	b.Reset()
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		}
	}
	s = b.String()

	// Step 4: collapse runs of hyphens.
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}

	// Step 5: trim leading/trailing hyphens.
	s = strings.Trim(s, "-")

	if s == "" {
		if pageID == "" {
			return ""
		}
		return "page-" + pageID
	}
	return s
}

// ReverseSlugify converts a filename (with or without the .md extension) back
// to a best-effort page title. A trailing collision suffix of the form
// "-DDDDDD" (exactly six digits) is stripped first so that it does not become
// part of the reconstructed title.
func ReverseSlugify(filename string) string {
	s := strings.TrimSuffix(filename, ".md")
	s = stripCollisionSuffix(s)
	s = strings.ReplaceAll(s, "-", " ")

	var b strings.Builder
	b.Grow(len(s))
	newWord := true
	for _, r := range s {
		if unicode.IsSpace(r) {
			b.WriteRune(r)
			newWord = true
			continue
		}
		if newWord {
			b.WriteRune(unicode.ToUpper(r))
			newWord = false
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// stripCollisionSuffix removes a trailing "-DDDDDD" from a slug if present,
// where DDDDDD is exactly collisionSuffixLen ASCII digits. This is the inverse
// of the sibling-collision disambiguation applied by DisambiguateSiblings.
func stripCollisionSuffix(slug string) string {
	if len(slug) <= collisionSuffixLen+1 {
		return slug
	}
	cut := len(slug) - collisionSuffixLen - 1
	if slug[cut] != '-' {
		return slug
	}
	for i := cut + 1; i < len(slug); i++ {
		if slug[i] < '0' || slug[i] > '9' {
			return slug
		}
	}
	return slug[:cut]
}

// PageRef is the minimal shape DisambiguateSiblings needs. Real code passes
// index.Page or tree.Page values; tests construct it directly.
type PageRef struct {
	PageID string
	Title  string
}

// DisambiguateSiblings assigns a unique slug to each sibling page. Where two or
// more siblings produce the same base slug, the one with the numerically
// lowest Confluence page ID keeps the plain slug; every other colliding sibling
// has its page ID's last six digits appended as a suffix, e.g.
// "database-design-100042".
//
// The mapping from page ID → slug is deterministic and stable across sibling
// renames, because the "canonical winner" is selected by page ID (which never
// changes) rather than by slug ordering or encounter order.
//
// The input slice is not mutated.
func DisambiguateSiblings(siblings []PageRef) map[string]string {
	if len(siblings) == 0 {
		return map[string]string{}
	}

	// Group by base slug.
	type entry struct {
		pageID string
		title  string
	}
	groups := make(map[string][]entry, len(siblings))
	order := make([]string, 0, len(siblings)) // preserve first-seen order of slugs
	for _, p := range siblings {
		slug := Slugify(p.Title, p.PageID)
		if _, seen := groups[slug]; !seen {
			order = append(order, slug)
		}
		groups[slug] = append(groups[slug], entry{p.PageID, p.Title})
	}

	out := make(map[string]string, len(siblings))
	for _, slug := range order {
		grp := groups[slug]
		if len(grp) == 1 {
			out[grp[0].pageID] = slug
			continue
		}
		// Sort by numeric page ID (lowest wins). Fall back to lexicographic
		// for non-numeric IDs so the function is total on any input.
		sort.SliceStable(grp, func(i, j int) bool {
			return pageIDLess(grp[i].pageID, grp[j].pageID)
		})
		out[grp[0].pageID] = slug
		for _, e := range grp[1:] {
			out[e.pageID] = slug + "-" + collisionSuffix(e.pageID)
		}
	}
	return out
}

// pageIDLess orders page IDs numerically when both parse as non-negative
// integers and lexicographically otherwise. Numeric ordering matters because
// Confluence page IDs are not zero-padded, so "99" must come before "100".
func pageIDLess(a, b string) bool {
	aNum, aOK := parsePageID(a)
	bNum, bOK := parsePageID(b)
	if aOK && bOK {
		return aNum < bNum
	}
	return a < b
}

// parsePageID parses a Confluence page ID as a uint64. Returns ok=false if the
// ID contains any non-digit.
func parsePageID(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	var n uint64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + uint64(r-'0')
	}
	return n, true
}

// collisionSuffix returns the last collisionSuffixLen digits of a numeric page
// ID, left-padded with zeros if the ID is shorter than the suffix length. For
// non-numeric page IDs, it returns a zero-padded decimal hash derived from the
// ID so the suffix is always exactly collisionSuffixLen digits.
func collisionSuffix(pageID string) string {
	if n, ok := parsePageID(pageID); ok {
		// Use low-order digits — these change most often across IDs and are the
		// most collision-resistant among pages created close in time.
		mod := uint64(1)
		for range collisionSuffixLen {
			mod *= 10
		}
		return fmt.Sprintf("%0*d", collisionSuffixLen, n%mod)
	}
	// Non-numeric fallback: fold the string into a 6-digit number. Rare in
	// practice; Confluence page IDs are always numeric in current APIs.
	var h uint64 = 1469598103934665603 // FNV-1a 64-bit offset basis
	for i := range len(pageID) {
		h ^= uint64(pageID[i])
		h *= 1099511628211 // FNV-1a 64-bit prime
	}
	mod := uint64(1)
	for range collisionSuffixLen {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", collisionSuffixLen, h%mod)
}

// TitleSlugsMatch implements the Title Stability Rule: on a push-direction
// rename, the Confluence page title is updated only if the slug of the
// currently-recorded title differs from the new filename's slug. This prevents
// capitalisation and punctuation drift when a developer rename is a no-op at
// the slug level.
//
// The filenameSlug argument must already be the stripped slug (without .md and
// without any collision suffix). The indexTitle is the raw title as stored in
// the index (typically from Confluence).
func TitleSlugsMatch(indexTitle, filenameSlug string) bool {
	return Slugify(indexTitle, "") == filenameSlug
}
