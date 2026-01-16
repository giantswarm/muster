package cmd

import (
	"context"
	"fmt"
	"muster/internal/cli"
	"muster/internal/config"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var (
	getOutputFormat string
	getQuiet        bool
	getConfigPath   string
	getEndpoint     string
	getAuthMode     string
)

// Available resource types for autocompletion
var getResourceTypes = []string{
	"service",
	"serviceclass",
	"mcpserver",
	"workflow",
	"workflow-execution",
}

// Dynamic completion function for resource names
func getResourceNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) != 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	resourceType := args[0]

	// Try to get available resources from the server
	executor, err := cli.NewToolExecutor(cli.ExecutorOptions{
		Format:     cli.OutputFormatJSON,
		Quiet:      true,
		ConfigPath: getConfigPath,
	})
	if err != nil {
		// Fallback if server not available
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	ctx := context.Background()
	err = executor.Connect(ctx)
	if err != nil {
		// Fallback if server not available
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer executor.Close()

	// Map resource types to tools
	toolMap := map[string]string{
		"service":            "core_service_list",
		"serviceclass":       "core_serviceclass_list",
		"mcpserver":          "core_mcpserver_list",
		"workflow":           "core_workflow_list",
		"workflow-execution": "core_workflow_execution_list",
	}

	toolName, exists := toolMap[resourceType]
	if !exists {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// Get the list and extract names
	names, err := getResourceNames(ctx, executor, toolName, resourceType)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// Filter by what the user has typed so far
	var completions []string
	for _, name := range names {
		if strings.HasPrefix(strings.ToLower(name), strings.ToLower(toComplete)) {
			completions = append(completions, name)
		}
	}

	return completions, cobra.ShellCompDirectiveNoFileComp
}

// Helper function to extract resource names from server response
func getResourceNames(ctx context.Context, executor *cli.ToolExecutor, toolName, resourceType string) ([]string, error) {
	result, err := executor.ExecuteJSON(ctx, toolName, map[string]interface{}{})
	if err != nil {
		return nil, err
	}

	// Parse the response to extract names
	var names []string

	// Handle different response structures
	switch data := result.(type) {
	case map[string]interface{}:
		// Look for array in wrapped response
		for _, value := range data {
			if arr, ok := value.([]interface{}); ok {
				names = extractNamesFromArray(arr, resourceType)
				break
			}
		}
	case []interface{}:
		names = extractNamesFromArray(data, resourceType)
	}

	sort.Strings(names)
	return names, nil
}

// Extract names from an array of resources
func extractNamesFromArray(arr []interface{}, resourceType string) []string {
	var names []string

	for _, item := range arr {
		if itemMap, ok := item.(map[string]interface{}); ok {
			if name, exists := itemMap["name"]; exists {
				if nameStr, ok := name.(string); ok && nameStr != "" {
					names = append(names, nameStr)
				}
			}
		}
	}

	return names
}

// getCmd represents the get command
var getCmd = &cobra.Command{
	Use:   "get",
	Short: "Get detailed information about a resource",
	Long: `Get detailed information about a specific resource by name.

Available resource types:
  service             - Get detailed status of a service
  serviceclass        - Get ServiceClass details and configuration
  mcpserver           - Get MCP server details and configuration
  workflow            - Get workflow definition and details
  workflow-execution  - Get workflow execution details and results

Examples:
  muster get service prometheus
  muster get workflow auth-flow
  muster get workflow-execution abc123-def456-789
  muster get serviceclass kubernetes --output yaml

Note: The aggregator server must be running (use 'muster serve') before using these commands.`,
	Args: cobra.ExactArgs(2),
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) == 0 {
			return getResourceTypes, cobra.ShellCompDirectiveNoFileComp
		}
		if len(args) == 1 {
			return getResourceNameCompletion(cmd, args, toComplete)
		}
		return nil, cobra.ShellCompDirectiveNoFileComp
	},
	DisableFlagsInUseLine: true,
	RunE:                  runGet,
}

// Resource type mappings for get operations
var getResourceMappings = map[string]string{
	"service":            "core_service_status",
	"serviceclass":       "core_serviceclass_get",
	"mcpserver":          "core_mcpserver_get",
	"workflow":           "core_workflow_get",
	"workflow-execution": "core_workflow_execution_get",
}

func init() {
	rootCmd.AddCommand(getCmd)

	// Add flags to the command
	getCmd.PersistentFlags().StringVarP(&getOutputFormat, "output", "o", "table", "Output format (table, json, yaml)")
	getCmd.PersistentFlags().BoolVarP(&getQuiet, "quiet", "q", false, "Suppress non-essential output")
	getCmd.PersistentFlags().StringVar(&getConfigPath, "config-path", config.GetDefaultConfigPathOrPanic(), "Configuration directory")
	getCmd.PersistentFlags().StringVar(&getEndpoint, "endpoint", cli.GetDefaultEndpoint(), "Remote muster aggregator endpoint URL (env: MUSTER_ENDPOINT)")
	getCmd.PersistentFlags().StringVar(&getAuthMode, "auth", "", "Authentication mode: auto (default), prompt, or none (env: MUSTER_AUTH_MODE)")
}

func runGet(cmd *cobra.Command, args []string) error {
	resourceType := args[0]
	resourceName := args[1]

	// Validate resource type
	toolName, exists := getResourceMappings[resourceType]
	if !exists {
		return fmt.Errorf("unknown resource type '%s'. Available types: service, serviceclass, mcpserver, workflow, workflow-execution", resourceType)
	}

	// Parse auth mode (uses environment variable as default if not specified)
	authMode := cli.GetDefaultAuthMode()
	if getAuthMode != "" {
		var err error
		authMode, err = cli.ParseAuthMode(getAuthMode)
		if err != nil {
			return err
		}
	}

	executor, err := cli.NewToolExecutor(cli.ExecutorOptions{
		Format:     cli.OutputFormat(getOutputFormat),
		Quiet:      getQuiet,
		ConfigPath: getConfigPath,
		Endpoint:   getEndpoint,
		AuthMode:   authMode,
	})
	if err != nil {
		return err
	}
	defer executor.Close()

	ctx := cmd.Context()
	if err := executor.Connect(ctx); err != nil {
		return err
	}

	// Prepare arguments based on resource type
	var toolArgs map[string]interface{}
	if resourceType == "workflow-execution" {
		// workflow-execution uses execution_id instead of name
		toolArgs = map[string]interface{}{
			"execution_id": resourceName,
		}
	} else {
		toolArgs = map[string]interface{}{
			"name": resourceName,
		}
	}

	return executor.Execute(ctx, toolName, toolArgs)
}
