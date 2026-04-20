package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/aravindarc/cfmd/internal/confluence"
	"github.com/aravindarc/cfmd/internal/convert/storage2md"
	"github.com/aravindarc/cfmd/internal/diff"
	"github.com/spf13/cobra"
)

func newDiffCommand() *cobra.Command {
	var launchID bool
	cmd := &cobra.Command{
		Use:   "diff <file.md>",
		Short: "Show the diff between local file and remote page (no side effects)",
		Long: `Fetches the remote page, renders it to markdown, and prints a unified
diff vs the local file's body. Exit code is 0 if identical, 1 if different,
>1 on error. Files are also written to the cache dir so you can open them
with 'idea diff' or another diff tool.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiff(args[0], launchID)
		},
	}
	cmd.Flags().BoolVar(&launchID, "idea", false, "after writing diff files, also launch IntelliJ via `idea diff`")
	return cmd
}

func runDiff(path string, launchID bool) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := cfg.RequireAuth(); err != nil {
		return err
	}
	lf, err := readFile(path)
	if err != nil {
		return err
	}
	if lf.Frontmatter.PageID == "" {
		return fmt.Errorf("file %s has no page_id in frontmatter — nothing to diff against", path)
	}
	client := confluence.New(cfg)
	ctx := context.Background()
	page, err := client.GetPage(ctx, lf.Frontmatter.PageID)
	if err != nil {
		return err
	}
	remoteMD, err := storage2md.Convert([]byte(page.Body.Storage.Value))
	if err != nil {
		return err
	}
	cch, err := openCache(cfg)
	if err != nil {
		return err
	}
	leftPath, rightPath, err := cch.WriteDiffPair(page.ID, lf.Body, remoteMD)
	if err != nil {
		return err
	}

	_, shown, changed, err := renderDiff(lf.Body, remoteMD,
		fmt.Sprintf("local:%s", path),
		fmt.Sprintf("remote:page-%s@v%d", page.ID, page.Version.Number))
	if err != nil {
		return err
	}
	if !changed {
		fmt.Fprintln(os.Stderr, "No differences between local and remote.")
		return nil
	}
	fmt.Println(shown)
	printIdeaHint(leftPath, rightPath)
	if launchID {
		if err := diff.LaunchIdea(leftPath, rightPath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not launch idea: %v\n", err)
		}
	}
	// Signal "diff present" to scripts via exit code 1.
	return &diffPresent{}
}

// diffPresent is returned when diff finds differences. main.go maps this to
// exit code 1 without printing an error message (since the diff itself is
// the informative output). The diffSentinel method is the marker main.go
// checks via interface assertion.
type diffPresent struct{}

func (d *diffPresent) Error() string  { return "diff present" }
func (d *diffPresent) diffSentinel()  {}
