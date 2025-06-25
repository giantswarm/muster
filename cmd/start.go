package cmd

import (
	"muster/internal/cli"
	"fmt"

	"github.com/spf13/cobra"
)

var (
	startOutputFormat string
	startQuiet        bool
)

// Available resource types for start operations
var startResourceTypes = []string{
	"service",
}

// Dynamic completion for service names
func startServiceNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 1 || args[0] != "service" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// Reuse the completion logic from get.go
	return getResourceNameCompletion(cmd, args, toComplete)
}

// startCmd represents the start command
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a resource",
	Long: `Start a resource in the muster environment.

Available resource types:
  service - Start a service by its name

Examples:
  muster start service prometheus
  muster start service vault

Note: The aggregator server must be running (use 'muster serve') before using these commands.`,
	Args: cobra.ExactArgs(2),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return startResourceTypes, cobra.ShellCompDirectiveNoFileComp
		}
		if len(args) == 1 {
			return startServiceNameCompletion(cmd, args, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	},
	DisableFlagsInUseLine: true,
	RunE:                  runStart,
}

// Resource type mappings for start operations
var startResourceMappings = map[string]string{
	"service": "core_service_start",
}

func init() {
	rootCmd.AddCommand(startCmd)

	// Add flags to the command
	startCmd.PersistentFlags().StringVarP(&startOutputFormat, "output", "o", "table", "Output format (table, json, yaml)")
	startCmd.PersistentFlags().BoolVarP(&startQuiet, "quiet", "q", false, "Suppress non-essential output")
}

func runStart(cmd *cobra.Command, args []string) error {
	resourceType := args[0]
	resourceName := args[1]

	// Validate resource type
	toolName, exists := startResourceMappings[resourceType]
	if !exists {
		return fmt.Errorf("unknown resource type '%s'. Available types: service", resourceType)
	}

	executor, err := cli.NewToolExecutor(cli.ExecutorOptions{
		Format: cli.OutputFormat(startOutputFormat),
		Quiet:  startQuiet,
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
