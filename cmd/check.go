package cmd

import (
	"muster/internal/cli"
	"fmt"

	"github.com/spf13/cobra"
)

var (
	checkOutputFormat string
	checkQuiet        bool
)

// Available resource types for check operations
var checkResourceTypes = []string{
	"serviceclass",
	"mcpserver",
	"capability",
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
  capability   - Check if a capability is available

Examples:
  muster check serviceclass kubernetes
  muster check capability cluster-auth
  muster check mcpserver prometheus

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
	"capability":   "core_capability_available",
}

func init() {
	rootCmd.AddCommand(checkCmd)

	// Add flags to the command
	checkCmd.PersistentFlags().StringVarP(&checkOutputFormat, "output", "o", "table", "Output format (table, json, yaml)")
	checkCmd.PersistentFlags().BoolVarP(&checkQuiet, "quiet", "q", false, "Suppress non-essential output")
}

func runCheck(cmd *cobra.Command, args []string) error {
	resourceType := args[0]
	resourceName := args[1]

	// Validate resource type
	toolName, exists := checkResourceMappings[resourceType]
	if !exists {
		return fmt.Errorf("unknown resource type '%s'. Available types: serviceclass, mcpserver, capability", resourceType)
	}

	executor, err := cli.NewToolExecutor(cli.ExecutorOptions{
		Format: cli.OutputFormat(checkOutputFormat),
		Quiet:  checkQuiet,
	})
	if err != nil {
		return err
	}
	defer executor.Close()

	ctx := cmd.Context()
	if err := executor.Connect(ctx); err != nil {
		return err
	}

	arguments := map[string]interface{}{
		"name": resourceName,
	}

	return executor.Execute(ctx, toolName, arguments)
}
