// Package frontmatter parses and serializes the HTML-comment metadata
// at the top of a cfmd-managed markdown file.
//
// Format:
//
//	<!-- cfmd:page_id: 123 -->
//	<!-- cfmd:space: ENG -->
//	<!-- cfmd:title: My Page -->
//	<!-- cfmd:parent_id: 999 -->
//	<!-- cfmd:version: 12 -->
//	<!-- cfmd:last_synced: 2026-04-21T10:30:00Z -->
//
//	# Body starts here
package frontmatter

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Frontmatter holds the metadata fields cfmd tracks for a markdown file.
type Frontmatter struct {
	PageID     string
	Space      string
	Title      string
	ParentID   string
	Version    int
	LastSynced time.Time

	// Extras preserves any `cfmd:<key>: value` lines we didn't explicitly model,
	// so we don't drop user-added metadata on re-serialization.
	Extras map[string]string
}

// lineRegexp matches "<!-- cfmd:key: value -->" with flexible whitespace.
var lineRegexp = regexp.MustCompile(`^\s*<!--\s*cfmd:([a-zA-Z_][a-zA-Z0-9_]*)\s*:\s*(.*?)\s*-->\s*$`)

// Parse reads the frontmatter block from the top of the input and returns the
// parsed Frontmatter plus the remaining body (with leading blank lines trimmed).
//
// The frontmatter block is defined as a contiguous run of `<!-- cfmd:... -->`
// lines starting at the first non-blank line. Parsing stops at the first line
// that is not a cfmd frontmatter comment.
func Parse(input string) (*Frontmatter, string, error) {
	fm := &Frontmatter{Extras: map[string]string{}}

	scanner := bufio.NewScanner(strings.NewReader(input))
	scanner.Buffer(make([]byte, 1024*1024), 64*1024*1024)

	var bodyLines []string
	inFrontmatter := true
	started := false

	for scanner.Scan() {
		line := scanner.Text()
		if inFrontmatter {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" && !started {
				// leading blank line before frontmatter; skip
				continue
			}
			m := lineRegexp.FindStringSubmatch(line)
			if m == nil {
				// End of frontmatter. This line belongs to the body.
				inFrontmatter = false
				// Skip blank lines between frontmatter and body.
				if trimmed == "" {
					continue
				}
				bodyLines = append(bodyLines, line)
				continue
			}
			started = true
			key := m[1]
			val := m[2]
			if err := fm.set(key, val); err != nil {
				return nil, "", err
			}
		} else {
			bodyLines = append(bodyLines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, "", err
	}

	body := strings.Join(bodyLines, "\n")
	return fm, body, nil
}

func (f *Frontmatter) set(key, val string) error {
	switch key {
	case "page_id":
		f.PageID = val
	case "space":
		f.Space = val
	case "title":
		f.Title = val
	case "parent_id":
		f.ParentID = val
	case "version":
		if val == "" {
			return nil
		}
		var v int
		if _, err := fmt.Sscanf(val, "%d", &v); err != nil {
			return fmt.Errorf("invalid version %q: %w", val, err)
		}
		f.Version = v
	case "last_synced":
		if val == "" {
			return nil
		}
		t, err := time.Parse(time.RFC3339, val)
		if err != nil {
			return fmt.Errorf("invalid last_synced %q: %w", val, err)
		}
		f.LastSynced = t
	default:
		f.Extras[key] = val
	}
	return nil
}

// Serialize renders the frontmatter and body back to a markdown string. The
// output always ends with a newline.
func Serialize(fm *Frontmatter, body string) string {
	var b strings.Builder
	writeLine := func(key, val string) {
		if val == "" {
			return
		}
		fmt.Fprintf(&b, "<!-- cfmd:%s: %s -->\n", key, val)
	}
	writeLine("page_id", fm.PageID)
	writeLine("space", fm.Space)
	writeLine("title", fm.Title)
	writeLine("parent_id", fm.ParentID)
	if fm.Version > 0 {
		fmt.Fprintf(&b, "<!-- cfmd:version: %d -->\n", fm.Version)
	}
	if !fm.LastSynced.IsZero() {
		fmt.Fprintf(&b, "<!-- cfmd:last_synced: %s -->\n", fm.LastSynced.UTC().Format(time.RFC3339))
	}
	// Stable ordering for extras: alphabetical by key.
	if len(fm.Extras) > 0 {
		keys := make([]string, 0, len(fm.Extras))
		for k := range fm.Extras {
			keys = append(keys, k)
		}
		// manual sort to avoid importing sort just for this
		for i := 1; i < len(keys); i++ {
			for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
				keys[j], keys[j-1] = keys[j-1], keys[j]
			}
		}
		for _, k := range keys {
			writeLine(k, fm.Extras[k])
		}
	}
	b.WriteString("\n")
	b.WriteString(strings.TrimLeft(body, "\n"))
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

// RequireForPush validates the frontmatter has the fields needed to push.
// page_id may be empty (new page will be created), in which case version is
// not required.
func (f *Frontmatter) RequireForPush() error {
	if f.Space == "" {
		return fmt.Errorf("frontmatter: missing required field 'space'")
	}
	if f.Title == "" {
		return fmt.Errorf("frontmatter: missing required field 'title'")
	}
	if f.PageID != "" && f.Version == 0 {
		return fmt.Errorf("frontmatter: page_id is set but version is missing; cannot safely update without knowing last-synced version")
	}
	return nil
}
