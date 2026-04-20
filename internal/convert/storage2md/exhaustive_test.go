package storage2md

import (
	"fmt"
	"strings"
	"testing"
)

// TestExhaustive_StorageToMarkdown covers every supported construct. Each
// case is self-contained: storage-format input → expected substring in
// markdown output. We assert on substrings rather than full-string equality
// because whitespace rules have some give, but every structurally important
// token must appear.
//
// Every row in this table corresponds to a row in the README macro matrix.
// If a row is added/removed there, the same change must appear here.
func TestExhaustive_StorageToMarkdown(t *testing.T) {
	cases := []struct {
		name string
		// in is the storage-format XML.
		in string
		// must is a list of substrings that must appear in the output.
		must []string
		// mustNot is a list of substrings that must NOT appear.
		mustNot []string
	}{
		// ---- Headings (all six levels) ----
		{name: "h1", in: "<h1>One</h1>", must: []string{"# One"}},
		{name: "h2", in: "<h2>Two</h2>", must: []string{"## Two"}},
		{name: "h3", in: "<h3>Three</h3>", must: []string{"### Three"}},
		{name: "h4", in: "<h4>Four</h4>", must: []string{"#### Four"}},
		{name: "h5", in: "<h5>Five</h5>", must: []string{"##### Five"}},
		{name: "h6", in: "<h6>Six</h6>", must: []string{"###### Six"}},

		// ---- Inline emphasis ----
		{name: "strong", in: "<p><strong>x</strong></p>", must: []string{"**x**"}},
		{name: "b_as_strong", in: "<p><b>x</b></p>", must: []string{"**x**"}},
		{name: "em", in: "<p><em>x</em></p>", must: []string{"*x*"}},
		{name: "i_as_em", in: "<p><i>x</i></p>", must: []string{"*x*"}},
		{name: "strikethrough", in: `<p><span style="text-decoration: line-through">x</span></p>`, must: []string{"~~x~~"}},
		{name: "inline_code", in: "<p><code>x</code></p>", must: []string{"`x`"}},
		{name: "br", in: "<p>a<br/>b</p>", must: []string{"a", "b"}},

		// ---- Block ----
		{name: "paragraph", in: "<p>Hello world.</p>", must: []string{"Hello world."}},
		{name: "hr", in: "<hr/>", must: []string{"---"}},
		{name: "blockquote_plain", in: "<blockquote><p>quoted</p></blockquote>", must: []string{"> quoted"}},
		{name: "blockquote_nested", in: "<blockquote><blockquote><p>deep</p></blockquote></blockquote>", must: []string{"> > deep"}},

		// ---- Lists ----
		{name: "ul_one", in: "<ul><li>only</li></ul>", must: []string{"- only"}},
		{name: "ul_many", in: "<ul><li>a</li><li>b</li><li>c</li></ul>", must: []string{"- a", "- b", "- c"}},
		{name: "ol", in: "<ol><li>a</li><li>b</li></ol>", must: []string{"1. a", "2. b"}},

		// ---- Tables ----
		{name: "table_basic", in: "<table><tbody><tr><th>Col</th></tr><tr><td>val</td></tr></tbody></table>", must: []string{"| Col |", "| val |", "|---|"}},
		{name: "table_empty_cell", in: "<table><tbody><tr><th>A</th><th>B</th></tr><tr><td></td><td>x</td></tr></tbody></table>", must: []string{"| A | B |", "|  | x |"}},

		// ---- Links ----
		{name: "link", in: `<p><a href="https://example.com">text</a></p>`, must: []string{"[text](https://example.com)"}},
		{name: "link_url_with_query", in: `<p><a href="https://example.com/?a=1">text</a></p>`, must: []string{"[text](https://example.com/?a=1)"}},

		// ---- Images: simple forms friendly-convert ----
		{
			name: "image_simple_url",
			in:   `<p><ac:image ac:alt="d"><ri:url ri:value="https://ex.com/x.png"/></ac:image></p>`,
			must: []string{"![d](https://ex.com/x.png)"},
		},
		{
			name: "image_simple_attachment",
			in:   `<p><ac:image ac:alt="x"><ri:attachment ri:filename="d.png"/></ac:image></p>`,
			must: []string{"![x](d.png)"},
		},
		{
			name: "image_no_alt",
			in:   `<p><ac:image><ri:attachment ri:filename="d.png"/></ac:image></p>`,
			must: []string{"![](d.png)"},
		},

		// ---- Images: extra attributes → passthrough ----
		{
			name:    "image_with_width_passes_through",
			in:      `<p><ac:image ac:alt="x" ac:width="200"><ri:attachment ri:filename="d.png"/></ac:image></p>`,
			must:    []string{`ac:width="200"`, "<ri:attachment"},
			mustNot: []string{"![x](d.png)"},
		},
		{
			name:    "image_with_height_passes_through",
			in:      `<p><ac:image ac:height="300"><ri:attachment ri:filename="d.png"/></ac:image></p>`,
			must:    []string{`ac:height="300"`},
			mustNot: []string{"![]"},
		},
		{
			name: "image_with_align_passes_through",
			in:   `<p><ac:image ac:align="center"><ri:attachment ri:filename="d.png"/></ac:image></p>`,
			must: []string{`ac:align="center"`},
		},
		{
			name: "image_with_border_passes_through",
			in:   `<p><ac:image ac:border="true"><ri:attachment ri:filename="d.png"/></ac:image></p>`,
			must: []string{`ac:border="true"`},
		},
		{
			name: "image_with_thumbnail_passes_through",
			in:   `<p><ac:image ac:thumbnail="true"><ri:attachment ri:filename="d.png"/></ac:image></p>`,
			must: []string{`ac:thumbnail="true"`},
		},

		// ---- ac:link ----
		{
			name: "ac_link_attachment",
			in:   `<p><ac:link><ri:attachment ri:filename="manual.pdf"/><ac:plain-text-link-body><![CDATA[user manual]]></ac:plain-text-link-body></ac:link></p>`,
			must: []string{"[user manual](manual.pdf)"},
		},
		{
			name:    "ac_link_page_passes_through",
			in:      `<p>See <ac:link><ri:page ri:content-title="Other"/><ac:plain-text-link-body><![CDATA[other]]></ac:plain-text-link-body></ac:link> for more.</p>`,
			must:    []string{`<ri:page ri:content-title="Other"`},
			mustNot: []string{"[other](other)"},
		},

		// ---- Code macro: friendly vs passthrough ----
		{
			name: "code_python_only_lang_friendly",
			in:   `<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">python</ac:parameter><ac:plain-text-body><![CDATA[print(1)]]></ac:plain-text-body></ac:structured-macro>`,
			must: []string{"```python", "print(1)", "```"},
		},
		{
			name: "code_no_lang_friendly",
			in:   `<ac:structured-macro ac:name="code"><ac:plain-text-body><![CDATA[raw code]]></ac:plain-text-body></ac:structured-macro>`,
			must: []string{"```", "raw code"},
		},
		{
			name:    "code_with_title_passes_through",
			in:      `<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">go</ac:parameter><ac:parameter ac:name="title">Main</ac:parameter><ac:plain-text-body><![CDATA[x:=1]]></ac:plain-text-body></ac:structured-macro>`,
			must:    []string{"cfmd-raw", `ac:name="title"`, "Main"},
			mustNot: []string{"```go\n"},
		},
		{
			name: "code_with_linenumbers_passes_through",
			in:   `<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">go</ac:parameter><ac:parameter ac:name="linenumbers">true</ac:parameter><ac:plain-text-body><![CDATA[x:=1]]></ac:plain-text-body></ac:structured-macro>`,
			must: []string{"cfmd-raw", `ac:name="linenumbers"`},
		},
		{
			name: "code_with_theme_passes_through",
			in:   `<ac:structured-macro ac:name="code"><ac:parameter ac:name="theme">Midnight</ac:parameter><ac:plain-text-body><![CDATA[x]]></ac:plain-text-body></ac:structured-macro>`,
			must: []string{"cfmd-raw", `ac:name="theme"`},
		},
		{
			name: "code_with_firstline_passes_through",
			in:   `<ac:structured-macro ac:name="code"><ac:parameter ac:name="firstline">10</ac:parameter><ac:plain-text-body><![CDATA[x]]></ac:plain-text-body></ac:structured-macro>`,
			must: []string{"cfmd-raw", `ac:name="firstline"`},
		},
		{
			name: "code_with_collapse_passes_through",
			in:   `<ac:structured-macro ac:name="code"><ac:parameter ac:name="collapse">true</ac:parameter><ac:plain-text-body><![CDATA[x]]></ac:plain-text-body></ac:structured-macro>`,
			must: []string{"cfmd-raw", `ac:name="collapse"`},
		},

		// ---- Admonition macros: friendly vs passthrough ----
		{
			name: "info_simple",
			in:   `<ac:structured-macro ac:name="info"><ac:rich-text-body><p>Be aware.</p></ac:rich-text-body></ac:structured-macro>`,
			must: []string{"> [!NOTE]", "> Be aware."},
		},
		{
			name: "warning_simple",
			in:   `<ac:structured-macro ac:name="warning"><ac:rich-text-body><p>Danger.</p></ac:rich-text-body></ac:structured-macro>`,
			must: []string{"> [!WARNING]", "> Danger."},
		},
		{
			name: "tip_simple",
			in:   `<ac:structured-macro ac:name="tip"><ac:rich-text-body><p>Helpful.</p></ac:rich-text-body></ac:structured-macro>`,
			must: []string{"> [!TIP]", "> Helpful."},
		},
		{
			name: "note_simple",
			in:   `<ac:structured-macro ac:name="note"><ac:rich-text-body><p>Important.</p></ac:rich-text-body></ac:structured-macro>`,
			must: []string{"> [!IMPORTANT]", "> Important."},
		},
		{
			name:    "info_with_title_passes_through",
			in:      `<ac:structured-macro ac:name="info"><ac:parameter ac:name="title">Heads up</ac:parameter><ac:rich-text-body><p>Body.</p></ac:rich-text-body></ac:structured-macro>`,
			must:    []string{"cfmd-raw", "Heads up"},
			mustNot: []string{"> [!NOTE]"},
		},
		{
			name:    "info_with_icon_passes_through",
			in:      `<ac:structured-macro ac:name="info"><ac:parameter ac:name="icon">false</ac:parameter><ac:rich-text-body><p>Body.</p></ac:rich-text-body></ac:structured-macro>`,
			must:    []string{"cfmd-raw", `ac:name="icon"`},
			mustNot: []string{"> [!NOTE]"},
		},
		{
			name:    "warning_with_title_passes_through",
			in:      `<ac:structured-macro ac:name="warning"><ac:parameter ac:name="title">Risk</ac:parameter><ac:rich-text-body><p>x</p></ac:rich-text-body></ac:structured-macro>`,
			must:    []string{"cfmd-raw", "Risk"},
			mustNot: []string{"> [!WARNING]"},
		},

		// ---- Expand → HTML ----
		{
			name: "expand_with_title",
			in:   `<ac:structured-macro ac:name="expand"><ac:parameter ac:name="title">Click</ac:parameter><ac:rich-text-body><p>body</p></ac:rich-text-body></ac:structured-macro>`,
			must: []string{"<details>", "<summary>Click</summary>", "body", "</details>"},
		},
		{
			name:    "expand_no_title",
			in:      `<ac:structured-macro ac:name="expand"><ac:rich-text-body><p>body</p></ac:rich-text-body></ac:structured-macro>`,
			must:    []string{"<details>", "<summary>Click to expand</summary>", "body", "</details>"},
			mustNot: []string{`ac:name="title"`},
		},
		{
			name: "expand_with_code_inside",
			in:   `<ac:structured-macro ac:name="expand"><ac:parameter ac:name="title">Logs</ac:parameter><ac:rich-text-body><ac:structured-macro ac:name="code"><ac:parameter ac:name="language">go</ac:parameter><ac:plain-text-body><![CDATA[x:=1]]></ac:plain-text-body></ac:structured-macro></ac:rich-text-body></ac:structured-macro>`,
			must: []string{"<details>", "<summary>Logs</summary>", "```go", "x:=1", "```", "</details>"},
		},
		{
			name: "expand_with_list_inside",
			in:   `<ac:structured-macro ac:name="expand"><ac:parameter ac:name="title">Items</ac:parameter><ac:rich-text-body><ul><li>a</li><li>b</li></ul></ac:rich-text-body></ac:structured-macro>`,
			must: []string{"<details>", "<summary>Items</summary>", "- a", "- b", "</details>"},
		},

		// ---- CDATA handling in code bodies ----
		{
			name: "code_xml_body",
			in:   `<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">xml</ac:parameter><ac:plain-text-body><![CDATA[<tag attr="v"/>]]></ac:plain-text-body></ac:structured-macro>`,
			must: []string{"```xml", `<tag attr="v"/>`},
		},

		// ---- Additional HTML tags per Confluence storage-format docs ----
		// https://confluence.atlassian.com/doc/confluence-storage-format-790796544.html
		{
			name:    "pre_block",
			in:      `<pre>text with &lt;angles&gt; and "quotes"</pre>`,
			must:    []string{"```\n", `text with <angles> and "quotes"`},
			mustNot: []string{"cfmd-raw"},
		},
		{
			name: "underline_inline",
			in:   `<p>before <u>under</u> after</p>`,
			must: []string{"<u>under</u>"},
		},
		{
			name: "superscript_inline",
			in:   `<p>x<sup>2</sup></p>`,
			must: []string{"<sup>2</sup>"},
		},
		{
			name: "subscript_inline",
			in:   `<p>H<sub>2</sub>O</p>`,
			must: []string{"<sub>2</sub>"},
		},
		{
			name: "hard_line_break",
			in:   `<p>line1<br/>line2</p>`,
			must: []string{"line1", "line2"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Convert([]byte(c.in))
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			for _, s := range c.must {
				if !strings.Contains(got, s) {
					t.Errorf("missing %q in output:\n---input---\n%s\n---output---\n%s", s, c.in, got)
				}
			}
			for _, s := range c.mustNot {
				if strings.Contains(got, s) {
					t.Errorf("unexpected %q in output:\n---input---\n%s\n---output---\n%s", s, c.in, got)
				}
			}
		})
	}
}

// TestUnknownMacros_AlwaysPassThrough covers every documented Confluence
// macro that we DON'T convert to friendly markdown. Each is verified to
// round-trip via cfmd-raw passthrough with the ac:name attribute preserved.
//
// This is the safety net: adding/removing a macro from the support table
// must never silently change behavior — this test will flag it.
func TestUnknownMacros_AlwaysPassThrough(t *testing.T) {
	// Every built-in Confluence macro name that we deliberately passthrough.
	// Source: https://confluence.atlassian.com/doc/macros-139387.html plus the
	// storage-format-for-macros reference.
	passthroughMacros := []string{
		// Structure / layout
		"panel", "section", "column", "anchor", "details", "detailssummary",
		"excerpt", "excerpt-include",
		// Navigation / dynamic content
		"toc", "children", "include", "livesearch", "pagetree", "pagetree-search",
		"navmap", "contentbylabel",
		// Status / visual
		"status",
		// Jira / external
		"jiraissues", "jira", "jira-chart", "jiraroadmap",
		// External embeds
		"widget", "rss", "html", "html-include", "multimedia", "gallery",
		// Office files
		"viewxls", "viewdoc", "viewppt", "viewpdf", "view-file",
		// Dynamic lists / reports
		"attachments", "blog-posts", "change-history", "contributors",
		"contributors-summary", "content-by-user", "content-report-table",
		"popular-labels", "recently-updated", "recently-updated-dashboard",
		"recently-used-labels", "related-labels", "listlabels", "listusers",
		"space-attachments", "space-details", "spaces-list", "tasks-report-macro",
		"team-calendars", "profile", "profile-picture", "favpages", "global-reports",
		// Utility
		"gadget", "cheese", "loremipsum", "junitreport", "im", "chart",
		"create-space-button", "noformat", "roadmap",
	}

	for _, name := range passthroughMacros {
		t.Run(name, func(t *testing.T) {
			in := fmt.Sprintf(`<ac:structured-macro ac:name="%s"><ac:parameter ac:name="foo">bar</ac:parameter></ac:structured-macro>`, name)
			got, err := Convert([]byte(in))
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			if !strings.Contains(got, "```cfmd-raw") {
				t.Errorf("macro %q not wrapped in cfmd-raw fence:\n%s", name, got)
			}
			if !strings.Contains(got, fmt.Sprintf(`ac:name="%s"`, name)) {
				t.Errorf("macro %q name lost in passthrough:\n%s", name, got)
			}
			if !strings.Contains(got, `ac:name="foo"`) || !strings.Contains(got, "bar") {
				t.Errorf("macro %q params lost in passthrough:\n%s", name, got)
			}
		})
	}
}
