package cmd

import (
	"fmt"
	"muster/internal/cli"

	"github.com/spf13/cobra"
)

var (
	stopOutputFormat string
	stopQuiet        bool
	stopConfigPath   string
)

// Available resource types for stop operations
var stopResourceTypes = []string{
	"service",
}

// Dynamic completion for service names
func stopServiceNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 1 || args[0] != "service" {
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

// Resource type mappings for stop operations
var stopResourceMappings = map[string]string{
	"service": "core_service_stop",
}

func init() {
	rootCmd.AddCommand(stopCmd)

	// Add flags to the command
	stopCmd.PersistentFlags().StringVarP(&stopOutputFormat, "output", "o", "table", "Output format (table, json, yaml)")
	stopCmd.PersistentFlags().BoolVarP(&stopQuiet, "quiet", "q", false, "Suppress non-essential output")
	stopCmd.PersistentFlags().StringVar(&stopConfigPath, "config-path", "", "Custom configuration directory path")
}

func runStop(cmd *cobra.Command, args []string) error {
	resourceType := args[0]
	resourceName := args[1]

	// Validate resource type
	toolName, exists := stopResourceMappings[resourceType]
	if !exists {
		return fmt.Errorf("unknown resource type '%s'. Available types: service", resourceType)
	}

	executor, err := cli.NewToolExecutor(cli.ExecutorOptions{
		Format:     cli.OutputFormat(stopOutputFormat),
		Quiet:      stopQuiet,
		ConfigPath: stopConfigPath,
	})
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
