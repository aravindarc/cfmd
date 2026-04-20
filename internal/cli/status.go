package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/aravindarc/cfmd/internal/confluence"
	"github.com/spf13/cobra"
)

func newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status <file.md>",
		Short: "Report local-vs-remote sync state without modifying anything",
		Long: `Shows the stored frontmatter, the current remote version, and a
qualitative sync state: in_sync / local_ahead / remote_ahead / diverged.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus(args[0])
		},
	}
}

func runStatus(path string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	lf, err := readFile(path)
	if err != nil {
		return err
	}
	fm := lf.Frontmatter

	fmt.Printf("File:       %s\n", path)
	fmt.Printf("Page ID:    %s\n", fm.PageID)
	fmt.Printf("Space:      %s\n", fm.Space)
	fmt.Printf("Title:      %s\n", fm.Title)
	fmt.Printf("Local ver:  %d\n", fm.Version)
	fmt.Printf("Last sync:  %s\n", fm.LastSynced.Format("2006-01-02 15:04:05 MST"))

	if fm.PageID == "" {
		fmt.Println("\nStatus: unsynced (no page_id yet)")
		return nil
	}

	if err := cfg.RequireAuth(); err != nil {
		fmt.Fprintf(os.Stderr, "\nCannot reach Confluence: %v\n", err)
		return err
	}
	client := confluence.New(cfg)
	ctx := context.Background()
	remoteVer, err := client.GetPageVersion(ctx, fm.PageID)
	if err != nil {
		return err
	}
	fmt.Printf("Remote ver: %d\n", remoteVer)

	// Determine local-modified by comparing current body hash against cached
	// last_local.md (if present).
	cch, err := openCache(cfg)
	if err != nil {
		return err
	}
	_, cachedLocal, _, _ := cch.LoadSnapshot(fm.PageID)
	localChanged := cachedLocal != "" && cachedLocal != lf.Body

	state := "in_sync"
	switch {
	case remoteVer > fm.Version && localChanged:
		state = "diverged"
	case remoteVer > fm.Version:
		state = "remote_ahead"
	case localChanged:
		state = "local_ahead"
	}
	fmt.Printf("\nStatus: %s\n", state)
	return nil
}
