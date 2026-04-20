// Command cfmd syncs Confluence pages with local markdown files.
//
// See README.md for usage.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/aravindarc/cfmd/internal/cli"
	"github.com/aravindarc/cfmd/internal/confluence"
)

// version is overridden at build time via -ldflags "-X main.version=<tag>"
// (see .github/workflows/release.yml).
var version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cmd := cli.NewRootCommand()
	cmd.Version = version
	cmd.SetVersionTemplate("cfmd {{.Version}}\n")
	err := cmd.ExecuteContext(ctx)
	os.Exit(mapErrorToExitCode(err))
}

// mapErrorToExitCode converts the final error from cobra Execute into a
// process exit code that scripts can act on.
//
//	0 success
//	1 generic failure or "canceled by user"
//	2 version conflict
//	3 auth failure
//	4 network / API error
//
// See docs/SPEC.md §6 for the full table.
func mapErrorToExitCode(err error) int {
	if err == nil {
		return cli.ExitSuccess
	}
	// The diff command returns a sentinel "diff present" when the two sides
	// differ. We want exit code 1 in that case, without printing the error
	// text (the diff itself is the informative output).
	if isDiffPresent(err) {
		return cli.ExitGeneric
	}
	// Emit the error text to stderr for all other failure modes.
	fmt.Fprintln(os.Stderr, "error:", err)
	switch {
	case errors.Is(err, confluence.ErrAuth):
		return cli.ExitAuth
	case errors.Is(err, confluence.ErrConflict):
		return cli.ExitConflict
	case errors.Is(err, confluence.ErrNotFound), errors.Is(err, confluence.ErrAPI):
		return cli.ExitNetwork
	default:
		return cli.ExitGeneric
	}
}

func isDiffPresent(err error) bool {
	// Use the concrete error type from the cli package.
	_, ok := err.(interface{ diffSentinel() })
	return ok
}
