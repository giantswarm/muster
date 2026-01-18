package cli

import (
	"muster/internal/config"

	"github.com/spf13/cobra"
)

// CommandFlags holds the common flag values used across CLI commands that connect
// to a muster aggregator. This struct consolidates the repetitive flag pattern used
// by commands like get, list, check, start, stop, create, and events.
type CommandFlags struct {
	// OutputFormat specifies the desired output format (table, json, yaml)
	OutputFormat string
	// NoHeaders suppresses the header row in table output
	NoHeaders bool
	// Quiet suppresses progress indicators and non-essential output
	Quiet bool
	// Debug enables verbose logging of MCP protocol messages
	Debug bool
	// ConfigPath specifies a custom configuration directory path
	ConfigPath string
	// Endpoint overrides the aggregator endpoint URL for remote connections
	Endpoint string
	// Context specifies a named context to use for endpoint resolution
	Context string
	// AuthMode controls authentication behavior (auto, prompt, none)
	AuthMode string
}

// RegisterCommonFlags registers the common flags used by most CLI commands that
// connect to a muster aggregator. This reduces duplication across command files
// and ensures consistent flag naming and descriptions.
//
// The registered flags are:
//   - --output/-o: Output format (table, wide, json, yaml), default: "table"
//   - --no-headers: Suppress header row in table output
//   - --quiet/-q: Suppress non-essential output
//   - --debug: Enable debug logging (show MCP protocol messages)
//   - --config-path: Configuration directory
//   - --endpoint: Remote muster aggregator endpoint URL (env: MUSTER_ENDPOINT)
//   - --context: Use a specific context (env: MUSTER_CONTEXT)
//   - --auth: Authentication mode (env: MUSTER_AUTH_MODE)
func RegisterCommonFlags(cmd *cobra.Command, flags *CommandFlags) {
	cmd.PersistentFlags().StringVarP(&flags.OutputFormat, "output", "o", "table", "Output format (table, wide, json, yaml)")
	cmd.PersistentFlags().BoolVar(&flags.NoHeaders, "no-headers", false, "Suppress header row in table output")
	cmd.PersistentFlags().BoolVarP(&flags.Quiet, "quiet", "q", false, "Suppress non-essential output")
	cmd.PersistentFlags().BoolVar(&flags.Debug, "debug", false, "Enable debug logging (show MCP protocol messages)")
	cmd.PersistentFlags().StringVar(&flags.ConfigPath, "config-path", config.GetDefaultConfigPathOrPanic(), "Configuration directory")
	cmd.PersistentFlags().StringVar(&flags.Endpoint, "endpoint", GetDefaultEndpoint(), "Remote muster aggregator endpoint URL (env: MUSTER_ENDPOINT)")
	cmd.PersistentFlags().StringVar(&flags.Context, "context", "", "Use a specific context (env: MUSTER_CONTEXT)")
	cmd.PersistentFlags().StringVar(&flags.AuthMode, "auth", "", "Authentication mode: auto (default), prompt, or none (env: MUSTER_AUTH_MODE)")
}

// RegisterConnectionFlags registers only the connection-related flags (endpoint,
// context, auth) without the output formatting flags. This is useful for commands
// that don't produce formatted output but still need to connect to an aggregator.
//
// The registered flags are:
//   - --config-path: Configuration directory
//   - --endpoint: Remote muster aggregator endpoint URL (env: MUSTER_ENDPOINT)
//   - --context: Use a specific context (env: MUSTER_CONTEXT)
//   - --auth: Authentication mode (env: MUSTER_AUTH_MODE)
func RegisterConnectionFlags(cmd *cobra.Command, flags *CommandFlags) {
	cmd.PersistentFlags().StringVar(&flags.ConfigPath, "config-path", config.GetDefaultConfigPathOrPanic(), "Configuration directory")
	cmd.PersistentFlags().StringVar(&flags.Endpoint, "endpoint", GetDefaultEndpoint(), "Remote muster aggregator endpoint URL (env: MUSTER_ENDPOINT)")
	cmd.PersistentFlags().StringVar(&flags.Context, "context", "", "Use a specific context (env: MUSTER_CONTEXT)")
	cmd.PersistentFlags().StringVar(&flags.AuthMode, "auth", "", "Authentication mode: auto (default), prompt, or none (env: MUSTER_AUTH_MODE)")
}

// ToExecutorOptions converts CommandFlags to ExecutorOptions for use with NewToolExecutor.
// This provides a convenient bridge between the flag registration and executor creation.
func (f *CommandFlags) ToExecutorOptions() (ExecutorOptions, error) {
	authMode, err := GetAuthModeWithOverride(f.AuthMode)
	if err != nil {
		return ExecutorOptions{}, err
	}

	return ExecutorOptions{
		Format:     OutputFormat(f.OutputFormat),
		NoHeaders:  f.NoHeaders,
		Quiet:      f.Quiet,
		Debug:      f.Debug,
		ConfigPath: f.ConfigPath,
		Endpoint:   f.Endpoint,
		Context:    f.Context,
		AuthMode:   authMode,
	}, nil
}
