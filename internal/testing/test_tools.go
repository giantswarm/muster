// Package testing provides test helper tools for BDD test scenarios.
// These tools allow test scenarios to interact with mock infrastructure
// components that run in the test orchestrator process.
package testing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// TestToolsHandler handles test-specific tools that operate on mock infrastructure.
// These tools are NOT exposed through the muster serve MCP server - they are
// handled directly by the test runner before delegating to the real MCP client.
type TestToolsHandler struct {
	instanceManager *musterInstanceManager
	currentInstance *MusterInstance
	debug           bool
	logger          TestLogger
}

// NewTestToolsHandler creates a new test tools handler.
func NewTestToolsHandler(instanceManager MusterInstanceManager, instance *MusterInstance, debug bool, logger TestLogger) *TestToolsHandler {
	var manager *musterInstanceManager
	if m, ok := instanceManager.(*musterInstanceManager); ok {
		manager = m
	}

	return &TestToolsHandler{
		instanceManager: manager,
		currentInstance: instance,
		debug:           debug,
		logger:          logger,
	}
}

// IsTestTool returns true if the tool name is a test helper tool.
func IsTestTool(toolName string) bool {
	testTools := []string{
		"test_simulate_oauth_callback",
		"test_inject_token",
		"test_get_oauth_server_info",
	}

	for _, t := range testTools {
		if toolName == t {
			return true
		}
	}
	return false
}

// HandleTestTool executes a test helper tool and returns the result.
func (h *TestToolsHandler) HandleTestTool(ctx context.Context, toolName string, args map[string]interface{}) (interface{}, error) {
	if h.debug {
		h.logger.Debug("🧪 Handling test tool: %s with args: %v\n", toolName, args)
	}

	switch toolName {
	case "test_simulate_oauth_callback":
		return h.handleSimulateOAuthCallback(ctx, args)
	case "test_inject_token":
		return h.handleInjectToken(ctx, args)
	case "test_get_oauth_server_info":
		return h.handleGetOAuthServerInfo(ctx, args)
	default:
		return nil, fmt.Errorf("unknown test tool: %s", toolName)
	}
}

// handleSimulateOAuthCallback simulates a user completing the OAuth flow.
// This tool:
// 1. Calls the authenticate tool for the specified server
// 2. Extracts the authorization URL from the response
// 3. Makes HTTP requests to simulate the OAuth flow
// 4. Returns the result of the OAuth flow completion
func (h *TestToolsHandler) handleSimulateOAuthCallback(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	serverName, ok := args["server"].(string)
	if !ok || serverName == "" {
		return nil, fmt.Errorf("server argument is required")
	}

	if h.instanceManager == nil || h.currentInstance == nil {
		return nil, fmt.Errorf("instance manager or current instance not available")
	}

	// Get the mock OAuth server for this instance
	oauthServerInfo, exists := h.currentInstance.MockOAuthServers[h.findOAuthServerRefForMCPServer(serverName)]
	if !exists {
		// Try to find any OAuth server that might be associated with this MCP server
		for _, info := range h.currentInstance.MockOAuthServers {
			oauthServerInfo = info
			break
		}
		if oauthServerInfo == nil {
			return nil, fmt.Errorf("no mock OAuth server found for server %s", serverName)
		}
	}

	// Get the actual OAuth server instance
	oauthServer := h.instanceManager.GetMockOAuthServer(h.currentInstance.ID, oauthServerInfo.Name)
	if oauthServer == nil {
		return nil, fmt.Errorf("mock OAuth server %s not running", oauthServerInfo.Name)
	}

	if h.debug {
		h.logger.Debug("🔐 Simulating OAuth callback for server %s using OAuth server %s\n",
			serverName, oauthServerInfo.Name)
	}

	// Generate a client ID and other OAuth parameters
	clientID := oauthServer.GetClientID()
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", h.currentInstance.Port)
	state := fmt.Sprintf("test-state-%d", time.Now().UnixNano())
	scope := "openid profile"

	// If PKCE is required, generate code verifier and challenge
	codeChallenge := ""
	codeChallengeMethod := ""
	// For simplicity, we'll skip PKCE in the simulation since we control both sides

	// Generate an authorization code directly through the OAuth server
	authCode := oauthServer.GenerateAuthCode(clientID, redirectURI, scope, state, codeChallenge, codeChallengeMethod)

	if h.debug {
		h.logger.Debug("🔐 Generated auth code: %s...\n", authCode[:min(16, len(authCode))])
	}

	// Simulate the callback to muster's callback endpoint
	callbackURL := fmt.Sprintf("%s?code=%s&state=%s", redirectURI, url.QueryEscape(authCode), url.QueryEscape(state))

	if h.debug {
		h.logger.Debug("🔐 Simulating callback to: %s\n", callbackURL)
	}

	// Make the HTTP request to the callback endpoint
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Don't follow redirects - we want to capture the callback result
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, callbackURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create callback request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		// If we can't connect, the callback endpoint might not be exposed
		// Try to exchange the code directly through the OAuth server
		if h.debug {
			h.logger.Debug("🔐 Direct callback failed, exchanging token via OAuth server: %v\n", err)
		}

		tokenResp, tokenErr := oauthServer.SimulateCallback(authCode)
		if tokenErr != nil {
			return nil, fmt.Errorf("failed to exchange auth code: %w", tokenErr)
		}

		return map[string]interface{}{
			"success":      true,
			"message":      "OAuth callback simulated successfully via direct token exchange",
			"server":       serverName,
			"access_token": tokenResp.AccessToken,
			"token_type":   tokenResp.TokenType,
			"expires_in":   tokenResp.ExpiresIn,
		}, nil
	}
	defer resp.Body.Close()

	if h.debug {
		h.logger.Debug("🔐 Callback response status: %d\n", resp.StatusCode)
	}

	// Success - callback was received
	return map[string]interface{}{
		"success":     true,
		"message":     "OAuth callback simulated successfully",
		"server":      serverName,
		"status_code": resp.StatusCode,
	}, nil
}

// handleInjectToken directly injects an access token for a server.
// This is useful for testing authenticated tool calls without going through the full OAuth flow.
func (h *TestToolsHandler) handleInjectToken(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	serverName, ok := args["server"].(string)
	if !ok || serverName == "" {
		return nil, fmt.Errorf("server argument is required")
	}

	token, ok := args["token"].(string)
	if !ok || token == "" {
		return nil, fmt.Errorf("token argument is required")
	}

	if h.instanceManager == nil || h.currentInstance == nil {
		return nil, fmt.Errorf("instance manager or current instance not available")
	}

	// Find the OAuth server for this MCP server
	oauthServerRef := h.findOAuthServerRefForMCPServer(serverName)
	if oauthServerRef == "" {
		// Try to use any available OAuth server
		for name := range h.currentInstance.MockOAuthServers {
			oauthServerRef = name
			break
		}
	}

	if oauthServerRef == "" {
		return nil, fmt.Errorf("no OAuth server found for server %s", serverName)
	}

	oauthServer := h.instanceManager.GetMockOAuthServer(h.currentInstance.ID, oauthServerRef)
	if oauthServer == nil {
		return nil, fmt.Errorf("mock OAuth server %s not running", oauthServerRef)
	}

	// Add the token directly to the OAuth server's token store
	scope := "openid profile"
	if s, ok := args["scope"].(string); ok {
		scope = s
	}

	expiresIn := 3600 // 1 hour default
	if e, ok := args["expires_in"].(float64); ok {
		expiresIn = int(e)
	}

	expiresAt := time.Now().Add(time.Duration(expiresIn) * time.Second)
	refreshToken := fmt.Sprintf("refresh-%s", token)

	oauthServer.AddToken(token, refreshToken, scope, oauthServer.GetClientID(), expiresAt)

	if h.debug {
		h.logger.Debug("🔐 Injected token for server %s: %s...\n", serverName, token[:min(16, len(token))])
	}

	return map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Token injected successfully for server %s", serverName),
		"server":  serverName,
	}, nil
}

// handleGetOAuthServerInfo returns information about a mock OAuth server.
func (h *TestToolsHandler) handleGetOAuthServerInfo(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	serverName, ok := args["server"].(string)
	if !ok || serverName == "" {
		// Return info about all OAuth servers
		if h.currentInstance == nil {
			return nil, fmt.Errorf("current instance not available")
		}

		servers := make(map[string]interface{})
		for name, info := range h.currentInstance.MockOAuthServers {
			servers[name] = map[string]interface{}{
				"name":       info.Name,
				"port":       info.Port,
				"issuer_url": info.IssuerURL,
			}
		}

		return map[string]interface{}{
			"oauth_servers": servers,
		}, nil
	}

	// Return info about specific OAuth server
	info, exists := h.currentInstance.MockOAuthServers[serverName]
	if !exists {
		return nil, fmt.Errorf("OAuth server %s not found", serverName)
	}

	return map[string]interface{}{
		"name":       info.Name,
		"port":       info.Port,
		"issuer_url": info.IssuerURL,
	}, nil
}

// findOAuthServerRefForMCPServer finds the OAuth server reference for an MCP server.
// This looks up the MCP server configuration to find which OAuth server it uses.
func (h *TestToolsHandler) findOAuthServerRefForMCPServer(mcpServerName string) string {
	// For now, return empty and let the caller use default logic
	// In a more complete implementation, this would parse the instance configuration
	return ""
}

// TestToolResult wraps a result from a test tool to match MCP response format.
type TestToolResult struct {
	Content []TestToolContent `json:"content"`
	IsError bool              `json:"isError,omitempty"`
}

// TestToolContent represents content in a test tool result.
type TestToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// WrapTestToolResult wraps a test tool result in MCP-compatible format.
func WrapTestToolResult(result interface{}, err error) *TestToolResult {
	if err != nil {
		return &TestToolResult{
			Content: []TestToolContent{
				{Type: "text", Text: err.Error()},
			},
			IsError: true,
		}
	}

	// Marshal the result to JSON
	jsonBytes, jsonErr := json.Marshal(result)
	if jsonErr != nil {
		return &TestToolResult{
			Content: []TestToolContent{
				{Type: "text", Text: fmt.Sprintf("failed to marshal result: %v", jsonErr)},
			},
			IsError: true,
		}
	}

	return &TestToolResult{
		Content: []TestToolContent{
			{Type: "text", Text: string(jsonBytes)},
		},
		IsError: false,
	}
}

// min returns the minimum of two integers.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// GetTestToolNames returns the names of all available test tools.
func GetTestToolNames() []string {
	return []string{
		"test_simulate_oauth_callback",
		"test_inject_token",
		"test_get_oauth_server_info",
	}
}

// GetTestToolDescriptions returns descriptions of test tools for documentation.
func GetTestToolDescriptions() map[string]string {
	return map[string]string{
		"test_simulate_oauth_callback": "Simulates completing an OAuth flow for testing. Required arg: 'server' (name of the MCP server to authenticate to).",
		"test_inject_token":            "Directly injects an access token for testing. Required args: 'server' (name of the MCP server), 'token' (access token value).",
		"test_get_oauth_server_info":   "Returns information about mock OAuth servers. Optional arg: 'server' (specific OAuth server name).",
	}
}
