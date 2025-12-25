package aggregator

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	internalmcp "muster/internal/mcpserver"
	"muster/pkg/logging"
)

// SubmitAuthTokenToolName is the name of the submit_auth_token tool.
const SubmitAuthTokenToolName = "submit_auth_token"

// createSubmitAuthTokenTool creates the submit_auth_token tool for SSO token forwarding.
// This tool allows the agent to submit access tokens for servers that require authentication.
// It enables SSO by allowing tokens obtained for one server to be forwarded to another
// server that uses the same identity provider (issuer).
func (a *AggregatorServer) createSubmitAuthTokenTool() mcpserver.ServerTool {
	return mcpserver.ServerTool{
		Tool: mcp.Tool{
			Name:        SubmitAuthTokenToolName,
			Description: "Submit an OAuth access token for a server that requires authentication. This enables SSO by allowing tokens to be forwarded to servers sharing the same identity provider.",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]interface{}{
					"server_name": map[string]interface{}{
						"type":        "string",
						"description": "The name of the MCP server to authenticate to",
					},
					"access_token": map[string]interface{}{
						"type":        "string",
						"description": "The OAuth access token to use for authentication",
					},
				},
				Required: []string{"server_name", "access_token"},
			},
		},
		Handler: a.handleSubmitAuthToken,
	}
}

// handleSubmitAuthToken handles the submit_auth_token tool call.
func (a *AggregatorServer) handleSubmitAuthToken(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Extract arguments
	args, ok := req.Params.Arguments.(map[string]interface{})
	if !ok {
		return mcp.NewToolResultError("Invalid arguments format"), nil
	}

	serverName, ok := args["server_name"].(string)
	if !ok || serverName == "" {
		return mcp.NewToolResultError("server_name is required"), nil
	}

	accessToken, ok := args["access_token"].(string)
	if !ok || accessToken == "" {
		return mcp.NewToolResultError("access_token is required"), nil
	}

	logging.Debug("Aggregator", "Received submit_auth_token for server: %s", serverName)

	// Check if the server exists and requires authentication
	serverInfo, exists := a.registry.GetServerInfo(serverName)
	if !exists || serverInfo == nil {
		return mcp.NewToolResultError(fmt.Sprintf("Server '%s' not found", serverName)), nil
	}

	if serverInfo.Status != StatusAuthRequired {
		return mcp.NewToolResultError(fmt.Sprintf("Server '%s' does not require authentication (status: %s)", serverName, serverInfo.Status)), nil
	}

	// Get session ID from context for session-scoped connection
	sessionID := getSessionIDFromContext(ctx)
	if sessionID == "" {
		// Fall back to global upgrade if no session ID
		return a.handleGlobalTokenSubmit(ctx, serverName, serverInfo.URL, accessToken)
	}

	// Try to connect with the token for this session
	result, err := a.tryConnectWithToken(ctx, sessionID, serverName, serverInfo.URL, accessToken)
	if err != nil {
		logging.Warn("Aggregator", "Failed to connect with submitted token: server=%s error=%v", serverName, err)
		return mcp.NewToolResultError(fmt.Sprintf("Token accepted but connection failed: %v", err)), nil
	}

	logging.Info("Aggregator", "Successfully authenticated to server %s via SSO", serverName)
	return result, nil
}

// handleGlobalTokenSubmit handles token submission when there's no session context.
// This upgrades the server's global connection state.
func (a *AggregatorServer) handleGlobalTokenSubmit(ctx context.Context, serverName, serverURL, accessToken string) (*mcp.CallToolResult, error) {
	// Create an authenticated MCP client with the token
	headers := map[string]string{
		"Authorization": "Bearer " + accessToken,
	}

	client := internalmcp.NewStreamableHTTPClientWithHeaders(serverURL, headers)

	// Try to initialize the client
	if err := client.Initialize(ctx); err != nil {
		client.Close()
		return mcp.NewToolResultError(fmt.Sprintf("Failed to initialize connection: %v", err)), nil
	}

	// Upgrade the server to connected status
	if err := a.registry.UpgradeToConnected(ctx, serverName, client); err != nil {
		client.Close()
		return mcp.NewToolResultError(fmt.Sprintf("Failed to upgrade server connection: %v", err)), nil
	}

	// Trigger capability update
	a.updateCapabilities()

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(fmt.Sprintf("Successfully authenticated to %s", serverName)),
		},
	}, nil
}

// registerSubmitAuthTokenTool registers the submit_auth_token tool with the MCP server.
// This should be called during server initialization.
func (a *AggregatorServer) registerSubmitAuthTokenTool() {
	tool := a.createSubmitAuthTokenTool()
	a.mcpServer.AddTools(tool)
	a.toolManager.setActive(SubmitAuthTokenToolName, true)
	logging.Info("Aggregator", "Registered submit_auth_token tool")
}
