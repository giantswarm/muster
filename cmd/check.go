package cmd

import (
	"fmt"

	"github.com/giantswarm/muster/internal/cli"

	"github.com/spf13/cobra"
)

var checkFlags cli.CommandFlags

// Available resource types for check operations
var checkResourceTypes = []string{
	"serviceclass",
	"mcpserver",
	"workflow",
}

// Dynamic completion for resource names
func checkResourceNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// Reuse the completion logic from get.go
	return getResourceNameCompletion(cmd, args, toComplete)
}

// checkCmd represents the check command
var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Check if a resource is available",
	Long: `Check if a resource is available and properly configured.

Available resource types:
  serviceclass - Check if a ServiceClass is available for use
  mcpserver    - Check MCP server status
  workflow     - Check if a workflow is available (all required tools present)

Examples:
  muster check serviceclass kubernetes
  muster check mcpserver prometheus
  muster check workflow my-deployment

Note: The aggregator server must be running (use 'muster serve') before using these commands.`,
	Args: cobra.ExactArgs(2),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return checkResourceTypes, cobra.ShellCompDirectiveNoFileComp
		}
		if len(args) == 1 {
			return checkResourceNameCompletion(cmd, args, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	},
	DisableFlagsInUseLine: true,
	RunE:                  runCheck,
}

// Resource type mappings for check operations
var checkResourceMappings = map[string]string{
	"serviceclass": "core_serviceclass_available",
	"mcpserver":    "core_service_status",
	"workflow":     "core_workflow_available",
}

func init() {
	rootCmd.AddCommand(checkCmd)
	cli.RegisterCommonFlags(checkCmd, &checkFlags)
}

func runCheck(cmd *cobra.Command, args []string) error {
	resourceType := args[0]
	resourceName := args[1]

	// Validate resource type
	toolName, exists := checkResourceMappings[resourceType]
	if !exists {
		return fmt.Errorf("unknown resource type '%s'. Available types: serviceclass, mcpserver, workflow", resourceType)
	}

	opts, err := checkFlags.ToExecutorOptions()
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

	toolArgs := map[string]interface{}{
		"name": resourceName,
	}

	return executor.Execute(ctx, toolName, toolArgs)
}
