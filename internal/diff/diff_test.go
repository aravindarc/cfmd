package diff

import (
	"strings"
	"testing"
)

func TestUnified_Basic(t *testing.T) {
	before := "alpha\nbeta\ngamma\n"
	after := "alpha\nBETA\ngamma\n"
	got, err := Unified(before, after, "a.md", "b.md", 1)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(got, "-beta") || !strings.Contains(got, "+BETA") {
		t.Errorf("diff missing expected lines:\n%s", got)
	}
	if !strings.Contains(got, "--- a.md") || !strings.Contains(got, "+++ b.md") {
		t.Errorf("diff labels missing:\n%s", got)
	}
}

func TestUnified_Equal(t *testing.T) {
	got, err := Unified("same\n", "same\n", "a", "b", 3)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if got != "" {
		t.Errorf("expected empty diff for equal inputs, got:\n%s", got)
	}
}

func TestOSC8(t *testing.T) {
	got := OSC8Hyperlink("file:///tmp/x", "open")
	// Check structure: ESC ] 8 ;; url ESC \ text ESC ] 8 ;; ESC \
	if !strings.Contains(got, "\x1b]8;;file:///tmp/x\x1b\\") {
		t.Errorf("OSC 8 open sequence missing:\n%q", got)
	}
	if !strings.HasSuffix(got, "\x1b]8;;\x1b\\") {
		t.Errorf("OSC 8 close sequence missing:\n%q", got)
	}
}

func TestFilePathToURL(t *testing.T) {
	if got := FilePathToURL("/tmp/foo bar.md"); got != "file:///tmp/foo%20bar.md" {
		t.Errorf("got %q", got)
	}
}

func TestColorize_NoTerminalSafe(t *testing.T) {
	out := Colorize("--- a\n+++ b\n@@ -1 +1 @@\n-old\n+new\n context\n")
	for _, expect := range []string{"--- a", "+++ b", "-old", "+new"} {
		if !strings.Contains(out, expect) {
			t.Errorf("lost content %q:\n%s", expect, out)
		}
	}
}
