package aggregator

import (
	"context"
	"encoding/json"

	"muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// AuthStatusResourceURI is the URI for the auth status MCP resource.
// This resource provides real-time authentication status for all MCP servers.
const AuthStatusResourceURI = "auth://status"

// AuthStatusResponse is the structured response from the auth://status resource.
// It provides the AI with complete information about which servers need authentication
// and includes SSO hints through the issuer field.
type AuthStatusResponse struct {
	Servers []ServerAuthStatus `json:"servers"`
}

// ServerAuthStatus represents the authentication status of a single MCP server.
// The issuer field enables SSO detection - servers with the same issuer can share auth.
type ServerAuthStatus struct {
	Name     string `json:"name"`
	Status   string `json:"status"` // "connected", "auth_required", "error"
	Issuer   string `json:"issuer,omitempty"`
	Scope    string `json:"scope,omitempty"`
	AuthTool string `json:"auth_tool,omitempty"`
	Error    string `json:"error,omitempty"`
}

// registerAuthStatusResource registers the auth://status resource with the MCP server.
// This resource is polled by the agent to get current auth state for all servers.
func (a *AggregatorServer) registerAuthStatusResource() {
	a.mu.RLock()
	mcpServer := a.mcpServer
	a.mu.RUnlock()

	if mcpServer == nil {
		logging.Warn("Aggregator", "Cannot register auth status resource: MCP server not initialized")
		return
	}

	// Create the resource
	resource := mcp.NewResource(
		AuthStatusResourceURI,
		"Authentication status for all MCP servers. Provides information about which servers require authentication and their OAuth issuer URLs for SSO detection.",
	)

	// Add the resource with its handler
	mcpServer.AddResource(resource, a.handleAuthStatusResource)
	logging.Info("Aggregator", "Registered auth://status resource")
}

// handleAuthStatusResource handles requests for the auth://status resource.
// It returns the authentication status of all registered MCP servers.
func (a *AggregatorServer) handleAuthStatusResource(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	servers := a.registry.GetAllServers()
	response := AuthStatusResponse{Servers: make([]ServerAuthStatus, 0, len(servers))}

	for name, info := range servers {
		status := ServerAuthStatus{
			Name:   name,
			Status: string(info.Status),
		}

		// For servers requiring auth, include issuer and auth tool info
		if info.Status == StatusAuthRequired && info.AuthInfo != nil {
			status.Issuer = info.AuthInfo.Issuer
			status.Scope = info.AuthInfo.Scope
			// The auth tool name follows the pattern: x_<server>_authenticate
			status.AuthTool = a.registry.nameTracker.GetExposedToolName(name, "authenticate")
		} else if info.IsConnected() {
			status.Status = "connected"
		} else {
			status.Status = "disconnected"
		}

		response.Servers = append(response.Servers, status)
	}

	data, err := json.Marshal(response)
	if err != nil {
		return nil, err
	}

	logging.Debug("Aggregator", "Returning auth status for %d servers", len(response.Servers))

	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      AuthStatusResourceURI,
			MIMEType: "application/json",
			Text:     string(data),
		},
	}, nil
}
