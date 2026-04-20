package md2storage

import (
	"strings"
	"testing"
)

// TestExhaustive_MarkdownToStorage covers every markdown construct we emit on
// pull, proving we can also parse it on push. Every row here mirrors a row in
// the storage2md exhaustive test — together they prove symmetric coverage.
func TestExhaustive_MarkdownToStorage(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		must    []string
		mustNot []string
	}{
		// Headings
		{"h1", "# One", []string{"<h1>One</h1>"}, nil},
		{"h2", "## Two", []string{"<h2>Two</h2>"}, nil},
		{"h3", "### Three", []string{"<h3>Three</h3>"}, nil},
		{"h4", "#### Four", []string{"<h4>Four</h4>"}, nil},
		{"h5", "##### Five", []string{"<h5>Five</h5>"}, nil},
		{"h6", "###### Six", []string{"<h6>Six</h6>"}, nil},

		// Inline
		{"strong", "**x**", []string{"<strong>x</strong>"}, nil},
		{"em", "*x*", []string{"<em>x</em>"}, nil},
		{"strikethrough", "~~x~~", []string{`<span style="text-decoration: line-through;">x</span>`}, nil},
		{"inline_code", "`x`", []string{"<code>x</code>"}, nil},
		{"inline_code_html_escape", "`<x>`", []string{"<code>&lt;x&gt;</code>"}, nil},

		// Block
		{"paragraph", "hello", []string{"<p>hello</p>"}, nil},
		{"hr", "---", []string{"<hr/>"}, nil},
		{"blockquote", "> quoted", []string{"<blockquote>", "<p>quoted</p>", "</blockquote>"}, nil},

		// Lists
		{"ul_single", "- only", []string{"<ul>", "<li>only</li>", "</ul>"}, nil},
		{"ul_many", "- a\n- b\n- c", []string{"<li>a</li>", "<li>b</li>", "<li>c</li>"}, nil},
		{"ol", "1. a\n2. b", []string{"<ol>", "<li>a</li>", "<li>b</li>", "</ol>"}, nil},

		// Links
		{"link", "[text](https://ex.com)", []string{`<a href="https://ex.com">text</a>`}, nil},
		{"link_ampersand_escape", "[x](https://ex.com/?a=1&b=2)", []string{`href="https://ex.com/?a=1&amp;b=2"`}, nil},
		{"autolink_email", "<mailto:hi@ex.com>", []string{`href="mailto:hi@ex.com"`}, nil},

		// Images
		{"image_remote", "![d](https://ex.com/x.png)", []string{`<ri:url ri:value="https://ex.com/x.png"/>`}, nil},
		{"image_local", "![d](./x.png)", []string{`<ri:attachment ri:filename="x.png"/>`}, nil},
		{"image_alt", "![d](https://ex.com/x.png)", []string{`ac:alt="d"`}, nil},

		// Table
		{"table", "| A | B |\n|---|---|\n| 1 | 2 |", []string{"<table>", "<th>A</th>", "<th>B</th>", "<td>1</td>", "<td>2</td>"}, nil},

		// Fenced code (friendly)
		{"code_python", "```python\nx\n```", []string{`<ac:structured-macro ac:name="code">`, `ac:name="language">python`, "x"}, nil},
		{"code_no_lang", "```\nraw\n```", []string{`<ac:structured-macro ac:name="code">`, "raw"}, []string{`ac:name="language"`}},

		// Code CDATA split
		{"code_cdata_escape", "```\n]]>\n```", []string{"]]]]><![CDATA[>"}, nil},

		// Alert admonitions (friendly)
		{"alert_note", "> [!NOTE]\n> body", []string{`<ac:structured-macro ac:name="info">`, "<p>body</p>"}, []string{"[!NOTE]"}},
		{"alert_warning", "> [!WARNING]\n> body", []string{`<ac:structured-macro ac:name="warning">`}, nil},
		{"alert_tip", "> [!TIP]\n> body", []string{`<ac:structured-macro ac:name="tip">`}, nil},
		{"alert_important", "> [!IMPORTANT]\n> body", []string{`<ac:structured-macro ac:name="note">`}, nil},
		{"alert_caution", "> [!CAUTION]\n> body", []string{`<ac:structured-macro ac:name="warning">`}, nil},

		// cfmd-raw passthrough
		{
			"passthrough_cfmd_raw_fence",
			"```cfmd-raw\n<ac:structured-macro ac:name=\"jira\"><ac:parameter ac:name=\"key\">X</ac:parameter></ac:structured-macro>\n```",
			[]string{`<ac:structured-macro ac:name="jira">`, `ac:name="key"`, "X"},
			[]string{`ac:name="code"`, "cfmd-raw"},
		},
		{
			"passthrough_legacy_html_comments",
			"<!-- cfmd:raw:begin -->\n<ac:structured-macro ac:name=\"status\"/>\n<!-- cfmd:raw:end -->",
			[]string{`<ac:structured-macro ac:name="status"/>`},
			[]string{"cfmd:raw:begin", "cfmd:raw:end"},
		},

		// Expand via <details>
		{
			"details_to_expand",
			"<details>\n<summary>click</summary>\n\nbody\n\n</details>",
			[]string{`<ac:structured-macro ac:name="expand">`, `ac:name="title">click`, `<ac:rich-text-body>`, "<p>body</p>"},
			nil,
		},
		{
			"details_without_summary",
			"<details>\n\nbody\n\n</details>",
			[]string{`<ac:structured-macro ac:name="expand">`, `<ac:rich-text-body>`, "<p>body</p>"},
			[]string{`ac:name="title"`},
		},

		// XML-escape of prose
		{"ampersand_escape", "A & B", []string{"A &amp; B"}, nil},
		{"lt_gt_escape", "2 < 3", []string{"2 &lt; 3"}, nil},

		// Raw HTML that Confluence accepts verbatim in storage format and
		// markdown engines render natively in previews.
		{"underline", "text <u>under</u> here", []string{"<u>under</u>"}, nil},
		{"superscript", "x<sup>2</sup>", []string{"<sup>2</sup>"}, nil},
		{"subscript", "H<sub>2</sub>O", []string{"<sub>2</sub>"}, nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Convert([]byte(c.in))
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			for _, s := range c.must {
				if !strings.Contains(got, s) {
					t.Errorf("missing %q:\n---in---\n%s\n---out---\n%s", s, c.in, got)
				}
			}
			for _, s := range c.mustNot {
				if strings.Contains(got, s) {
					t.Errorf("unexpected %q:\n---in---\n%s\n---out---\n%s", s, c.in, got)
				}
			}
		})
	}
}
