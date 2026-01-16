package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"muster/internal/cli"
	"muster/internal/config"

	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	listOutputFormat string
	listQuiet        bool
	listConfigPath   string
	listEndpoint     string
	listContext      string
	listAuthMode     string
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

// MCP resource types that are handled specially (not via tool execution)
var mcpResourceTypes = map[string]string{
	"tool":      "tool",
	"tools":     "tool",
	"resource":  "resource",
	"resources": "resource",
	"prompt":    "prompt",
	"prompts":   "prompt",
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
  tool(s)                 - List all MCP tools from aggregated servers
  resource(s)             - List all MCP resources from aggregated servers
  prompt(s)               - List all MCP prompts from aggregated servers

Examples:
  muster list service
  muster list workflow
  muster list workflow-execution
  muster list serviceclass --output json
  muster list tool
  muster list resources --output yaml

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
	listCmd.PersistentFlags().StringVar(&listEndpoint, "endpoint", cli.GetDefaultEndpoint(), "Remote muster aggregator endpoint URL (env: MUSTER_ENDPOINT)")
	listCmd.PersistentFlags().StringVar(&listContext, "context", "", "Use a specific context (env: MUSTER_CONTEXT)")
	listCmd.PersistentFlags().StringVar(&listAuthMode, "auth", "", "Authentication mode: auto (default), prompt, or none (env: MUSTER_AUTH_MODE)")
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
		return fmt.Errorf("unknown resource type '%s'. Available types: service, serviceclass, mcpserver, workflow, workflow-execution, tool, resource, prompt", resourceType)
	}

	// Parse auth mode (uses environment variable as default if not specified)
	authMode, err := cli.GetAuthModeWithOverride(listAuthMode)
	if err != nil {
		return err
	}

	executor, err := cli.NewToolExecutor(cli.ExecutorOptions{
		Format:     cli.OutputFormat(listOutputFormat),
		Quiet:      listQuiet,
		ConfigPath: listConfigPath,
		Endpoint:   listEndpoint,
		Context:    listContext,
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

	return executor.Execute(ctx, toolName, nil)
}

// runListMCP handles listing MCP primitives (tools, resources, prompts)
func runListMCP(cmd *cobra.Command, mcpType string) error {
	// Parse auth mode
	authMode, err := cli.GetAuthModeWithOverride(listAuthMode)
	if err != nil {
		return err
	}

	executor, err := cli.NewToolExecutor(cli.ExecutorOptions{
		Format:     cli.OutputFormat(listOutputFormat),
		Quiet:      listQuiet,
		ConfigPath: listConfigPath,
		Endpoint:   listEndpoint,
		Context:    listContext,
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
		return runListMCPTools(cmd, executor)
	case "resource":
		return runListMCPResources(cmd, executor)
	case "prompt":
		return runListMCPPrompts(cmd, executor)
	default:
		return fmt.Errorf("unknown MCP type: %s", mcpType)
	}
}

// runListMCPTools lists all MCP tools
func runListMCPTools(cmd *cobra.Command, executor *cli.ToolExecutor) error {
	tools, err := executor.ListMCPTools(cmd.Context())
	if err != nil {
		return fmt.Errorf("failed to list tools: %w", err)
	}

	return formatMCPTools(tools, executor.GetOptions().Format)
}

// runListMCPResources lists all MCP resources
func runListMCPResources(cmd *cobra.Command, executor *cli.ToolExecutor) error {
	resources, err := executor.ListMCPResources(cmd.Context())
	if err != nil {
		return fmt.Errorf("failed to list resources: %w", err)
	}

	return formatMCPResources(resources, executor.GetOptions().Format)
}

// runListMCPPrompts lists all MCP prompts
func runListMCPPrompts(cmd *cobra.Command, executor *cli.ToolExecutor) error {
	prompts, err := executor.ListMCPPrompts(cmd.Context())
	if err != nil {
		return fmt.Errorf("failed to list prompts: %w", err)
	}

	return formatMCPPrompts(prompts, executor.GetOptions().Format)
}

// formatMCPTools formats and displays MCP tools
func formatMCPTools(tools []cli.MCPTool, format cli.OutputFormat) error {
	if len(tools) == 0 {
		fmt.Printf("%s %s\n",
			text.Colors{text.FgHiYellow, text.Bold}.Sprint("📋"),
			text.Colors{text.FgHiYellow, text.Bold}.Sprint("No tools found"))
		return nil
	}

	// Sort tools by name
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})

	switch format {
	case cli.OutputFormatJSON:
		type ToolInfo struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		toolList := make([]ToolInfo, len(tools))
		for i, tool := range tools {
			toolList[i] = ToolInfo{
				Name:        tool.Name,
				Description: tool.Description,
			}
		}
		jsonData, err := json.MarshalIndent(toolList, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to format as JSON: %w", err)
		}
		fmt.Println(string(jsonData))
		return nil

	case cli.OutputFormatYAML:
		type ToolInfo struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		}
		toolList := make([]ToolInfo, len(tools))
		for i, tool := range tools {
			toolList[i] = ToolInfo{
				Name:        tool.Name,
				Description: tool.Description,
			}
		}
		yamlData, err := yaml.Marshal(toolList)
		if err != nil {
			return fmt.Errorf("failed to format as YAML: %w", err)
		}
		fmt.Print(string(yamlData))
		return nil

	default: // table format
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{
			text.Colors{text.FgHiBlue, text.Bold}.Sprint("NAME"),
			text.Colors{text.FgHiBlue, text.Bold}.Sprint("DESCRIPTION"),
		})

		for _, tool := range tools {
			desc := tool.Description
			if len(desc) > 60 {
				desc = desc[:57] + "..."
			}
			t.AppendRow(table.Row{
				text.Colors{text.FgHiBlue, text.Bold}.Sprint(tool.Name),
				desc,
			})
		}

		t.Render()
		fmt.Printf("\n%s %s %s %s\n",
			text.Colors{text.FgHiMagenta, text.Bold}.Sprint("🔧"),
			text.FgHiBlue.Sprint("Total:"),
			text.Bold.Sprint(len(tools)),
			text.FgHiBlue.Sprint("tools"))
		return nil
	}
}

// formatMCPResources formats and displays MCP resources
func formatMCPResources(resources []cli.MCPResource, format cli.OutputFormat) error {
	if len(resources) == 0 {
		fmt.Printf("%s %s\n",
			text.Colors{text.FgHiYellow, text.Bold}.Sprint("📋"),
			text.Colors{text.FgHiYellow, text.Bold}.Sprint("No resources found"))
		return nil
	}

	// Sort resources by URI
	sort.Slice(resources, func(i, j int) bool {
		return resources[i].URI < resources[j].URI
	})

	switch format {
	case cli.OutputFormatJSON:
		type ResourceInfo struct {
			URI         string `json:"uri"`
			Name        string `json:"name"`
			Description string `json:"description,omitempty"`
			MIMEType    string `json:"mimeType,omitempty"`
		}
		resourceList := make([]ResourceInfo, len(resources))
		for i, resource := range resources {
			resourceList[i] = ResourceInfo{
				URI:         resource.URI,
				Name:        resource.Name,
				Description: resource.Description,
				MIMEType:    resource.MIMEType,
			}
		}
		jsonData, err := json.MarshalIndent(resourceList, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to format as JSON: %w", err)
		}
		fmt.Println(string(jsonData))
		return nil

	case cli.OutputFormatYAML:
		type ResourceInfo struct {
			URI         string `yaml:"uri"`
			Name        string `yaml:"name"`
			Description string `yaml:"description,omitempty"`
			MIMEType    string `yaml:"mimeType,omitempty"`
		}
		resourceList := make([]ResourceInfo, len(resources))
		for i, resource := range resources {
			resourceList[i] = ResourceInfo{
				URI:         resource.URI,
				Name:        resource.Name,
				Description: resource.Description,
				MIMEType:    resource.MIMEType,
			}
		}
		yamlData, err := yaml.Marshal(resourceList)
		if err != nil {
			return fmt.Errorf("failed to format as YAML: %w", err)
		}
		fmt.Print(string(yamlData))
		return nil

	default: // table format
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{
			text.Colors{text.FgHiBlue, text.Bold}.Sprint("URI"),
			text.Colors{text.FgHiBlue, text.Bold}.Sprint("NAME"),
			text.Colors{text.FgHiBlue, text.Bold}.Sprint("DESCRIPTION"),
			text.Colors{text.FgHiBlue, text.Bold}.Sprint("MIME TYPE"),
		})

		for _, resource := range resources {
			desc := resource.Description
			if desc == "" {
				desc = resource.Name
			}
			if len(desc) > 40 {
				desc = desc[:37] + "..."
			}
			uri := resource.URI
			if len(uri) > 40 {
				uri = uri[:37] + "..."
			}
			t.AppendRow(table.Row{
				text.Colors{text.FgHiCyan, text.Bold}.Sprint(uri),
				resource.Name,
				desc,
				resource.MIMEType,
			})
		}

		t.Render()
		fmt.Printf("\n%s %s %s %s\n",
			text.Colors{text.FgHiCyan, text.Bold}.Sprint("📦"),
			text.FgHiBlue.Sprint("Total:"),
			text.Bold.Sprint(len(resources)),
			text.FgHiBlue.Sprint("resources"))
		return nil
	}
}

// formatMCPPrompts formats and displays MCP prompts
func formatMCPPrompts(prompts []cli.MCPPrompt, format cli.OutputFormat) error {
	if len(prompts) == 0 {
		fmt.Printf("%s %s\n",
			text.Colors{text.FgHiYellow, text.Bold}.Sprint("📋"),
			text.Colors{text.FgHiYellow, text.Bold}.Sprint("No prompts found"))
		return nil
	}

	// Sort prompts by name
	sort.Slice(prompts, func(i, j int) bool {
		return prompts[i].Name < prompts[j].Name
	})

	switch format {
	case cli.OutputFormatJSON:
		type PromptInfo struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		promptList := make([]PromptInfo, len(prompts))
		for i, prompt := range prompts {
			promptList[i] = PromptInfo{
				Name:        prompt.Name,
				Description: prompt.Description,
			}
		}
		jsonData, err := json.MarshalIndent(promptList, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to format as JSON: %w", err)
		}
		fmt.Println(string(jsonData))
		return nil

	case cli.OutputFormatYAML:
		type PromptInfo struct {
			Name        string `yaml:"name"`
			Description string `yaml:"description"`
		}
		promptList := make([]PromptInfo, len(prompts))
		for i, prompt := range prompts {
			promptList[i] = PromptInfo{
				Name:        prompt.Name,
				Description: prompt.Description,
			}
		}
		yamlData, err := yaml.Marshal(promptList)
		if err != nil {
			return fmt.Errorf("failed to format as YAML: %w", err)
		}
		fmt.Print(string(yamlData))
		return nil

	default: // table format
		t := table.NewWriter()
		t.SetOutputMirror(os.Stdout)
		t.SetStyle(table.StyleRounded)
		t.AppendHeader(table.Row{
			text.Colors{text.FgHiBlue, text.Bold}.Sprint("NAME"),
			text.Colors{text.FgHiBlue, text.Bold}.Sprint("DESCRIPTION"),
		})

		for _, prompt := range prompts {
			desc := prompt.Description
			if len(desc) > 60 {
				desc = desc[:57] + "..."
			}
			t.AppendRow(table.Row{
				text.Colors{text.FgHiBlue, text.Bold}.Sprint(prompt.Name),
				desc,
			})
		}

		t.Render()
		fmt.Printf("\n%s %s %s %s\n",
			text.Colors{text.FgHiYellow, text.Bold}.Sprint("📝"),
			text.FgHiBlue.Sprint("Total:"),
			text.Bold.Sprint(len(prompts)),
			text.FgHiBlue.Sprint("prompts"))
		return nil
	}
}
