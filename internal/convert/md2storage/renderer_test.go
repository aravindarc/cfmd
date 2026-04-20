package md2storage

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Run `go test ./internal/convert/md2storage -update` to rewrite golden files.
var updateGolden = flag.Bool("update", false, "rewrite golden files")

func TestGolden(t *testing.T) {
	testdataRoot := filepath.Join("..", "..", "..", "testdata", "md2storage")
	entries, err := os.ReadDir(testdataRoot)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(testdataRoot, name)
			inputPath := filepath.Join(dir, "input.md")
			goldenPath := filepath.Join(dir, "expected.xhtml")
			input, err := os.ReadFile(inputPath)
			if err != nil {
				t.Fatalf("read input: %v", err)
			}
			got, err := Convert(input)
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			got = strings.TrimRight(got, "\n") + "\n"

			if *updateGolden {
				if err := os.WriteFile(goldenPath, []byte(got), 0644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden: %v (run with -update to create)", err)
			}
			wantStr := strings.TrimRight(string(want), "\n") + "\n"
			if got != wantStr {
				t.Errorf("mismatch for %s:\n--- got ---\n%s\n--- want ---\n%s", name, got, wantStr)
			}
		})
	}
}

// TestBasicSmoke tests a handful of individual features without golden files,
// so failures give precise reasons.
func TestBasicSmoke(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			"h1",
			"# Hello",
			"<h1>Hello</h1>",
		},
		{
			"bold",
			"a **bold** b",
			"<p>a <strong>bold</strong> b</p>",
		},
		{
			"italic",
			"a *i* b",
			"<p>a <em>i</em> b</p>",
		},
		{
			"inline code",
			"a `code` b",
			"<p>a <code>code</code> b</p>",
		},
		{
			"link",
			"[click](https://example.com)",
			`<p><a href="https://example.com">click</a></p>`,
		},
		{
			"link with ampersand",
			"[x](https://example.com/?a=1&b=2)",
			`<p><a href="https://example.com/?a=1&amp;b=2">x</a></p>`,
		},
		{
			"strikethrough",
			"~~gone~~",
			`<p><span style="text-decoration: line-through;">gone</span></p>`,
		},
		{
			"hr",
			"---",
			"<hr/>",
		},
		{
			"blockquote plain",
			"> hi",
			"<blockquote><p>hi</p>\n</blockquote>",
		},
		{
			"escape ampersand in text",
			"A & B",
			"<p>A &amp; B</p>",
		},
		{
			"escape lt gt in text",
			"2 < 3 > 1",
			"<p>2 &lt; 3 &gt; 1</p>",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Convert([]byte(c.input))
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			if strings.TrimSpace(got) != strings.TrimSpace(c.want) {
				t.Errorf("\n got: %q\nwant: %q", got, c.want)
			}
		})
	}
}

func TestCodeBlock_CDATAEscape(t *testing.T) {
	input := "```\nsome ]]> text\n```\n"
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	// Must split CDATA to avoid premature termination.
	if !strings.Contains(got, "]]]]><![CDATA[>") {
		t.Errorf("CDATA not escaped:\n%s", got)
	}
	// Must not contain a raw unescaped ]]> inside CDATA.
	// We allow the one at the end of CDATA section.
	if strings.Count(got, "]]>") != 2 {
		// Expected: one for escape-split closer, one for the CDATA end.
		t.Errorf("unexpected ]]> count: %s", got)
	}
}

func TestRawPassthrough(t *testing.T) {
	input := "before\n\n<!-- cfmd:raw:begin -->\n<ac:structured-macro ac:name=\"jira\"><ac:parameter ac:name=\"key\">PROJ-1</ac:parameter></ac:structured-macro>\n<!-- cfmd:raw:end -->\n\nafter\n"
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !strings.Contains(got, `<ac:structured-macro ac:name="jira">`) {
		t.Errorf("passthrough macro lost:\n%s", got)
	}
	if strings.Contains(got, "cfmd:raw:begin") || strings.Contains(got, "cfmd:raw:end") {
		t.Errorf("sentinels leaked:\n%s", got)
	}
}

func TestAlertNote(t *testing.T) {
	input := "> [!NOTE]\n> Launching Q2\n"
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !strings.Contains(got, `<ac:structured-macro ac:name="info">`) {
		t.Errorf("info macro missing:\n%s", got)
	}
	if !strings.Contains(got, "Launching Q2") {
		t.Errorf("body lost:\n%s", got)
	}
	if strings.Contains(got, "[!NOTE]") {
		t.Errorf("marker not stripped:\n%s", got)
	}
}

func TestAlertAllTypes(t *testing.T) {
	cases := []struct {
		marker string
		macro  string
	}{
		{"NOTE", "info"},
		{"WARNING", "warning"},
		{"TIP", "tip"},
		{"IMPORTANT", "note"},
		{"CAUTION", "warning"},
	}
	for _, c := range cases {
		t.Run(c.marker, func(t *testing.T) {
			input := "> [!" + c.marker + "]\n> body\n"
			got, err := Convert([]byte(input))
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			if !strings.Contains(got, `<ac:structured-macro ac:name="`+c.macro+`">`) {
				t.Errorf("macro %q missing for %s:\n%s", c.macro, c.marker, got)
			}
		})
	}
}

func TestTable(t *testing.T) {
	input := "| a | b |\n|---|---|\n| 1 | 2 |\n"
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	for _, s := range []string{"<table>", "<tbody>", "<th>a</th>", "<th>b</th>", "<td>1</td>", "<td>2</td>"} {
		if !strings.Contains(got, s) {
			t.Errorf("table missing %q:\n%s", s, got)
		}
	}
}

func TestLists(t *testing.T) {
	input := "- one\n- two\n\n1. a\n2. b\n"
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	for _, s := range []string{"<ul>", "<li>one</li>", "<li>two</li>", "</ul>", "<ol>", "<li>a</li>", "<li>b</li>", "</ol>"} {
		if !strings.Contains(got, s) {
			t.Errorf("lists missing %q:\n%s", s, got)
		}
	}
}

func TestImage_Remote(t *testing.T) {
	got, err := Convert([]byte("![diagram](https://example.com/d.png)"))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !strings.Contains(got, `<ri:url ri:value="https://example.com/d.png"/>`) {
		t.Errorf("remote image:\n%s", got)
	}
	if !strings.Contains(got, `ac:alt="diagram"`) {
		t.Errorf("alt missing:\n%s", got)
	}
}

func TestImage_Local(t *testing.T) {
	got, err := Convert([]byte("![d](./img/foo.png)"))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !strings.Contains(got, `<ri:attachment ri:filename="foo.png"/>`) {
		t.Errorf("local image:\n%s", got)
	}
}
