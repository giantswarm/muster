# OAuth BDD Testing Implementation Plan

## Overview

This document provides a detailed implementation plan for testing OAuth authentication locally in the muster BDD test framework. The goal is to enable testing of:

1. **ADR-004**: OAuth Proxy (Muster Server → Remote MCP Servers)
2. **ADR-005**: Muster Server Auth (Agent → Muster Server)
3. **ADR-008**: Unified Authentication (auth status polling, `_meta` fields, SSO detection)

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                           BDD Test Scenario                                      │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                  │
│  ┌──────────────┐     ┌──────────────┐     ┌──────────────────────────────────┐│
│  │  MCP Test    │────▶│   Muster     │────▶│  Mock OAuth-Protected MCP Server ││
│  │   Client     │     │   Serve      │     │  (requires authentication)       ││
│  └──────────────┘     │              │     └───────────────┬──────────────────┘│
│                       │  (OAuth      │                     │                    │
│                       │   enabled)   │                     │                    │
│                       └──────┬───────┘                     │                    │
│                              │                             │                    │
│                              ▼                             ▼                    │
│                       ┌──────────────────────────────────────┐                  │
│                       │        Mock OAuth Server              │                  │
│                       │  - /.well-known/oauth-authorization-server              │
│                       │  - /authorize                        │                  │
│                       │  - /token                            │                  │
│                       └──────────────────────────────────────┘                  │
│                                                                                  │
└─────────────────────────────────────────────────────────────────────────────────┘
```

## Implementation Components

### 1. Mock OAuth Server (`internal/testing/mock/oauth_server.go`)

A lightweight OAuth 2.1 server for testing without real IdP dependencies.

```go
// internal/testing/mock/oauth_server.go
package mock

import (
    "context"
    "crypto/rand"
    "encoding/base64"
    "encoding/json"
    "fmt"
    "net"
    "net/http"
    "net/url"
    "sync"
    "time"
)

// OAuthServerConfig configures the mock OAuth server behavior
type OAuthServerConfig struct {
    // Issuer is the OAuth issuer identifier (e.g., "http://localhost:9999")
    Issuer string

    // AcceptedScopes lists scopes the server will accept
    AcceptedScopes []string

    // TokenLifetime is how long tokens remain valid
    TokenLifetime time.Duration

    // SimulateErrors can be set to simulate various error conditions
    SimulateErrors *OAuthErrorSimulation

    // Debug enables debug logging
    Debug bool

    // PKCERequired enforces PKCE flow
    PKCERequired bool

    // AutoApprove skips user consent in tests
    AutoApprove bool
}

// OAuthErrorSimulation allows simulating error conditions
type OAuthErrorSimulation struct {
    // TokenEndpointError returns this error from /token
    TokenEndpointError string

    // AuthorizeEndpointDelay adds delay to /authorize
    AuthorizeEndpointDelay time.Duration

    // InvalidGrant rejects all token exchanges
    InvalidGrant bool
}

// OAuthServer is a mock OAuth 2.1 authorization server
type OAuthServer struct {
    config     OAuthServerConfig
    httpServer *http.Server
    listener   net.Listener
    port       int
    running    bool
    mu         sync.RWMutex

    // State tracking
    authCodes     map[string]*authCodeEntry   // code -> entry
    pkceVerifiers map[string]string           // code -> verifier
    issuedTokens  map[string]*issuedToken     // access_token -> token info
}

type authCodeEntry struct {
    ClientID    string
    RedirectURI string
    Scope       string
    State       string
    CodeChallenge string
    ChallengeMethod string
    CreatedAt   time.Time
}

type issuedToken struct {
    AccessToken  string
    RefreshToken string
    Scope        string
    ClientID     string
    ExpiresAt    time.Time
}

// NewOAuthServer creates a new mock OAuth server
func NewOAuthServer(config OAuthServerConfig) *OAuthServer {
    if config.TokenLifetime == 0 {
        config.TokenLifetime = 1 * time.Hour
    }
    if len(config.AcceptedScopes) == 0 {
        config.AcceptedScopes = []string{"openid", "profile", "email"}
    }

    return &OAuthServer{
        config:        config,
        authCodes:     make(map[string]*authCodeEntry),
        pkceVerifiers: make(map[string]string),
        issuedTokens:  make(map[string]*issuedToken),
    }
}

// Start starts the OAuth server on a random available port
func (s *OAuthServer) Start(ctx context.Context) (int, error) {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.running {
        return s.port, nil
    }

    // Listen on random port
    listener, err := net.Listen("tcp", ":0")
    if err != nil {
        return 0, fmt.Errorf("failed to listen: %w", err)
    }

    s.listener = listener
    s.port = listener.Addr().(*net.TCPAddr).Port

    // Update issuer with actual port if it's a placeholder
    if s.config.Issuer == "" {
        s.config.Issuer = fmt.Sprintf("http://localhost:%d", s.port)
    }

    mux := http.NewServeMux()
    mux.HandleFunc("/.well-known/oauth-authorization-server", s.handleMetadata)
    mux.HandleFunc("/.well-known/openid-configuration", s.handleMetadata)
    mux.HandleFunc("/authorize", s.handleAuthorize)
    mux.HandleFunc("/token", s.handleToken)
    mux.HandleFunc("/userinfo", s.handleUserInfo)

    s.httpServer = &http.Server{Handler: mux}

    go func() {
        if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
            if s.config.Debug {
                fmt.Printf("OAuth server error: %v\n", err)
            }
        }
    }()

    s.running = true
    return s.port, nil
}

// Stop stops the OAuth server
func (s *OAuthServer) Stop(ctx context.Context) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if !s.running {
        return nil
    }

    err := s.httpServer.Shutdown(ctx)
    s.running = false
    return err
}

// GetIssuerURL returns the full issuer URL
func (s *OAuthServer) GetIssuerURL() string {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.config.Issuer
}

// GetMetadataURL returns the OAuth metadata URL
func (s *OAuthServer) GetMetadataURL() string {
    return s.GetIssuerURL() + "/.well-known/oauth-authorization-server"
}

// SimulateCallback simulates a user completing OAuth flow
// This is called by tests to complete authentication without a real browser
func (s *OAuthServer) SimulateCallback(code string) (*TokenResponse, error) {
    s.mu.RLock()
    entry, exists := s.authCodes[code]
    s.mu.RUnlock()

    if !exists {
        return nil, fmt.Errorf("invalid authorization code")
    }

    // Exchange code for tokens
    accessToken := generateToken()
    refreshToken := generateToken()

    token := &issuedToken{
        AccessToken:  accessToken,
        RefreshToken: refreshToken,
        Scope:        entry.Scope,
        ClientID:     entry.ClientID,
        ExpiresAt:    time.Now().Add(s.config.TokenLifetime),
    }

    s.mu.Lock()
    s.issuedTokens[accessToken] = token
    delete(s.authCodes, code)
    s.mu.Unlock()

    return &TokenResponse{
        AccessToken:  accessToken,
        RefreshToken: refreshToken,
        TokenType:    "Bearer",
        ExpiresIn:    int(s.config.TokenLifetime.Seconds()),
        Scope:        entry.Scope,
    }, nil
}

// ValidateToken checks if a token is valid
func (s *OAuthServer) ValidateToken(accessToken string) bool {
    s.mu.RLock()
    defer s.mu.RUnlock()

    token, exists := s.issuedTokens[accessToken]
    if !exists {
        return false
    }

    return time.Now().Before(token.ExpiresAt)
}

// handleMetadata returns OAuth 2.1 server metadata
func (s *OAuthServer) handleMetadata(w http.ResponseWriter, r *http.Request) {
    metadata := map[string]interface{}{
        "issuer":                 s.config.Issuer,
        "authorization_endpoint": s.config.Issuer + "/authorize",
        "token_endpoint":         s.config.Issuer + "/token",
        "userinfo_endpoint":      s.config.Issuer + "/userinfo",
        "jwks_uri":               s.config.Issuer + "/jwks",
        "response_types_supported": []string{"code"},
        "grant_types_supported":    []string{"authorization_code", "refresh_token"},
        "token_endpoint_auth_methods_supported": []string{"none", "client_secret_post"},
        "scopes_supported":         s.config.AcceptedScopes,
        "code_challenge_methods_supported": []string{"S256", "plain"},
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(metadata)
}

// handleAuthorize handles authorization requests
func (s *OAuthServer) handleAuthorize(w http.ResponseWriter, r *http.Request) {
    if s.config.SimulateErrors != nil && s.config.SimulateErrors.AuthorizeEndpointDelay > 0 {
        time.Sleep(s.config.SimulateErrors.AuthorizeEndpointDelay)
    }

    clientID := r.URL.Query().Get("client_id")
    redirectURI := r.URL.Query().Get("redirect_uri")
    scope := r.URL.Query().Get("scope")
    state := r.URL.Query().Get("state")
    codeChallenge := r.URL.Query().Get("code_challenge")
    codeChallengeMethod := r.URL.Query().Get("code_challenge_method")

    if s.config.PKCERequired && codeChallenge == "" {
        http.Error(w, "PKCE required", http.StatusBadRequest)
        return
    }

    // Generate authorization code
    code := generateToken()

    s.mu.Lock()
    s.authCodes[code] = &authCodeEntry{
        ClientID:        clientID,
        RedirectURI:     redirectURI,
        Scope:           scope,
        State:           state,
        CodeChallenge:   codeChallenge,
        ChallengeMethod: codeChallengeMethod,
        CreatedAt:       time.Now(),
    }
    s.mu.Unlock()

    if s.config.AutoApprove {
        // Auto-redirect with code (simulating user approval)
        redirectURL, _ := url.Parse(redirectURI)
        q := redirectURL.Query()
        q.Set("code", code)
        if state != "" {
            q.Set("state", state)
        }
        redirectURL.RawQuery = q.Encode()

        http.Redirect(w, r, redirectURL.String(), http.StatusFound)
        return
    }

    // Return HTML page for manual testing (shows code for test to capture)
    w.Header().Set("Content-Type", "text/html")
    fmt.Fprintf(w, `<html><body>
        <h1>Mock OAuth Server</h1>
        <p>Authorization Code: <code id="code">%s</code></p>
        <p>State: <code>%s</code></p>
        <p>Redirect URI: <code>%s</code></p>
        <a href="%s?code=%s&state=%s">Complete Authorization</a>
    </body></html>`, code, state, redirectURI, redirectURI, code, state)
}

// handleToken handles token exchange requests
func (s *OAuthServer) handleToken(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }

    if err := r.ParseForm(); err != nil {
        http.Error(w, "invalid request", http.StatusBadRequest)
        return
    }

    grantType := r.FormValue("grant_type")

    if s.config.SimulateErrors != nil {
        if s.config.SimulateErrors.TokenEndpointError != "" {
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusBadRequest)
            json.NewEncoder(w).Encode(map[string]string{
                "error":             "server_error",
                "error_description": s.config.SimulateErrors.TokenEndpointError,
            })
            return
        }
        if s.config.SimulateErrors.InvalidGrant {
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusBadRequest)
            json.NewEncoder(w).Encode(map[string]string{
                "error":             "invalid_grant",
                "error_description": "authorization code is invalid",
            })
            return
        }
    }

    switch grantType {
    case "authorization_code":
        s.handleAuthCodeExchange(w, r)
    case "refresh_token":
        s.handleRefreshToken(w, r)
    default:
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusBadRequest)
        json.NewEncoder(w).Encode(map[string]string{
            "error":             "unsupported_grant_type",
            "error_description": fmt.Sprintf("grant_type %s not supported", grantType),
        })
    }
}

func (s *OAuthServer) handleAuthCodeExchange(w http.ResponseWriter, r *http.Request) {
    code := r.FormValue("code")
    codeVerifier := r.FormValue("code_verifier")

    s.mu.Lock()
    entry, exists := s.authCodes[code]
    if exists {
        delete(s.authCodes, code)
    }
    s.mu.Unlock()

    if !exists {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusBadRequest)
        json.NewEncoder(w).Encode(map[string]string{
            "error":             "invalid_grant",
            "error_description": "authorization code not found or expired",
        })
        return
    }

    // PKCE verification
    if entry.CodeChallenge != "" && codeVerifier == "" {
        w.Header().Set("Content-Type", "application/json")
        w.WriteHeader(http.StatusBadRequest)
        json.NewEncoder(w).Encode(map[string]string{
            "error":             "invalid_grant",
            "error_description": "code_verifier required",
        })
        return
    }

    // TODO: Verify code_verifier against code_challenge if needed

    // Issue tokens
    accessToken := generateToken()
    refreshToken := generateToken()

    token := &issuedToken{
        AccessToken:  accessToken,
        RefreshToken: refreshToken,
        Scope:        entry.Scope,
        ClientID:     entry.ClientID,
        ExpiresAt:    time.Now().Add(s.config.TokenLifetime),
    }

    s.mu.Lock()
    s.issuedTokens[accessToken] = token
    s.mu.Unlock()

    response := TokenResponse{
        AccessToken:  accessToken,
        RefreshToken: refreshToken,
        TokenType:    "Bearer",
        ExpiresIn:    int(s.config.TokenLifetime.Seconds()),
        Scope:        entry.Scope,
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(response)
}

func (s *OAuthServer) handleRefreshToken(w http.ResponseWriter, r *http.Request) {
    // Simplified refresh - in tests we mainly care about the flow
    accessToken := generateToken()

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(TokenResponse{
        AccessToken: accessToken,
        TokenType:   "Bearer",
        ExpiresIn:   int(s.config.TokenLifetime.Seconds()),
    })
}

func (s *OAuthServer) handleUserInfo(w http.ResponseWriter, r *http.Request) {
    auth := r.Header.Get("Authorization")
    if auth == "" || len(auth) < 7 {
        w.WriteHeader(http.StatusUnauthorized)
        return
    }

    token := auth[7:] // Remove "Bearer "
    if !s.ValidateToken(token) {
        w.WriteHeader(http.StatusUnauthorized)
        return
    }

    userInfo := map[string]interface{}{
        "sub":   "test-user-123",
        "name":  "Test User",
        "email": "test@example.com",
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(userInfo)
}

// TokenResponse is the OAuth token response
type TokenResponse struct {
    AccessToken  string `json:"access_token"`
    RefreshToken string `json:"refresh_token,omitempty"`
    TokenType    string `json:"token_type"`
    ExpiresIn    int    `json:"expires_in"`
    Scope        string `json:"scope,omitempty"`
}

func generateToken() string {
    b := make([]byte, 32)
    rand.Read(b)
    return base64.RawURLEncoding.EncodeToString(b)
}
```

### 2. OAuth-Protected Mock MCP Server (`internal/testing/mock/protected_mcp_server.go`)

Extends the existing mock MCP server to require OAuth authentication.

```go
// internal/testing/mock/protected_mcp_server.go
package mock

import (
    "context"
    "fmt"
    "net"
    "net/http"
    "strings"
    "sync"
    "time"

    "github.com/mark3labs/mcp-go/server"
)

// ProtectedMCPServerConfig configures an OAuth-protected mock MCP server
type ProtectedMCPServerConfig struct {
    // OAuthServer is the mock OAuth server to validate tokens against
    OAuthServer *OAuthServer

    // Issuer is the expected token issuer
    Issuer string

    // RequiredScope is the OAuth scope required to access this server
    RequiredScope string

    // Tools are the tools to expose when authenticated
    Tools []ToolConfig

    // Transport is the HTTP transport type (sse or streamable-http)
    Transport HTTPTransportType

    // Debug enables debug logging
    Debug bool
}

// ProtectedMCPServer is a mock MCP server that requires OAuth authentication
type ProtectedMCPServer struct {
    config     ProtectedMCPServerConfig
    mockServer *Server
    httpServer *http.Server
    listener   net.Listener
    port       int
    running    bool
    mu         sync.RWMutex
}

// NewProtectedMCPServer creates a new OAuth-protected mock MCP server
func NewProtectedMCPServer(config ProtectedMCPServerConfig) (*ProtectedMCPServer, error) {
    // Create the underlying mock server with the tools
    // We need to create a temporary config file or use in-memory config
    mockServer := &Server{
        name:         "protected-mock",
        tools:        config.Tools,
        toolHandlers: make(map[string]*ToolHandler),
        debug:        config.Debug,
    }

    return &ProtectedMCPServer{
        config:     config,
        mockServer: mockServer,
    }, nil
}

// Start starts the protected MCP server
func (s *ProtectedMCPServer) Start(ctx context.Context) (int, error) {
    s.mu.Lock()
    defer s.mu.Unlock()

    if s.running {
        return s.port, nil
    }

    listener, err := net.Listen("tcp", ":0")
    if err != nil {
        return 0, err
    }

    s.listener = listener
    s.port = listener.Addr().(*net.TCPAddr).Port

    // Create the protected HTTP handler
    handler := s.createProtectedHandler()

    s.httpServer = &http.Server{Handler: handler}

    go func() {
        if err := s.httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
            if s.config.Debug {
                fmt.Printf("Protected MCP server error: %v\n", err)
            }
        }
    }()

    s.running = true
    return s.port, nil
}

// Stop stops the protected MCP server
func (s *ProtectedMCPServer) Stop(ctx context.Context) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if !s.running {
        return nil
    }

    err := s.httpServer.Shutdown(ctx)
    s.running = false
    return err
}

// Endpoint returns the MCP endpoint URL
func (s *ProtectedMCPServer) Endpoint() string {
    s.mu.RLock()
    defer s.mu.RUnlock()

    switch s.config.Transport {
    case HTTPTransportSSE:
        return fmt.Sprintf("http://localhost:%d/sse", s.port)
    default:
        return fmt.Sprintf("http://localhost:%d/mcp", s.port)
    }
}

// GetIssuer returns the OAuth issuer for this server
func (s *ProtectedMCPServer) GetIssuer() string {
    return s.config.Issuer
}

// createProtectedHandler wraps the MCP handler with OAuth validation
func (s *ProtectedMCPServer) createProtectedHandler() http.Handler {
    // Create the underlying MCP handler
    mcpServer := server.NewMCPServer(
        "protected-mock",
        "1.0.0",
        server.WithToolCapabilities(false),
    )

    // Register tools
    for _, tool := range s.config.Tools {
        handler := NewToolHandler(tool, nil, s.config.Debug)
        s.mockServer.toolHandlers[tool.Name] = handler
        // Note: simplified - would need to properly register tools
    }

    var underlyingHandler http.Handler
    switch s.config.Transport {
    case HTTPTransportSSE:
        baseURL := fmt.Sprintf("http://localhost:%d", s.port)
        underlyingHandler = server.NewSSEServer(
            mcpServer,
            server.WithBaseURL(baseURL),
            server.WithSSEEndpoint("/sse"),
        )
    default:
        underlyingHandler = server.NewStreamableHTTPServer(mcpServer)
    }

    // Wrap with OAuth protection
    return &oauthProtectionMiddleware{
        handler:     underlyingHandler,
        oauthServer: s.config.OAuthServer,
        issuer:      s.config.Issuer,
        debug:       s.config.Debug,
    }
}

// oauthProtectionMiddleware validates OAuth tokens before passing to MCP handler
type oauthProtectionMiddleware struct {
    handler     http.Handler
    oauthServer *OAuthServer
    issuer      string
    debug       bool
}

func (m *oauthProtectionMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    // Check for Authorization header
    auth := r.Header.Get("Authorization")
    if auth == "" || !strings.HasPrefix(auth, "Bearer ") {
        // Return 401 with OAuth challenge
        m.sendAuthChallenge(w)
        return
    }

    token := strings.TrimPrefix(auth, "Bearer ")

    // Validate token with the OAuth server
    if m.oauthServer != nil && !m.oauthServer.ValidateToken(token) {
        m.sendAuthChallenge(w)
        return
    }

    // Token valid - pass through to MCP handler
    m.handler.ServeHTTP(w, r)
}

func (m *oauthProtectionMiddleware) sendAuthChallenge(w http.ResponseWriter) {
    // Send WWW-Authenticate header per RFC 9728
    authHeader := fmt.Sprintf(`Bearer realm="%s"`, m.issuer)
    w.Header().Set("WWW-Authenticate", authHeader)
    w.WriteHeader(http.StatusUnauthorized)
}

// Port returns the port the server is running on
func (s *ProtectedMCPServer) Port() int {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.port
}
```

### 3. Extended Testing Types (`internal/testing/types.go` additions)

Add OAuth configuration support to the existing types:

```go
// Add to MusterPreConfiguration struct
type MusterPreConfiguration struct {
    // ... existing fields ...

    // OAuthConfig defines OAuth configuration for this test instance
    OAuthConfig *TestOAuthConfig `yaml:"oauth_config,omitempty"`

    // MockOAuthServers defines mock OAuth servers to start for testing
    MockOAuthServers []MockOAuthServerConfig `yaml:"mock_oauth_servers,omitempty"`
}

// TestOAuthConfig defines OAuth configuration for test scenarios
type TestOAuthConfig struct {
    // Enabled turns on OAuth protection for the muster instance
    Enabled bool `yaml:"enabled"`

    // PublicURL is the public URL for OAuth callbacks
    PublicURL string `yaml:"public_url,omitempty"`

    // Provider specifies the OAuth provider (mock, google, dex)
    Provider string `yaml:"provider,omitempty"`

    // MockOAuthServerRef references a mock OAuth server defined in MockOAuthServers
    MockOAuthServerRef string `yaml:"mock_oauth_server_ref,omitempty"`
}

// MockOAuthServerConfig defines a mock OAuth server for testing
type MockOAuthServerConfig struct {
    // Name is the unique identifier for this OAuth server
    Name string `yaml:"name"`

    // Issuer is the OAuth issuer URL (auto-generated if not specified)
    Issuer string `yaml:"issuer,omitempty"`

    // Scopes are the scopes this OAuth server accepts
    Scopes []string `yaml:"scopes,omitempty"`

    // AutoApprove automatically approves authentication requests
    AutoApprove bool `yaml:"auto_approve,omitempty"`

    // PKCERequired enforces PKCE flow
    PKCERequired bool `yaml:"pkce_required,omitempty"`

    // TokenLifetime is how long tokens are valid
    TokenLifetime string `yaml:"token_lifetime,omitempty"`

    // SimulateError can simulate error conditions
    SimulateError string `yaml:"simulate_error,omitempty"`
}

// Add to MCPServerConfig
type MCPServerConfig struct {
    // ... existing fields ...

    // OAuth defines OAuth protection for this MCP server
    OAuth *MCPServerOAuthConfig `yaml:"oauth,omitempty"`
}

// MCPServerOAuthConfig defines OAuth for an MCP server in tests
type MCPServerOAuthConfig struct {
    // Required indicates this server requires OAuth authentication
    Required bool `yaml:"required"`

    // MockOAuthServerRef references a mock OAuth server
    MockOAuthServerRef string `yaml:"mock_oauth_server_ref"`

    // Scope is the required OAuth scope
    Scope string `yaml:"scope,omitempty"`
}
```

### 4. Updated Muster Instance Manager

Extend `muster_manager.go` to handle OAuth configuration:

```go
// Add to musterInstanceManager struct
type musterInstanceManager struct {
    // ... existing fields ...

    // Mock OAuth servers for this manager
    mockOAuthServers map[string]map[string]*mock.OAuthServer // instanceID -> serverName -> server
}

// startMockOAuthServers starts mock OAuth servers for a test instance
func (m *musterInstanceManager) startMockOAuthServers(
    ctx context.Context,
    instanceID string,
    config *MusterPreConfiguration,
) (map[string]*MockOAuthServerInfo, error) {
    result := make(map[string]*MockOAuthServerInfo)

    if config == nil || len(config.MockOAuthServers) == 0 {
        return result, nil
    }

    // Initialize the map for this instance
    m.mu.Lock()
    if m.mockOAuthServers[instanceID] == nil {
        m.mockOAuthServers[instanceID] = make(map[string]*mock.OAuthServer)
    }
    m.mu.Unlock()

    for _, oauthCfg := range config.MockOAuthServers {
        tokenLifetime := 1 * time.Hour
        if oauthCfg.TokenLifetime != "" {
            if d, err := time.ParseDuration(oauthCfg.TokenLifetime); err == nil {
                tokenLifetime = d
            }
        }

        serverConfig := mock.OAuthServerConfig{
            Issuer:         oauthCfg.Issuer,
            AcceptedScopes: oauthCfg.Scopes,
            TokenLifetime:  tokenLifetime,
            PKCERequired:   oauthCfg.PKCERequired,
            AutoApprove:    oauthCfg.AutoApprove,
            Debug:          m.debug,
        }

        server := mock.NewOAuthServer(serverConfig)
        port, err := server.Start(ctx)
        if err != nil {
            return nil, fmt.Errorf("failed to start mock OAuth server %s: %w", oauthCfg.Name, err)
        }

        m.mu.Lock()
        m.mockOAuthServers[instanceID][oauthCfg.Name] = server
        m.mu.Unlock()

        result[oauthCfg.Name] = &MockOAuthServerInfo{
            Name:      oauthCfg.Name,
            Port:      port,
            IssuerURL: server.GetIssuerURL(),
        }

        if m.debug {
            m.logger.Debug("✅ Started mock OAuth server %s on port %d (issuer: %s)\n",
                oauthCfg.Name, port, server.GetIssuerURL())
        }
    }

    return result, nil
}

// MockOAuthServerInfo contains info about a running mock OAuth server
type MockOAuthServerInfo struct {
    Name      string
    Port      int
    IssuerURL string
}
```

### 5. BDD Scenario Examples

#### 5.1 Basic OAuth Protection Test

```yaml
# internal/testing/scenarios/oauth/oauth-protected-mcp-server.yaml
name: "oauth-protected-mcp-server"
category: "behavioral"
concept: "oauth"
tags: ["oauth", "adr-004", "authentication"]
timeout: "2m"

pre_configuration:
  # Start a mock OAuth server
  mock_oauth_servers:
    - name: "mock-idp"
      scopes: ["openid", "profile", "mcp:read"]
      auto_approve: true
      pkce_required: true
      token_lifetime: "1h"

  # Configure muster with OAuth enabled
  oauth_config:
    enabled: true
    provider: "mock"
    mock_oauth_server_ref: "mock-idp"

  # OAuth-protected MCP server
  mcp_servers:
    - name: "protected-server"
      config:
        type: "streamable-http"
        oauth:
          required: true
          mock_oauth_server_ref: "mock-idp"
          scope: "mcp:read"
        tools:
          - name: "get_secret"
            description: "Returns a secret value"
            responses:
              - response:
                  secret: "super-secret-value"

steps:
  - id: verify-auth-required
    tool: "x_protected-server_get_secret"
    args: {}
    expected:
      success: false
      error_contains: ["authentication required"]

  - id: authenticate
    tool: "authenticate_protected-server"
    args: {}
    expected:
      success: true
      contains: ["authorization_url"]

  - id: simulate-oauth-callback
    tool: "test_simulate_oauth_callback"
    args:
      server: "protected-server"
    expected:
      success: true

  - id: call-protected-tool
    tool: "x_protected-server_get_secret"
    args: {}
    expected:
      success: true
      contains: ["super-secret-value"]
```

#### 5.2 Auth Meta Propagation Test

```yaml
# internal/testing/scenarios/oauth/auth-meta-propagation.yaml
name: "auth-meta-propagation"
category: "behavioral"
concept: "oauth"
tags: ["oauth", "adr-008", "meta"]
timeout: "2m"

pre_configuration:
  mock_oauth_servers:
    - name: "shared-idp"
      issuer: "https://idp.example.com"
      scopes: ["openid", "profile"]
      auto_approve: true

  mcp_servers:
    - name: "server-a"
      config:
        type: "streamable-http"
        oauth:
          required: true
          mock_oauth_server_ref: "shared-idp"
        tools:
          - name: "tool_a"
            responses:
              - response: { status: "ok" }

    - name: "server-b"
      config:
        type: "streamable-http"
        oauth:
          required: true
          mock_oauth_server_ref: "shared-idp"
        tools:
          - name: "tool_b"
            responses:
              - response: { status: "ok" }

steps:
  - id: call-any-tool-check-meta
    description: "Call a core tool and verify _meta contains auth_required info"
    tool: "core_mcpserver_list"
    args: {}
    expected:
      success: true
      json_path:
        "_meta.giantswarm.io/auth_required.0.server": "server-a"
        "_meta.giantswarm.io/auth_required.0.issuer": "https://idp.example.com"

  - id: verify-sso-hint
    description: "Both servers use same issuer - should show SSO opportunity"
    tool: "core_mcpserver_list"
    args: {}
    expected:
      success: true
      contains: ["same identity provider", "authenticate once"]
```

#### 5.3 Token Refresh Test

```yaml
# internal/testing/scenarios/oauth/token-refresh-flow.yaml
name: "token-refresh-flow"
category: "behavioral"
concept: "oauth"
tags: ["oauth", "token-refresh"]
timeout: "3m"

pre_configuration:
  mock_oauth_servers:
    - name: "short-lived-tokens"
      token_lifetime: "5s"  # Very short-lived tokens for testing refresh
      auto_approve: true

  mcp_servers:
    - name: "protected-api"
      config:
        type: "streamable-http"
        oauth:
          required: true
          mock_oauth_server_ref: "short-lived-tokens"
        tools:
          - name: "check_auth"
            responses:
              - response: { authenticated: true }

steps:
  - id: authenticate
    tool: "authenticate_protected-api"
    args: {}
    expected:
      success: true

  - id: simulate-callback
    tool: "test_simulate_oauth_callback"
    args:
      server: "protected-api"
    expected:
      success: true

  - id: call-tool-immediately
    tool: "x_protected-api_check_auth"
    args: {}
    expected:
      success: true
      json_path:
        "authenticated": true

  - id: wait-for-token-expiry
    tool: "test_wait"
    args:
      duration: "6s"
    expected:
      success: true

  - id: call-tool-after-expiry
    description: "Should auto-refresh or prompt for re-auth"
    tool: "x_protected-api_check_auth"
    args: {}
    expected:
      success: true  # Should succeed due to refresh token
```

### 6. Test Helper Tools

Add test-specific tools that are only available in test mode:

```yaml
# These tools are registered by the test framework, not exposed in production

test_simulate_oauth_callback:
  description: "Simulates completing an OAuth flow for testing"
  input_schema:
    type: object
    properties:
      server:
        type: string
        description: "Name of the server to authenticate to"
    required: ["server"]

test_wait:
  description: "Waits for a specified duration"
  input_schema:
    type: object
    properties:
      duration:
        type: string
        description: "Duration to wait (e.g., '5s', '1m')"
    required: ["duration"]

test_inject_token:
  description: "Directly injects a token for testing"
  input_schema:
    type: object
    properties:
      server:
        type: string
      token:
        type: string
    required: ["server", "token"]
```

## Implementation Phases

### Phase 1: Mock OAuth Server (Week 1)

1. **Create `internal/testing/mock/oauth_server.go`**
   - OAuth 2.1 metadata endpoints
   - Authorization endpoint
   - Token endpoint
   - PKCE support
   - Token validation
   - `SimulateCallback()` for test automation

2. **Unit tests for Mock OAuth Server**
   - Metadata endpoint returns correct format
   - Authorization code flow works
   - PKCE verification
   - Token validation
   - Error simulation

### Phase 2: Protected MCP Server (Week 1-2)

1. **Create `internal/testing/mock/protected_mcp_server.go`**
   - OAuth middleware for HTTP handlers
   - WWW-Authenticate header generation
   - Token validation against mock OAuth server

2. **Integration with existing mock HTTP server**
   - Extend `HTTPServer` to support OAuth protection
   - Wire up to mock OAuth server

### Phase 3: Test Framework Integration (Week 2)

1. **Extend `internal/testing/types.go`**
   - Add OAuth configuration types
   - Add mock OAuth server configuration

2. **Update `internal/testing/muster_manager.go`**
   - Start mock OAuth servers during instance creation
   - Configure muster with OAuth settings
   - Clean up OAuth servers on destroy

3. **Add test helper tools**
   - `test_simulate_oauth_callback`
   - `test_wait`
   - `test_inject_token`

### Phase 4: BDD Scenarios (Week 2-3)

1. **Create test scenarios**
   - `oauth-protected-mcp-server.yaml`
   - `auth-meta-propagation.yaml`
   - `sso-detection.yaml`
   - `token-refresh-flow.yaml`

2. **Run and validate scenarios**
   - Ensure all scenarios pass
   - Debug any issues

### Phase 5: Documentation & CI (Week 3)

1. **Update documentation**
   - Add OAuth testing section to testing docs
   - Document mock OAuth server usage

2. **CI integration**
   - Add OAuth tests to CI pipeline
   - Ensure parallel execution works

## Configuration Examples

### Scenario with Full OAuth Stack

```yaml
name: "full-oauth-stack"
category: "integration"
concept: "oauth"
timeout: "5m"

pre_configuration:
  # Mock OAuth Provider (simulates Dex/Google)
  mock_oauth_servers:
    - name: "enterprise-idp"
      issuer: "https://login.enterprise.example.com"
      scopes: ["openid", "profile", "email", "mcp:admin"]
      auto_approve: true
      pkce_required: true

  # Enable OAuth on muster itself (ADR-005)
  oauth_config:
    enabled: true
    provider: "mock"
    mock_oauth_server_ref: "enterprise-idp"

  # Protected remote MCP servers (ADR-004)
  mcp_servers:
    - name: "kubernetes-mcp"
      config:
        type: "streamable-http"
        oauth:
          required: true
          mock_oauth_server_ref: "enterprise-idp"
          scope: "mcp:admin"
        tools:
          - name: "list_pods"
            responses:
              - response:
                  pods: ["pod-1", "pod-2"]

    - name: "github-mcp"
      config:
        type: "streamable-http"
        oauth:
          required: true
          mock_oauth_server_ref: "enterprise-idp"  # Same IdP = SSO
          scope: "mcp:admin"
        tools:
          - name: "list_repos"
            responses:
              - response:
                  repos: ["repo-1", "repo-2"]

steps:
  # Test SSO detection (ADR-008)
  - id: verify-sso-detection
    tool: "core_mcpserver_list"
    args: {}
    expected:
      success: true
      contains: ["kubernetes-mcp", "github-mcp"]
      # Check _meta shows both need auth from same issuer

  # Authenticate once
  - id: authenticate-first-server
    tool: "authenticate_kubernetes-mcp"
    args: {}
    expected:
      success: true

  - id: complete-first-auth
    tool: "test_simulate_oauth_callback"
    args:
      server: "kubernetes-mcp"
    expected:
      success: true

  # Verify first server works
  - id: call-kubernetes-tool
    tool: "x_kubernetes-mcp_list_pods"
    args: {}
    expected:
      success: true
      contains: ["pod-1", "pod-2"]

  # SSO should work for second server (same issuer)
  - id: call-github-tool-with-sso
    description: "Should work without separate auth due to SSO"
    tool: "x_github-mcp_list_repos"
    args: {}
    expected:
      success: true
      contains: ["repo-1", "repo-2"]
```

## Acceptance Criteria

- [ ] Mock OAuth server implements OAuth 2.1 with PKCE
- [ ] Protected MCP server returns 401 with WWW-Authenticate header
- [ ] Muster instance can be configured with OAuth in BDD tests
- [ ] `test_simulate_oauth_callback` tool automates OAuth flow
- [ ] SSO detection works when multiple servers share issuer
- [ ] Auth status appears in `_meta` field of tool responses
- [ ] Token refresh is handled automatically
- [ ] All OAuth scenarios pass in parallel execution
- [ ] No real OAuth provider needed for BDD tests

## References

- [ADR-004: OAuth Proxy](../../explanation/decisions/004-oauth-proxy.md)
- [ADR-005: Muster Server Auth](../../explanation/decisions/005-muster-auth.md)
- [ADR-008: Unified Authentication](../../explanation/decisions/008-unified-authentication.md)
- [OAuth 2.1 Specification](https://oauth.net/2.1/)
- [RFC 7636: PKCE](https://datatracker.ietf.org/doc/html/rfc7636)
- [RFC 9728: OAuth Protected Resource Metadata](https://datatracker.ietf.org/doc/html/rfc9728)
