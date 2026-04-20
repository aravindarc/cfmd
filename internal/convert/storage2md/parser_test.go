package storage2md

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite golden files")

func TestGolden(t *testing.T) {
	testdataRoot := filepath.Join("..", "..", "..", "testdata", "storage2md")
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
			input, err := os.ReadFile(filepath.Join(dir, "input.xhtml"))
			if err != nil {
				t.Fatalf("read input: %v", err)
			}
			got, err := Convert(input)
			if err != nil {
				t.Fatalf("convert: %v", err)
			}
			got = strings.TrimRight(got, "\n") + "\n"
			goldenPath := filepath.Join(dir, "expected.md")
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

func TestHeadings(t *testing.T) {
	got, err := Convert([]byte("<h1>One</h1><h2>Two</h2>"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(got, "# One") {
		t.Errorf("h1:\n%s", got)
	}
	if !strings.Contains(got, "## Two") {
		t.Errorf("h2:\n%s", got)
	}
}

func TestEmphasis(t *testing.T) {
	got, err := Convert([]byte("<p>a <strong>B</strong> c <em>D</em> e</p>"))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(got, "**B**") {
		t.Errorf("strong:\n%s", got)
	}
	if !strings.Contains(got, "*D*") {
		t.Errorf("em:\n%s", got)
	}
}

func TestSimpleCode(t *testing.T) {
	input := `<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">python</ac:parameter><ac:plain-text-body><![CDATA[print("hi")
]]></ac:plain-text-body></ac:structured-macro>`
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(got, "```python") {
		t.Errorf("fence missing:\n%s", got)
	}
	if !strings.Contains(got, `print("hi")`) {
		t.Errorf("body missing:\n%s", got)
	}
}

func TestCodeWithExtraParamsPassesThrough(t *testing.T) {
	// A code block with linenumbers should be preserved via passthrough.
	input := `<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">python</ac:parameter><ac:parameter ac:name="linenumbers">true</ac:parameter><ac:plain-text-body><![CDATA[x=1]]></ac:plain-text-body></ac:structured-macro>`
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(got, "```cfmd-raw") {
		t.Errorf("expected cfmd-raw fence:\n%s", got)
	}
	if !strings.Contains(got, `ac:name="linenumbers"`) {
		t.Errorf("linenumbers param lost:\n%s", got)
	}
}

func TestInfoAdmonitionSimple(t *testing.T) {
	input := `<ac:structured-macro ac:name="info"><ac:rich-text-body><p>Be aware.</p></ac:rich-text-body></ac:structured-macro>`
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(got, "> [!NOTE]") {
		t.Errorf("GFM alert missing:\n%s", got)
	}
	if !strings.Contains(got, "Be aware.") {
		t.Errorf("body missing:\n%s", got)
	}
}

func TestInfoAdmonitionWithTitlePassesThrough(t *testing.T) {
	// With a title parameter, the admonition can't be expressed as a GFM alert,
	// so it must passthrough to preserve the title.
	input := `<ac:structured-macro ac:name="info"><ac:parameter ac:name="title">Heads up</ac:parameter><ac:rich-text-body><p>Body.</p></ac:rich-text-body></ac:structured-macro>`
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(got, "```cfmd-raw") {
		t.Errorf("expected cfmd-raw fence:\n%s", got)
	}
	if !strings.Contains(got, "Heads up") {
		t.Errorf("title preserved verbatim:\n%s", got)
	}
}

func TestExpandToDetails(t *testing.T) {
	input := `<ac:structured-macro ac:name="expand"><ac:parameter ac:name="title">Click me</ac:parameter><ac:rich-text-body><p>Hidden <strong>content</strong>.</p></ac:rich-text-body></ac:structured-macro>`
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(got, "<details>") {
		t.Errorf("<details> missing:\n%s", got)
	}
	if !strings.Contains(got, "<summary>Click me</summary>") {
		t.Errorf("summary missing:\n%s", got)
	}
	if !strings.Contains(got, "**content**") {
		t.Errorf("body markdown missing:\n%s", got)
	}
	if !strings.Contains(got, "</details>") {
		t.Errorf("</details> missing:\n%s", got)
	}
}

func TestImageSimple(t *testing.T) {
	input := `<p><ac:image ac:alt="diagram"><ri:url ri:value="https://example.com/x.png"/></ac:image></p>`
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(got, "![diagram](https://example.com/x.png)") {
		t.Errorf("simple image:\n%s", got)
	}
}

func TestImageWithSizePassesThrough(t *testing.T) {
	input := `<p><ac:image ac:width="200" ac:alt="x"><ri:attachment ri:filename="d.png"/></ac:image></p>`
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(got, `ac:width="200"`) {
		t.Errorf("width attr lost:\n%s", got)
	}
}

func TestInternalLinkPassesThrough(t *testing.T) {
	input := `<p>See <ac:link><ri:page ri:content-title="Other Page"/><ac:plain-text-link-body><![CDATA[that page]]></ac:plain-text-link-body></ac:link> for more.</p>`
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("%v", err)
	}
	// Internal links are preserved inline verbatim.
	if !strings.Contains(got, `<ri:page ri:content-title="Other Page"/>`) {
		t.Errorf("ri:page lost:\n%s", got)
	}
}

func TestUnknownMacroPassesThrough(t *testing.T) {
	input := `<ac:structured-macro ac:name="status"><ac:parameter ac:name="colour">Green</ac:parameter><ac:parameter ac:name="title">Done</ac:parameter></ac:structured-macro>`
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(got, `ac:name="status"`) {
		t.Errorf("unknown macro lost:\n%s", got)
	}
	// Status is a structured-macro emitted as block passthrough.
	if !strings.Contains(got, "```cfmd-raw") {
		t.Errorf("not wrapped in cfmd-raw fence:\n%s", got)
	}
}

func TestList(t *testing.T) {
	input := `<ul><li>a</li><li>b</li></ul>`
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(got, "- a") || !strings.Contains(got, "- b") {
		t.Errorf("list:\n%s", got)
	}
}

func TestTable(t *testing.T) {
	input := `<table><tbody><tr><th>Name</th><th>Role</th></tr><tr><td>Alice</td><td>Eng</td></tr></tbody></table>`
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(got, "| Name | Role |") {
		t.Errorf("header row:\n%s", got)
	}
	if !strings.Contains(got, "| Alice | Eng |") {
		t.Errorf("body row:\n%s", got)
	}
}
