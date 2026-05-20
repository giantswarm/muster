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
	if len(args) != 1 || args[0] != api.ResourceTypeService {
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

// stopResourceMappings routes `muster stop <resource>` to the MCP tool that
// implements pause. The service entry patches MCPServer.spec.suspended=true
// via core_mcpserver_update.
var stopResourceMappings = map[string]string{
	api.ResourceTypeService: "core_mcpserver_update",
}

func init() {
	rootCmd.AddCommand(stopCmd)
	cli.RegisterCommonFlags(stopCmd, &stopFlags)
}

func runStop(cmd *cobra.Command, args []string) error {
	resourceType := args[0]
	resourceName := args[1]

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

	if resourceType == api.ResourceTypeService {
		suspended, err := mcpServerSuspended(ctx, executor, resourceName)
		if err != nil {
			return err
		}
		if suspended {
			fmt.Printf("Service '%s' is already suspended\n", resourceName)
			return nil
		}
	}

	toolArgs := map[string]interface{}{
		"name":      resourceName,
		"suspended": true,
	}
	return executor.Execute(ctx, toolName, toolArgs)
}
