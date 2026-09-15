package cmd

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/giantswarm/muster/v5/internal/update"
)

// newSelfUpdateCmd replaces the running binary with the latest GitHub release
// once its signature verifies -- the command agentlab and mcp-kubernetes ship
// as well -- or, with --check, only says whether one exists. The work lives
// in internal/update; this is the cobra shell.
func newSelfUpdateCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Replace this binary with the latest GitHub release (--check only reports whether one exists)",
		Long: `Looks up the latest release of ` + update.Repository + ` on GitHub and, when it is
newer than this binary, installs its binary for this OS and architecture over
the running executable. --check only reports both versions (exit status 125
when a newer release exists).

Release binaries are signed in CI (cosign, keyless) and published next to
their Sigstore bundle. The downloaded binary is installed only after that
bundle verifies for a CircleCI build of ` + update.Repository + `; a release
without a bundle, or a download that does not match its signature, is refused
and the installed binary stays as it is.

A binary without a release version (` + "`muster version`" + ` says dev) is refused:
reinstall it from a release or with go install. Every other command prints a
one-line hint on stderr while a newer release is out; MUSTER_NO_UPDATE_CHECK=1
silences it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if ctx == nil {
				ctx = context.Background()
			}
			err := update.Run(ctx, cmd.OutOrStdout(), check)
			if errors.Is(err, update.ErrOutdated) {
				// --check has reported both versions; the exit status is the
				// answer, not an "Error:" line (see getExitCode).
				cmd.SilenceErrors = true
			}
			return err
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "report the running and the latest release without installing anything; exit status 125 when a newer one exists")
	return cmd
}
