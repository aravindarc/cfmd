package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aravindarc/cfmd/internal/cache"
	"github.com/aravindarc/cfmd/internal/confluence"
	"github.com/aravindarc/cfmd/internal/convert/md2storage"
	"github.com/aravindarc/cfmd/internal/convert/storage2md"
	"github.com/aravindarc/cfmd/internal/diff"
	"github.com/aravindarc/cfmd/internal/frontmatter"
	"github.com/spf13/cobra"
)

func newPushCommand() *cobra.Command {
	var (
		force    bool
		assumeY  bool
		dryRun   bool
		launchID bool
		message  string
	)
	cmd := &cobra.Command{
		Use:   "push <file.md>",
		Short: "Push a local markdown file to Confluence, after showing a diff",
		Long: `Reads a cfmd-managed markdown file, converts it to Confluence storage
format, fetches the current remote page, renders the remote back to
markdown, and shows a unified diff. With --yes the push proceeds without
confirmation; with --dry-run the push is never performed.

If frontmatter lacks a page_id the page is created; existing pages are
updated, with a version-number bump. Version conflicts abort unless
--force is given.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPush(args[0], pushOptions{
				Force:    force,
				AssumeY:  assumeY,
				DryRun:   dryRun,
				LaunchID: launchID,
				Message:  message,
			})
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite remote even if version has advanced")
	cmd.Flags().BoolVarP(&assumeY, "yes", "y", false, "skip confirmation")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show the diff and exit without pushing")
	cmd.Flags().BoolVar(&launchID, "idea", false, "after writing diff files, also launch IntelliJ via `idea diff`")
	cmd.Flags().StringVarP(&message, "message", "m", "Updated via cfmd", "Confluence version comment")
	return cmd
}

type pushOptions struct {
	Force    bool
	AssumeY  bool
	DryRun   bool
	LaunchID bool
	Message  string
}

func runPush(path string, opts pushOptions) error {
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
	fm := lf.Frontmatter

	// Fill missing fields from defaults.
	if fm.Space == "" {
		fm.Space = cfg.DefaultSpace
	}
	if fm.ParentID == "" {
		fm.ParentID = cfg.DefaultParentID
	}
	// Title default: first `# ` heading, or filename sans extension.
	if fm.Title == "" {
		fm.Title = inferTitle(lf.Body, path)
	}
	if err := fm.RequireForPush(); err != nil {
		return err
	}

	// Convert the body to storage format.
	storageXHTML, err := md2storage.Convert([]byte(lf.Body))
	if err != nil {
		return fmt.Errorf("convert body to storage format: %w", err)
	}

	client := confluence.New(cfg)
	ctx := context.Background()
	cch, err := openCache(cfg)
	if err != nil {
		return err
	}

	// Branch: create vs update.
	if fm.PageID == "" {
		return pushCreate(ctx, client, cch, lf, storageXHTML)
	}
	return pushUpdate(ctx, client, cch, lf, storageXHTML, opts)
}

// pushCreate handles the first-time creation. We don't diff here (nothing to
// diff against), but we still confirm the operation.
func pushCreate(ctx context.Context, client *confluence.Client, cch *cache.Cache, lf *loadedFile, storageXHTML string) error {
	fm := lf.Frontmatter
	fmt.Fprintf(os.Stderr, "Creating new page %q in space %s…\n", fm.Title, fm.Space)
	create := &confluence.PageCreate{
		Type:  "page",
		Title: fm.Title,
		Space: confluence.Space{Key: fm.Space},
		Body: confluence.Body{Storage: confluence.StorageBody{
			Value:          storageXHTML,
			Representation: "storage",
		}},
	}
	if fm.ParentID != "" {
		create.Ancestors = []confluence.Ancestor{{ID: fm.ParentID}}
	}
	page, err := client.CreatePage(ctx, create)
	if err != nil {
		return err
	}
	fm.PageID = page.ID
	fm.Version = page.Version.Number
	fm.LastSynced = nowUTC()

	// Rewrite file with injected metadata.
	newContent := frontmatter.Serialize(fm, lf.Body)
	if err := writeFileAtomic(lf.Path, newContent); err != nil {
		return fmt.Errorf("rewrite file with page_id: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Created page id=%s version=%d\n", page.ID, page.Version.Number)
	if url := client.BuildPageURL(page); url != "" {
		fmt.Fprintf(os.Stderr, "URL: %s\n", url)
	}
	return nil
}

// pushUpdate handles updating an existing page, including the diff step and
// the version-conflict check.
func pushUpdate(ctx context.Context, client *confluence.Client, cch *cache.Cache, lf *loadedFile, storageXHTML string, opts pushOptions) error {
	fm := lf.Frontmatter

	remote, err := client.GetPage(ctx, fm.PageID)
	if err != nil {
		return err
	}

	// Version conflict check.
	if remote.Version.Number != fm.Version && !opts.Force {
		fmt.Fprintf(os.Stderr, "CONFLICT: remote version %d, local last-synced version %d.\n",
			remote.Version.Number, fm.Version)
		fmt.Fprintln(os.Stderr, "Someone else has edited this page since your last sync.")
		fmt.Fprintln(os.Stderr, "Options:")
		fmt.Fprintln(os.Stderr, "  cfmd pull <file> --force     overwrite local with remote")
		fmt.Fprintln(os.Stderr, "  cfmd push <file> --force     overwrite remote with local")
		return confluence.ErrConflict
	}

	// Show a markdown-vs-markdown diff: render remote storage back through
	// storage2md so the user sees markdown on both sides.
	remoteMD, err := storage2md.Convert([]byte(remote.Body.Storage.Value))
	if err != nil {
		return fmt.Errorf("render remote for diff: %w", err)
	}
	// Also render our own local body through the round-trip pipeline so the
	// two sides are comparable in form (the alternative would show lots of
	// cosmetic diffs caused by converter-vs-author formatting choices).
	localRoundTripped, err := convertLocalForDiff(lf.Body)
	if err != nil {
		return fmt.Errorf("render local for diff: %w", err)
	}

	// Write the pair to cache and show the diff.
	leftPath, rightPath, err := cch.WriteDiffPair(fm.PageID, localRoundTripped, remoteMD)
	if err != nil {
		return fmt.Errorf("write diff pair: %w", err)
	}

	plain, shown, changed, err := renderDiff(remoteMD, localRoundTripped,
		fmt.Sprintf("remote:page-%s@v%d", fm.PageID, remote.Version.Number),
		fmt.Sprintf("local:%s", lf.Path))
	if err != nil {
		return err
	}
	if !changed {
		fmt.Fprintln(os.Stderr, "No changes to push (local matches remote).")
		return nil
	}
	fmt.Fprintln(os.Stderr, "Proposed changes (remote → local):")
	fmt.Println(shown)
	_ = plain
	printIdeaHint(leftPath, rightPath)

	if opts.LaunchID {
		if err := diff.LaunchIdea(leftPath, rightPath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not launch idea: %v\n", err)
		}
	}

	if opts.DryRun {
		fmt.Fprintln(os.Stderr, "--dry-run set; not pushing.")
		return nil
	}
	if !confirm(fmt.Sprintf("Apply these changes to Confluence (page %s, bumping v%d → v%d)?",
		fm.PageID, remote.Version.Number, remote.Version.Number+1), opts.AssumeY) {
		fmt.Fprintln(os.Stderr, "Canceled.")
		return errors.New("canceled by user")
	}

	update := &confluence.PageUpdate{
		ID:      fm.PageID,
		Type:    "page",
		Title:   fm.Title,
		Space:   confluence.Space{Key: fm.Space},
		Version: confluence.Version{Number: remote.Version.Number + 1, Message: opts.Message},
		Body: confluence.Body{Storage: confluence.StorageBody{
			Value:          storageXHTML,
			Representation: "storage",
		}},
	}
	updated, err := client.UpdatePage(ctx, fm.PageID, update)
	if err != nil {
		return err
	}

	// Rewrite file with new version number.
	fm.Version = updated.Version.Number
	fm.LastSynced = nowUTC()
	newContent := frontmatter.Serialize(fm, lf.Body)
	if err := writeFileAtomic(lf.Path, newContent); err != nil {
		return fmt.Errorf("rewrite frontmatter: %w", err)
	}

	// Save snapshot for conflict detection.
	_ = cch.SaveSnapshot(fm.PageID, storageXHTML, lf.Body, updated.Version.Number)

	fmt.Fprintf(os.Stderr, "Pushed. New version: %d\n", updated.Version.Number)
	if url := client.BuildPageURL(updated); url != "" {
		fmt.Fprintf(os.Stderr, "URL: %s\n", url)
	}
	return nil
}

// convertLocalForDiff renders the local body through the md→storage→md
// pipeline so the displayed diff is symmetrical with the remote side (both
// have been through the same converter normalization). Without this, we'd
// see a lot of "- [text](x) + [text](x)" style no-op diffs caused by how
// the converter re-emits links/tables/etc.
func convertLocalForDiff(body string) (string, error) {
	storage, err := md2storage.Convert([]byte(body))
	if err != nil {
		return "", err
	}
	return storage2md.Convert([]byte(storage))
}
