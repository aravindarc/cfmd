package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aravindarc/cfmd/internal/cache"
	"github.com/aravindarc/cfmd/internal/config"
	"github.com/aravindarc/cfmd/internal/confluence"
	"github.com/aravindarc/cfmd/internal/diff"
	"github.com/aravindarc/cfmd/internal/frontmatter"
)

// Exit codes — consistent with docs/SPEC.md §6.
const (
	ExitSuccess      = 0
	ExitGeneric      = 1
	ExitConflict     = 2
	ExitAuth         = 3
	ExitNetwork      = 4
	ExitCanceled     = 5
	ExitLocalChanged = 6
)

// loadedFile is a parsed cfmd-managed markdown file.
type loadedFile struct {
	Path        string
	Frontmatter *frontmatter.Frontmatter
	Body        string
	Raw         string
}

func readFile(path string) (*loadedFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	fm, body, err := frontmatter.Parse(string(b))
	if err != nil {
		return nil, fmt.Errorf("parse frontmatter in %s: %w", path, err)
	}
	return &loadedFile{
		Path:        path,
		Frontmatter: fm,
		Body:        body,
		Raw:         string(b),
	}, nil
}

// writeFileAtomic writes content to path via a temp file + rename, preserving
// 0644 perms. It does not fsync; for our use case (text files on dev
// machines) that's sufficient.
func writeFileAtomic(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".cfmd-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, path)
}

// confirm prompts the user for y/N unless assumeYes is set. Returns true if
// the user confirmed (or --yes was passed).
func confirm(prompt string, assumeYes bool) bool {
	if assumeYes {
		return true
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}

// renderDiff renders a unified diff from local to remote markdown bodies
// and (if stdout is a TTY) colorizes it. Returns the plain diff, the
// displayable diff, and whether the two bodies differ.
func renderDiff(localBody, remoteBody, localLabel, remoteLabel string) (plain, shown string, changed bool, err error) {
	plain, err = diff.Unified(localBody, remoteBody, localLabel, remoteLabel, 3)
	if err != nil {
		return "", "", false, err
	}
	if plain == "" {
		return "", "", false, nil
	}
	if diff.IsTerminal(os.Stdout) {
		return plain, diff.Colorize(plain), true, nil
	}
	return plain, plain, true, nil
}

// printIdeaHint writes a final line suggesting how to open the diff in
// IntelliJ, using OSC 8 hyperlinks if the terminal supports them.
func printIdeaHint(left, right string) {
	leftAbs, _ := filepath.Abs(left)
	rightAbs, _ := filepath.Abs(right)
	cmd := fmt.Sprintf("idea diff %s %s", leftAbs, rightAbs)
	if diff.IsTerminal(os.Stderr) {
		leftLink := diff.OSC8Hyperlink(diff.FilePathToURL(leftAbs), filepath.Base(leftAbs))
		rightLink := diff.OSC8Hyperlink(diff.FilePathToURL(rightAbs), filepath.Base(rightAbs))
		fmt.Fprintf(os.Stderr, "\nDiff files written:\n  local  → %s\n  remote → %s\n", leftLink, rightLink)
		fmt.Fprintf(os.Stderr, "Open in IntelliJ: %s\n", cmd)
		return
	}
	fmt.Fprintf(os.Stderr, "\nDiff files: %s  %s\n", leftAbs, rightAbs)
	fmt.Fprintf(os.Stderr, "Open in IntelliJ: %s\n", cmd)
}

// slugify converts a page title to a filename-safe slug.
var slugNonAlphaNum = regexp.MustCompile(`[^a-zA-Z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(s)
	s = slugNonAlphaNum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "page"
	}
	return s
}

// exitCodeForError maps an error returned by the Confluence client to a
// process exit code.
func exitCodeForError(err error) int {
	switch {
	case err == nil:
		return ExitSuccess
	case errors.Is(err, confluence.ErrAuth):
		return ExitAuth
	case errors.Is(err, confluence.ErrConflict):
		return ExitConflict
	case errors.Is(err, confluence.ErrNotFound), errors.Is(err, confluence.ErrAPI):
		return ExitNetwork
	default:
		return ExitGeneric
	}
}

// openCache opens the on-disk cache or returns a friendly error.
func openCache(cfg *config.Config) (*cache.Cache, error) {
	return cache.New(cfg.CacheDir)
}

// nowUTC returns the current time, in UTC, trimmed to seconds.
func nowUTC() time.Time {
	return time.Now().UTC().Truncate(time.Second)
}

// loadConfig runs the single standard config loader; commands call it first.
func loadConfig() (*config.Config, error) {
	return config.Load()
}

// inferTitle returns the first level-1 heading in body, or the filename base
// (without extension) as a fallback. Used when frontmatter lacks `title` on
// first push.
var h1Pattern = regexp.MustCompile(`(?m)^#\s+(.+?)\s*$`)

func inferTitle(body, path string) string {
	if m := h1Pattern.FindStringSubmatch(body); m != nil {
		return strings.TrimSpace(m[1])
	}
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return base
}
