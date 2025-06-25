package api

import (
	"context"
	"fmt"

	"muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// APIToolCaller implements tool calling interfaces using the API layer
type ToolCaller struct{}

// NewToolCaller creates a new API-based tool caller
func NewToolCaller() *ToolCaller {
	return &ToolCaller{}
}

// AggregatorHandler provides aggregator-specific functionality
type AggregatorHandler interface {
	GetServiceData() map[string]interface{}
	GetEndpoint() string
	GetPort() int

	// Tool calling methods
	CallTool(ctx context.Context, toolName string, args map[string]interface{}) (*CallToolResult, error)
	CallToolInternal(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error)
	IsToolAvailable(toolName string) bool
	GetAvailableTools() []string

	// Capability management
	UpdateCapabilities()
}

// CallTool implements the capability ToolCaller interface
func (atc *ToolCaller) CallTool(ctx context.Context, toolName string, arguments map[string]interface{}) (map[string]interface{}, error) {
	aggregatorHandler := GetAggregator()
	if aggregatorHandler == nil {
		return nil, fmt.Errorf("aggregator handler not available")
	}

	// Check if tool is available before calling
	if !aggregatorHandler.IsToolAvailable(toolName) {
		return nil, fmt.Errorf("tool %s is not available", toolName)
	}

	logging.Debug("APIToolCaller", "Calling tool %s with args: %v", toolName, arguments)

	// Call the tool through the aggregator handler
	result, err := aggregatorHandler.CallTool(ctx, toolName, arguments)
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

// CallToolInternal implements the workflow ToolCaller interface
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

// IsToolAvailable checks if a tool is available through the aggregator
func (atc *ToolCaller) IsToolAvailable(toolName string) bool {
	aggregatorHandler := GetAggregator()
	if aggregatorHandler == nil {
		return false
	}
	return aggregatorHandler.IsToolAvailable(toolName)
}

// GetAvailableTools returns all available tools from the aggregator
func (atc *ToolCaller) GetAvailableTools() []string {
	aggregatorHandler := GetAggregator()
	if aggregatorHandler == nil {
		return []string{}
	}
	return aggregatorHandler.GetAvailableTools()
}

// ToolChecker implements config.ToolAvailabilityChecker using the API layer
type ToolChecker struct{}

// NewToolChecker creates a new API-based tool checker
func NewToolChecker() *ToolChecker {
	return &ToolChecker{}
}

// IsToolAvailable checks if a tool is available using the aggregator API handler
func (atc *ToolChecker) IsToolAvailable(toolName string) bool {
	aggregatorHandler := GetAggregator()
	if aggregatorHandler == nil {
		return false
	}
	return aggregatorHandler.IsToolAvailable(toolName)
}

// GetAvailableTools returns all available tools using the aggregator API handler
func (atc *ToolChecker) GetAvailableTools() []string {
	aggregatorHandler := GetAggregator()
	if aggregatorHandler == nil {
		return []string{}
	}
	return aggregatorHandler.GetAvailableTools()
}
