package aggregator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/metatools"
	"github.com/giantswarm/muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// createToolsFromProviders creates MCP tools to be exposed to clients.
//
// IMPORTANT: As of the server-side meta-tools migration (Issue #343), this method
// ONLY exposes meta-tools (list_tools, call_tool, describe_tool, etc.) to MCP clients.
// All other tools (workflow, service, config, etc.) are accessed ONLY through the
// call_tool meta-tool, which delegates to callCoreToolDirectly() for execution.
//
// This architecture provides:
//   - Centralized tool execution through a single call_tool endpoint
//   - Session-scoped tool visibility (tools are filtered per session)
//   - Simplified agent implementation (agents only see meta-tools)
//   - Consistent error handling and response formatting
//
// For internal tool execution (used by call_tool), see callCoreToolDirectly().
//
// Returns a slice of MCP server tools (meta-tools only) ready to be registered.
func (a *AggregatorServer) createToolsFromProviders() []mcpserver.ServerTool {
	var tools []mcpserver.ServerTool

	// Register ONLY meta-tools from the metatools provider
	// All other tools are accessed through call_tool meta-tool
	metaToolsHandler := api.GetMetaTools()
	if metaToolsHandler == nil {
		logging.Warn("Aggregator", "Meta-tools handler not available, creating temporary provider")
		// If the adapter isn't registered yet, create a temporary provider for tool definitions
		tempProvider := metatools.NewProvider()
		tools = a.createMetaToolsFromProvider(tempProvider)
	} else {
		// Use the registered adapter which also implements ToolProvider
		if provider, ok := metaToolsHandler.(api.ToolProvider); ok {
			tools = a.createMetaToolsFromProvider(provider)
		} else {
			logging.Warn("Aggregator", "Meta-tools handler does not implement ToolProvider")
		}
	}

	return tools
}

// createMetaToolsFromProvider creates MCP tools from a meta-tools provider.
// This helper extracts tool definitions and creates handlers for meta-tools.
func (a *AggregatorServer) createMetaToolsFromProvider(provider api.ToolProvider) []mcpserver.ServerTool {
	var tools []mcpserver.ServerTool

	for _, toolMeta := range provider.GetTools() {
		// Meta-tools don't get prefixed - they use their original names
		toolName := toolMeta.Name
		a.toolManager.setActive(toolName, true)

		tool := mcpserver.ServerTool{
			Tool: mcp.Tool{
				Name:        toolName,
				Description: toolMeta.Description,
				InputSchema: convertToMCPSchema(toolMeta.Args),
			},
			Handler: a.createMetaToolHandler(provider, toolMeta.Name),
		}

		tools = append(tools, tool)
	}

	logging.Debug("Aggregator", "Created %d meta-tools from provider", len(tools))
	return tools
}

// createMetaToolHandler creates an MCP handler for a meta-tool.
// Meta-tools are executed directly through the provider without name prefixing.
func (a *AggregatorServer) createMetaToolHandler(provider api.ToolProvider, toolName string) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Extract arguments from MCP request format
		args := make(map[string]interface{})
		if req.Params.Arguments != nil {
			if argsMap, ok := req.Params.Arguments.(map[string]interface{}); ok {
				args = argsMap
			}
		}

		// Execute the meta-tool through the provider
		result, err := provider.ExecuteTool(ctx, toolName, args)
		if err != nil {
			logging.Error("AggregatorMetaToolHandler", err, "Meta-tool execution failed for %s with args %+v", toolName, args)
			return mcp.NewToolResultError(fmt.Sprintf("Meta-tool execution failed: %v", err)), nil
		}

		// Convert API result to MCP result format
		return convertToMCPResult(result), nil
	}
}

// convertToMCPSchema converts internal arg metadata to MCP input schema format.
//
// This function bridges the gap between the internal tool arg representation
// and the JSON Schema format expected by MCP clients. It handles:
//   - Arg types and descriptions
//   - Required arg specification
//   - Default value handling
//   - Schema property generation
//   - Detailed nested schemas for complex types (objects, arrays)
//
// When a arg has a detailed Schema field, that takes precedence over
// the basic Type field, allowing for comprehensive validation rules and
// nested structure definitions.
//
// The resulting schema allows MCP clients to understand what args a tool
// expects and how to validate input before sending requests.
//
// Args:
//   - params: Slice of arg metadata from the tool provider
//
// Returns an MCP-compatible input schema with proper type information and validation rules.
func convertToMCPSchema(params []api.ArgMetadata) mcp.ToolInputSchema {
	properties := make(map[string]interface{})
	required := []string{}

	for _, param := range params {
		var propSchema map[string]interface{}

		// Use detailed schema if available, otherwise fall back to basic type
		if len(param.Schema) > 0 {
			// Use the detailed schema definition
			propSchema = make(map[string]interface{})
			for key, value := range param.Schema {
				propSchema[key] = value
			}

			// Ensure description is included (override schema description if needed)
			if param.Description != "" {
				propSchema["description"] = param.Description
			}
		} else {
			// Fall back to basic type-based schema
			propSchema = map[string]interface{}{
				"type":        param.Type,
				"description": param.Description,
			}
		}

		// Add default value if specified
		if param.Default != nil {
			propSchema["default"] = param.Default
		}

		properties[param.Name] = propSchema

		if param.Required {
			required = append(required, param.Name)
		}
	}

	return mcp.ToolInputSchema{
		Type:       "object",
		Properties: properties,
		Required:   required,
	}
}

// getAllCoreToolsAsMCPTools collects all core tools from all internal providers
// and returns them as MCP tools with the core_ prefix.
//
// This function is used by ListToolsForContext to include core tools in the
// tool listings returned by the list_tools meta-tool. The core tools include:
//   - core_workflow_* tools (workflow management and execution)
//   - core_service_* tools (service lifecycle management)
//   - core_config_* tools (configuration management)
//   - core_serviceclass_* tools (service class management)
//   - core_mcpserver_* tools (MCP server management)
//   - core_events tool (event management)
//   - core_auth_* tools (authentication operations)
//
// Each tool is prefixed with "core_" to distinguish it from MCP server tools
// which are prefixed with "x_<server>_".
//
// Returns a slice of MCP tools representing all available core tools.
func (a *AggregatorServer) getAllCoreToolsAsMCPTools() []mcp.Tool {
	var tools []mcp.Tool
	const corePrefix = "core_"

	// Helper to add tools from a provider
	addToolsFromProvider := func(handler interface{}) {
		if handler == nil {
			return
		}
		if provider, ok := handler.(api.ToolProvider); ok {
			for _, toolMeta := range provider.GetTools() {
				tools = append(tools, mcp.Tool{
					Name:        corePrefix + toolMeta.Name,
					Description: toolMeta.Description,
					InputSchema: convertToMCPSchema(toolMeta.Args),
				})
			}
		}
	}

	// Collect tools from all core providers using table-driven approach
	providers := []interface{}{
		api.GetWorkflow(),
		api.GetServiceManager(),
		api.GetConfig(),
		api.GetServiceClassManager(),
		api.GetMCPServerManager(),
		api.GetEventManager(),
	}

	for _, provider := range providers {
		addToolsFromProvider(provider)
	}

	// Auth tools - these are defined locally in the aggregator package
	// since AuthToolProvider doesn't implement the ToolProvider interface
	authTools := []mcp.Tool{
		{
			Name:        corePrefix + "auth_login",
			Description: "Authenticate to an OAuth-protected MCP server",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"server": map[string]interface{}{
						"type":        "string",
						"description": "Name of the MCP server to authenticate to",
					},
				},
				Required: []string{"server"},
			},
		},
		{
			Name:        corePrefix + "auth_logout",
			Description: "Log out from an OAuth-protected MCP server",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"server": map[string]interface{}{
						"type":        "string",
						"description": "Name of the MCP server to log out from",
					},
				},
				Required: []string{"server"},
			},
		},
	}
	tools = append(tools, authTools...)

	logging.Debug("Aggregator", "Collected %d core tools from providers", len(tools))
	return tools
}

// convertToMCPResult converts an internal tool result to MCP format.
//
// This function handles the conversion from the internal CallToolResult format
// to the MCP CallToolResult format. It processes different types of content:
//   - String content is converted directly to MCP text content
//   - Non-string content is marshaled to JSON and converted to text content
//   - Error status is preserved in the result
//
// This allows tools to return various types of data while ensuring compatibility
// with MCP clients that expect specific content formats.
//
// Args:
//   - result: Internal tool result from the tool provider
//
// Returns an MCP-compatible tool result with properly formatted content.
func convertToMCPResult(result *api.CallToolResult) *mcp.CallToolResult {
	mcpContent := make([]mcp.Content, len(result.Content))

	for i, content := range result.Content {
		if text, ok := content.(string); ok {
			mcpContent[i] = mcp.NewTextContent(text)
		} else {
			// Marshal non-string content to JSON for MCP compatibility
			jsonBytes, _ := json.Marshal(content)
			mcpContent[i] = mcp.NewTextContent(string(jsonBytes))
		}
	}

	return &mcp.CallToolResult{
		Content: mcpContent,
		IsError: result.IsError,
	}
}
