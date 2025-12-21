package aggregator

import (
	"context"
	"fmt"

	"muster/internal/aggregator"
	"muster/internal/api"

	"github.com/mark3labs/mcp-go/mcp"
)

// APIAdapter adapts the AggregatorService to implement api.AggregatorHandler
type APIAdapter struct {
	service *AggregatorService
}

// NewAPIAdapter creates a new aggregator API adapter
func NewAPIAdapter(s *AggregatorService) *APIAdapter {
	return &APIAdapter{service: s}
}

// GetServiceData returns aggregator service data
func (a *APIAdapter) GetServiceData() map[string]interface{} {
	if a.service == nil {
		return nil
	}
	return a.service.GetServiceData()
}

// GetEndpoint returns the aggregator's SSE endpoint URL
func (a *APIAdapter) GetEndpoint() string {
	if a.service == nil {
		return ""
	}
	return a.service.GetEndpoint()
}

// GetPort returns the aggregator port
func (a *APIAdapter) GetPort() int {
	if a.service == nil {
		return 0
	}
	// Extract port from service data
	data := a.service.GetServiceData()
	if port, ok := data["port"].(int); ok {
		return port
	}
	return 0
}

// CallTool calls a tool and returns the result in API format
func (a *APIAdapter) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	if a.service == nil {
		return nil, fmt.Errorf("aggregator service not available")
	}

	manager := a.service.GetManager()
	if manager == nil {
		return nil, fmt.Errorf("aggregator manager not available")
	}

	server := manager.GetAggregatorServer()
	if server == nil {
		return nil, fmt.Errorf("aggregator server not available")
	}

	// Call the tool through the aggregator server
	result, err := server.CallToolInternal(ctx, toolName, args)
	if err != nil {
		return nil, err
	}

	// Convert MCP result to API result
	content := make([]interface{}, len(result.Content))
	for i, c := range result.Content {
		if textContent, ok := c.(mcp.TextContent); ok {
			content[i] = textContent.Text
		} else {
			content[i] = c
		}
	}

	return &api.CallToolResult{
		Content: content,
		IsError: result.IsError,
	}, nil
}

// CallToolInternal calls a tool and returns the raw MCP result
func (a *APIAdapter) CallToolInternal(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	if a.service == nil {
		return nil, fmt.Errorf("aggregator service not available")
	}

	manager := a.service.GetManager()
	if manager == nil {
		return nil, fmt.Errorf("aggregator manager not available")
	}

	server := manager.GetAggregatorServer()
	if server == nil {
		return nil, fmt.Errorf("aggregator server not available")
	}

	// Delegate directly to the aggregator server
	return server.CallToolInternal(ctx, toolName, args)
}

// IsToolAvailable checks if a tool is available
func (a *APIAdapter) IsToolAvailable(toolName string) bool {
	if a.service == nil {
		return false
	}

	manager := a.service.GetManager()
	if manager == nil {
		return false
	}

	server := manager.GetAggregatorServer()
	if server == nil {
		return false
	}

	return server.IsToolAvailable(toolName)
}

// GetAvailableTools returns all available tools
func (a *APIAdapter) GetAvailableTools() []string {
	if a.service == nil {
		return []string{}
	}

	manager := a.service.GetManager()
	if manager == nil {
		return []string{}
	}

	server := manager.GetAggregatorServer()
	if server == nil {
		return []string{}
	}

	return server.GetAvailableTools()
}

// UpdateCapabilities updates the aggregator's capabilities
func (a *APIAdapter) UpdateCapabilities() {
	if a.service == nil {
		return
	}

	manager := a.service.GetManager()
	if manager == nil {
		return
	}

	server := manager.GetAggregatorServer()
	if server == nil {
		return
	}

	server.UpdateCapabilities()
}

// RegisterServerPendingAuth registers a server that requires OAuth authentication
func (a *APIAdapter) RegisterServerPendingAuth(serverName, url, toolPrefix string, authInfo *api.AuthInfo) error {
	if a.service == nil {
		return fmt.Errorf("aggregator service not available")
	}

	manager := a.service.GetManager()
	if manager == nil {
		return fmt.Errorf("aggregator manager not available")
	}

	// Convert api.AuthInfo to aggregator.AuthInfo
	aggAuthInfo := &aggregator.AuthInfo{
		Issuer:              authInfo.Issuer,
		Scope:               authInfo.Scope,
		ResourceMetadataURL: authInfo.ResourceMetadataURL,
	}

	return manager.RegisterServerPendingAuth(serverName, url, toolPrefix, aggAuthInfo)
}

// Register registers this adapter with the API package
func (a *APIAdapter) Register() {
	api.RegisterAggregator(a)
}
