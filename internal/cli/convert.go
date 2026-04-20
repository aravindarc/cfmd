package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/aravindarc/cfmd/internal/convert/md2storage"
	"github.com/aravindarc/cfmd/internal/convert/storage2md"
	"github.com/aravindarc/cfmd/internal/frontmatter"
	"github.com/spf13/cobra"
)

// newConvertCommand exposes the converters locally without touching the
// network. Useful for verifying what a push would send, or inspecting what
// a pull would produce from saved XHTML.
func newConvertCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "convert",
		Short: "Local-only conversion utilities (no network)",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "md-to-storage <file.md>",
		Short: "Render a markdown file to Confluence storage format",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := os.ReadFile(args[0])
			if err != nil {
				return err
			}
			_, body, err := frontmatter.Parse(string(b))
			if err != nil {
				return err
			}
			out, err := md2storage.Convert([]byte(body))
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "storage-to-md <file.xhtml>",
		Short: "Render a Confluence storage format file to markdown (reads stdin if file is '-')",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var b []byte
			var err error
			if args[0] == "-" {
				b, err = io.ReadAll(os.Stdin)
			} else {
				b, err = os.ReadFile(args[0])
			}
			if err != nil {
				return err
			}
			out, err := storage2md.Convert(b)
			if err != nil {
				return err
			}
			fmt.Print(out)
			return nil
		},
	})
	return cmd
}
