package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"muster/internal/api"
	"muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// createToolsFromProviders creates MCP tools from all registered tool providers in the system.
//
// This method discovers and integrates tools from various muster components that implement
// the ToolProvider interface, including:
//   - Workflow manager (for workflow execution and management)
//   - Service manager (for service lifecycle operations)
//   - Config manager (for configuration management)
//   - ServiceClass manager (for service class operations)
//   - MCP server manager (for MCP server management)
//
// Each provider's tools are integrated with appropriate prefixing to avoid naming conflicts
// and ensure consistent tool naming across the aggregator.
//
// Returns a slice of MCP server tools ready to be registered with the aggregator server.
func (a *AggregatorServer) createToolsFromProviders() []mcpserver.ServerTool {
	var tools []mcpserver.ServerTool

	// Integrate workflow management tools
	if workflowHandler := api.GetWorkflow(); workflowHandler != nil {
		if provider, ok := workflowHandler.(api.ToolProvider); ok {
			for _, toolMeta := range provider.GetTools() {
				// Apply intelligent prefixing based on tool type and purpose
				mcpToolName := a.prefixToolName(toolMeta.Name)
				a.toolManager.setActive(mcpToolName, true)

				tool := mcpserver.ServerTool{
					Tool: mcp.Tool{
						Name:        mcpToolName,
						Description: toolMeta.Description,
						InputSchema: convertToMCPSchema(toolMeta.Args),
					},
					Handler: a.createToolHandler(provider, toolMeta.Name),
				}

				tools = append(tools, tool)
			}
		}
	}

	// Integrate service management tools
	if serviceManagerHandler := api.GetServiceManager(); serviceManagerHandler != nil {
		if provider, ok := serviceManagerHandler.(api.ToolProvider); ok {
			for _, toolMeta := range provider.GetTools() {
				// Apply intelligent prefixing based on tool type and purpose
				mcpToolName := a.prefixToolName(toolMeta.Name)
				a.toolManager.setActive(mcpToolName, true)

				tool := mcpserver.ServerTool{
					Tool: mcp.Tool{
						Name:        mcpToolName,
						Description: toolMeta.Description,
						InputSchema: convertToMCPSchema(toolMeta.Args),
					},
					Handler: a.createToolHandler(provider, toolMeta.Name),
				}

				tools = append(tools, tool)
			}
		}
	}

	// Integrate configuration management tools
	if configHandler := api.GetConfig(); configHandler != nil {
		if provider, ok := configHandler.(api.ToolProvider); ok {
			for _, toolMeta := range provider.GetTools() {
				// Apply intelligent prefixing based on tool type and purpose
				mcpToolName := a.prefixToolName(toolMeta.Name)
				a.toolManager.setActive(mcpToolName, true)

				tool := mcpserver.ServerTool{
					Tool: mcp.Tool{
						Name:        mcpToolName,
						Description: toolMeta.Description,
						InputSchema: convertToMCPSchema(toolMeta.Args),
					},
					Handler: a.createToolHandler(provider, toolMeta.Name),
				}

				tools = append(tools, tool)
			}
		}
	}

	// Integrate service class management tools
	if serviceClassHandler := api.GetServiceClassManager(); serviceClassHandler != nil {
		if provider, ok := serviceClassHandler.(api.ToolProvider); ok {
			for _, toolMeta := range provider.GetTools() {
				// Apply intelligent prefixing based on tool type and purpose
				mcpToolName := a.prefixToolName(toolMeta.Name)
				a.toolManager.setActive(mcpToolName, true)

				tool := mcpserver.ServerTool{
					Tool: mcp.Tool{
						Name:        mcpToolName,
						Description: toolMeta.Description,
						InputSchema: convertToMCPSchema(toolMeta.Args),
					},
					Handler: a.createToolHandler(provider, toolMeta.Name),
				}

				tools = append(tools, tool)
			}
		}
	}

	// Integrate MCP server management tools
	if mcpServerManagerHandler := api.GetMCPServerManager(); mcpServerManagerHandler != nil {
		if provider, ok := mcpServerManagerHandler.(api.ToolProvider); ok {
			for _, toolMeta := range provider.GetTools() {
				// Apply intelligent prefixing based on tool type and purpose
				mcpToolName := a.prefixToolName(toolMeta.Name)
				a.toolManager.setActive(mcpToolName, true)

				tool := mcpserver.ServerTool{
					Tool: mcp.Tool{
						Name:        mcpToolName,
						Description: toolMeta.Description,
						InputSchema: convertToMCPSchema(toolMeta.Args),
					},
					Handler: a.createToolHandler(provider, toolMeta.Name),
				}

				tools = append(tools, tool)
			}
		}
	}

	// Integrate event management tools
	if eventManagerHandler := api.GetEventManager(); eventManagerHandler != nil {
		if provider, ok := eventManagerHandler.(api.ToolProvider); ok {
			for _, toolMeta := range provider.GetTools() {
				// Apply intelligent prefixing based on tool type and purpose
				mcpToolName := a.prefixToolName(toolMeta.Name)
				a.toolManager.setActive(mcpToolName, true)

				tool := mcpserver.ServerTool{
					Tool: mcp.Tool{
						Name:        mcpToolName,
						Description: toolMeta.Description,
						InputSchema: convertToMCPSchema(toolMeta.Args),
					},
					Handler: a.createToolHandler(provider, toolMeta.Name),
				}

				tools = append(tools, tool)
			}
		}
	}

	// Integrate authentication tools (core_auth_login, core_auth_logout)
	// These are always available and allow users to authenticate to OAuth-protected MCP servers
	authProvider := NewAuthToolProvider(a)
	for _, toolMeta := range authProvider.GetTools() {
		mcpToolName := a.prefixToolName(toolMeta.Name)
		a.toolManager.setActive(mcpToolName, true)

		tool := mcpserver.ServerTool{
			Tool: mcp.Tool{
				Name:        mcpToolName,
				Description: toolMeta.Description,
				InputSchema: convertToMCPSchema(toolMeta.Args),
			},
			Handler: a.createAuthToolHandler(authProvider, toolMeta.Name),
		}

		tools = append(tools, tool)
	}

	return tools
}

// prefixToolName applies intelligent prefixing to tool names based on their purpose and patterns.
//
// This method implements a sophisticated naming strategy that categorizes tools into different
// types and applies appropriate prefixes:
//
// Management Tools (get "core_" prefix):
//   - service_*, serviceclass_*, mcpserver_*, workflow_*, config_*, auth_* operations
//   - These are administrative tools for managing muster components
//
// Execution Tools (get transformed prefixes):
//   - action_* tools become workflow_* tools (for workflow execution)
//
// External Tools (get configurable prefix):
//   - Tools from external MCP servers get the configured muster prefix
//
// This naming strategy ensures that:
//  1. Management tools are clearly identified as core muster functionality
//  2. Execution tools have intuitive names that match their purpose
//  3. External tools are properly namespaced to avoid conflicts
//
// Args:
//   - toolName: The original tool name from the provider
//
// Returns the appropriately prefixed tool name for exposure through the aggregator.
func (a *AggregatorServer) prefixToolName(toolName string) string {
	// Define management tool patterns that should get core_ prefix
	managementPatterns := []string{
		"service_",      // service management operations
		"serviceclass_", // ServiceClass management operations
		"mcpserver_",    // MCP server management operations
		"workflow_",     // workflow management (not execution) operations
		"config_",       // configuration management operations
		"events",        // Event management operations
		"auth_",         // authentication operations (core_auth_login, core_auth_logout)
	}

	// Check if this is a management tool that should get core_ prefix
	for _, pattern := range managementPatterns {
		if strings.HasPrefix(toolName, pattern) {
			return "core_" + toolName
		}
	}

	// Handle execution tools that need prefix transformation
	switch {
	case strings.HasPrefix(toolName, "action_"):
		// Transform action_* to workflow_* for execution tools
		// This makes workflow execution tools more intuitive
		workflowName := strings.Replace(toolName, "action_", "workflow_", 1)
		return workflowName
	default:
		// For other tools (external MCP servers), use the configurable prefix
		prefix := a.config.MusterPrefix + "_"
		return prefix + toolName
	}
}

// createToolHandler creates an MCP handler function for a specific tool.
//
// This method wraps the tool provider's ExecuteTool method in an MCP-compatible
// handler function. It handles the conversion between MCP request/response formats
// and the internal tool provider interface.
//
// The handler performs the following operations:
//  1. Extracts arguments from the MCP request
//  2. Calls the tool provider's ExecuteTool method
//  3. Converts the result to MCP format
//  4. Handles errors appropriately
//
// Args:
//   - provider: The tool provider that will execute the tool
//   - toolName: The original tool name (before prefixing)
//
// Returns an MCP handler function that can be registered with the MCP server.
func (a *AggregatorServer) createToolHandler(provider api.ToolProvider, toolName string) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Extract arguments from MCP request format
		args := make(map[string]interface{})
		if req.Params.Arguments != nil {
			if argsMap, ok := req.Params.Arguments.(map[string]interface{}); ok {
				args = argsMap
			}
		}

		// Execute the tool through the provider
		result, err := provider.ExecuteTool(ctx, toolName, args)
		if err != nil {
			logging.Error("AggregatorToolHandler", err, "Tool execution failed for %s with args %+v", toolName, args)
			return mcp.NewToolResultError(fmt.Sprintf("Tool execution failed: %v", err)), nil
		}

		// Convert API result to MCP result format
		return convertToMCPResult(result), nil
	}
}

// createAuthToolHandler creates an MCP handler function for authentication tools.
//
// This is similar to createToolHandler but uses the AuthToolProvider directly.
// Authentication tools need special handling because they require session context
// for managing per-session OAuth state.
//
// Args:
//   - provider: The auth tool provider that will execute the tool
//   - toolName: The original tool name (before prefixing)
//
// Returns an MCP handler function that can be registered with the MCP server.
func (a *AggregatorServer) createAuthToolHandler(provider *AuthToolProvider, toolName string) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Extract arguments from MCP request format
		args := make(map[string]interface{})
		if req.Params.Arguments != nil {
			if argsMap, ok := req.Params.Arguments.(map[string]interface{}); ok {
				args = argsMap
			}
		}

		// Execute the tool through the auth provider
		result, err := provider.ExecuteTool(ctx, toolName, args)
		if err != nil {
			logging.Error("AggregatorAuthToolHandler", err, "Auth tool execution failed for %s", toolName)
			return mcp.NewToolResultError(fmt.Sprintf("Auth tool execution failed: %v", err)), nil
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
