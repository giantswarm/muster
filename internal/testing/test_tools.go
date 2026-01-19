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
	pkgoauth "muster/pkg/oauth"

	"github.com/mark3labs/mcp-go/mcp"
)

// Test tool name constants for BDD test scenarios.
const (
	// TestToolSimulateOAuthCallback simulates completing an OAuth flow for testing.
	TestToolSimulateOAuthCallback = "test_simulate_oauth_callback"
	// TestToolInjectToken directly injects an access token for testing.
	TestToolInjectToken = "test_inject_token"
	// TestToolGetOAuthServerInfo returns information about mock OAuth servers.
	TestToolGetOAuthServerInfo = "test_get_oauth_server_info"
	// TestToolAdvanceOAuthClock advances the mock OAuth server's clock for testing.
	TestToolAdvanceOAuthClock = "test_advance_oauth_clock"
	// TestToolReadAuthStatus reads the auth://status resource to verify auth state.
	TestToolReadAuthStatus = "test_read_auth_status"
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
	switch toolName {
	case TestToolSimulateOAuthCallback,
		TestToolInjectToken,
		TestToolGetOAuthServerInfo,
		TestToolAdvanceOAuthClock,
		TestToolReadAuthStatus:
		return true
	}
	return false
}

// HandleTestTool executes a test helper tool and returns the result.
func (h *TestToolsHandler) HandleTestTool(ctx context.Context, toolName string, args map[string]interface{}) (interface{}, error) {
	if h.debug {
		h.logger.Debug("🧪 Handling test tool: %s with args: %v\n", toolName, args)
	}

	switch toolName {
	case TestToolSimulateOAuthCallback:
		return h.handleSimulateOAuthCallback(ctx, args)
	case TestToolInjectToken:
		return h.handleInjectToken(ctx, args)
	case TestToolGetOAuthServerInfo:
		return h.handleGetOAuthServerInfo(ctx, args)
	case TestToolAdvanceOAuthClock:
		return h.handleAdvanceOAuthClock(ctx, args)
	case TestToolReadAuthStatus:
		return h.handleReadAuthStatus(ctx, args)
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

	// Step 1: Call the authenticate tool via MCP to get the auth URL with real state
	// We do this FIRST because the auth URL tells us which OAuth server to use
	authURL, err := h.callAuthenticateTool(ctx, serverName)
	if err != nil {
		// If we can't call the authenticate tool, fall back to direct token injection
		if h.debug {
			h.logger.Debug("🔐 Could not call authenticate tool, falling back to direct token injection: %v\n", err)
		}
		// Use any available OAuth server for fallback
		var oauthServer *mock.OAuthServer
		for name := range h.currentInstance.MockOAuthServers {
			oauthServer = h.instanceManager.GetMockOAuthServer(h.currentInstance.ID, name)
			if oauthServer != nil {
				break
			}
		}
		if oauthServer == nil {
			return nil, fmt.Errorf("no mock OAuth server available for fallback")
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

	// Step 3: Find the OAuth server that matches the auth URL's host
	// The auth URL points to the OAuth server's authorize endpoint
	authHost := parsedURL.Scheme + "://" + parsedURL.Host

	var oauthServer *mock.OAuthServer
	var oauthServerName string
	for name, info := range h.currentInstance.MockOAuthServers {
		if strings.HasPrefix(info.IssuerURL, authHost) {
			oauthServer = h.instanceManager.GetMockOAuthServer(h.currentInstance.ID, name)
			oauthServerName = name
			break
		}
	}

	if oauthServer == nil {
		// Fall back to looking up by server ref if issuer match didn't work
		var oauthServerInfo *MockOAuthServerInfo
		oauthServerRef := h.findOAuthServerRefForMCPServer(serverName)
		if ref, exists := h.currentInstance.MockOAuthServers[oauthServerRef]; exists && ref != nil {
			oauthServerInfo = ref
			oauthServerName = ref.Name
		} else {
			// Use first available OAuth server as fallback
			for name, info := range h.currentInstance.MockOAuthServers {
				if info != nil {
					oauthServerInfo = info
					oauthServerName = name
					break
				}
			}
		}
		if oauthServerInfo != nil {
			oauthServer = h.instanceManager.GetMockOAuthServer(h.currentInstance.ID, oauthServerInfo.Name)
		}
	}

	if oauthServer == nil {
		return nil, fmt.Errorf("no mock OAuth server found for server %s (auth host: %s)", serverName, authHost)
	}

	if h.debug {
		h.logger.Debug("🔐 Simulating OAuth callback for server %s using OAuth server %s (matched via issuer %s)\n",
			serverName, oauthServerName, authHost)
		h.logger.Debug("🔐 Extracted from auth URL: state=%s..., redirect_uri=%s\n",
			state[:min(16, len(state))], redirectURI)
	}

	// Step 4: Generate an authorization code in the mock OAuth server
	// Use the parameters from muster's auth URL so PKCE verification will pass
	authCode := oauthServer.GenerateAuthCode(clientID, redirectURI, scope, state, codeChallenge, codeChallengeMethod)

	if h.debug {
		h.logger.Debug("🔐 Generated auth code: %s...\n", authCode[:min(16, len(authCode))])
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
			h.logger.Debug("🔐 Callback succeeded - token stored in muster's OAuth manager\n")
		}

		// Note: The token is now stored in muster's OAuth manager.
		// The NEXT call to any protected tool (or the authenticate tool) will:
		// 1. Find the token via GetTokenByIssuer(sessionID, issuer)
		// 2. Use it to connect to the protected MCP server
		// 3. Make the protected tools available
		// We do NOT call authenticate a second time here - that was a workaround
		// that masked aggregator bugs and didn't match real user behavior.

		return map[string]interface{}{
			"success":     true,
			"message":     "OAuth callback completed successfully - token stored",
			"server":      serverName,
			"status_code": resp.StatusCode,
		}, nil
	}

	return nil, fmt.Errorf("callback returned error status: %d", resp.StatusCode)
}

// callAuthenticateTool calls the core_auth_login tool via MCP to get the auth URL.
func (h *TestToolsHandler) callAuthenticateTool(ctx context.Context, serverName string) (string, error) {
	if h.mcpClient == nil {
		return "", fmt.Errorf("MCP client not available")
	}

	// Use the unified core_auth_login tool with server argument
	authToolName := "core_auth_login"

	if h.debug {
		h.logger.Debug("🔐 Calling authenticate tool: %s with server=%s\n", authToolName, serverName)
	}

	result, err := h.mcpClient.CallTool(ctx, authToolName, map[string]interface{}{
		"server": serverName,
	})
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

// handleAdvanceOAuthClock advances the mock OAuth server's clock for testing token expiry.
// This allows tests to simulate token expiry without waiting for real time to pass.
func (h *TestToolsHandler) handleAdvanceOAuthClock(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	duration, ok := args["duration"].(string)
	if !ok || duration == "" {
		return nil, fmt.Errorf("duration argument required (e.g., '5m', '1h')")
	}

	d, err := time.ParseDuration(duration)
	if err != nil {
		return nil, fmt.Errorf("invalid duration: %w", err)
	}

	if h.instanceManager == nil || h.currentInstance == nil {
		return nil, fmt.Errorf("instance manager or current instance not available")
	}

	// Get optional server name - if provided, only advance that server's clock
	serverName, _ := args["server"].(string)

	advancedServers := []string{}

	if serverName != "" {
		// Advance specific OAuth server's clock
		oauthServer := h.instanceManager.GetMockOAuthServer(h.currentInstance.ID, serverName)
		if oauthServer == nil {
			return nil, fmt.Errorf("OAuth server %s not found", serverName)
		}
		if mockClock, ok := oauthServer.GetClock().(*mock.MockClock); ok {
			mockClock.Advance(d)
			advancedServers = append(advancedServers, serverName)
			if h.debug {
				h.logger.Debug("🕐 Advanced OAuth clock for %s by %s\n", serverName, duration)
			}
		} else {
			return nil, fmt.Errorf("OAuth server %s does not use a mock clock", serverName)
		}
	} else {
		// Advance all OAuth servers' clocks
		for name := range h.currentInstance.MockOAuthServers {
			oauthServer := h.instanceManager.GetMockOAuthServer(h.currentInstance.ID, name)
			if oauthServer != nil {
				if mockClock, ok := oauthServer.GetClock().(*mock.MockClock); ok {
					mockClock.Advance(d)
					advancedServers = append(advancedServers, name)
					if h.debug {
						h.logger.Debug("🕐 Advanced OAuth clock for %s by %s\n", name, duration)
					}
				}
			}
		}
	}

	if len(advancedServers) == 0 {
		return nil, fmt.Errorf("no OAuth servers with mock clocks found")
	}

	return map[string]interface{}{
		"success":          true,
		"message":          fmt.Sprintf("Advanced OAuth clock by %s", duration),
		"advanced_by":      d.String(),
		"servers_advanced": advancedServers,
	}, nil
}

// findOAuthServerRefForMCPServer finds the OAuth server reference for an MCP server.
// This looks up the MCP server configuration to find which OAuth server it uses.
//
// Currently returns empty string as the MCP server -> OAuth server mapping isn't
// stored with the running instance. The caller falls back to using the first
// available OAuth server when empty is returned. This works for most test scenarios
// which use a single OAuth server.
//
// Future enhancement: Store the oauth.mock_oauth_server_ref configuration with
// the MockHTTPServerInfo to enable proper mapping when multiple OAuth servers exist.
func (h *TestToolsHandler) findOAuthServerRefForMCPServer(_ string) string {
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

// handleReadAuthStatus reads the auth://status resource to verify authentication state.
// This tool reads the auth://status MCP resource and returns the authentication status
// of all MCP servers. It can optionally filter by server name.
//
// Args:
//   - server (optional): Name of a specific server to check status for
//
// Returns the auth status as JSON, including:
//   - Server names and their connection status ("connected", "auth_required", etc.)
//   - SSO mechanism info (token_forwarding_enabled, token_reuse_enabled)
func (h *TestToolsHandler) handleReadAuthStatus(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	if h.mcpClient == nil {
		return nil, fmt.Errorf("MCP client not available for reading auth status")
	}

	// Read the auth://status resource
	result, err := h.mcpClient.ReadResource(ctx, "auth://status")
	if err != nil {
		return &TestToolResult{
			Content: []TestToolContent{
				{Type: "text", Text: fmt.Sprintf("failed to read auth://status: %v", err)},
			},
			IsError: true,
		}, nil
	}

	// Extract the text content from the resource result
	if result == nil || len(result.Contents) == 0 {
		return &TestToolResult{
			Content: []TestToolContent{
				{Type: "text", Text: "auth://status resource returned empty content"},
			},
			IsError: true,
		}, nil
	}

	// Get the text content - the auth status is returned as JSON
	var statusJSON string
	for _, content := range result.Contents {
		// Use the mcp helper to extract text content
		if textContent, ok := mcp.AsTextResourceContents(content); ok {
			statusJSON = textContent.Text
			break
		}
	}

	if statusJSON == "" {
		return &TestToolResult{
			Content: []TestToolContent{
				{Type: "text", Text: "could not extract text from auth://status resource"},
			},
			IsError: true,
		}, nil
	}

	// If a specific server was requested, filter the response
	if serverName, ok := args["server"].(string); ok && serverName != "" {
		// Parse the JSON using the shared type from pkg/oauth
		var authStatus pkgoauth.AuthStatusResponse
		if err := json.Unmarshal([]byte(statusJSON), &authStatus); err != nil {
			return &TestToolResult{
				Content: []TestToolContent{
					{Type: "text", Text: fmt.Sprintf("failed to parse auth status: %v", err)},
				},
				IsError: true,
			}, nil
		}

		// Find the specific server
		for _, srv := range authStatus.Servers {
			if srv.Name == serverName {
				filtered, _ := json.MarshalIndent(srv, "", "  ")
				return &TestToolResult{
					Content: []TestToolContent{
						{Type: "text", Text: string(filtered)},
					},
					IsError: false,
				}, nil
			}
		}

		return &TestToolResult{
			Content: []TestToolContent{
				{Type: "text", Text: fmt.Sprintf("server '%s' not found in auth status", serverName)},
			},
			IsError: true,
		}, nil
	}

	// Return the full auth status
	return &TestToolResult{
		Content: []TestToolContent{
			{Type: "text", Text: statusJSON},
		},
		IsError: false,
	}, nil
}

// GetTestToolNames returns the names of all available test tools.
func GetTestToolNames() []string {
	return []string{
		TestToolSimulateOAuthCallback,
		TestToolInjectToken,
		TestToolGetOAuthServerInfo,
		TestToolAdvanceOAuthClock,
		TestToolReadAuthStatus,
	}
}

// GetTestToolDescriptions returns descriptions of test tools for documentation.
func GetTestToolDescriptions() map[string]string {
	return map[string]string{
		TestToolSimulateOAuthCallback: "Simulates completing an OAuth flow for testing. Required arg: 'server' (name of the MCP server to authenticate to).",
		TestToolInjectToken:           "Directly injects an access token for testing. Required args: 'server' (name of the MCP server), 'token' (access token value).",
		TestToolGetOAuthServerInfo:    "Returns information about mock OAuth servers. Optional arg: 'server' (specific OAuth server name).",
		TestToolAdvanceOAuthClock:     "Advances the mock OAuth server's clock for testing token expiry. Required arg: 'duration' (e.g., '5m', '1h'). Optional arg: 'server' (specific OAuth server name).",
		TestToolReadAuthStatus:        "Reads the auth://status resource to verify authentication state. Optional arg: 'server' (specific server to check).",
	}
}
