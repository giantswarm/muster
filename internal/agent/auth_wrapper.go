package agent

import (
	"fmt"
	"strings"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/mark3labs/mcp-go/mcp"
)

// AuthMetaKey is the namespaced key for auth metadata in MCP _meta field.
// Following MCP specification for custom metadata keys.
const AuthMetaKey = "giantswarm.io/auth_required"

// wrapToolResultWithAuth wraps a tool result with authentication status metadata.
// This implements ADR-008: Explicit Authentication State.
//
// The wrapper:
//  1. Adds a human-readable notification to the response content
//  2. Adds structured data in _meta for programmatic use
//
// If no servers require authentication, the original result is returned unchanged.
func (m *MCPServer) wrapToolResultWithAuth(result *mcp.CallToolResult) *mcp.CallToolResult {
	if m.authPoller == nil {
		return result
	}

	authRequired := m.authPoller.GetAuthRequired()
	if len(authRequired) == 0 {
		return result
	}

	// Add human-readable notification to content
	notification := buildAuthNotification(authRequired)
	result.Content = append(result.Content, mcp.TextContent{
		Type: "text",
		Text: notification,
	})

	// Add structured data in _meta using AdditionalFields
	if result.Meta == nil {
		result.Meta = &mcp.Meta{}
	}
	if result.Meta.AdditionalFields == nil {
		result.Meta.AdditionalFields = make(map[string]any)
	}
	result.Meta.AdditionalFields[AuthMetaKey] = authRequired

	return result
}

// buildAuthNotification creates a human-readable notification about auth requirements.
// It includes SSO hints when multiple servers share the same issuer.
func buildAuthNotification(authRequired []pkgoauth.AuthRequiredInfo) string {
	var sb strings.Builder
	sb.WriteString("\n---\n")
	sb.WriteString("Authentication Required:\n")

	// Group by issuer for SSO hints
	issuerServers := make(map[string][]string)
	for _, auth := range authRequired {
		if auth.Issuer != "" {
			issuerServers[auth.Issuer] = append(issuerServers[auth.Issuer], auth.Server)
		}
	}

	// List each server requiring auth
	// Per ADR-008: Use core_auth_login with server parameter instead of per-server tools
	for _, auth := range authRequired {
		sb.WriteString(fmt.Sprintf("- %s: call 'core_auth_login' with server='%s' to sign in\n", auth.Server, auth.Server))
	}

	// Add SSO hints for servers sharing the same issuer
	for issuer, servers := range issuerServers {
		if len(servers) > 1 {
			sb.WriteString(fmt.Sprintf("\nNote: %s use the same identity provider (%s). ",
				strings.Join(servers, " and "), issuer))
			sb.WriteString("Signing in to one will authenticate all of them.\n")
		}
	}

	return sb.String()
}
