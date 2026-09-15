package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/giantswarm/muster/v5/internal/cli"

	"github.com/spf13/cobra"
)

var callFlags cli.CommandFlags

// callCmd represents the call command for invoking any MCP tool by name
var callCmd = &cobra.Command{
	Use:   "call <tool-name> [--arg=value ...]",
	Short: "Call an MCP tool by name",
	Long: `Call any MCP tool directly by name with arbitrary arguments.

Arguments are passed as --key=value or --key value flags. The flags muster
itself declares (--endpoint, --auth, --output and the others listed below) are
never passed on. To pass an argument that shares a name with one of them, put
it after "--": everything after the separator is an argument. Use --json to
pass a JSON object as arguments instead.

Examples:
  muster call core_service_list
  muster call core_service_status --name=prometheus
  muster call workflow_deploy --environment=production --replicas=3
  muster call core_mcpserver_list --output json
  muster call x_http_get --endpoint http://muster:8090/mcp -- --endpoint=https://target

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
	defer func() { _ = executor.Close() }()

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

// coerceValue converts a string to the most appropriate Go type.
// Only lowercase "true"/"false" become bool; "null" becomes nil.
// Integer strings become int64, floating-point strings become float64;
// everything else stays as string.
func coerceValue(s string) interface{} {
	switch s {
	case stringTrue:
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
	defer func() { _ = executor.Close() }()

	ctx := cmd.Context()
	if err := executor.Connect(ctx); err != nil {
		return err
	}

	// --json carries the whole argument object. Otherwise the arguments
	// are the flags call does not declare: cobra dropped them, the command
	// line still has them.
	jsonArg, err := cmd.Flags().GetString("json")
	if err != nil {
		return err
	}
	var toolArgs map[string]interface{}
	if jsonArg != "" {
		toolArgs = make(map[string]interface{})
		if err := json.Unmarshal([]byte(jsonArg), &toolArgs); err != nil {
			return fmt.Errorf("invalid JSON argument: %w", err)
		}
	} else {
		toolArgs = parseDynamicArgs(cmd, os.Args[1:], coerceValue)
	}

	return executor.Execute(ctx, toolName, toolArgs)
}
