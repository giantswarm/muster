package cmd

import (
	"fmt"
	"muster/internal/cli"

	"github.com/spf13/cobra"
)

var (
	listOutputFormat string
	listQuiet        bool
	listConfigPath   string
)

// Available resource types for autocompletion
var listResourceTypes = []string{
	"service",
	"serviceclass",
	"mcpserver",
	"workflow",
	"workflow-execution",
}

// listCmd represents the list command
var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List resources",
	Long: `List resources in the muster environment.

Available resource types:
  service             - List all services with their status
  serviceclass        - List all ServiceClass definitions  
  mcpserver           - List all MCP server definitions
  workflow            - List all workflow definitions
  workflow-execution  - List all workflow execution history

Examples:
  muster list service
  muster list workflow
  muster list workflow-execution
  muster list serviceclass --output json

Note: The aggregator server must be running (use 'muster serve') before using these commands.`,
	Args:                  cobra.ExactArgs(1),
	ValidArgs:             listResourceTypes,
	ArgAliases:            []string{"resource_type"},
	DisableFlagsInUseLine: true,
	RunE:                  runList,
}

// Resource type mappings
var listResourceMappings = map[string]string{
	"service":            "core_service_list",
	"serviceclass":       "core_serviceclass_list",
	"mcpserver":          "core_mcpserver_list",
	"workflow":           "core_workflow_list",
	"workflow-execution": "core_workflow_execution_list",
}

func init() {
	rootCmd.AddCommand(listCmd)

	// Add flags to the command
	listCmd.PersistentFlags().StringVarP(&listOutputFormat, "output", "o", "table", "Output format (table, json, yaml)")
	listCmd.PersistentFlags().BoolVarP(&listQuiet, "quiet", "q", false, "Suppress non-essential output")
	listCmd.PersistentFlags().StringVar(&listConfigPath, "config-path", "", "Custom configuration directory path")
}

func runList(cmd *cobra.Command, args []string) error {
	resourceType := args[0]

	// Validate resource type
	toolName, exists := listResourceMappings[resourceType]
	if !exists {
		return fmt.Errorf("unknown resource type '%s'. Available types: service, serviceclass, mcpserver, workflow, workflow-execution", resourceType)
	}

	executor, err := cli.NewToolExecutor(cli.ExecutorOptions{
		Format:     cli.OutputFormat(listOutputFormat),
		Quiet:      listQuiet,
		ConfigPath: listConfigPath,
	})
	if err != nil {
		return err
	}
	defer executor.Close()

	ctx := cmd.Context()
	if err := executor.Connect(ctx); err != nil {
		return err
	}

	return executor.Execute(ctx, toolName, nil)
}
