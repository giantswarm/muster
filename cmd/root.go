package cmd

import (
	"errors"
	"os"

	"github.com/giantswarm/muster/internal/cli"

	"github.com/spf13/cobra"
)

// Exit codes for CLI commands.
// These follow common conventions and are documented in docs/reference/cli/auth.md
const (
	// ExitCodeSuccess indicates successful execution.
	ExitCodeSuccess = 0
	// ExitCodeError indicates a general error (command failed, invalid arguments).
	ExitCodeError = 1
	// ExitCodeAuthRequired indicates authentication is required but not available.
	ExitCodeAuthRequired = 2
	// ExitCodeAuthFailed indicates the OAuth flow failed.
	ExitCodeAuthFailed = 3
)

// rootCmd represents the base command for the muster application.
// It is the entry point when the application is called without any subcommands.
var rootCmd = &cobra.Command{
	Use:   "muster",
	Short: "Connect your environment to Giant Swarm clusters",
	Long: `muster simplifies connecting your local development environment
(e.g., MCP servers in Cursor) to Giant Swarm clusters via Teleport
and setting up necessary connections like Prometheus port-forwarding.`,
	// SilenceUsage prevents Cobra from printing the usage message on errors that are handled by the application.
	// This is useful for providing cleaner error output to the user.
	SilenceUsage: true,
}

// SetVersion sets the version for the root command.
// This function is typically called from the main package to inject the application version at build time.
func SetVersion(v string) {
	rootCmd.Version = v
}

// GetVersion returns the current version of the application.
// This can be used by other commands to access the build version.
func GetVersion() string {
	return rootCmd.Version
}

// Execute is the main entry point for the CLI application.
// It initializes and executes the root command, which in turn handles subcommands and flags.
// This function is called by main.main().
func Execute() {
	// SetVersionTemplate defines a custom template for displaying the version.
	// This is used when the --version flag is invoked.
	rootCmd.SetVersionTemplate(`{{printf "muster version %s\n" .Version}}`)

	err := rootCmd.Execute()
	if err != nil {
		// Check for specific error types and return appropriate exit codes
		exitCode := getExitCode(err)
		os.Exit(exitCode)
	}
}

// getExitCode determines the appropriate exit code based on the error type.
// This provides semantic exit codes for scripting and automation.
func getExitCode(err error) int {
	// Check for authentication-related errors
	var authRequired *cli.AuthRequiredError
	if errors.As(err, &authRequired) {
		return ExitCodeAuthRequired
	}

	var authExpired *cli.AuthExpiredError
	if errors.As(err, &authExpired) {
		return ExitCodeAuthRequired
	}

	var authFailed *cli.AuthFailedError
	if errors.As(err, &authFailed) {
		return ExitCodeAuthFailed
	}

	// Default to general error
	return ExitCodeError
}

// init is a special Go function that is executed when the package is initialized.
// It is used here to add subcommands to the root command.
func init() {
	// connectCmdDef is now added in cmd/connect.go's init() function
	// rootCmd.AddCommand(newConnectCmd())
	rootCmd.AddCommand(newVersionCmd())
	rootCmd.AddCommand(newSelfUpdateCmd())

	// Example of how to define persistent flags (global for the application):
	// rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.config/muster/config.yaml)")

	// Example of how to define local flags (only run when this action is called directly):
	// rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
