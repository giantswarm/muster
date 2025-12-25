package aggregator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"muster/pkg/logging"
)

// AuthStatusResourceURI is the URI for the auth status resource.
const AuthStatusResourceURI = "auth://status"

// AuthStatusResponse represents the structured authentication state.
// This is returned by the auth://status resource.
type AuthStatusResponse struct {
	// MusterAuth describes authentication to Muster Server itself
	MusterAuth *MusterAuthStatus `json:"muster_auth"`

	// ServerAuths describes authentication to each remote MCP server
	ServerAuths []ServerAuthStatus `json:"server_auths"`
}

// MusterAuthStatus describes the authentication state for Muster Server.
type MusterAuthStatus struct {
	Authenticated bool   `json:"authenticated"`
	User          string `json:"user,omitempty"`
	Issuer        string `json:"issuer,omitempty"`
}

// ServerAuthStatus describes the authentication state for a remote MCP server.
type ServerAuthStatus struct {
	// ServerName is the name of the MCP server
	ServerName string `json:"server_name"`

	// Status is one of: "connected", "auth_required", "error", "initializing"
	Status string `json:"status"`

	// AuthChallenge is present when Status == "auth_required"
	AuthChallenge *AuthChallengeInfo `json:"auth_challenge,omitempty"`

	// Error is present when Status == "error"
	Error string `json:"error,omitempty"`
}

// AuthChallengeInfo contains information about an authentication challenge.
type AuthChallengeInfo struct {
	// Issuer is the IdP URL that will issue tokens
	Issuer string `json:"issuer"`

	// Scope is the OAuth scope required
	Scope string `json:"scope,omitempty"`

	// AuthToolName is the tool to call for browser-based auth
	AuthToolName string `json:"auth_tool_name"`
}

// createAuthStatusResource creates the auth://status resource and handler.
func (a *AggregatorServer) createAuthStatusResource() mcpserver.ServerResource {
	resource := mcp.Resource{
		URI:         AuthStatusResourceURI,
		Name:        "Authentication Status",
		Description: "Returns the current authentication state for Muster and all remote MCP servers",
	}

	handler := func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return a.handleAuthStatusResource(ctx, req)
	}

	return mcpserver.ServerResource{
		Resource: resource,
		Handler:  handler,
	}
}

// handleAuthStatusResource handles reads of the auth://status resource.
func (a *AggregatorServer) handleAuthStatusResource(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	logging.Debug("Aggregator", "Reading auth://status resource")

	// Build the auth status response
	status := a.buildAuthStatus(ctx)

	// Marshal to JSON
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal auth status: %w", err)
	}

	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      AuthStatusResourceURI,
			MIMEType: "application/json",
			Text:     string(data),
		},
	}, nil
}

// buildAuthStatus constructs the auth status response from current state.
func (a *AggregatorServer) buildAuthStatus(ctx context.Context) *AuthStatusResponse {
	response := &AuthStatusResponse{
		MusterAuth:  a.getMusterAuthStatus(ctx),
		ServerAuths: a.getServerAuthStatuses(ctx),
	}

	return response
}

// getMusterAuthStatus returns the Muster Server authentication status.
func (a *AggregatorServer) getMusterAuthStatus(ctx context.Context) *MusterAuthStatus {
	// If OAuth is not enabled, Muster is always "authenticated" (no auth required)
	if !a.config.OAuth.Enabled {
		return &MusterAuthStatus{
			Authenticated: true,
		}
	}

	// When OAuth is enabled, the request has already been authenticated
	// by the OAuth middleware, so we're authenticated if we got here
	return &MusterAuthStatus{
		Authenticated: true,
		// TODO: Extract user info from context if available
	}
}

// getServerAuthStatuses returns auth status for all registered MCP servers.
func (a *AggregatorServer) getServerAuthStatuses(ctx context.Context) []ServerAuthStatus {
	var statuses []ServerAuthStatus

	// Get all servers from registry
	servers := a.registry.GetAllServers()

	for serverName, info := range servers {
		status := ServerAuthStatus{
			ServerName: serverName,
		}

		switch info.Status {
		case StatusConnected:
			status.Status = "connected"

		case StatusAuthRequired:
			status.Status = "auth_required"
			// Extract auth challenge info
			if info.AuthInfo != nil {
				status.AuthChallenge = &AuthChallengeInfo{
					Issuer:       info.AuthInfo.Issuer,
					Scope:        info.AuthInfo.Scope,
					AuthToolName: a.registry.nameTracker.GetExposedToolName(serverName, "authenticate"),
				}
			}

		case StatusDisconnected:
			status.Status = "disconnected"

		default:
			status.Status = "unknown"
		}

		statuses = append(statuses, status)
	}

	return statuses
}

// registerAuthStatusResource adds the auth://status resource to the MCP server.
// This should be called during server initialization.
func (a *AggregatorServer) registerAuthStatusResource() {
	resource := a.createAuthStatusResource()
	a.mcpServer.AddResources(resource)
	logging.Info("Aggregator", "Registered auth://status resource")
}
