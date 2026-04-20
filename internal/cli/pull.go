package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aravindarc/cfmd/internal/confluence"
	"github.com/aravindarc/cfmd/internal/convert/storage2md"
	"github.com/aravindarc/cfmd/internal/diff"
	"github.com/aravindarc/cfmd/internal/frontmatter"
	"github.com/spf13/cobra"
)

func newPullCommand() *cobra.Command {
	var (
		outPath  string
		force    bool
		assumeY  bool
		dryRun   bool
		launchID bool
	)
	cmd := &cobra.Command{
		Use:   "pull <page-id-or-url>",
		Short: "Pull a Confluence page to a local markdown file, after showing a diff",
		Long: `Fetches a page's storage format from Confluence, converts it to markdown,
and shows a unified diff against the existing local file (if any). With --yes
the pull proceeds without confirmation; with --dry-run the file is never
written.

The target can be a numeric page id or a full Confluence URL.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPull(args[0], pullOptions{
				OutPath:  outPath,
				Force:    force,
				AssumeY:  assumeY,
				DryRun:   dryRun,
				LaunchID: launchID,
			})
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "", "output path (default: slugified title in cwd)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite local file even if it has uncommitted edits")
	cmd.Flags().BoolVarP(&assumeY, "yes", "y", false, "skip confirmation")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the diff and exit without writing")
	cmd.Flags().BoolVar(&launchID, "idea", false, "after writing diff files, also launch IntelliJ via `idea diff`")
	return cmd
}

type pullOptions struct {
	OutPath  string
	Force    bool
	AssumeY  bool
	DryRun   bool
	LaunchID bool
}

func runPull(target string, opts pullOptions) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := cfg.RequireAuth(); err != nil {
		return err
	}
	client := confluence.New(cfg)
	if err := client.SameHostAsBase(target); err != nil {
		return fmt.Errorf("refusing to pull from unknown host: %w", err)
	}
	id, err := confluence.ParsePageIDFromURL(target)
	if err != nil {
		return err
	}
	ctx := context.Background()

	page, err := client.GetPage(ctx, id)
	if err != nil {
		return err
	}

	// Convert to markdown.
	remoteMD, err := storage2md.Convert([]byte(page.Body.Storage.Value))
	if err != nil {
		return fmt.Errorf("convert storage to markdown: %w", err)
	}

	// Determine output path.
	outPath := opts.OutPath
	if outPath == "" {
		outPath = slugify(page.Title) + ".md"
	}

	// Build the frontmatter for the new file.
	fm := &frontmatter.Frontmatter{
		PageID:     page.ID,
		Space:      page.Space.Key,
		Title:      page.Title,
		Version:    page.Version.Number,
		LastSynced: nowUTC(),
		Extras:     map[string]string{},
	}
	if len(page.Ancestors) > 0 {
		// Immediate parent is the last ancestor.
		fm.ParentID = page.Ancestors[len(page.Ancestors)-1].ID
	}
	newContent := frontmatter.Serialize(fm, remoteMD)

	// If the output file exists, compute a diff against its body.
	cch, err := openCache(cfg)
	if err != nil {
		return err
	}
	var leftBody, rightBody string
	leftBody = remoteMD
	rightBody = "" // will fill below if file exists

	var existing *loadedFile
	if _, err := os.Stat(outPath); err == nil {
		existing, err = readFile(outPath)
		if err != nil {
			return err
		}
		if existing.Frontmatter.PageID != "" && existing.Frontmatter.PageID != page.ID && !opts.Force {
			return fmt.Errorf("%s is already managed by cfmd but points to page %s, not %s; use --out to choose a different path or --force to overwrite",
				outPath, existing.Frontmatter.PageID, page.ID)
		}
		rightBody = existing.Body
	}

	leftPath, rightPath, err := cch.WriteDiffPair(page.ID, rightBody, leftBody) // local, remote
	if err != nil {
		return fmt.Errorf("write diff pair: %w", err)
	}

	// Show the diff (local → remote means "what will change if I accept").
	var changed bool
	if existing != nil {
		_, shown, ch, err := renderDiff(existing.Body, remoteMD,
			fmt.Sprintf("local:%s", outPath),
			fmt.Sprintf("remote:page-%s@v%d", page.ID, page.Version.Number))
		if err != nil {
			return err
		}
		changed = ch
		if !changed {
			fmt.Fprintln(os.Stderr, "Local file is already up to date with remote.")
			return nil
		}
		fmt.Fprintln(os.Stderr, "Pull will change:")
		fmt.Println(shown)
	} else {
		fmt.Fprintf(os.Stderr, "Creating new file %s from page %q (v%d, %d chars markdown).\n",
			outPath, page.Title, page.Version.Number, len(remoteMD))
		changed = true
	}
	printIdeaHint(leftPath, rightPath)

	if opts.LaunchID {
		if err := diff.LaunchIdea(leftPath, rightPath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not launch idea: %v\n", err)
		}
	}

	if opts.DryRun {
		fmt.Fprintln(os.Stderr, "--dry-run set; not writing.")
		return nil
	}
	if !confirm(fmt.Sprintf("Overwrite %s with remote content?", outPath), opts.AssumeY) {
		fmt.Fprintln(os.Stderr, "Canceled.")
		return errors.New("canceled by user")
	}

	if err := writeFileAtomic(outPath, newContent); err != nil {
		return err
	}
	abs, _ := filepath.Abs(outPath)
	fmt.Fprintf(os.Stderr, "Wrote %s\n", abs)

	// Save snapshot.
	_ = cch.SaveSnapshot(page.ID, page.Body.Storage.Value, remoteMD, page.Version.Number)
	return nil
}
