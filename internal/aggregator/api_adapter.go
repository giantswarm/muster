package aggregator

import (
	"context"
	"fmt"

	"github.com/giantswarm/muster/internal/api"

	"github.com/mark3labs/mcp-go/mcp"
)

// APIAdapter exposes AggregatorManager through api.AggregatorHandler so other
// internal packages can reach the aggregator without importing this one.
type APIAdapter struct {
	manager *AggregatorManager
}

// NewAPIAdapter wraps manager. manager must be non-nil.
func NewAPIAdapter(manager *AggregatorManager) *APIAdapter {
	return &APIAdapter{manager: manager}
}

// Register publishes the adapter through internal/api so consumers reach it
// via api.GetAggregator().
func (a *APIAdapter) Register() {
	api.RegisterAggregator(a)
}

// GetServiceData reports the manager's current runtime state.
func (a *APIAdapter) GetServiceData() map[string]interface{} {
	if a.manager == nil {
		return nil
	}
	return a.manager.GetServiceData()
}

// GetEndpoint returns the aggregator's MCP endpoint URL.
func (a *APIAdapter) GetEndpoint() string {
	if a.manager == nil {
		return ""
	}
	return a.manager.GetEndpoint()
}

// GetPort returns the aggregator's listen port.
func (a *APIAdapter) GetPort() int {
	if a.manager == nil {
		return 0
	}
	if port, ok := a.manager.GetServiceData()["port"].(int); ok {
		return port
	}
	return 0
}

// CallTool routes a tool call through the underlying aggregator server,
// converting the MCP result to the API-layer envelope.
func (a *APIAdapter) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	server, err := a.server()
	if err != nil {
		return nil, err
	}
	result, err := server.CallToolInternal(ctx, toolName, args)
	if err != nil {
		return nil, err
	}
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

// CallToolInternal returns the raw MCP result, used by workflow execution.
func (a *APIAdapter) CallToolInternal(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	server, err := a.server()
	if err != nil {
		return nil, err
	}
	return server.CallToolInternal(ctx, toolName, args)
}

// IsToolAvailable reports whether the named tool is currently exposed.
func (a *APIAdapter) IsToolAvailable(toolName string) bool {
	server, err := a.server()
	if err != nil {
		return false
	}
	return server.IsToolAvailable(toolName)
}

// GetAvailableTools lists every currently exposed tool.
func (a *APIAdapter) GetAvailableTools() []string {
	server, err := a.server()
	if err != nil {
		return []string{}
	}
	return server.GetAvailableTools()
}

// UpdateCapabilities forces the aggregator to refresh its capability set.
func (a *APIAdapter) UpdateCapabilities() {
	server, err := a.server()
	if err != nil {
		return
	}
	server.UpdateCapabilities()
}

// RegisterServerPendingAuth registers a server requiring OAuth with no
// additional auth configuration.
func (a *APIAdapter) RegisterServerPendingAuth(serverName, url, toolPrefix string, authInfo *api.AuthInfo) error {
	return a.RegisterServerPendingAuthWithConfig(serverName, url, toolPrefix, authInfo, nil)
}

// RegisterServerPendingAuthWithConfig registers an OAuth-protected server
// and stores its auth configuration for later token forwarding / exchange.
func (a *APIAdapter) RegisterServerPendingAuthWithConfig(serverName, url, toolPrefix string, authInfo *api.AuthInfo, authConfig *api.MCPServerAuth) error {
	if a.manager == nil {
		return fmt.Errorf("aggregator manager not available")
	}
	aggAuthInfo := &AuthInfo{
		Issuer:              authInfo.Issuer,
		Scope:               authInfo.Scope,
		ResourceMetadataURL: authInfo.ResourceMetadataURL,
	}
	return a.manager.RegisterServerPendingAuthWithConfig(serverName, url, toolPrefix, aggAuthInfo, authConfig)
}

// RegisterUpstream forwards to AggregatorManager.RegisterUpstream.
func (a *APIAdapter) RegisterUpstream(ctx context.Context, name string) error {
	if a.manager == nil {
		return fmt.Errorf("aggregator manager not available")
	}
	return a.manager.RegisterUpstream(ctx, name)
}

// DeregisterUpstream forwards to AggregatorManager.DeregisterUpstream.
func (a *APIAdapter) DeregisterUpstream(ctx context.Context, name string) error {
	if a.manager == nil {
		return nil
	}
	return a.manager.DeregisterUpstream(ctx, name)
}

// UpstreamServerState forwards to AggregatorManager.UpstreamServerState.
func (a *APIAdapter) UpstreamServerState(name string) api.UpstreamServerState {
	if a.manager == nil {
		return api.UpstreamServerAbsent
	}
	return a.manager.UpstreamServerState(name)
}

// UpstreamServerStateForSession forwards to AggregatorManager.UpstreamServerStateForSession.
func (a *APIAdapter) UpstreamServerStateForSession(ctx context.Context, name string) api.UpstreamServerState {
	if a.manager == nil {
		return api.UpstreamServerAbsent
	}
	return a.manager.UpstreamServerStateForSession(ctx, name)
}

func (a *APIAdapter) server() (*AggregatorServer, error) {
	if a.manager == nil {
		return nil, fmt.Errorf("aggregator manager not available")
	}
	server := a.manager.GetAggregatorServer()
	if server == nil {
		return nil, fmt.Errorf("aggregator server not available")
	}
	return server, nil
}
