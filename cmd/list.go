package cmd

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/cli"

	"github.com/spf13/cobra"
)

var (
	listFlags       cli.CommandFlags
	listFilter      string
	listDescription string
	listServer      string
	listShowAll     bool
	listVerbose     bool
)

// Resource configurations mapping tool names to their aliases
var listResourceConfigs = map[string][]string{
	"core_service_list":            {api.ResourceTypeService, api.ResourceTypeServices},
	"core_mcpserver_list":          {api.ResourceTypeMCPServer, api.ResourceTypeMCPServers},
	"core_workflow_list":           {api.ResourceTypeWorkflow, api.ResourceTypeWorkflows},
	"core_workflow_execution_list": {api.ResourceTypeWorkflowExecution, api.ResourceTypeWorkflowExecutions},
}

// Build resource types for autocompletion
func getListResourceTypes() []string {
	var types []string
	for _, aliases := range listResourceConfigs {
		types = append(types, aliases...)
	}
	// Add MCP resource types
	for alias := range mcpResourceTypes {
		types = append(types, alias)
	}
	sort.Strings(types)
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

var mcpResourceTypes = mcpPrimitiveTypes

// MCPFilterOptions contains filter criteria for MCP primitives
type MCPFilterOptions struct {
	// Pattern is a wildcard pattern to match against names (* and ? supported)
	Pattern string
	// Description is a case-insensitive substring to match against descriptions
	Description string
	// Server selects the items of one server: the server an item belongs to
	// as the aggregator reports it, or the prefix of its exposed name
	// ("x_files" for a server named "files"). Case-insensitive.
	Server string
}

// IsEmpty returns true if no filters are set
func (o MCPFilterOptions) IsEmpty() bool {
	return o.Pattern == "" && o.Description == "" && o.Server == ""
}

// HasMCPOnlyFilters returns true if any MCP-specific filters are set
func (o MCPFilterOptions) HasMCPOnlyFilters() bool {
	return o.Pattern != "" || o.Description != "" || o.Server != ""
}

// matchesWildcard checks if a name matches a wildcard pattern.
// Supports * (matches any sequence of characters) and ? (matches any single character).
func matchesWildcard(name, pattern string) bool {
	if pattern == "" {
		return true
	}
	// path.Match uses the same wildcard syntax we want
	matched, err := path.Match(pattern, name)
	if err != nil {
		// Invalid pattern - return false
		return false
	}
	return matched
}

// matchesDescription checks if a description contains the given substring (case-insensitive)
func matchesDescription(description, filter string) bool {
	if filter == "" {
		return true
	}
	return strings.Contains(strings.ToLower(description), strings.ToLower(filter))
}

// matchesServer reports whether an item belongs to the server named by the
// filter. The filter is compared, case-insensitively, with the server the
// aggregator reports for the item (empty when the listing carries none) and
// with the prefix of the item's exposed name, so the tools of a server
// registered as "files" are selected by "files" as well as by "x_files". The
// exposed name alone is not enough: a server's tool prefix is its configured
// toolPrefix, which need not be its name.
func matchesServer(name, server, filter string) bool {
	if filter == "" {
		return true
	}
	if strings.EqualFold(server, filter) {
		return true
	}
	return strings.HasPrefix(strings.ToLower(name), strings.ToLower(filter)+"_")
}

// matchesMCPFilter checks if an item matches the name pattern, description and
// server filters. server is the item's server as the aggregator reports it;
// resources and prompts, listed over the native protocol, carry none.
func matchesMCPFilter(name, description, server string, opts MCPFilterOptions) bool {
	return matchesWildcard(name, opts.Pattern) &&
		matchesDescription(description, opts.Description) &&
		matchesServer(name, server, opts.Server)
}

// filterMCPTools filters tools by name pattern, description and server
func filterMCPTools(tools []cli.MCPToolInfo, opts MCPFilterOptions) []cli.MCPToolInfo {
	if opts.IsEmpty() {
		return tools
	}
	var filtered []cli.MCPToolInfo
	for _, tool := range tools {
		if matchesMCPFilter(tool.Name, tool.Description, tool.Server, opts) {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

// filterMCPResources filters resources by name pattern and description
func filterMCPResources(resources []cli.MCPResource, opts MCPFilterOptions) []cli.MCPResource {
	if opts.IsEmpty() {
		return resources
	}
	var filtered []cli.MCPResource
	for _, resource := range resources {
		if matchesMCPFilter(resource.Name, resource.Description, "", opts) {
			filtered = append(filtered, resource)
		}
	}
	return filtered
}

// filterMCPPrompts filters prompts by name pattern and description
func filterMCPPrompts(prompts []cli.MCPPrompt, opts MCPFilterOptions) []cli.MCPPrompt {
	if opts.IsEmpty() {
		return prompts
	}
	var filtered []cli.MCPPrompt
	for _, prompt := range prompts {
		if matchesMCPFilter(prompt.Name, prompt.Description, "", opts) {
			filtered = append(filtered, prompt)
		}
	}
	return filtered
}

// availableListResourceTypes returns a comma-separated list of available resource types
func availableListResourceTypes() string {
	types := getListResourceTypes()
	// Deduplicate and sort
	seen := make(map[string]bool)
	var unique []string
	for _, t := range types {
		if !seen[t] {
			seen[t] = true
			unique = append(unique, t)
		}
	}
	sort.Strings(unique)
	return strings.Join(unique, ", ")
}

// listCmd represents the list command
var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List resources",
	Long: `List resources in the muster environment.

Available resource types:
  service(s)              - List all services with their status
  mcpserver(s)            - List all MCP server definitions
  workflow(s)             - List all workflow definitions
  workflow-execution(s)   - List all workflow execution history
  tool(s)                 - List all MCP tools from aggregated servers
  resource(s)             - List all MCP resources from aggregated servers
  prompt(s)               - List all MCP prompts from aggregated servers

Filtering (for MCP primitives only: tool, resource, prompt):
  --filter <pattern>       - Filter by name pattern (wildcards * and ? supported)
  --description <text>     - Filter by description content (case-insensitive substring)
  --server <name>          - Filter by server. Tools: the server a tool belongs to as the
                             aggregator reports it (e.g. "files" for x_files_*, "core",
                             "workflow"), or the prefix of the exposed name (e.g. "x_files").
                             Resources and prompts: the prefix of the exposed name.

Output options:
  --output/-o <format>     - Output format: table (default), wide, json, yaml
  --no-headers             - Suppress header row in table output (useful for scripting)

The 'wide' format (-o wide) shows additional columns for each resource type:
  services       - endpoint, tools count
  mcpservers     - url/command, timeout
  workflows      - input arguments
  tools          - server, argument count
  resources      - name
  prompts        - argument count

Examples:
  muster list service
  muster list services -o wide
  muster list workflow
  muster list workflow-execution
  muster list mcpservers -o wide
  muster list tool
  muster list tools -o wide
  muster list tools --filter "core_*"
  muster list tools --server github
  muster list tools --server files -o wide
  muster list tools --filter "*service*" --description "status"
  muster list resources --output yaml
  muster list mcpservers --no-headers | awk '{print $1}'

Note: The aggregator server must be running (use 'muster serve') before using these commands.`,
	Args:                  cobra.ExactArgs(1),
	ValidArgs:             getListResourceTypes(),
	ArgAliases:            []string{"resource_type"},
	DisableFlagsInUseLine: true,
	RunE:                  runList,
}

func init() {
	rootCmd.AddCommand(listCmd)
	cli.RegisterCommonFlags(listCmd, &listFlags)

	// List-specific filtering flags
	listCmd.PersistentFlags().StringVar(&listFilter, "filter", "", "Filter by name pattern (wildcards * and ? supported, for MCP primitives only)")
	listCmd.PersistentFlags().StringVar(&listDescription, "description", "", "Filter by description content (case-insensitive substring, for MCP primitives only)")
	listCmd.PersistentFlags().StringVar(&listServer, "server", "", "Filter by server: the server a tool belongs to (e.g. \"files\", \"core\") or the exposed name prefix (e.g. \"x_files\"); for MCP primitives only")
	listCmd.PersistentFlags().BoolVar(&listShowAll, "all", false, "Show all servers including unreachable ones (for mcpserver only)")
	listCmd.PersistentFlags().BoolVar(&listVerbose, "verbose", false, "Show detailed error information for failed/unreachable servers (for mcpserver only)")
}

func runList(cmd *cobra.Command, args []string) error {
	resourceType := args[0]

	// Check if this is an MCP primitive type
	if mcpType, isMCP := mcpResourceTypes[resourceType]; isMCP {
		return runListMCP(cmd, mcpType)
	}

	// Get resource mappings and validate resource type
	resourceMappings := getListResourceMappings()
	toolName, exists := resourceMappings[resourceType]
	if !exists {
		return fmt.Errorf("unknown resource type '%s'. Available types: %s", resourceType, availableListResourceTypes())
	}

	// Warn if MCP-only filter flags are used with non-MCP resources
	filterOpts := MCPFilterOptions{
		Pattern:     listFilter,
		Description: listDescription,
		Server:      listServer,
	}
	if filterOpts.HasMCPOnlyFilters() && !listFlags.Quiet {
		var ignoredFlags []string
		if listFilter != "" {
			ignoredFlags = append(ignoredFlags, "--filter")
		}
		if listDescription != "" {
			ignoredFlags = append(ignoredFlags, "--description")
		}
		if listServer != "" {
			ignoredFlags = append(ignoredFlags, "--server")
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s ignored for '%s' (only works with tools, resources, prompts)\n",
			strings.Join(ignoredFlags, ", "), resourceType)
	}

	// Warn if mcpserver-specific flags are used with non-mcpserver resources
	if toolName != "core_mcpserver_list" && !listFlags.Quiet {
		var mcpserverFlags []string
		if listShowAll {
			mcpserverFlags = append(mcpserverFlags, "--all")
		}
		if listVerbose {
			mcpserverFlags = append(mcpserverFlags, "--verbose")
		}
		if len(mcpserverFlags) > 0 {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %s ignored for '%s' (only works with mcpserver)\n",
				strings.Join(mcpserverFlags, ", "), resourceType)
		}
	}

	opts, err := listFlags.ToExecutorOptions()
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

	// For mcpserver list, pass the showAll and verbose parameters
	var toolArgs map[string]interface{}
	if toolName == "core_mcpserver_list" {
		toolArgs = map[string]interface{}{}
		if listShowAll {
			toolArgs["showAll"] = true
		}
		if listVerbose {
			toolArgs["verbose"] = true
		}
	}

	return executor.Execute(ctx, toolName, toolArgs)
}

// runListMCP handles listing MCP primitives (tools, resources, prompts)
func runListMCP(cmd *cobra.Command, mcpType string) error {
	opts, err := listFlags.ToExecutorOptions()
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

	filterOpts := MCPFilterOptions{
		Pattern:     listFilter,
		Description: listDescription,
		Server:      listServer,
	}

	switch mcpType {
	case api.MCPPrimitiveTool:
		return runListMCPTools(cmd, executor, filterOpts)
	case api.MCPPrimitiveResource:
		return runListMCPResources(cmd, executor, filterOpts)
	case api.MCPPrimitivePrompt:
		return runListMCPPrompts(cmd, executor, filterOpts)
	default:
		return fmt.Errorf("unknown MCP type: %s", mcpType)
	}
}

// runListMCPTools lists all MCP tools with optional filtering
func runListMCPTools(cmd *cobra.Command, executor *cli.ToolExecutor, filterOpts MCPFilterOptions) error {
	tools, err := executor.ListMCPTools(cmd.Context())
	if err != nil {
		return fmt.Errorf("failed to list tools: %w", err)
	}

	tools = filterMCPTools(tools, filterOpts)
	opts := executor.GetOptions()
	return cli.FormatMCPToolsWithOptions(tools, opts.Format, opts.NoHeaders)
}

// runListMCPResources lists all MCP resources with optional filtering
func runListMCPResources(cmd *cobra.Command, executor *cli.ToolExecutor, filterOpts MCPFilterOptions) error {
	resources, err := executor.ListMCPResources(cmd.Context())
	if err != nil {
		return fmt.Errorf("failed to list resources: %w", err)
	}

	resources = filterMCPResources(resources, filterOpts)
	opts := executor.GetOptions()
	return cli.FormatMCPResourcesWithOptions(resources, opts.Format, opts.NoHeaders)
}

// runListMCPPrompts lists all MCP prompts with optional filtering
func runListMCPPrompts(cmd *cobra.Command, executor *cli.ToolExecutor, filterOpts MCPFilterOptions) error {
	prompts, err := executor.ListMCPPrompts(cmd.Context())
	if err != nil {
		return fmt.Errorf("failed to list prompts: %w", err)
	}

	prompts = filterMCPPrompts(prompts, filterOpts)
	opts := executor.GetOptions()
	return cli.FormatMCPPromptsWithOptions(prompts, opts.Format, opts.NoHeaders)
}
