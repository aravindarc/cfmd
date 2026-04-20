// Package convert hosts round-trip tests between md2storage and storage2md.
// These tests are the load-bearing proof that the "pull an existing page,
// edit part of it, push back without garbling" workflow is safe.
//
// Two complementary assertions:
//
//  1. TestRoundTrip_MarkdownStable: for every supported friendly-conversion
//     construct, md → storage → md produces the same markdown (modulo
//     whitespace). This catches asymmetries in the two converters.
//
//  2. TestRoundTrip_StoragePreserved: for every construct we preserve via
//     passthrough (unknown macros, sized images, internal links, etc.), the
//     original storage XML is recoverable after storage → md → storage.
//     Byte-identity would be too strict (whitespace normalization, attribute
//     reordering), so we instead assert that every ac:* and ri:* attribute
//     present in the input is present in the re-generated output.
package convert

import (
	"regexp"
	"strings"
	"testing"

	"github.com/aravindarc/cfmd/internal/convert/md2storage"
	"github.com/aravindarc/cfmd/internal/convert/storage2md"
)

// normalizeMarkdown canonicalizes trivial cosmetic differences between two
// markdown strings that a round-trip can legally introduce but that have no
// semantic effect:
//
//   - runs of blank lines collapse to a single blank line
//   - leading/trailing whitespace is trimmed
//   - GFM table separator cells shrink to their minimum (3 dashes) — writers
//     often align `|------|------|` but readers accept `|---|---|`. We emit
//     the minimum form; the test should not penalize that
//   - trailing spaces on lines are stripped
//
// This is the right definition for a "semantic round-trip" test: output need
// not be byte-identical to input, just meaningfully equivalent.
func normalizeMarkdown(s string) string {
	// Collapse blank-line runs.
	blanks := regexp.MustCompile(`\n{2,}`)
	s = blanks.ReplaceAllString(s, "\n\n")
	// Shrink long runs of dashes inside table separators.
	sepCellPat := regexp.MustCompile(`-{4,}`)
	s = sepCellPat.ReplaceAllString(s, "---")
	// Strip trailing spaces per line.
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	s = strings.Join(lines, "\n")
	return strings.TrimSpace(s)
}

func TestRoundTrip_MarkdownStable(t *testing.T) {
	cases := []struct {
		name string
		md   string
	}{
		{"h1", "# Hello"},
		{"h2_to_h6",
			"## H2\n\n### H3\n\n#### H4\n\n##### H5\n\n###### H6"},
		{"paragraph", "Just a paragraph."},
		{"bold_italic_strike",
			"Some **bold**, some *italic*, and some ~~strike~~."},
		{"inline_code", "Use `go test` to run."},
		{"blockquote", "> quoted line"},
		{"hr", "Before\n\n---\n\nAfter"},
		{"ul",
			"- a\n- b\n- c"},
		{"ol",
			"1. first\n2. second\n3. third"},
		{"table",
			"| Name | Role |\n|------|------|\n| Alice | Eng |\n| Bob | PM |"},
		{"link", "See [docs](https://example.com)."},
		{"image_remote",
			"![diagram](https://example.com/d.png)"},
		{"alert_note",
			"> [!NOTE]\n> Launching Q2."},
		{"alert_warning",
			"> [!WARNING]\n> Be careful."},
		{"alert_tip",
			"> [!TIP]\n> A tip."},
		{"alert_important",
			"> [!IMPORTANT]\n> Read this."},
		{"code_block_python",
			"```python\nprint(\"hi\")\n```"},
		{"code_block_no_lang",
			"```\nraw text\n```"},
		{"mixed_1",
			"# Title\n\nProse with **bold**, *italic*, and [a link](https://x.com).\n\n- item 1\n- item 2\n\n```go\nx := 1\n```"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			storage, err := md2storage.Convert([]byte(c.md))
			if err != nil {
				t.Fatalf("md→storage: %v", err)
			}
			back, err := storage2md.Convert([]byte(storage))
			if err != nil {
				t.Fatalf("storage→md: %v", err)
			}
			got := normalizeMarkdown(back)
			want := normalizeMarkdown(c.md)
			if got != want {
				t.Errorf("round-trip diverged:\n  orig md:\n%s\n  storage:\n%s\n  final md:\n%s",
					c.md, storage, back)
			}
		})
	}
}

// extractACAttrs returns the set of "name=\"value\"" tokens in input that
// belong to ac: or ri: attributes (matching simple-quoted form). This is a
// coarse fidelity check — we don't care about attribute order or whitespace,
// only that each attribute key+value present in input is present in output.
var acRiAttrPattern = regexp.MustCompile(`((?:ac|ri):[a-zA-Z-]+)="([^"]*)"`)

func extractACAttrs(s string) map[string]string {
	m := map[string]string{}
	for _, match := range acRiAttrPattern.FindAllStringSubmatch(s, -1) {
		key := match[1]
		val := match[2]
		// When the same attribute key appears multiple times, join values so
		// we don't clobber (e.g., multiple <ac:parameter ac:name="X"> entries
		// in a macro — they all have ac:name but differ elsewhere).
		if existing, ok := m[key]; ok {
			m[key] = existing + "|" + val
		} else {
			m[key] = val
		}
	}
	return m
}

// containsAllAttrs asserts every ac:/ri: attribute (key+value) in want also
// appears in got. Extra attributes in got are allowed.
func containsAllAttrs(t *testing.T, got, want string) {
	t.Helper()
	wantAttrs := extractACAttrs(want)
	gotAttrs := extractACAttrs(got)
	for k, v := range wantAttrs {
		gv, ok := gotAttrs[k]
		if !ok {
			t.Errorf("attribute %q lost through round-trip (was %q)\n---want---\n%s\n---got---\n%s",
				k, v, want, got)
			continue
		}
		// Split by | and check every fragment of want is in got.
		for _, vp := range strings.Split(v, "|") {
			if !strings.Contains(gv, vp) {
				t.Errorf("attribute %q value %q lost through round-trip (got %q)\n---want---\n%s\n---got---\n%s",
					k, vp, gv, want, got)
			}
		}
	}
}

func TestRoundTrip_StoragePreserved(t *testing.T) {
	cases := []struct {
		name    string
		storage string
	}{
		{
			"image_with_width",
			`<p><ac:image ac:alt="x" ac:width="200"><ri:attachment ri:filename="d.png"/></ac:image></p>`,
		},
		{
			"image_with_height_and_align",
			`<p><ac:image ac:height="300" ac:align="center"><ri:attachment ri:filename="d.png"/></ac:image></p>`,
		},
		{
			"image_with_border",
			`<p><ac:image ac:border="true"><ri:attachment ri:filename="d.png"/></ac:image></p>`,
		},
		{
			"image_with_thumbnail",
			`<p><ac:image ac:thumbnail="true"><ri:attachment ri:filename="d.png"/></ac:image></p>`,
		},
		{
			"internal_page_link",
			`<p>See <ac:link><ri:page ri:content-title="Other"/><ac:plain-text-link-body><![CDATA[other]]></ac:plain-text-link-body></ac:link>.</p>`,
		},
		{
			"code_with_linenumbers",
			`<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">python</ac:parameter><ac:parameter ac:name="linenumbers">true</ac:parameter><ac:plain-text-body><![CDATA[x=1]]></ac:plain-text-body></ac:structured-macro>`,
		},
		{
			"code_with_title_and_theme",
			`<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">go</ac:parameter><ac:parameter ac:name="title">Main</ac:parameter><ac:parameter ac:name="theme">Midnight</ac:parameter><ac:plain-text-body><![CDATA[x:=1]]></ac:plain-text-body></ac:structured-macro>`,
		},
		{
			"info_with_title",
			`<ac:structured-macro ac:name="info"><ac:parameter ac:name="title">Heads up</ac:parameter><ac:rich-text-body><p>Body.</p></ac:rich-text-body></ac:structured-macro>`,
		},
		{
			"status_macro",
			`<ac:structured-macro ac:name="status"><ac:parameter ac:name="colour">Green</ac:parameter><ac:parameter ac:name="title">Done</ac:parameter></ac:structured-macro>`,
		},
		{
			"panel_macro",
			`<ac:structured-macro ac:name="panel"><ac:parameter ac:name="bgColor">#72bc72</ac:parameter><ac:parameter ac:name="title">Panel</ac:parameter><ac:rich-text-body><p>x</p></ac:rich-text-body></ac:structured-macro>`,
		},
		{
			"jira_macro",
			`<ac:structured-macro ac:name="jira"><ac:parameter ac:name="key">PROJ-123</ac:parameter></ac:structured-macro>`,
		},
		{
			"toc_macro",
			`<ac:structured-macro ac:name="toc"><ac:parameter ac:name="maxLevel">3</ac:parameter></ac:structured-macro>`,
		},
		{
			"children_macro",
			`<ac:structured-macro ac:name="children"><ac:parameter ac:name="depth">2</ac:parameter></ac:structured-macro>`,
		},
		{
			"expand_macro",
			// Expand ↔ <details> has a dedicated path but must still preserve
			// the title and body structure.
			`<ac:structured-macro ac:name="expand"><ac:parameter ac:name="title">Click</ac:parameter><ac:rich-text-body><p>body</p></ac:rich-text-body></ac:structured-macro>`,
		},
	}

	// These aren't in the table above because they don't have ac:/ri: attrs
	// to check, but they deserve a round-trip identity assertion.
	identityCases := []struct {
		name string
		in   string
		// A substring that must appear in the re-emitted storage.
		expect string
	}{
		{"pre_block", "<pre>some preformatted text</pre>", "some preformatted text"},
		{"underline_tag", "<p>see <u>this</u></p>", "<u>this</u>"},
		{"superscript_tag", "<p>x<sup>2</sup></p>", "<sup>2</sup>"},
		{"subscript_tag", "<p>H<sub>2</sub>O</p>", "<sub>2</sub>"},
	}
	for _, ic := range identityCases {
		t.Run("identity_"+ic.name, func(t *testing.T) {
			md, err := storage2md.Convert([]byte(ic.in))
			if err != nil {
				t.Fatal(err)
			}
			back, err := md2storage.Convert([]byte(md))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(back, ic.expect) {
				t.Errorf("%s: lost through round-trip\n  in:    %s\n  md:    %s\n  back:  %s",
					ic.name, ic.in, md, back)
			}
		})
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			md, err := storage2md.Convert([]byte(c.storage))
			if err != nil {
				t.Fatalf("storage→md: %v", err)
			}
			back, err := md2storage.Convert([]byte(md))
			if err != nil {
				t.Fatalf("md→storage: %v", err)
			}
			containsAllAttrs(t, back, c.storage)
		})
	}
}

// TestRoundTrip_EditBetween proves the strongest property: pull a mixed page,
// edit a paragraph that sits BETWEEN passthrough blocks, push back, and
// verify that (a) the edit is reflected and (b) no passthrough block is
// altered.
func TestRoundTrip_EditBetween(t *testing.T) {
	original := `<h1>Doc</h1>` +
		`<p>Unchanged intro paragraph.</p>` +
		`<ac:structured-macro ac:name="status"><ac:parameter ac:name="colour">Green</ac:parameter><ac:parameter ac:name="title">Done</ac:parameter></ac:structured-macro>` +
		`<p>This will be edited.</p>` +
		`<ac:structured-macro ac:name="jira"><ac:parameter ac:name="key">X-1</ac:parameter></ac:structured-macro>` +
		`<p>Tail paragraph.</p>`

	md, err := storage2md.Convert([]byte(original))
	if err != nil {
		t.Fatalf("storage→md: %v", err)
	}
	// Simulate a user edit of the middle paragraph.
	edited := strings.Replace(md, "This will be edited.", "This is the edited version.", 1)
	if edited == md {
		t.Fatalf("test setup bug: edit substitution didn't apply; md was:\n%s", md)
	}

	back, err := md2storage.Convert([]byte(edited))
	if err != nil {
		t.Fatalf("md→storage: %v", err)
	}

	// Assertion 1: the edit landed.
	if !strings.Contains(back, "This is the edited version.") {
		t.Errorf("edit not reflected in push output:\n%s", back)
	}
	// Assertion 2: all passthrough attributes survived.
	wantAttrs := []string{
		`ac:name="status"`,
		`ac:name="colour"`, // present in status
		"Green",
		"Done",
		`ac:name="jira"`,
		`ac:name="key"`,
		"X-1",
	}
	for _, s := range wantAttrs {
		if !strings.Contains(back, s) {
			t.Errorf("passthrough content lost (%q):\n%s", s, back)
		}
	}
	// Assertion 3: pre-edit paragraphs are still there untouched.
	if !strings.Contains(back, "Unchanged intro paragraph.") {
		t.Errorf("intro paragraph lost:\n%s", back)
	}
	if !strings.Contains(back, "Tail paragraph.") {
		t.Errorf("tail paragraph lost:\n%s", back)
	}
}
