package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/giantswarm/muster/internal/cli"

	"github.com/spf13/cobra"
)

var callFlags cli.CommandFlags

// callCmd represents the call command for invoking any MCP tool by name
var callCmd = &cobra.Command{
	Use:   "call <tool-name> [--arg=value ...]",
	Short: "Call an MCP tool by name",
	Long: `Call any MCP tool directly by name with arbitrary arguments.

Arguments can be passed as --key=value or --key value flags.
Use --json to pass a JSON object as arguments instead.

Examples:
  muster call core_service_list
  muster call core_service_status --name=prometheus
  muster call workflow_deploy --environment=production --replicas=3
  muster call core_mcpserver_list --output json
  muster call core_service_create --json '{"name":"my-svc","serviceClassName":"example"}'

Note: The aggregator server must be running (use 'muster serve') before using this command.`,
	Args: cobra.MinimumNArgs(1),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return callToolNameCompletion(cmd, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	},
	DisableFlagsInUseLine: true,
	FParseErrWhitelist: cobra.FParseErrWhitelist{
		UnknownFlags: true, // Allow unknown flags as tool arguments
	},
	RunE: runCall,
}

func init() {
	rootCmd.AddCommand(callCmd)
	cli.RegisterCommonFlags(callCmd, &callFlags)
	callCmd.Flags().String("json", "", "Pass tool arguments as a JSON object")
}

// callToolNameCompletion provides tab completion for tool names
func callToolNameCompletion(cmd *cobra.Command, toComplete string) ([]string, cobra.ShellCompDirective) {
	executor, err := cli.NewToolExecutor(cli.ExecutorOptions{
		Format:     cli.OutputFormatJSON,
		Quiet:      true,
		ConfigPath: callFlags.ConfigPath,
	})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	ctx := context.Background()
	err = executor.Connect(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer executor.Close()

	tools, err := executor.ListMCPTools(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var completions []string
	for _, tool := range tools {
		if strings.HasPrefix(strings.ToLower(tool.Name), strings.ToLower(toComplete)) {
			completions = append(completions, tool.Name)
		}
	}

	return completions, cobra.ShellCompDirectiveNoFileComp
}

// parseCallArguments extracts tool arguments from raw command line arguments.
// Looks for --param=value or --param value patterns after the tool name.
func parseCallArguments(toolName string) map[string]interface{} {
	params := make(map[string]interface{})

	args := os.Args
	toolIndex := -1

	// Find the index of the "call" subcommand first.
	callIndex := -1
	for i, arg := range args {
		if arg == "call" {
			callIndex = i
			break
		}
	}

	// If "call" isn't present, we can't reliably locate the tool name.
	if callIndex == -1 {
		return params
	}

	// Starting after "call", find the first non-flag argument, skipping known
	// flags and their values. That first non-flag argument should be the tool.
	for i := callIndex + 1; i < len(args); {
		arg := args[i]

		if strings.HasPrefix(arg, "--") {
			flagName := strings.TrimPrefix(arg, "--")

			// Skip known flags (and their values, if provided separately).
			if isKnownFlag(flagName) {
				if !strings.Contains(flagName, "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
					i += 2
					continue
				}
			}

			i++
			continue
		}

		// First non-flag argument after "call" should be the tool name.
		if arg == toolName {
			toolIndex = i
		}
		// Regardless of match, stop scanning at the first non-flag arg.
		break
	}
	if toolIndex == -1 || toolIndex+1 >= len(args) {
		return params
	}

	// Parse arguments after the tool name
	toolArgs := args[toolIndex+1:]

	for i := 0; i < len(toolArgs); i++ {
		arg := toolArgs[i]

		if !strings.HasPrefix(arg, "--") {
			continue
		}

		paramArg := strings.TrimPrefix(arg, "--")

		// Skip known flags
		if isKnownFlag(paramArg) {
			if !strings.Contains(paramArg, "=") && i+1 < len(toolArgs) && !strings.HasPrefix(toolArgs[i+1], "--") {
				i++ // Skip the value too
			}
			continue
		}

		if strings.Contains(paramArg, "=") {
			// --param=value format
			parts := strings.SplitN(paramArg, "=", 2)
			if len(parts) == 2 {
				params[parts[0]] = coerceValue(parts[1])
			}
		} else {
			// --param value format (check next argument)
			if i+1 < len(toolArgs) && !strings.HasPrefix(toolArgs[i+1], "--") {
				params[paramArg] = coerceValue(toolArgs[i+1])
				i++ // Skip the next argument since we consumed it
			} else {
				// Boolean flag
				params[paramArg] = true
			}
		}
	}

	return params
}

// coerceValue attempts to convert a string argument value to its native JSON type.
// It recognises "true"/"false" as booleans, "null" as nil, integers and floats as
// their respective numeric types, and leaves everything else as a plain string.
func coerceValue(s string) interface{} {
	switch s {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// isKnownFlag checks if a flag name (without --) is a known CLI flag that should be skipped
func isKnownFlag(flag string) bool {
	knownFlags := []string{
		"output", "quiet", "debug", "config-path", "endpoint", "context", "auth",
		"no-headers", "json",
	}
	for _, known := range knownFlags {
		if flag == known || strings.HasPrefix(flag, known+"=") {
			return true
		}
	}
	// Also handle short flags
	if flag == "o" || flag == "q" {
		return true
	}
	return false
}

func runCall(cmd *cobra.Command, args []string) error {
	toolName := args[0]

	opts, err := callFlags.ToExecutorOptions()
	if err != nil {
		return err
	}

	executor, err := cli.NewToolExecutor(opts)
	if err != nil {
		return err
	}
	defer executor.Close()

	ctx := cmd.Context()
	if err := executor.Connect(ctx); err != nil {
		return err
	}

	// Check if --json flag was provided
	jsonArg := getJSONFlag()
	var toolArgs map[string]interface{}

	if jsonArg != "" {
		toolArgs = make(map[string]interface{})
		if err := json.Unmarshal([]byte(jsonArg), &toolArgs); err != nil {
			return fmt.Errorf("invalid JSON argument: %w", err)
		}
	} else {
		toolArgs = parseCallArguments(toolName)
	}

	return executor.Execute(ctx, toolName, toolArgs)
}

// getJSONFlag extracts the --json flag value from os.Args since cobra won't parse it
// due to UnknownFlags being enabled.
func getJSONFlag() string {
	args := os.Args
	for i, arg := range args {
		if arg == "--json" {
			// Ensure there is a following argument and that it is not another flag.
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				return args[i+1]
			}
			// No usable value for --json; treat as if --json was not provided.
			return ""
		}
		if strings.HasPrefix(arg, "--json=") {
			return strings.TrimPrefix(arg, "--json=")
		}
	}
	return ""
}
