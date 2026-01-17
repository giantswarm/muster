package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"muster/internal/cli"
	"muster/internal/config"

	"github.com/spf13/cobra"
)

var (
	getOutputFormat string
	getQuiet        bool
	getConfigPath   string
	getEndpoint     string
	getContext      string
	getAuthMode     string
)

// Available resource types for autocompletion
var getResourceTypes = []string{
	"service",
	"serviceclass",
	"mcpserver",
	"workflow",
	"workflow-execution",
	"tool",
	"resource",
	"prompt",
}

// getMCPResourceTypes aliases to the shared mcpPrimitiveTypes for backward compatibility
var getMCPResourceTypes = mcpPrimitiveTypes

// Resource type mappings for get operations
var getResourceMappings = map[string]string{
	"service":            "core_service_status",
	"serviceclass":       "core_serviceclass_get",
	"mcpserver":          "core_mcpserver_get",
	"workflow":           "core_workflow_get",
	"workflow-execution": "core_workflow_execution_get",
}

// availableGetResourceTypes returns a comma-separated list of available resource types
func availableGetResourceTypes() string {
	types := make([]string, 0, len(getResourceMappings)+len(getMCPResourceTypes))
	for t := range getResourceMappings {
		types = append(types, t)
	}
	for t := range getMCPResourceTypes {
		types = append(types, t)
	}
	sort.Strings(types)
	return strings.Join(types, ", ")
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

	// Check if this is an MCP primitive type
	if _, isMCP := getMCPResourceTypes[resourceType]; isMCP {
		return getMCPPrimitiveCompletion(ctx, executor, resourceType, toComplete)
	}

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

// getMCPPrimitiveCompletion provides tab completion for MCP primitives (tools, resources, prompts)
func getMCPPrimitiveCompletion(ctx context.Context, executor *cli.ToolExecutor, resourceType, toComplete string) ([]string, cobra.ShellCompDirective) {
	var names []string

	switch resourceType {
	case "tool":
		tools, err := executor.ListMCPTools(ctx)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		for _, tool := range tools {
			names = append(names, tool.Name)
		}
	case "resource":
		resources, err := executor.ListMCPResources(ctx)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		for _, resource := range resources {
			// For resources, we complete on URI
			names = append(names, resource.URI)
		}
	case "prompt":
		prompts, err := executor.ListMCPPrompts(ctx)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		for _, prompt := range prompts {
			names = append(names, prompt.Name)
		}
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	// Filter by what the user has typed so far
	var completions []string
	for _, name := range names {
		if strings.HasPrefix(strings.ToLower(name), strings.ToLower(toComplete)) {
			completions = append(completions, name)
		}
	}

	sort.Strings(completions)
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
	Use:   "get <type> <name|uri|id>",
	Short: "Get detailed information about a resource",
	Long: `Get detailed information about a specific resource.

Available resource types:
  service             - Get detailed status of a service (by name)
  serviceclass        - Get ServiceClass details and configuration (by name)
  mcpserver           - Get MCP server details and configuration (by name)
  workflow            - Get workflow definition and details (by name)
  workflow-execution  - Get workflow execution details and results (by execution ID)
  tool                - Get MCP tool details including input schema (by name)
  resource            - Get MCP resource metadata (by URI)
  prompt              - Get MCP prompt details including arguments (by name)

Examples:
  muster get service prometheus
  muster get workflow auth-flow
  muster get workflow-execution abc123-def456-789
  muster get serviceclass kubernetes --output yaml
  muster get tool core_service_list
  muster get resource muster://auth/status
  muster get prompt code_review

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

func init() {
	rootCmd.AddCommand(getCmd)

	// Add flags to the command
	getCmd.PersistentFlags().StringVarP(&getOutputFormat, "output", "o", "table", "Output format (table, json, yaml)")
	getCmd.PersistentFlags().BoolVarP(&getQuiet, "quiet", "q", false, "Suppress non-essential output")
	getCmd.PersistentFlags().StringVar(&getConfigPath, "config-path", config.GetDefaultConfigPathOrPanic(), "Configuration directory")
	getCmd.PersistentFlags().StringVar(&getEndpoint, "endpoint", cli.GetDefaultEndpoint(), "Remote muster aggregator endpoint URL (env: MUSTER_ENDPOINT)")
	getCmd.PersistentFlags().StringVar(&getContext, "context", "", "Use a specific context (env: MUSTER_CONTEXT)")
	getCmd.PersistentFlags().StringVar(&getAuthMode, "auth", "", "Authentication mode: auto (default), prompt, or none (env: MUSTER_AUTH_MODE)")
}

func runGet(cmd *cobra.Command, args []string) error {
	resourceType := args[0]
	resourceName := args[1]

	// Check if this is an MCP primitive type
	if mcpType, isMCP := getMCPResourceTypes[resourceType]; isMCP {
		return runGetMCP(cmd, mcpType, resourceName)
	}

	// Validate resource type
	toolName, exists := getResourceMappings[resourceType]
	if !exists {
		return fmt.Errorf("unknown resource type '%s'. Available types: %s", resourceType, availableGetResourceTypes())
	}

	// Parse auth mode (uses environment variable as default if not specified)
	authMode, err := cli.GetAuthModeWithOverride(getAuthMode)
	if err != nil {
		return err
	}

	executor, err := cli.NewToolExecutor(cli.ExecutorOptions{
		Format:     cli.OutputFormat(getOutputFormat),
		Quiet:      getQuiet,
		ConfigPath: getConfigPath,
		Endpoint:   getEndpoint,
		Context:    getContext,
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

// runGetMCP handles getting MCP primitives (tools, resources, prompts)
func runGetMCP(cmd *cobra.Command, mcpType, name string) error {
	// Parse auth mode
	authMode, err := cli.GetAuthModeWithOverride(getAuthMode)
	if err != nil {
		return err
	}

	executor, err := cli.NewToolExecutor(cli.ExecutorOptions{
		Format:     cli.OutputFormat(getOutputFormat),
		Quiet:      getQuiet,
		ConfigPath: getConfigPath,
		Endpoint:   getEndpoint,
		Context:    getContext,
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

	switch mcpType {
	case "tool":
		return runGetMCPTool(cmd, executor, name)
	case "resource":
		return runGetMCPResource(cmd, executor, name)
	case "prompt":
		return runGetMCPPrompt(cmd, executor, name)
	default:
		return fmt.Errorf("unknown MCP type: %s", mcpType)
	}
}

// runGetMCPTool gets details of a specific MCP tool
func runGetMCPTool(cmd *cobra.Command, executor *cli.ToolExecutor, name string) error {
	tool, err := executor.GetMCPTool(cmd.Context(), name)
	if err != nil {
		return fmt.Errorf("failed to get tool: %w", err)
	}

	if tool == nil {
		return fmt.Errorf("tool not found: %s", name)
	}

	return cli.FormatMCPToolDetail(*tool, executor.GetOptions().Format)
}

// runGetMCPResource gets details of a specific MCP resource
func runGetMCPResource(cmd *cobra.Command, executor *cli.ToolExecutor, uri string) error {
	resource, err := executor.GetMCPResource(cmd.Context(), uri)
	if err != nil {
		return fmt.Errorf("failed to get resource: %w", err)
	}

	if resource == nil {
		return fmt.Errorf("resource not found: %s", uri)
	}

	return cli.FormatMCPResourceDetail(*resource, executor.GetOptions().Format)
}

// runGetMCPPrompt gets details of a specific MCP prompt
func runGetMCPPrompt(cmd *cobra.Command, executor *cli.ToolExecutor, name string) error {
	prompt, err := executor.GetMCPPrompt(cmd.Context(), name)
	if err != nil {
		return fmt.Errorf("failed to get prompt: %w", err)
	}

	if prompt == nil {
		return fmt.Errorf("prompt not found: %s", name)
	}

	return cli.FormatMCPPromptDetail(*prompt, executor.GetOptions().Format)
}
