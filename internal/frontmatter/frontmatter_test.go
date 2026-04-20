package frontmatter

import (
	"strings"
	"testing"
	"time"
)

func TestParse_Basic(t *testing.T) {
	input := `<!-- cfmd:page_id: 123 -->
<!-- cfmd:space: ENG -->
<!-- cfmd:title: My Page -->
<!-- cfmd:parent_id: 999 -->
<!-- cfmd:version: 12 -->
<!-- cfmd:last_synced: 2026-04-21T10:30:00Z -->

# Heading

Body here.
`
	fm, body, err := Parse(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fm.PageID != "123" {
		t.Errorf("page_id = %q", fm.PageID)
	}
	if fm.Space != "ENG" {
		t.Errorf("space = %q", fm.Space)
	}
	if fm.Title != "My Page" {
		t.Errorf("title = %q", fm.Title)
	}
	if fm.ParentID != "999" {
		t.Errorf("parent_id = %q", fm.ParentID)
	}
	if fm.Version != 12 {
		t.Errorf("version = %d", fm.Version)
	}
	want, _ := time.Parse(time.RFC3339, "2026-04-21T10:30:00Z")
	if !fm.LastSynced.Equal(want) {
		t.Errorf("last_synced = %v", fm.LastSynced)
	}
	if !strings.HasPrefix(body, "# Heading") {
		t.Errorf("body: %q", body)
	}
}

func TestParse_Empty(t *testing.T) {
	fm, body, err := Parse("# Just a heading\n\ntext\n")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fm.PageID != "" || fm.Space != "" || fm.Title != "" {
		t.Errorf("expected empty frontmatter, got %+v", fm)
	}
	if !strings.HasPrefix(body, "# Just a heading") {
		t.Errorf("body lost: %q", body)
	}
}

func TestParse_Extras(t *testing.T) {
	input := `<!-- cfmd:space: ENG -->
<!-- cfmd:title: X -->
<!-- cfmd:labels: foo,bar -->

body
`
	fm, _, err := Parse(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fm.Extras["labels"] != "foo,bar" {
		t.Errorf("extras lost: %+v", fm.Extras)
	}
}

func TestSerialize_RoundTrip(t *testing.T) {
	fm := &Frontmatter{
		PageID:   "123",
		Space:    "ENG",
		Title:    "My Page",
		ParentID: "999",
		Version:  12,
		LastSynced: func() time.Time {
			t, _ := time.Parse(time.RFC3339, "2026-04-21T10:30:00Z")
			return t
		}(),
		Extras: map[string]string{"labels": "foo,bar"},
	}
	body := "# Heading\n\nBody.\n"
	out := Serialize(fm, body)

	fm2, body2, err := Parse(out)
	if err != nil {
		t.Fatalf("reparse: %v\ninput:\n%s", err, out)
	}
	if fm2.PageID != fm.PageID || fm2.Space != fm.Space || fm2.Title != fm.Title ||
		fm2.ParentID != fm.ParentID || fm2.Version != fm.Version ||
		!fm2.LastSynced.Equal(fm.LastSynced) {
		t.Errorf("round-trip mismatch:\n  got:  %+v\n  want: %+v", fm2, fm)
	}
	if fm2.Extras["labels"] != "foo,bar" {
		t.Errorf("extras lost: %+v", fm2.Extras)
	}
	if strings.TrimSpace(body2) != strings.TrimSpace(body) {
		t.Errorf("body changed:\n  got:  %q\n  want: %q", body2, body)
	}
}

func TestRequireForPush(t *testing.T) {
	cases := []struct {
		name    string
		fm      Frontmatter
		wantErr bool
	}{
		{"missing all", Frontmatter{}, true},
		{"missing space", Frontmatter{Title: "X"}, true},
		{"missing title", Frontmatter{Space: "ENG"}, true},
		{"new page ok", Frontmatter{Space: "ENG", Title: "X"}, false},
		{"existing page needs version", Frontmatter{Space: "ENG", Title: "X", PageID: "1"}, true},
		{"existing page with version ok", Frontmatter{Space: "ENG", Title: "X", PageID: "1", Version: 1}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.fm.RequireForPush()
			if (err != nil) != c.wantErr {
				t.Errorf("got err=%v, wantErr=%v", err, c.wantErr)
			}
		})
	}
}

func TestParse_NoLeadingBlank(t *testing.T) {
	// Frontmatter immediately followed by body (no blank line).
	input := `<!-- cfmd:space: ENG -->
<!-- cfmd:title: X -->
# Heading
`
	fm, body, err := Parse(input)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if fm.Space != "ENG" || fm.Title != "X" {
		t.Errorf("frontmatter lost: %+v", fm)
	}
	if !strings.HasPrefix(body, "# Heading") {
		t.Errorf("body lost: %q", body)
	}
}
