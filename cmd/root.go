package cmd

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/giantswarm/muster/v5/internal/cli"
	"github.com/giantswarm/muster/v5/internal/update"
	"github.com/giantswarm/muster/v5/pkg/project"
)

// Exit codes for CLI commands.
// These follow common conventions and are documented in docs/how-to/authenticate-the-cli.md
const (
	// ExitCodeSuccess indicates successful execution.
	ExitCodeSuccess = 0
	// ExitCodeError indicates a general error (command failed, invalid arguments).
	ExitCodeError = 1
	// ExitCodeAuthRequired indicates authentication is required but not available.
	ExitCodeAuthRequired = 2
	// ExitCodeAuthFailed indicates the OAuth flow failed.
	ExitCodeAuthFailed = 3
	// ExitCodeOutdated is what `muster self-update --check` exits with when a
	// newer release exists -- devctl's convention for `version check`.
	ExitCodeOutdated = 125
)

// rootCmd represents the base command for the muster application.
// It is the entry point when the application is called without any subcommands.
var rootCmd = &cobra.Command{
	Use:   "muster",
	Short: "Aggregate MCP servers behind one authenticated endpoint",
	Long: `muster aggregates the tools of many MCP servers behind one Model Context
Protocol endpoint. An AI agent connects once and discovers, filters and calls
the tools of every registered server through a small set of meta-tools;
platform teams register servers, control access with toolsets and OAuth,
and run muster locally or as a Kubernetes service.

Start here:
  muster serve                Run the aggregator with the local configuration
  muster standalone           Aggregator and stdio bridge in one process, for an IDE
  muster agent --repl         Explore the aggregated tools interactively
  muster list mcpserver       Show the registered MCP servers

Documentation: https://giantswarm.github.io/muster/`,
	// SilenceUsage prevents Cobra from printing the usage message on errors that are handled by the application.
	// This is useful for providing cleaner error output to the user.
	SilenceUsage: true,
	// The build identity, printed by `muster --version` (see Execute for the
	// template) and `muster version`.
	Version: project.VersionLine(),
	// Runs for every subcommand, none of which has a PersistentPreRun of its
	// own: the one-line hint that a newer release exists, on stderr ahead of
	// the command's own output, the way agentlab does it
	// (docs/operations/installation.md; MUSTER_NO_UPDATE_CHECK=1 disables
	// it). Quiet for the processes nobody watches and for the commands that
	// speak about versions themselves, see remindsOfNewerRelease.
	PersistentPreRun: func(cmd *cobra.Command, _ []string) {
		if remindsOfNewerRelease(cmd) {
			update.Remind(cmd.Context(), cmd.ErrOrStderr())
		}
	},
}

// quietCommands never print the newer-release hint: the long-running and
// machine-facing processes, where a line on stderr is noise in someone's logs
// (the aggregator, the stdio bridge, the agent in any of its modes, the test
// runner), the commands about versions (version, self-update) and cobra's
// plumbing (help, completion).
var quietCommands = map[string]bool{
	"serve":       true,
	"standalone":  true,
	"agent":       true,
	"test":        true,
	"version":     true,
	"self-update": true,
	"help":        true,
	"completion":  true,
}

// remindsOfNewerRelease says whether cmd is a command a person runs at a
// terminal, the ones the hint is for: neither it nor a command above it is
// hidden or in quietCommands (`completion bash` is under `completion`).
func remindsOfNewerRelease(cmd *cobra.Command) bool {
	for c := cmd; c != nil; c = c.Parent() {
		if c.Hidden || quietCommands[c.Name()] {
			return false
		}
	}
	return true
}

// RootCommand returns the root command with every subcommand registered. The
// CLI reference in docs/reference/cli is rendered from this tree by
// hack/gen-cli-docs, so the command help texts are the documentation.
func RootCommand() *cobra.Command {
	return rootCmd
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
	// `self-update --check` found a newer release: the status is the answer.
	if errors.Is(err, update.ErrOutdated) {
		return ExitCodeOutdated
	}

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
