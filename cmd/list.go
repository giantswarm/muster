package cmd

import (
	"fmt"
	"muster/internal/cli"
	"muster/internal/config"

	"github.com/spf13/cobra"
)

var (
	listOutputFormat string
	listQuiet        bool
	listConfigPath   string
)

// Resource configurations mapping tool names to their aliases
var listResourceConfigs = map[string][]string{
	"core_service_list":            {"service", "services"},
	"core_serviceclass_list":       {"serviceclass", "serviceclasses"},
	"core_mcpserver_list":          {"mcpserver", "mcpservers"},
	"core_workflow_list":           {"workflow", "workflows"},
	"core_workflow_execution_list": {"workflow-execution", "workflow-executions"},
}

// Build resource types for autocompletion
func getListResourceTypes() []string {
	var types []string
	for _, aliases := range listResourceConfigs {
		types = append(types, aliases...)
	}
	return types
}

// Build resource mappings for lookup
func getListResourceMappings() map[string]string {
	mappings := make(map[string]string)
	for toolName, aliases := range listResourceConfigs {
		for _, alias := range aliases {
			mappings[alias] = toolName
		}
	}
	return mappings
}

// listCmd represents the list command
var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List resources",
	Long: `List resources in the muster environment.

Available resource types:
  service(s)              - List all services with their status
  serviceclass(es)        - List all ServiceClass definitions
  mcpserver(s)            - List all MCP server definitions
  workflow(s)             - List all workflow definitions
  workflow-execution(s)   - List all workflow execution history

Examples:
  muster list service
  muster list workflow
  muster list workflow-execution
  muster list serviceclass --output json

Note: The aggregator server must be running (use 'muster serve') before using these commands.`,
	Args:                  cobra.ExactArgs(1),
	ValidArgs:             getListResourceTypes(),
	ArgAliases:            []string{"resource_type"},
	DisableFlagsInUseLine: true,
	RunE:                  runList,
}

func init() {
	rootCmd.AddCommand(listCmd)

	// Add flags to the command
	listCmd.PersistentFlags().StringVarP(&listOutputFormat, "output", "o", "table", "Output format (table, json, yaml)")
	listCmd.PersistentFlags().BoolVarP(&listQuiet, "quiet", "q", false, "Suppress non-essential output")
	listCmd.PersistentFlags().StringVar(&listConfigPath, "config-path", config.GetDefaultConfigPathOrPanic(), "Configuration directory")
}

func runList(cmd *cobra.Command, args []string) error {
	resourceType := args[0]

	// Get resource mappings and validate resource type
	resourceMappings := getListResourceMappings()
	toolName, exists := resourceMappings[resourceType]
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
