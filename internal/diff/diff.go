// Package diff produces human-readable unified-text diffs between two
// versions of the same markdown document, and offers helpers to launch (or
// print a command line to launch) IntelliJ's native diff viewer.
package diff

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
)

// Unified returns a standard unified diff of before vs after, labelled with
// the given filenames. The context parameter is the number of unchanged
// lines shown around each hunk; 3 is conventional.
func Unified(before, after, nameBefore, nameAfter string, context int) (string, error) {
	ud := difflib.UnifiedDiff{
		A:        difflib.SplitLines(before),
		B:        difflib.SplitLines(after),
		FromFile: nameBefore,
		ToFile:   nameAfter,
		Context:  context,
	}
	return difflib.GetUnifiedDiffString(ud)
}

// Colorize adds ANSI color codes to a unified diff (red for removals, green
// for additions, cyan for hunk headers) if and only if the output stream is
// a terminal. We decide inside the caller; Colorize is unconditional.
func Colorize(diff string) string {
	var b strings.Builder
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			b.WriteString(bold(line))
		case strings.HasPrefix(line, "@@"):
			b.WriteString(cyan(line))
		case strings.HasPrefix(line, "+"):
			b.WriteString(green(line))
		case strings.HasPrefix(line, "-"):
			b.WriteString(red(line))
		default:
			b.WriteString(line)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiRed   = "\x1b[31m"
	ansiGreen = "\x1b[32m"
	ansiCyan  = "\x1b[36m"
)

func bold(s string) string  { return ansiBold + s + ansiReset }
func red(s string) string   { return ansiRed + s + ansiReset }
func green(s string) string { return ansiGreen + s + ansiReset }
func cyan(s string) string  { return ansiCyan + s + ansiReset }

// IsTerminal reports whether the given FD is attached to a terminal. We keep
// this tiny check here to avoid pulling in a 3rd-party lib.
func IsTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// IdeaAvailable returns true if the `idea` CLI launcher is on PATH.
func IdeaAvailable() bool {
	_, err := exec.LookPath("idea")
	return err == nil
}

// LaunchIdea opens a two-way diff in IntelliJ via `idea diff left right`.
// Returns immediately (IntelliJ launches as a side-effect).
func LaunchIdea(left, right string) error {
	if !IdeaAvailable() {
		return fmt.Errorf("'idea' CLI launcher not found on PATH; install from IntelliJ via Tools → Create Command-line Launcher")
	}
	cmd := exec.Command("idea", "diff", left, right)
	// Don't attach stdin/stdout; IntelliJ runs detached.
	return cmd.Start()
}

// OSC8Hyperlink wraps text in an OSC 8 terminal hyperlink escape sequence.
// Modern terminals (including IntelliJ's, since 2021) render it as a
// clickable link that opens the URL. Older terminals just display the text.
func OSC8Hyperlink(url, text string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// FilePathToURL converts a local filesystem path to a file:// URL suitable
// for OSC 8. Caller should use an absolute path.
func FilePathToURL(absPath string) string {
	// Minimal quoting: space → %20, others passed through. Sufficient for
	// typical cache paths which are alphanumeric + /_-.
	s := strings.ReplaceAll(absPath, " ", "%20")
	return "file://" + s
}
