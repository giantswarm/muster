package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/creativeprojects/go-selfupdate"
	selfupdatecosign "github.com/giantswarm/selfupdate-cosign"
	"github.com/spf13/cobra"
)

// githubRepoSlug is the GitHub repository (owner/repo) whose releases carry the
// muster binaries, one per platform, each next to its cosign Sigstore bundle.
const githubRepoSlug = "giantswarm/muster"

// Seams for the tests: where releases come from (nil is GitHub) and which file
// the update replaces (the running executable).
var (
	selfUpdateSource     selfupdate.Source
	selfUpdateExecutable = selfupdate.ExecutablePath
)

// newSelfUpdateCmd creates the Cobra command for the self-update functionality.
// This allows the application to update itself to the latest version from GitHub.
func newSelfUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "self-update",
		Short: "Update muster to the latest version",
		Long: `Checks for the latest release of muster on GitHub and
updates the current binary if a newer version is found.

Release binaries are signed in CI (cosign, keyless) and published next to
their Sigstore bundle. The downloaded binary is installed only after that
bundle verifies for a CircleCI build of ` + githubRepoSlug + `; a release
without a bundle, or a download that does not match its signature, is refused
and the installed binary stays as it is.`,
		RunE: runSelfUpdate,
	}
}

// runSelfUpdate performs the self-update logic.
// It checks the current version against the latest GitHub release and, when a
// newer one exists, installs its binary once the signature bundle verifies.
func runSelfUpdate(cmd *cobra.Command, args []string) error {
	out := io.Writer(os.Stdout)
	if cmd != nil {
		out = cmd.OutOrStdout()
	}
	ctx := context.Background()
	if cmd != nil && cmd.Context() != nil {
		ctx = cmd.Context()
	}

	currentVersion := rootCmd.Version
	// Self-update is typically disabled for development versions (e.g., "dev")
	// as they are not standard releases and might not follow semantic versioning.
	if currentVersion == "" || currentVersion == "dev" {
		return fmt.Errorf("cannot self-update a development version")
	}

	_, _ = fmt.Fprintf(out, "Current version: %s\n", currentVersion)
	_, _ = fmt.Fprintln(out, "Checking for updates...")

	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		Source: selfUpdateSource,
		// The validator makes DetectLatest look for <asset>.bundle next to the
		// binary and UpdateTo verify the download against it before anything
		// is written.
		Validator: selfupdatecosign.New(githubRepoSlug),
	})
	if err != nil {
		return fmt.Errorf("failed to create updater: %w", err)
	}

	// DetectLatest fetches the latest release information from the specified GitHub repository.
	latest, found, err := updater.DetectLatest(ctx, selfupdate.ParseSlug(githubRepoSlug))
	if errors.Is(err, selfupdate.ErrValidationAssetNotFound) {
		return fmt.Errorf("the latest release of %s has no signature bundle for this platform's binary, so it cannot be verified; refusing to install it: %w", githubRepoSlug, err)
	}
	if err != nil {
		return fmt.Errorf("error detecting latest version: %w", err)
	}
	if !found {
		return fmt.Errorf("latest release for %s could not be found", githubRepoSlug)
	}

	// Compare the latest version from GitHub with the current application version.
	if !latest.GreaterThan(currentVersion) {
		_, _ = fmt.Fprintln(out, "Current version is the latest.")
		return nil
	}

	_, _ = fmt.Fprintf(out, "Found newer version: %s (published at %s)\n", latest.Version(), latest.PublishedAt)
	_, _ = fmt.Fprintf(out, "Release notes:\n%s\n", latest.ReleaseNotes)

	// Get the path to the currently running executable to replace it with the new version.
	exe, err := selfUpdateExecutable()
	if err != nil {
		return fmt.Errorf("could not locate executable path: %w", err)
	}

	_, _ = fmt.Fprintf(out, "Updating %s to version %s...\n", exe, latest.Version())

	// Download the binary and its bundle, verify, then replace the current one;
	// a failed verification leaves the file untouched.
	if err := updater.UpdateTo(ctx, latest, exe); err != nil {
		return fmt.Errorf("update failed, %s is unchanged: %w", exe, err)
	}

	_, _ = fmt.Fprintf(out, "Verified the signature and updated to version %s\n", latest.Version())
	return nil
}
