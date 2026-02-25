// Package testing provides test helper tools for BDD test scenarios.
// These tools allow test scenarios to interact with mock infrastructure
// components that run in the test orchestrator process.
package testing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/giantswarm/muster/internal/testing/mock"

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
	// TestToolRevokeToken revokes a token on the mock OAuth server for testing.
	TestToolRevokeToken = "test_revoke_token"
	// TestToolCreateUser creates a new user session for multi-user testing.
	TestToolCreateUser = "test_create_user"
	// TestToolSwitchUser switches to a different user session for multi-user testing.
	TestToolSwitchUser = "test_switch_user"
	// TestToolListToolsForUser lists tools visible to a specific user.
	TestToolListToolsForUser = "test_list_tools_for_user"
	// TestToolGetCurrentUser returns the current active user name.
	TestToolGetCurrentUser = "test_get_current_user"
	// TestToolSimulateMusterReauth simulates re-authentication to muster with a new token
	// while preserving the session ID. Used to test proactive SSO re-triggering.
	TestToolSimulateMusterReauth = "test_simulate_muster_reauth"
	// TestToolMusterAuthLogin simulates `muster auth login` by completing the OAuth
	// callback to muster's token store. This is required for SSO token forwarding tests.
	TestToolMusterAuthLogin = "test_muster_auth_login"
)

// TestToolsHandler handles test-specific tools that operate on mock infrastructure.
// These tools are NOT exposed through the muster serve MCP server - they are
// handled directly by the test runner before delegating to the real MCP client.
type TestToolsHandler struct {
	instanceManager *musterInstanceManager
	currentInstance *MusterInstance
	mcpClient       MCPTestClient            // Default MCP client for calling tools
	userClients     map[string]MCPTestClient // Named user sessions for multi-user testing
	currentUser     string                   // Name of the currently active user ("default" if not set)
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
		userClients:     make(map[string]MCPTestClient),
		currentUser:     "default",
		debug:           debug,
		logger:          logger,
	}
}

// SetMCPClient sets the MCP client for calling tools in the muster instance.
// This is used by test_simulate_oauth_callback to call the authenticate tool.
func (h *TestToolsHandler) SetMCPClient(client MCPTestClient) {
	h.mcpClient = client
	// Also store as the "default" user for multi-user testing
	if h.userClients == nil {
		h.userClients = make(map[string]MCPTestClient)
	}
	h.userClients["default"] = client
	h.currentUser = "default"
}

// GetCurrentClient returns the MCP client for the currently active user.
func (h *TestToolsHandler) GetCurrentClient() MCPTestClient {
	if h.currentUser != "" && h.currentUser != "default" {
		if client, exists := h.userClients[h.currentUser]; exists {
			return client
		}
	}
	return h.mcpClient
}

// GetCurrentUserName returns the name of the currently active user.
func (h *TestToolsHandler) GetCurrentUserName() string {
	if h.currentUser == "" {
		return "default"
	}
	return h.currentUser
}

// CloseAllUserClients closes all user MCP clients except the default one.
// The default client is managed by the test runner.
func (h *TestToolsHandler) CloseAllUserClients() {
	for name, client := range h.userClients {
		if name != "default" && client != nil {
			if h.debug {
				h.logger.Debug("🔌 Closing MCP client for user %s\n", name)
			}
			client.Close()
		}
	}
}

// HasUser returns true if a user session with the given name exists.
// This provides encapsulated access to check user existence without
// exposing the internal userClients map.
func (h *TestToolsHandler) HasUser(name string) bool {
	_, exists := h.userClients[name]
	return exists
}

// SwitchToUser switches the current user context to the specified user.
// This is used by the test runner to switch user context for as_user steps.
// Returns false if the user doesn't exist.
func (h *TestToolsHandler) SwitchToUser(name string) bool {
	client, exists := h.userClients[name]
	if !exists {
		return false
	}
	h.currentUser = name
	h.mcpClient = client
	return true
}

// IsTestTool returns true if the tool name is a test helper tool.
func IsTestTool(toolName string) bool {
	switch toolName {
	case TestToolSimulateOAuthCallback,
		TestToolInjectToken,
		TestToolGetOAuthServerInfo,
		TestToolAdvanceOAuthClock,
		TestToolReadAuthStatus,
		TestToolRevokeToken,
		TestToolCreateUser,
		TestToolSwitchUser,
		TestToolListToolsForUser,
		TestToolGetCurrentUser,
		TestToolSimulateMusterReauth,
		TestToolMusterAuthLogin:
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
	case TestToolRevokeToken:
		return h.handleRevokeToken(ctx, args)
	case TestToolCreateUser:
		return h.handleCreateUser(ctx, args)
	case TestToolSwitchUser:
		return h.handleSwitchUser(ctx, args)
	case TestToolListToolsForUser:
		return h.handleListToolsForUser(ctx, args)
	case TestToolGetCurrentUser:
		return h.handleGetCurrentUser(ctx, args)
	case TestToolSimulateMusterReauth:
		return h.handleSimulateMusterReauth(ctx, args)
	case TestToolMusterAuthLogin:
		return h.handleMusterAuthLogin(ctx, args)
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

// handleRevokeToken revokes all tokens on the mock OAuth server.
// This simulates server-side token revocation while the client still has the token cached.
//
// Args:
//   - server (optional): Name of a specific OAuth server. If not provided, revokes on all servers.
//
// Returns success status and the number of tokens revoked.
func (h *TestToolsHandler) handleRevokeToken(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	if h.instanceManager == nil || h.currentInstance == nil {
		return nil, fmt.Errorf("instance manager or current instance not available")
	}

	// Get optional server name
	serverName, _ := args["server"].(string)

	revokedServers := []string{}
	totalRevoked := 0

	if serverName != "" {
		// Revoke on specific OAuth server
		oauthServer := h.instanceManager.GetMockOAuthServer(h.currentInstance.ID, serverName)
		if oauthServer == nil {
			return nil, fmt.Errorf("OAuth server %s not found", serverName)
		}
		count := oauthServer.RevokeAllTokens()
		totalRevoked += count
		revokedServers = append(revokedServers, serverName)
		if h.debug {
			h.logger.Debug("🔐 Revoked all tokens (%d) on OAuth server %s\n", count, serverName)
		}
	} else {
		// Revoke on all OAuth servers
		for name := range h.currentInstance.MockOAuthServers {
			oauthServer := h.instanceManager.GetMockOAuthServer(h.currentInstance.ID, name)
			if oauthServer != nil {
				count := oauthServer.RevokeAllTokens()
				totalRevoked += count
				revokedServers = append(revokedServers, name)
				if h.debug {
					h.logger.Debug("🔐 Revoked all tokens (%d) on OAuth server %s\n", count, name)
				}
			}
		}
	}

	if len(revokedServers) == 0 {
		return nil, fmt.Errorf("no OAuth servers found to revoke tokens on")
	}

	return map[string]interface{}{
		"success":         true,
		"message":         fmt.Sprintf("Revoked %d tokens on %d OAuth server(s)", totalRevoked, len(revokedServers)),
		"tokens_revoked":  totalRevoked,
		"servers_updated": revokedServers,
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
//   - SSO mechanism info (token_forwarding_enabled, token_exchange_enabled)
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

// handleCreateUser creates a new user session for multi-user testing.
// This tool creates a new MCP client connection with a separate session ID,
// simulating a different user connecting to the same muster instance.
//
// Args:
//   - name: Required. Name for this user session (e.g., "user-a", "alice").
//
// Returns success status with the user name.
func (h *TestToolsHandler) handleCreateUser(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	userName, ok := args["name"].(string)
	if !ok || userName == "" {
		return nil, fmt.Errorf("name argument is required")
	}

	// Check if user already exists
	if _, exists := h.userClients[userName]; exists {
		return nil, fmt.Errorf("user '%s' already exists", userName)
	}

	if h.currentInstance == nil {
		return nil, fmt.Errorf("current instance not available")
	}

	if h.debug {
		h.logger.Debug("👤 Creating new user session: %s\n", userName)
	}

	// Create a new MCP client for this user
	newClient := NewMCPTestClientWithLogger(h.debug, h.logger)

	// Connect with optional OAuth token if muster OAuth is enabled
	var connectErr error
	if h.currentInstance.MusterOAuthAccessToken != "" {
		connectErr = newClient.ConnectWithAuth(ctx, h.currentInstance.Endpoint, h.currentInstance.MusterOAuthAccessToken)
	} else {
		connectErr = newClient.Connect(ctx, h.currentInstance.Endpoint)
	}

	if connectErr != nil {
		return nil, fmt.Errorf("failed to create user session '%s': %w", userName, connectErr)
	}

	// Store the new client
	h.userClients[userName] = newClient

	if h.debug {
		h.logger.Debug("✅ Created user session: %s (now have %d user sessions)\n", userName, len(h.userClients))
	}

	return map[string]interface{}{
		"success":      true,
		"message":      fmt.Sprintf("Created user session '%s'", userName),
		"user":         userName,
		"total_users":  len(h.userClients),
		"current_user": h.currentUser,
	}, nil
}

// handleSwitchUser switches to a different user session.
// After switching, all subsequent tool calls will use this user's MCP client.
//
// Args:
//   - name: Required. Name of the user session to switch to.
//
// Returns success status with the new current user.
func (h *TestToolsHandler) handleSwitchUser(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	userName, ok := args["name"].(string)
	if !ok || userName == "" {
		return nil, fmt.Errorf("name argument is required")
	}

	// Check if user exists
	if _, exists := h.userClients[userName]; !exists {
		available := make([]string, 0, len(h.userClients))
		for name := range h.userClients {
			available = append(available, name)
		}
		return nil, fmt.Errorf("user '%s' not found; available users: %v", userName, available)
	}

	previousUser := h.currentUser
	h.currentUser = userName
	h.mcpClient = h.userClients[userName]

	if h.debug {
		h.logger.Debug("👤 Switched from user '%s' to user '%s'\n", previousUser, userName)
	}

	return map[string]interface{}{
		"success":       true,
		"message":       fmt.Sprintf("Switched to user '%s'", userName),
		"current_user":  userName,
		"previous_user": previousUser,
	}, nil
}

// handleListToolsForUser lists tools visible to a specific user.
// This allows verifying that different users see different tools
// based on their OAuth authentication state.
//
// Args:
//   - name: Optional. Name of the user session to list tools for.
//     If not provided, lists tools for the current user.
//
// Returns the list of tool names visible to the specified user.
func (h *TestToolsHandler) handleListToolsForUser(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	userName := h.currentUser
	if name, ok := args["name"].(string); ok && name != "" {
		userName = name
	}

	// Get the client for this user
	client, exists := h.userClients[userName]
	if !exists {
		return nil, fmt.Errorf("user '%s' not found", userName)
	}

	if h.debug {
		h.logger.Debug("🛠️  Listing tools for user '%s'\n", userName)
	}

	// List tools using this user's client
	tools, err := client.ListTools(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tools for user '%s': %w", userName, err)
	}

	if h.debug {
		h.logger.Debug("🛠️  User '%s' has %d tools\n", userName, len(tools))
	}

	return map[string]interface{}{
		"success":    true,
		"user":       userName,
		"tool_count": len(tools),
		"tools":      tools,
	}, nil
}

// handleGetCurrentUser returns the name of the currently active user.
func (h *TestToolsHandler) handleGetCurrentUser(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	available := make([]string, 0, len(h.userClients))
	for name := range h.userClients {
		available = append(available, name)
	}

	return map[string]interface{}{
		"success":         true,
		"current_user":    h.currentUser,
		"available_users": available,
		"total_users":     len(h.userClients),
	}, nil
}

// handleSimulateMusterReauth simulates re-authentication to muster with a new token
// while preserving the same MCP session ID. This is used to test session continuity
// when a user re-authenticates to muster.
//
// The key behavior being tested:
// - The session ID remains the same across re-authentication
// - After re-auth, the user can re-establish connections to SSO servers
// - This tests the session tracking and token refresh paths
//
// This tool:
// 1. Captures the current session ID from the MCP client
// 2. Generates a new access token from the mock OAuth server (different from the initial token)
// 3. Reconnects to muster with the new access token (same session ID)
// 4. The user can then use test_simulate_oauth_callback to re-authenticate to SSO servers
//
// Note: This simulates muster re-authentication by generating a new token and reconnecting
// with the same session ID. For SSO servers to reconnect, the user must explicitly
// re-authenticate to them (via test_simulate_oauth_callback or core_auth_login).
func (h *TestToolsHandler) handleSimulateMusterReauth(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	if h.mcpClient == nil {
		return nil, fmt.Errorf("MCP client not available")
	}

	if h.currentInstance == nil || h.instanceManager == nil {
		return nil, fmt.Errorf("instance manager or current instance not available")
	}

	// Step 1: Get the current session ID
	currentSessionID := h.mcpClient.GetSessionID()
	if currentSessionID == "" {
		return nil, fmt.Errorf("no session ID found - client may not be connected")
	}

	if h.debug {
		h.logger.Debug("🔄 Muster re-auth: Current session ID: %s...\n", currentSessionID[:min(16, len(currentSessionID))])
	}

	// Step 2: Use the refresh token from the initial login to get a new access token.
	// This avoids re-registering a client and hitting rate limits.
	if h.currentInstance.MusterOAuthRefreshToken == "" || h.currentInstance.MusterOAuthClientID == "" {
		return nil, fmt.Errorf("no refresh token or client ID from initial login (run test_muster_auth_login first)")
	}

	baseURL := pkgoauth.NormalizeServerURL(h.currentInstance.Endpoint)

	tokenResult, err := exchangeOAuthToken(ctx, baseURL+"/oauth/token", url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {h.currentInstance.MusterOAuthRefreshToken},
		"client_id":     {h.currentInstance.MusterOAuthClientID},
	})
	if err != nil {
		return nil, fmt.Errorf("token refresh failed: %w", err)
	}

	h.currentInstance.MusterOAuthAccessToken = tokenResult.AccessToken
	if tokenResult.RefreshToken != "" {
		h.currentInstance.MusterOAuthRefreshToken = tokenResult.RefreshToken
	}

	if h.debug {
		h.logger.Debug("🔄 Obtained new muster access token via refresh token\n")
	}

	// Step 3: Reconnect the MCP client with the new access token but SAME session ID
	if err := h.mcpClient.ReconnectWithSession(ctx, h.currentInstance.Endpoint, tokenResult.AccessToken, currentSessionID); err != nil {
		return nil, fmt.Errorf("failed to reconnect MCP client with new token: %w", err)
	}

	newSessionID := h.mcpClient.GetSessionID()

	if h.debug {
		h.logger.Debug("✅ Muster re-auth complete. Session preserved: %v (id: %s...)\n",
			newSessionID == currentSessionID, newSessionID[:min(16, len(newSessionID))])
	}

	return map[string]interface{}{
		"success":           true,
		"message":           "Successfully simulated muster re-authentication with new token",
		"session_id":        currentSessionID[:min(32, len(currentSessionID))],
		"new_session_id":    newSessionID[:min(32, len(newSessionID))],
		"session_preserved": newSessionID == currentSessionID,
	}, nil
}

// oauthTokenResult holds the tokens returned by an OAuth token endpoint.
type oauthTokenResult struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// exchangeOAuthToken POSTs form data to the given token URL and decodes the
// access/refresh token response. It is shared between handleMusterAuthLogin
// (authorization_code grant) and handleSimulateMusterReauth (refresh_token grant).
func exchangeOAuthToken(ctx context.Context, tokenURL string, data url.Values) (*oauthTokenResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("token request failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result oauthTokenResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode token response: %w", err)
	}

	if result.AccessToken == "" {
		return nil, fmt.Errorf("no access_token in token response")
	}

	return &result, nil
}

// handleMusterAuthLogin simulates `muster auth login` by completing the OAuth
// callback to muster itself. This stores the ID token in muster's token store,
// which is required for SSO token forwarding to work.
//
// This tool:
// 1. Finds the mock OAuth server configured as muster's OAuth server (use_as_muster_oauth_server: true)
// 2. Calls muster's /oauth/authorize to start the OAuth flow and get redirected to Dex
// 3. Generates an auth code on the mock Dex server
// 4. Calls muster's /oauth/callback endpoint with the auth code
// 5. Muster exchanges the code with Dex and stores the token
//
// After this, SSO token forwarding will work because the ID token is available.
func (h *TestToolsHandler) handleMusterAuthLogin(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	if h.currentInstance == nil || h.instanceManager == nil {
		return nil, fmt.Errorf("instance manager or current instance not available")
	}

	// Find the mock OAuth server configured as muster's OAuth server
	var musterOAuthServerName string
	var musterOAuthServerInfo *MockOAuthServerInfo
	for name, info := range h.currentInstance.MockOAuthServers {
		if info.UseAsMusterOAuthServer {
			musterOAuthServerName = name
			musterOAuthServerInfo = info
			break
		}
	}

	if musterOAuthServerInfo == nil {
		return nil, fmt.Errorf("no muster OAuth server found (need a mock OAuth server with use_as_muster_oauth_server: true)")
	}

	musterOAuthServer := h.instanceManager.GetMockOAuthServer(h.currentInstance.ID, musterOAuthServerName)
	if musterOAuthServer == nil {
		return nil, fmt.Errorf("mock OAuth server %s not running", musterOAuthServerName)
	}

	if h.debug {
		h.logger.Debug("🔐 Simulating muster auth login using OAuth server %s (issuer: %s)\n",
			musterOAuthServerName, musterOAuthServerInfo.IssuerURL)
	}

	baseURL := pkgoauth.NormalizeServerURL(h.currentInstance.Endpoint)

	// Get the client ID from the mock OAuth server config - this is the client muster uses to talk to Dex
	dexClientID := musterOAuthServer.GetClientID()

	// Client ID and redirect URI for muster's OAuth server
	musterClientID := baseURL + "/.well-known/oauth-client.json" // Self-hosted CIMD client ID
	musterRedirectURI := baseURL + "/oauth/callback"

	// Use all accepted scopes from the mock OAuth server to ensure downstream MCP servers
	// can validate tokens with their required scopes (e.g., mcp:admin)
	scopes := musterOAuthServer.GetScopes()
	scope := strings.Join(scopes, " ")

	// Step 0: Register the client with muster's OAuth server via Dynamic Client Registration
	// This is required before we can start the authorization flow
	registerURL := baseURL + "/oauth/register"
	registerPayload := map[string]interface{}{
		"client_name":                "muster-test-client",
		"redirect_uris":              []string{musterRedirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none", // Public client
		"application_type":           "web",
		"client_uri":                 musterClientID,
	}

	registerBody, err := json.Marshal(registerPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal registration payload: %w", err)
	}

	registerReq, err := http.NewRequestWithContext(ctx, http.MethodPost, registerURL, strings.NewReader(string(registerBody)))
	if err != nil {
		return nil, fmt.Errorf("failed to create registration request: %w", err)
	}
	registerReq.Header.Set("Content-Type", "application/json")

	registerClient := &http.Client{Timeout: 30 * time.Second}
	registerResp, err := registerClient.Do(registerReq)
	if err != nil {
		return nil, fmt.Errorf("client registration request failed: %w", err)
	}
	defer registerResp.Body.Close()

	if registerResp.StatusCode != http.StatusCreated && registerResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(registerResp.Body, 4096))
		return nil, fmt.Errorf("client registration failed (status %d): %s", registerResp.StatusCode, string(body))
	}

	var registrationResult struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(registerResp.Body).Decode(&registrationResult); err != nil {
		return nil, fmt.Errorf("failed to decode registration response: %w", err)
	}

	// Use the registered client ID (muster may assign its own)
	registeredClientID := registrationResult.ClientID
	if registeredClientID == "" {
		registeredClientID = musterClientID // Fall back to what we sent
	}

	if h.debug {
		h.logger.Debug("🔐 Registered client with ID: %s\n", registeredClientID)
	}

	// Step 1: Call muster's /oauth/authorize endpoint
	// This starts the OAuth flow and redirects to the upstream Dex
	// State must be at least 32 characters for security
	musterState := fmt.Sprintf("muster-auth-test-%d-%d", time.Now().UnixNano(), time.Now().Unix())

	// Generate PKCE code verifier and challenge (required by OAuth 2.1)
	pkce, err := pkgoauth.GeneratePKCE()
	if err != nil {
		return nil, fmt.Errorf("failed to generate PKCE: %w", err)
	}

	authorizeURL := fmt.Sprintf("%s/oauth/authorize?response_type=code&client_id=%s&redirect_uri=%s&scope=%s&state=%s&code_challenge=%s&code_challenge_method=%s",
		baseURL,
		url.QueryEscape(registeredClientID),
		url.QueryEscape(musterRedirectURI),
		url.QueryEscape(scope),
		url.QueryEscape(musterState),
		url.QueryEscape(pkce.CodeChallenge),
		url.QueryEscape(pkce.CodeChallengeMethod),
	)

	if h.debug {
		h.logger.Debug("🔐 Calling muster authorize: %s\n", authorizeURL)
	}

	// Create HTTP client that follows redirects to get to Dex's authorize endpoint
	var dexAuthURL string
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// Capture the Dex authorize URL
			if strings.Contains(req.URL.Path, "/authorize") {
				dexAuthURL = req.URL.String()
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authorizeURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create authorize request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("authorize request failed: %w", err)
	}
	defer resp.Body.Close()

	// If we didn't capture the Dex auth URL during redirects, check the Location header
	if dexAuthURL == "" {
		dexAuthURL = resp.Header.Get("Location")
	}

	if dexAuthURL == "" {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("no Dex redirect URL from authorize endpoint (status: %d, body: %s)", resp.StatusCode, string(body))
	}

	if h.debug {
		h.logger.Debug("🔐 Got Dex auth URL: %s\n", dexAuthURL)
	}

	// Step 2: Parse the Dex auth URL to extract the state and redirect_uri
	parsedDexURL, err := url.Parse(dexAuthURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Dex auth URL: %w", err)
	}

	dexState := parsedDexURL.Query().Get("state")
	dexRedirectURI := parsedDexURL.Query().Get("redirect_uri")
	dexScope := parsedDexURL.Query().Get("scope")
	codeChallenge := parsedDexURL.Query().Get("code_challenge")
	codeChallengeMethod := parsedDexURL.Query().Get("code_challenge_method")

	if dexState == "" {
		return nil, fmt.Errorf("no state in Dex auth URL")
	}

	if h.debug {
		h.logger.Debug("🔐 Dex state=%s..., redirect_uri=%s\n", dexState[:min(16, len(dexState))], dexRedirectURI)
	}

	// Step 3: Generate an auth code on the mock Dex server
	authCode := musterOAuthServer.GenerateAuthCode(dexClientID, dexRedirectURI, dexScope, dexState, codeChallenge, codeChallengeMethod)

	if h.debug {
		h.logger.Debug("🔐 Generated Dex auth code: %s...\n", authCode[:min(16, len(authCode))])
	}

	// Step 4: Call the Dex callback (which redirects to muster's callback)
	dexCallbackURL := fmt.Sprintf("%s?code=%s&state=%s", dexRedirectURI, url.QueryEscape(authCode), url.QueryEscape(dexState))

	if h.debug {
		h.logger.Debug("🔐 Calling Dex callback: %s\n", dexCallbackURL)
	}

	callbackClient := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	callbackReq, err := http.NewRequestWithContext(ctx, http.MethodGet, dexCallbackURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create callback request: %w", err)
	}

	callbackResp, err := callbackClient.Do(callbackReq)
	if err != nil {
		return nil, fmt.Errorf("callback request failed: %w", err)
	}
	defer callbackResp.Body.Close()

	if h.debug {
		h.logger.Debug("🔐 Callback response status: %d\n", callbackResp.StatusCode)
	}

	// The callback should redirect with a muster authorization code.
	// We need to exchange that code for a muster access token so that
	// subsequent MCP requests use a token that is stored in muster's
	// token store (which maps access tokens to upstream provider tokens).
	if callbackResp.StatusCode < 200 || callbackResp.StatusCode >= 400 {
		return nil, fmt.Errorf("muster callback returned error status: %d", callbackResp.StatusCode)
	}

	// Extract the muster auth code from the redirect Location header
	location := callbackResp.Header.Get("Location")
	musterAuthCode := ""
	if location != "" {
		parsedLocation, err := url.Parse(location)
		if err == nil {
			musterAuthCode = parsedLocation.Query().Get("code")
		}
	}

	if musterAuthCode == "" {
		if h.debug {
			h.logger.Debug("🔐 No muster auth code in redirect, falling back to ID token store only\n")
		}
		return map[string]interface{}{
			"success":      true,
			"message":      "Muster auth login completed - ID token stored for SSO forwarding",
			"oauth_server": musterOAuthServerName,
			"issuer_url":   musterOAuthServerInfo.IssuerURL,
		}, nil
	}

	// Step 5: Exchange the muster auth code for a muster access token at /oauth/token.
	// This stores the upstream provider token under the muster access token key,
	// which is required for the access-token-keyed provider token lookup after refresh.
	tokenResult, err := exchangeOAuthToken(ctx, baseURL+"/oauth/token", url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {musterAuthCode},
		"redirect_uri":  {musterRedirectURI},
		"client_id":     {registeredClientID},
		"code_verifier": {pkce.CodeVerifier},
	})
	if err != nil {
		return nil, fmt.Errorf("code exchange failed: %w", err)
	}

	if h.debug {
		h.logger.Debug("🔐 Obtained muster access token, reconnecting MCP client\n")
	}

	h.currentInstance.MusterOAuthAccessToken = tokenResult.AccessToken
	h.currentInstance.MusterOAuthRefreshToken = tokenResult.RefreshToken
	h.currentInstance.MusterOAuthClientID = registeredClientID
	if h.mcpClient != nil {
		h.mcpClient.Close()
		newClient := NewMCPTestClientWithLogger(h.debug, h.logger)
		if err := newClient.ConnectWithAuth(ctx, h.currentInstance.Endpoint, tokenResult.AccessToken); err != nil {
			return nil, fmt.Errorf("failed to reconnect with muster access token: %w", err)
		}
		h.mcpClient = newClient
	}

	return map[string]interface{}{
		"success":      true,
		"message":      "Muster auth login completed - ID token stored for SSO forwarding",
		"oauth_server": musterOAuthServerName,
		"issuer_url":   musterOAuthServerInfo.IssuerURL,
	}, nil
}
