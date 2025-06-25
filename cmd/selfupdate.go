package cmd

import (
	"context"
	"fmt"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/spf13/cobra"
)

// githubRepoSlug specifies the GitHub repository (owner/repo) to check for updates.
const (
	githubRepoSlug = "giantswarm/muster" // Replace with your actual repo path
)

// newSelfUpdateCmd creates the Cobra command for the self-update functionality.
// This allows the application to update itself to the latest version from GitHub.
func newSelfUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "self-update",
		Short: "Update muster to the latest version",
		Long: `Checks for the latest release of muster on GitHub and 
updates the current binary if a newer version is found.`,
		RunE: runSelfUpdate,
	}
}

// runSelfUpdate performs the self-update logic.
// It checks the current version against the latest GitHub release and updates if necessary.
func runSelfUpdate(cmd *cobra.Command, args []string) error {
	currentVersion := rootCmd.Version
	// Self-update is typically disabled for development versions (e.g., "dev")
	// as they are not standard releases and might not follow semantic versioning.
	if currentVersion == "" || currentVersion == "dev" {
		return fmt.Errorf("cannot self-update a development version")
	}

	fmt.Printf("Current version: %s\n", currentVersion)
	fmt.Println("Checking for updates...")

	updater, err := selfupdate.NewUpdater(selfupdate.Config{
		// For public GitHub repositories, no specific configuration is usually needed for the updater.
		// The library can automatically detect releases.
	})
	if err != nil {
		return fmt.Errorf("failed to create updater: %w", err)
	}

	// DetectLatest fetches the latest release information from the specified GitHub repository.
	latest, found, err := updater.DetectLatest(context.Background(), selfupdate.ParseSlug(githubRepoSlug))
	if err != nil {
		return fmt.Errorf("error detecting latest version: %w", err)
	}
	if !found {
		return fmt.Errorf("latest release for %s could not be found", githubRepoSlug)
	}

	// Compare the latest version from GitHub with the current application version.
	if !latest.GreaterThan(currentVersion) {
		fmt.Println("Current version is the latest.")
		return nil
	}

	fmt.Printf("Found newer version: %s (published at %s)\n", latest.Version(), latest.PublishedAt)
	fmt.Printf("Release notes:\n%s\n", latest.ReleaseNotes)

	// Get the path to the currently running executable to replace it with the new version.
	exe, err := selfupdate.ExecutablePath()
	if err != nil {
		return fmt.Errorf("could not locate executable path: %w", err)
	}

	fmt.Printf("Updating %s to version %s...\n", exe, latest.Version())

	// Perform the update. This will download the new binary and replace the current one.
	if err := updater.UpdateTo(context.Background(), latest, exe); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	fmt.Printf("Successfully updated to version %s\n", latest.Version())
	return nil
}
