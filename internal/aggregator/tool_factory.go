package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"muster/internal/api"
	"muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// createToolsFromProviders creates MCP tools from all registered tool providers
func (a *AggregatorServer) createToolsFromProviders() []server.ServerTool {
	var tools []server.ServerTool

	// Get workflow handler and check if it's a ToolProvider
	if workflowHandler := api.GetWorkflow(); workflowHandler != nil {
		if provider, ok := workflowHandler.(api.ToolProvider); ok {
			for _, toolMeta := range provider.GetTools() {
				// Apply appropriate prefix
				mcpToolName := a.prefixToolName("workflow", toolMeta.Name)
				a.toolManager.setActive(mcpToolName, true)

				tool := server.ServerTool{
					Tool: mcp.Tool{
						Name:        mcpToolName,
						Description: toolMeta.Description,
						InputSchema: convertToMCPSchema(toolMeta.Parameters),
					},
					Handler: a.createToolHandler(provider, toolMeta.Name),
				}

				tools = append(tools, tool)
			}
		}
	}

	// Get capability handler and check if it's a ToolProvider
	if capabilityHandler := api.GetCapability(); capabilityHandler != nil {
		if provider, ok := capabilityHandler.(api.ToolProvider); ok {
			for _, toolMeta := range provider.GetTools() {
				// Apply appropriate prefix
				mcpToolName := a.prefixToolName("capability", toolMeta.Name)
				a.toolManager.setActive(mcpToolName, true)

				tool := server.ServerTool{
					Tool: mcp.Tool{
						Name:        mcpToolName,
						Description: toolMeta.Description,
						InputSchema: convertToMCPSchema(toolMeta.Parameters),
					},
					Handler: a.createToolHandler(provider, toolMeta.Name),
				}

				tools = append(tools, tool)
			}
		}
	}

	// Get service manager handler and check if it's a ToolProvider
	if serviceManagerHandler := api.GetServiceManager(); serviceManagerHandler != nil {
		if provider, ok := serviceManagerHandler.(api.ToolProvider); ok {
			for _, toolMeta := range provider.GetTools() {
				// Apply appropriate prefix
				mcpToolName := a.prefixToolName("service", toolMeta.Name)
				a.toolManager.setActive(mcpToolName, true)

				tool := server.ServerTool{
					Tool: mcp.Tool{
						Name:        mcpToolName,
						Description: toolMeta.Description,
						InputSchema: convertToMCPSchema(toolMeta.Parameters),
					},
					Handler: a.createToolHandler(provider, toolMeta.Name),
				}

				tools = append(tools, tool)
			}
		}
	}

	// Get config handler and check if it's a ToolProvider
	if configHandler := api.GetConfig(); configHandler != nil {
		if provider, ok := configHandler.(api.ToolProvider); ok {
			for _, toolMeta := range provider.GetTools() {
				// Apply appropriate prefix
				mcpToolName := a.prefixToolName("config", toolMeta.Name)
				a.toolManager.setActive(mcpToolName, true)

				tool := server.ServerTool{
					Tool: mcp.Tool{
						Name:        mcpToolName,
						Description: toolMeta.Description,
						InputSchema: convertToMCPSchema(toolMeta.Parameters),
					},
					Handler: a.createToolHandler(provider, toolMeta.Name),
				}

				tools = append(tools, tool)
			}
		}
	}

	// Get service class manager handler and check if it's a ToolProvider
	if serviceClassHandler := api.GetServiceClassManager(); serviceClassHandler != nil {
		if provider, ok := serviceClassHandler.(api.ToolProvider); ok {
			for _, toolMeta := range provider.GetTools() {
				// Apply appropriate prefix
				mcpToolName := a.prefixToolName("serviceclass", toolMeta.Name)
				a.toolManager.setActive(mcpToolName, true)

				tool := server.ServerTool{
					Tool: mcp.Tool{
						Name:        mcpToolName,
						Description: toolMeta.Description,
						InputSchema: convertToMCPSchema(toolMeta.Parameters),
					},
					Handler: a.createToolHandler(provider, toolMeta.Name),
				}

				tools = append(tools, tool)
			}
		}
	}

	// Get MCP server manager handler and check if it's a ToolProvider
	if mcpServerManagerHandler := api.GetMCPServerManager(); mcpServerManagerHandler != nil {
		if provider, ok := mcpServerManagerHandler.(api.ToolProvider); ok {
			for _, toolMeta := range provider.GetTools() {
				// Apply appropriate prefix
				mcpToolName := a.prefixToolName("mcpserver", toolMeta.Name)
				a.toolManager.setActive(mcpToolName, true)

				tool := server.ServerTool{
					Tool: mcp.Tool{
						Name:        mcpToolName,
						Description: toolMeta.Description,
						InputSchema: convertToMCPSchema(toolMeta.Parameters),
					},
					Handler: a.createToolHandler(provider, toolMeta.Name),
				}

				tools = append(tools, tool)
			}
		}
	}

	return tools
}

// prefixToolName applies the appropriate prefix based on tool patterns and provider type
func (a *AggregatorServer) prefixToolName(provider, toolName string) string {
	// Define management tool patterns that should get core_ prefix
	managementPatterns := []string{
		"service_",      // service management
		"serviceclass_", // ServiceClass management
		"mcpserver_",    // MCP server management
		"workflow_",     // workflow management (not execution)
		"capability_",   // capability management
		"config_",       // configuration management
		"mcp_",          // MCP service management
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
		workflowName := strings.Replace(toolName, "action_", "workflow_", 1)
		return workflowName
	case strings.HasPrefix(toolName, "api_"):
		// Keep api_* tools unchanged (they're already correct for capability operations)
		return toolName
	default:
		// For other tools (external MCP servers, capability operations), use the configurable prefix
		prefix := a.config.MusterPrefix + "_"
		return prefix + toolName
	}
}

// createToolHandler creates an MCP handler for a tool
func (a *AggregatorServer) createToolHandler(provider api.ToolProvider, toolName string) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		// Extract arguments from MCP request
		args := make(map[string]interface{})
		if req.Params.Arguments != nil {
			if argsMap, ok := req.Params.Arguments.(map[string]interface{}); ok {
				args = argsMap
			}
		}

		// Execute through the provider
		result, err := provider.ExecuteTool(ctx, toolName, args)
		if err != nil {
			logging.Error("AggregatorToolHandler", err, "Tool execution failed for %s with args %+v", toolName, args)
			return mcp.NewToolResultError(fmt.Sprintf("Tool execution failed: %v", err)), nil
		}

		// Convert API result to MCP result
		return convertToMCPResult(result), nil
	}
}

// convertToMCPSchema converts parameter metadata to MCP input schema
func convertToMCPSchema(params []api.ParameterMetadata) mcp.ToolInputSchema {
	properties := make(map[string]interface{})
	required := []string{}

	for _, param := range params {
		propSchema := map[string]interface{}{
			"type":        param.Type,
			"description": param.Description,
		}

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

// convertToMCPResult converts an API CallToolResult to MCP CallToolResult
func convertToMCPResult(result *api.CallToolResult) *mcp.CallToolResult {
	mcpContent := make([]mcp.Content, len(result.Content))

	for i, content := range result.Content {
		if text, ok := content.(string); ok {
			mcpContent[i] = mcp.NewTextContent(text)
		} else {
			// Marshal non-string content to JSON
			jsonBytes, _ := json.Marshal(content)
			mcpContent[i] = mcp.NewTextContent(string(jsonBytes))
		}
	}

	return &mcp.CallToolResult{
		Content: mcpContent,
		IsError: result.IsError,
	}
}
