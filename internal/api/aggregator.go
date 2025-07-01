package api

import (
	"context"
	"fmt"

	"muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// ToolCaller implements tool calling interfaces using the API layer.
// It serves as an adapter that allows different subsystems (capabilities, workflows)
// to call tools through the aggregator without directly coupling to the aggregator implementation.
// This follows the service locator pattern to maintain architectural boundaries.
type ToolCaller struct{}

// NewToolCaller creates a new API-based tool caller instance.
// The returned ToolCaller uses the API service locator pattern to access
// aggregator functionality without direct coupling to implementations.
//
// Returns:
//   - *ToolCaller: A new tool caller instance
func NewToolCaller() *ToolCaller {
	return &ToolCaller{}
}

// AggregatorHandler provides aggregator-specific functionality for managing MCP server tools
// and capabilities. It acts as the central coordinator for tool availability, execution,
// and capability management across all registered MCP servers.
type AggregatorHandler interface {
	// GetServiceData returns runtime data and configuration information about the aggregator service.
	// This includes connection details, registered servers, and operational metrics.
	//
	// Returns:
	//   - map[string]interface{}: Service-specific data and metadata
	GetServiceData() map[string]interface{}

	// GetEndpoint returns the network endpoint where the aggregator is accessible.
	// This is typically used for client connections and service discovery.
	//
	// Returns:
	//   - string: The network endpoint (e.g., "localhost" or IP address)
	GetEndpoint() string

	// GetPort returns the network port where the aggregator is listening.
	// This is used in conjunction with GetEndpoint for complete connection information.
	//
	// Returns:
	//   - int: The port number where the aggregator service is accessible
	GetPort() int

	// CallTool executes a tool through the aggregator and returns a structured result.
	// This method provides the primary interface for external tool execution with
	// result formatting suitable for API responses.
	//
	// Args:
	//   - ctx: Context for request cancellation and timeout control
	//   - toolName: The name of the tool to execute
	//   - args: Arguments to pass to the tool as key-value pairs
	//
	// Returns:
	//   - *CallToolResult: Structured result containing tool output and metadata
	//   - error: nil on success, or an error if the tool call fails
	CallTool(ctx context.Context, toolName string, args map[string]interface{}) (*CallToolResult, error)

	// CallToolInternal executes a tool and returns the raw MCP result.
	// This method is used internally by workflow and other subsystems that need
	// direct access to the underlying MCP tool result format.
	//
	// Args:
	//   - ctx: Context for request cancellation and timeout control
	//   - toolName: The name of the tool to execute
	//   - args: Arguments to pass to the tool as key-value pairs
	//
	// Returns:
	//   - *mcp.CallToolResult: Raw MCP tool result
	//   - error: nil on success, or an error if the tool call fails
	CallToolInternal(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error)

	// IsToolAvailable checks whether a specific tool is currently available for execution.
	// This is used for validation before attempting tool calls and for capability reporting.
	//
	// Args:
	//   - toolName: The name of the tool to check
	//
	// Returns:
	//   - bool: true if the tool is available, false otherwise
	IsToolAvailable(toolName string) bool

	// GetAvailableTools returns a list of all currently available tool names.
	// This is used for tool discovery, validation, and capability reporting.
	//
	// Returns:
	//   - []string: Slice of available tool names (empty if no tools available)
	GetAvailableTools() []string

	// UpdateCapabilities triggers a refresh of the aggregator's capability information.
	// This should be called when MCP servers are added, removed, or their tools change.
	UpdateCapabilities()
}

// CallTool implements the capability ToolCaller interface by delegating to the aggregator handler.
// It provides a standardized way for capabilities to execute tools with proper error handling
// and result formatting. The method converts the aggregator's result format to the format
// expected by the capability system.
func (atc *ToolCaller) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (map[string]interface{}, error) {
	aggregatorHandler := GetAggregator()
	if aggregatorHandler == nil {
		return nil, fmt.Errorf("aggregator handler not available")
	}

	// Check if tool is available before calling
	if !aggregatorHandler.IsToolAvailable(toolName) {
		return nil, fmt.Errorf("tool %s is not available", toolName)
	}

	logging.Debug("APIToolCaller", "Calling tool %s with args: %v", toolName, args)

	// Call the tool through the aggregator handler
	result, err := aggregatorHandler.CallTool(ctx, toolName, args)
	if err != nil {
		logging.Error("APIToolCaller", err, "Failed to call tool %s", toolName)
		return nil, fmt.Errorf("failed to call tool %s: %w", toolName, err)
	}

	if result == nil {
		return nil, fmt.Errorf("tool %s returned nil result", toolName)
	}

	// Convert CallToolResult to map format for capability interface
	responseData := make(map[string]interface{})

	// Handle different types of content
	for i, content := range result.Content {
		if text, ok := content.(string); ok {
			if i == 0 {
				responseData["text"] = text
			} else {
				responseData[fmt.Sprintf("text_%d", i)] = text
			}
		} else {
			// Handle other content types as generic data
			if i == 0 {
				responseData["content"] = content
			} else {
				responseData[fmt.Sprintf("content_%d", i)] = content
			}
		}
	}

	// Add success indicator
	responseData["success"] = !result.IsError

	logging.Debug("APIToolCaller", "Tool %s call completed successfully", toolName)
	return responseData, nil
}

// CallToolInternal implements the workflow ToolCaller interface by delegating to the aggregator handler.
// This method provides direct access to the raw MCP tool result format, which is used by
// workflow execution and other internal subsystems that need unmodified tool results.
func (atc *ToolCaller) CallToolInternal(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	aggregatorHandler := GetAggregator()
	if aggregatorHandler == nil {
		return nil, fmt.Errorf("aggregator handler not available")
	}

	// Check if tool is available before calling
	if !aggregatorHandler.IsToolAvailable(toolName) {
		return nil, fmt.Errorf("tool %s is not available", toolName)
	}

	logging.Debug("APIToolCaller", "Calling tool %s internally with args: %v", toolName, args)

	// Delegate directly to the aggregator handler
	result, err := aggregatorHandler.CallToolInternal(ctx, toolName, args)
	if err != nil {
		logging.Error("APIToolCaller", err, "Failed to call tool %s internally", toolName)
		return nil, fmt.Errorf("failed to call tool %s: %w", toolName, err)
	}

	logging.Debug("APIToolCaller", "Tool %s internal call completed successfully", toolName)
	return result, nil
}

// IsToolAvailable checks if a tool is available through the aggregator.
// This method provides a convenient way to validate tool availability without
// attempting to execute the tool, which is useful for validation and UI purposes.
func (atc *ToolCaller) IsToolAvailable(toolName string) bool {
	aggregatorHandler := GetAggregator()
	if aggregatorHandler == nil {
		return false
	}
	return aggregatorHandler.IsToolAvailable(toolName)
}

// GetAvailableTools returns all available tools from the aggregator.
// This method provides tool discovery functionality for clients that need to
// understand what tools are currently available in the system.
func (atc *ToolCaller) GetAvailableTools() []string {
	aggregatorHandler := GetAggregator()
	if aggregatorHandler == nil {
		return []string{}
	}
	return aggregatorHandler.GetAvailableTools()
}

// ToolChecker implements config.ToolAvailabilityChecker using the API layer.
// It provides a way for the configuration system to validate tool availability
// without direct coupling to the aggregator implementation. This is particularly
// useful for ServiceClass validation where tools need to be checked before
// service instances can be created.
type ToolChecker struct{}

// NewToolChecker creates a new API-based tool checker instance.
// The returned ToolChecker uses the API service locator pattern to access
// aggregator functionality for tool availability checking.
//
// Returns:
//   - *ToolChecker: A new tool checker instance
func NewToolChecker() *ToolChecker {
	return &ToolChecker{}
}

// IsToolAvailable checks if a tool is available using the aggregator API handler.
// This method provides the config package with a way to validate tool availability
// during configuration loading and validation without creating tight coupling.
func (atc *ToolChecker) IsToolAvailable(toolName string) bool {
	aggregatorHandler := GetAggregator()
	if aggregatorHandler == nil {
		return false
	}
	return aggregatorHandler.IsToolAvailable(toolName)
}

// GetAvailableTools returns all available tools using the aggregator API handler.
// This method enables the config package to perform comprehensive tool availability
// checks and provide detailed validation feedback.
func (atc *ToolChecker) GetAvailableTools() []string {
	aggregatorHandler := GetAggregator()
	if aggregatorHandler == nil {
		return []string{}
	}
	return aggregatorHandler.GetAvailableTools()
}
