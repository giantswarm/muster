package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/giantswarm/muster/internal/agent/oauth"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// PendingAuthMCPServer is an MCP server that exposes only the authenticate_muster tool.
// This is used when the Agent cannot connect to the Muster Server because it requires
// OAuth authentication. The server provides a synthetic tool that initiates the auth flow.
//
// Once authentication is complete, the caller should replace this server with the
// full MCPServer that exposes all tools.
type PendingAuthMCPServer struct {
	logger      *Logger
	mcpServer   *server.MCPServer
	authManager *oauth.AuthManager
	serverURL   string
	authURL     string
}

// NewPendingAuthMCPServer creates a new MCP server in pending auth state.
// It exposes only the authenticate_muster synthetic tool.
func NewPendingAuthMCPServer(logger *Logger, authManager *oauth.AuthManager, serverURL string) (*PendingAuthMCPServer, error) {
	// Create MCP server with minimal capabilities
	mcpServer := server.NewMCPServer(
		"muster-agent",
		"1.0.0",
		server.WithToolCapabilities(true), // Enable notifications for tool list changes
	)

	ps := &PendingAuthMCPServer{
		logger:      logger,
		mcpServer:   mcpServer,
		authManager: authManager,
		serverURL:   serverURL,
	}

	// Register the synthetic authenticate_muster tool
	ps.registerAuthenticateTool()

	return ps, nil
}

// registerAuthenticateTool registers the synthetic authenticate_muster tool.
func (p *PendingAuthMCPServer) registerAuthenticateTool() {
	authTool := mcp.NewTool("authenticate_muster",
		mcp.WithDescription("Authenticate with the Muster Server. "+
			"Call this tool to get an authentication URL. "+
			"Open the URL in your browser to complete sign-in, then Muster will automatically connect."),
	)

	p.mcpServer.AddTool(authTool, p.handleAuthenticate)
}

// handleAuthenticate handles the authenticate_muster tool call.
// It initiates the OAuth flow and returns the authorization URL.
func (p *PendingAuthMCPServer) handleAuthenticate(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Start the auth flow
	authURL, err := p.authManager.StartAuthFlow(ctx)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to start authentication: %v", err)), nil
	}

	p.authURL = authURL

	// Try to open the browser automatically
	browserOpened := false
	if err := oauth.OpenBrowser(authURL); err == nil {
		browserOpened = true
		if p.logger != nil {
			p.logger.Info("Opened browser for authentication")
		}
	}

	// Build a user-friendly message with clickable markdown link
	var message string
	if browserOpened {
		message = fmt.Sprintf(
			"Your browser has been opened for authentication.\n\n"+
				"If it didn't open, click here: [Authenticate with Muster](%s)\n\n"+
				"After signing in, the muster tools will become available automatically.",
			authURL,
		)
	} else {
		message = fmt.Sprintf(
			"Please authenticate by clicking this link: [Authenticate with Muster](%s)\n\n"+
				"After signing in, the muster tools will become available automatically.",
			authURL,
		)
	}

	// Return the auth URL to the user
	response := AuthenticateResponse{
		Status:       "auth_required",
		AuthURL:      authURL,
		ClickableURL: fmt.Sprintf("[Authenticate with Muster](%s)", authURL),
		Message:      message,
	}

	jsonBytes, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to format response: %v", err)), nil
	}

	result := mcp.NewToolResultText(string(jsonBytes))

	// Start waiting for auth completion in background
	go p.waitForAuthCompletion(ctx)

	return result, nil
}

// AuthenticateResponse is the structured response from the authenticate_muster tool.
type AuthenticateResponse struct {
	Status       string `json:"status"`
	AuthURL      string `json:"auth_url"`
	ClickableURL string `json:"clickable_url"`
	Message      string `json:"message"`
}

// waitForAuthCompletion waits for the OAuth callback and notifies completion.
func (p *PendingAuthMCPServer) waitForAuthCompletion(ctx context.Context) {
	err := p.authManager.WaitForAuth(ctx)
	if err != nil {
		if p.logger != nil {
			p.logger.Error("Authentication failed: %v", err)
		}
		return
	}

	if p.logger != nil {
		p.logger.Success("Authentication successful! Reconnecting to Muster Server...")
	}

	// The parent code should detect the auth state change and reconnect
}

// Start starts the MCP server using stdio transport.
func (p *PendingAuthMCPServer) Start(ctx context.Context) error {
	return server.ServeStdio(p.mcpServer)
}

// GetMCPServer returns the underlying MCP server.
// This is used to send notifications when auth completes.
func (p *PendingAuthMCPServer) GetMCPServer() *server.MCPServer {
	return p.mcpServer
}

// IsAuthComplete returns true if authentication has completed successfully.
func (p *PendingAuthMCPServer) IsAuthComplete() bool {
	return p.authManager.GetState() == oauth.AuthStateAuthenticated
}
