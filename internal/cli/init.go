package cli

import (
	"fmt"
	"os"

	"github.com/aravindarc/cfmd/internal/config"
	"github.com/spf13/cobra"
)

func newInitCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a .env template to the current directory",
		Long: `Writes an annotated .env template to the current working directory. You
then edit the file to add your base URL, email, and API token.

The template is not overwritten if an .env file already exists, unless
--force is given.

Create an API token at:
  https://id.atlassian.com/manage-profile/security/api-tokens

cfmd never stores the token itself; it only reads it from the environment
(or from the .env file you point it at).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing .env")
	return cmd
}

func runInit(force bool) error {
	path := ".env"
	if _, err := os.Stat(path); err == nil && !force {
		return fmt.Errorf("%s already exists; pass --force to overwrite", path)
	}
	if err := os.WriteFile(path, []byte(config.DotEnvTemplate()), 0o600); err != nil {
		return err
	}
	fmt.Printf("Wrote %s. Edit it to fill in CFMD_BASE_URL, CFMD_USERNAME, CFMD_TOKEN.\n", path)
	fmt.Println("Add .env to your .gitignore — it contains an API token.")
	return nil
}
