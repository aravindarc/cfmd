package md2storage

import (
	"strings"
	"testing"
)

// TestFencedRawPassthrough verifies that a fenced `cfmd-raw` block has its
// contents emitted verbatim as storage format (no <ac:structured-macro
// ac:name="code"> wrapping).
func TestFencedRawPassthrough(t *testing.T) {
	input := "before\n\n```cfmd-raw\n<ac:structured-macro ac:name=\"jira\"><ac:parameter ac:name=\"key\">PROJ-1</ac:parameter></ac:structured-macro>\n```\n\nafter\n"
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !strings.Contains(got, `<ac:structured-macro ac:name="jira">`) {
		t.Errorf("passthrough macro lost:\n%s", got)
	}
	if strings.Contains(got, "cfmd-raw") {
		t.Errorf("fence tag leaked:\n%s", got)
	}
	if strings.Contains(got, `ac:name="code"`) {
		t.Errorf("content got wrapped in code macro:\n%s", got)
	}
}

// TestFencedRawLongerFence verifies that a 4-backtick fence is also handled.
func TestFencedRawLongerFence(t *testing.T) {
	input := "````cfmd-raw\n<ac:structured-macro ac:name=\"x\"/>\n````\n"
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !strings.Contains(got, `<ac:structured-macro ac:name="x"/>`) {
		t.Errorf("4-backtick fence not handled:\n%s", got)
	}
}

// TestFencedRawFenceMismatch: a 3-tick opener with a 4-tick "close" is not
// closed; the block stays raw (i.e., no interpretation happens).
func TestFencedRawFenceMismatch(t *testing.T) {
	input := "```cfmd-raw\n<ac:x/>\n````\n```\n"
	// The 4-tick line doesn't close the 3-tick fence; the trailing 3-tick
	// does. Verify we end up with ac:x in the output.
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !strings.Contains(got, "<ac:x/>") {
		t.Errorf("fence close misidentified:\n%s", got)
	}
}

// TestLegacyHTMLCommentPassthrough verifies backward compatibility with the
// old <!-- cfmd:raw:begin -->...<!-- cfmd:raw:end --> form.
func TestLegacyHTMLCommentPassthrough(t *testing.T) {
	input := "<!-- cfmd:raw:begin -->\n<ac:structured-macro ac:name=\"jira\"/>\n<!-- cfmd:raw:end -->\n"
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !strings.Contains(got, `<ac:structured-macro ac:name="jira"/>`) {
		t.Errorf("legacy passthrough lost:\n%s", got)
	}
}

// TestExpandBasic verifies <details><summary>X</summary>body</details> →
// Expand macro.
func TestExpandBasic(t *testing.T) {
	input := "<details>\n<summary>Click me</summary>\n\nHidden **content** here.\n\n</details>\n"
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !strings.Contains(got, `<ac:structured-macro ac:name="expand">`) {
		t.Errorf("expand macro missing:\n%s", got)
	}
	if !strings.Contains(got, `<ac:parameter ac:name="title">Click me</ac:parameter>`) {
		t.Errorf("title parameter missing:\n%s", got)
	}
	if !strings.Contains(got, `<ac:rich-text-body>`) {
		t.Errorf("rich-text-body missing:\n%s", got)
	}
	if !strings.Contains(got, "<strong>content</strong>") {
		t.Errorf("body markdown not converted:\n%s", got)
	}
}

// TestExpandNoSummary uses an Expand without a <summary>; title should be
// empty and we should not emit a title parameter.
func TestExpandNoSummary(t *testing.T) {
	input := "<details>\n\nBody text.\n\n</details>\n"
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !strings.Contains(got, `<ac:structured-macro ac:name="expand">`) {
		t.Errorf("expand macro missing:\n%s", got)
	}
	if strings.Contains(got, `<ac:parameter ac:name="title">`) {
		t.Errorf("unexpected title param:\n%s", got)
	}
}

// TestExpandWithCodeInside: body contains a code fence.
func TestExpandWithCodeInside(t *testing.T) {
	input := "<details>\n<summary>Logs</summary>\n\n```go\nfmt.Println(\"hi\")\n```\n\n</details>\n"
	got, err := Convert([]byte(input))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !strings.Contains(got, `<ac:name="expand">`) && !strings.Contains(got, `ac:name="expand"`) {
		t.Errorf("expand macro missing:\n%s", got)
	}
	if !strings.Contains(got, `<ac:structured-macro ac:name="code">`) {
		t.Errorf("code macro missing inside expand:\n%s", got)
	}
	if !strings.Contains(got, `<ac:parameter ac:name="language">go</ac:parameter>`) {
		t.Errorf("language param missing:\n%s", got)
	}
}
