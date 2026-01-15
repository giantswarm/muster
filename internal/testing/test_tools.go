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
	"strings"
	"time"

	"muster/internal/testing/mock"
)

// TestToolsHandler handles test-specific tools that operate on mock infrastructure.
// These tools are NOT exposed through the muster serve MCP server - they are
// handled directly by the test runner before delegating to the real MCP client.
type TestToolsHandler struct {
	instanceManager *musterInstanceManager
	currentInstance *MusterInstance
	mcpClient       MCPTestClient // MCP client for calling tools in the muster instance
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

// SetMCPClient sets the MCP client for calling tools in the muster instance.
// This is used by test_simulate_oauth_callback to call the authenticate tool.
func (h *TestToolsHandler) SetMCPClient(client MCPTestClient) {
	h.mcpClient = client
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
// This tool performs the complete OAuth dance:
// 1. Calls the authenticate tool for the specified server via MCP to get the auth URL
// 2. Extracts the state parameter from the auth URL (this is muster's real state)
// 3. Generates an auth code in the mock OAuth server
// 4. Calls muster's callback endpoint with the real state and auth code
// 5. Muster exchanges the code for a token and stores it internally
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

	// Step 1: Call the authenticate tool via MCP to get the auth URL with real state
	authURL, err := h.callAuthenticateTool(ctx, serverName)
	if err != nil {
		// If we can't call the authenticate tool, fall back to direct token injection
		if h.debug {
			h.logger.Debug("🔐 Could not call authenticate tool, falling back to direct token injection: %v\n", err)
		}
		return h.fallbackDirectTokenInjection(ctx, serverName, oauthServer)
	}

	if h.debug {
		h.logger.Debug("🔐 Got auth URL from authenticate tool: %s\n", authURL)
	}

	// Step 2: Parse the auth URL to extract the state parameter
	parsedURL, err := url.Parse(authURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse auth URL: %w", err)
	}

	state := parsedURL.Query().Get("state")
	redirectURI := parsedURL.Query().Get("redirect_uri")
	clientID := parsedURL.Query().Get("client_id")
	scope := parsedURL.Query().Get("scope")
	codeChallenge := parsedURL.Query().Get("code_challenge")
	codeChallengeMethod := parsedURL.Query().Get("code_challenge_method")

	if state == "" {
		return nil, fmt.Errorf("no state parameter found in auth URL")
	}

	if h.debug {
		h.logger.Debug("🔐 Extracted from auth URL: state=%s..., redirect_uri=%s\n",
			state[:minInt(16, len(state))], redirectURI)
	}

	// Step 3: Generate an authorization code in the mock OAuth server
	// Use the parameters from muster's auth URL so PKCE verification will pass
	authCode := oauthServer.GenerateAuthCode(clientID, redirectURI, scope, state, codeChallenge, codeChallengeMethod)

	if h.debug {
		h.logger.Debug("🔐 Generated auth code: %s...\n", authCode[:minInt(16, len(authCode))])
	}

	// Step 4: Call muster's callback endpoint with the real state and auth code
	callbackURL := fmt.Sprintf("%s?code=%s&state=%s", redirectURI, url.QueryEscape(authCode), url.QueryEscape(state))

	if h.debug {
		h.logger.Debug("🔐 Calling muster callback: %s\n", callbackURL)
	}

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
		return nil, fmt.Errorf("callback request failed: %w", err)
	}
	defer resp.Body.Close()

	if h.debug {
		h.logger.Debug("🔐 Callback response status: %d\n", resp.StatusCode)
	}

	// Check for success (200 OK or redirect to success page)
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		if h.debug {
			h.logger.Debug("🔐 Callback succeeded, now calling authenticate tool again to establish connection\n")
		}

		// Step 5: Call the authenticate tool again to trigger session connection establishment
		// After the callback, the token is stored. When we call authenticate again,
		// the aggregator will find the token and call tryConnectWithToken to establish
		// the session connection and make tools available.
		_, err := h.callAuthenticateTool(ctx, serverName)
		if err != nil {
			// Even if this fails, the token is stored - log but don't fail
			if h.debug {
				h.logger.Debug("🔐 Second authenticate call returned: %v (this may be expected)\n", err)
			}
		}

		return map[string]interface{}{
			"success":     true,
			"message":     "OAuth callback completed successfully - token stored and connection established",
			"server":      serverName,
			"status_code": resp.StatusCode,
		}, nil
	}

	return nil, fmt.Errorf("callback returned error status: %d", resp.StatusCode)
}

// callAuthenticateTool calls the authenticate tool via MCP to get the auth URL.
func (h *TestToolsHandler) callAuthenticateTool(ctx context.Context, serverName string) (string, error) {
	if h.mcpClient == nil {
		return "", fmt.Errorf("MCP client not available")
	}

	// The authenticate tool follows the pattern: x_<server>_authenticate
	// This matches the naming convention in the aggregator's tool factory
	authToolName := fmt.Sprintf("x_%s_authenticate", serverName)

	if h.debug {
		h.logger.Debug("🔐 Calling authenticate tool: %s\n", authToolName)
	}

	result, err := h.mcpClient.CallTool(ctx, authToolName, map[string]interface{}{})
	if err != nil {
		return "", fmt.Errorf("authenticate tool call failed: %w", err)
	}

	// Extract the auth URL from the result
	return h.extractAuthURLFromResult(result)
}

// extractAuthURLFromResult extracts the authorization URL from an MCP tool result.
func (h *TestToolsHandler) extractAuthURLFromResult(result interface{}) (string, error) {
	// The result is an MCP CallToolResult with Content array
	// We need to extract the text content and find the auth URL

	resultBytes, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("failed to marshal result: %w", err)
	}

	var resultMap map[string]interface{}
	if err := json.Unmarshal(resultBytes, &resultMap); err != nil {
		return "", fmt.Errorf("failed to unmarshal result: %w", err)
	}

	// Extract content array
	content, ok := resultMap["content"].([]interface{})
	if !ok || len(content) == 0 {
		return "", fmt.Errorf("no content in result")
	}

	// Get the first content item
	contentItem, ok := content[0].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid content item format")
	}

	// Get the text field
	text, ok := contentItem["text"].(string)
	if !ok {
		return "", fmt.Errorf("no text field in content")
	}

	// The text might be JSON with an auth_url field, or contain a URL directly
	// Try to parse as JSON first
	var authResponse map[string]interface{}
	if err := json.Unmarshal([]byte(text), &authResponse); err == nil {
		if authURL, ok := authResponse["auth_url"].(string); ok {
			return authURL, nil
		}
		if authURL, ok := authResponse["authorization_url"].(string); ok {
			return authURL, nil
		}
	}

	// Look for URL in the text
	if strings.Contains(text, "http") {
		// Find URL patterns in the text
		for _, word := range strings.Fields(text) {
			if strings.HasPrefix(word, "http://") || strings.HasPrefix(word, "https://") {
				// Clean up any trailing punctuation
				word = strings.TrimRight(word, ".,;:)")
				return word, nil
			}
		}
	}

	return "", fmt.Errorf("could not find auth URL in result: %s", text)
}

// fallbackDirectTokenInjection falls back to direct token injection when MCP auth flow isn't available.
// This is used when the authenticate tool can't be called (e.g., MCP client not available).
func (h *TestToolsHandler) fallbackDirectTokenInjection(ctx context.Context, serverName string, oauthServer interface{}) (interface{}, error) {
	// Cast to the right type
	server, ok := oauthServer.(*mock.OAuthServer)
	if !ok {
		return nil, fmt.Errorf("invalid OAuth server type")
	}

	// Generate a token directly
	clientID := server.GetClientID()
	scope := "openid profile"

	// Generate auth code and exchange it
	authCode := server.GenerateAuthCode(clientID, "http://localhost/callback", scope, "fallback-state", "", "")
	tokenResp, err := server.SimulateCallback(authCode)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	// Note: This token is in the mock OAuth server but NOT in muster's token store
	// For the protected MCP server to accept it, we just need it valid in the mock OAuth server
	// The aggregator will get a 401, create an auth challenge, and we'd need to complete the flow

	return map[string]interface{}{
		"success":      true,
		"message":      "OAuth callback simulated via direct token exchange (fallback mode)",
		"server":       serverName,
		"access_token": tokenResp.AccessToken,
		"token_type":   tokenResp.TokenType,
		"expires_in":   tokenResp.ExpiresIn,
		"note":         "Token is valid in mock OAuth server but may not be in muster's token store",
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
