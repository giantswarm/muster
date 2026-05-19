package cmd

import (
	"fmt"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/cli"

	"github.com/spf13/cobra"
)

var stopFlags cli.CommandFlags

// Available resource types for stop operations
var stopResourceTypes = []string{
	api.ResourceTypeService,
}

// Dynamic completion for service names
func stopServiceNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 1 || args[0] != api.ResourceTypeService { //nolint:goconst
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// Reuse the completion logic from get.go
	return getResourceNameCompletion(cmd, args, toComplete)
}

// stopCmd represents the stop command
var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop a resource",
	Long: `Stop a resource in the muster environment.

Available resource types:
  service - Stop a service by its name

Examples:
  muster stop service prometheus
  muster stop service vault

Note: The aggregator server must be running (use 'muster serve') before using these commands.`,
	Args: cobra.ExactArgs(2),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return stopResourceTypes, cobra.ShellCompDirectiveNoFileComp
		}
		if len(args) == 1 {
			return stopServiceNameCompletion(cmd, args, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	},
	DisableFlagsInUseLine: true,
	RunE:                  runStop,
}

// Resource type mappings for stop operations.
// api.ResourceTypeService used to call core_service_stop; that tool was removed when the
// orchestrator-driven dial loop went away. Pause is now declarative: patch
// MCPServer.spec.suspended=true through core_mcpserver_update.
var stopResourceMappings = map[string]string{
	"service": "core_mcpserver_update",
}

func init() {
	rootCmd.AddCommand(stopCmd)
	cli.RegisterCommonFlags(stopCmd, &stopFlags)
}

func runStop(cmd *cobra.Command, args []string) error {
	resourceType := args[0]
	resourceName := args[1]

	// Validate resource type
	toolName, exists := stopResourceMappings[resourceType]
	if !exists {
		return fmt.Errorf("unknown resource type '%s'. Available types: service", resourceType)
	}

	opts, err := stopFlags.ToExecutorOptions()
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

	toolArgs := map[string]interface{}{
		"name": resourceName,
	}
	if resourceType == api.ResourceTypeService {
		toolArgs["suspended"] = true
	}

	return executor.Execute(ctx, toolName, toolArgs)
}
