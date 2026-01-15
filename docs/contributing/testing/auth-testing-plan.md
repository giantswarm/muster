# Authentication Testing Plan

## Overview

This document outlines a comprehensive test strategy for the muster authentication implementation, covering ADR-004 (OAuth Proxy), ADR-005 (Muster Server Auth), and ADR-008 (Unified Authentication).

## Current State

### Deployed Version (Spidertron)
- **Version**: `0.0.125`
- **Endpoint**: `https://muster.k8s-internal.home.derstappen.com`
- **OAuth Provider**: Google OAuth
- **Session Storage**: Valkey (Redis-compatible)
- **Transport**: Streamable HTTP

### Implementation Components

| Component | Location | Lines | Purpose |
|-----------|----------|-------|---------|
| `auth_wrapper.go` | `internal/agent/` | ~84 | Wraps tool results with auth metadata |
| `auth_manager.go` | `internal/agent/oauth/` | ~473 | OAuth state machine for agent |
| `client.go` | `internal/agent/oauth/` | ~418 | OAuth client with PKCE flow |
| `token_store.go` | `internal/agent/oauth/` | ~434 | XDG-compliant token persistence |
| `callback_server.go` | `internal/agent/oauth/` | ~217 | Local HTTP server for OAuth callbacks |
| `pkg/oauth/types.go` | `pkg/oauth/` | ~230 | Shared types |

## Authentication Layers

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                                                                                 │
│  Layer 1: Cursor/IDE → Agent (stdio)                                            │
│     - No authentication required                                                │
│     - Agent exposes MCP tools to IDE                                            │
│                                                                                 │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Layer 2: Agent → Muster Server (HTTP/SSE) [ADR-005]                            │
│     - Google OAuth (or Dex) protects muster server endpoints                    │
│     - Agent detects 401, creates synthetic `authenticate_muster` tool           │
│     - Local callback server on port 3000                                        │
│     - Token stored: ~/.config/muster/tokens/                                    │
│                                                                                 │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Layer 3: Muster Server → Remote MCP Servers (OAuth Proxy) [ADR-004]            │
│     - Server acts as OAuth client for remote MCPs                               │
│     - Intercepts 401 from remote MCPs                                           │
│     - Exposes `authenticate_<server>` tools                                     │
│     - Token stored: Valkey (server-side, per session)                           │
│                                                                                 │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Cross-Cutting: ADR-008 Unified Authentication                                  │
│     - Agent polls auth://status every 30s                                       │
│     - Every tool response includes _meta["giantswarm.io/auth_required"]         │
│     - SSO detection via issuer grouping                                         │
│                                                                                 │
└─────────────────────────────────────────────────────────────────────────────────┘
```

## Test Categories

### 1. Unit Tests

#### 1.1 Auth Wrapper (internal/agent/auth_wrapper_test.go)

**Existing Coverage:**
- `TestBuildAuthNotification_SingleServer`
- `TestBuildAuthNotification_MultipleServersWithSSO`
- `TestBuildAuthNotification_MultipleServersDifferentIssuers`
- `TestBuildAuthNotification_EmptyIssuer`

**Gaps to Fill:**
```go
// Test wrapToolResultWithAuth behavior
func TestWrapToolResultWithAuth_NoAuthRequired(t *testing.T)
func TestWrapToolResultWithAuth_WithAuthRequired(t *testing.T)
func TestWrapToolResultWithAuth_MetaFieldStructure(t *testing.T)

// Test _meta field JSON serialization
func TestAuthMetaKey_Namespacing(t *testing.T)
```

#### 1.2 Auth Manager (internal/agent/oauth/auth_manager.go)

**Required Tests:**
```go
// State machine transitions
func TestAuthManager_StateTransitions(t *testing.T) {
    // Unknown → PendingAuth (on 401 detection)
    // PendingAuth → Authenticated (on successful token exchange)
    // PendingAuth → Error (on auth failure)
    // Authenticated → Unknown (on token clear)
}

// Connection probing
func TestAuthManager_CheckConnection_NoAuthRequired(t *testing.T)
func TestAuthManager_CheckConnection_AuthRequired(t *testing.T)
func TestAuthManager_CheckConnection_WithValidToken(t *testing.T)

// OAuth metadata discovery
func TestAuthManager_DiscoverOAuthMetadata_RFC9728(t *testing.T)
func TestAuthManager_DiscoverOAuthMetadata_Fallback(t *testing.T)

// URL normalization
func TestNormalizeServerURL(t *testing.T) {
    // "https://example.com/mcp" → "https://example.com"
    // "https://example.com/sse" → "https://example.com"
    // "https://example.com/" → "https://example.com"
}
```

#### 1.3 Token Store (internal/agent/oauth/token_store.go)

**Required Tests:**
```go
// SSO by issuer lookup
func TestTokenStore_GetByIssuer(t *testing.T)
func TestTokenStore_HasValidTokenForIssuer(t *testing.T)

// Token expiry with margin
func TestTokenStore_IsTokenValid_ExpiryMargin(t *testing.T)

// File persistence
func TestTokenStore_FileMode_Persistence(t *testing.T)
func TestTokenStore_FileMode_Permissions(t *testing.T) // 0600 for files, 0700 for dir

// Security: token values never logged
func TestTokenStore_SecurityAuditLogging(t *testing.T)
```

#### 1.4 Callback Server (internal/agent/oauth/callback_server.go)

**Required Tests:**
```go
func TestCallbackServer_Start_PortBinding(t *testing.T)
func TestCallbackServer_HandleCallback_Success(t *testing.T)
func TestCallbackServer_HandleCallback_Error(t *testing.T)
func TestCallbackServer_HandleCallback_StateParameter(t *testing.T)
func TestCallbackServer_WaitForCallback_Timeout(t *testing.T)
func TestCallbackServer_SecurityHeaders(t *testing.T)
```

### 2. Integration Tests

#### 2.1 Mock OAuth Server

Create a lightweight OAuth server for testing without real IdP dependencies:

```go
// internal/testing/mock/oauth_server.go
type MockOAuthServer struct {
    Issuer           string
    AuthorizeHandler func(w http.ResponseWriter, r *http.Request)
    TokenHandler     func(w http.ResponseWriter, r *http.Request)
    
    // Configurable responses
    TokenResponse    *oauth2.Token
    TokenError       error
    
    // PKCE validation
    ValidatePKCE     bool
    PKCEVerifier     string
}

// Methods
func (m *MockOAuthServer) Start(port int) error
func (m *MockOAuthServer) Stop() error
func (m *MockOAuthServer) GetIssuerURL() string
func (m *MockOAuthServer) GetMetadataURL() string

// Well-known endpoints served automatically:
// /.well-known/oauth-authorization-server
// /.well-known/openid-configuration
// /authorize
// /token
```

#### 2.2 Mock Protected MCP Server

Create an MCP server that requires OAuth authentication:

```go
// internal/testing/mock/protected_mcp_server.go
type ProtectedMCPServer struct {
    IssuerURL     string
    RequiredScope string
    ValidTokens   map[string]bool  // token -> valid
    
    // MCP tools to expose when authenticated
    Tools         []mcp.Tool
}

// Returns 401 with WWW-Authenticate header if no valid token
// Returns tools and handles calls if authenticated
```

#### 2.3 Integration Test Scenarios

```go
// tests/integration/oauth_test.go

func TestAgentToServerAuth_FullFlow(t *testing.T) {
    // 1. Start mock OAuth server
    // 2. Start muster server with mock OAuth config
    // 3. Start agent connecting to server
    // 4. Verify agent exposes authenticate_muster tool
    // 5. Simulate OAuth callback
    // 6. Verify agent transitions to authenticated
    // 7. Verify real tools are now exposed
}

func TestProxyAuth_FullFlow(t *testing.T) {
    // 1. Start mock OAuth server (for remote MCP)
    // 2. Start protected mock MCP server
    // 3. Start muster server (authenticated)
    // 4. Start agent
    // 5. Call tool that requires remote auth
    // 6. Verify authenticate_<server> tool appears in _meta
    // 7. Simulate OAuth callback
    // 8. Retry tool call, verify success
}

func TestSSODetection(t *testing.T) {
    // 1. Start mock OAuth server (shared issuer)
    // 2. Start two protected MCP servers with same issuer
    // 3. Authenticate to first server
    // 4. Verify _meta shows SSO hint for second server
    // 5. Verify second server auth reuses token
}
```

### 3. Scenario Tests (muster test framework)

Create YAML scenarios for the existing test framework:

#### 3.1 Auth Meta Propagation

```yaml
# internal/testing/scenarios/behavioral/oauth/auth-meta-propagation.yaml
name: "auth-meta-propagation"
category: "behavioral"
concept: "oauth"
description: "Verify auth status is included in tool response _meta field"
tags: ["oauth", "adr-008", "meta"]

pre_configuration:
  mcp_servers:
    - name: "protected-server"
      config:
        oauth:
          required: true
          issuer: "https://mock-idp.example.com"
        tools:
          - name: "protected_tool"
            description: "A tool requiring auth"

steps:
  - name: "call-any-tool"
    description: "Call a tool and check _meta for auth status"
    tool: "core_mcpserver_list"
    expected:
      success: true
      meta_contains:
        "giantswarm.io/auth_required":
          - server: "protected-server"
            issuer: "https://mock-idp.example.com"
            auth_tool: "x_protected-server_authenticate"
```

#### 3.2 SSO Detection

```yaml
# internal/testing/scenarios/behavioral/oauth/sso-detection.yaml
name: "sso-detection"
category: "behavioral"
concept: "oauth"
description: "Verify SSO hints when multiple servers share same issuer"
tags: ["oauth", "adr-008", "sso"]

pre_configuration:
  mcp_servers:
    - name: "server-a"
      config:
        oauth:
          required: true
          issuer: "https://shared-idp.example.com"
        tools:
          - name: "tool_a"
    - name: "server-b"
      config:
        oauth:
          required: true
          issuer: "https://shared-idp.example.com"  # Same issuer
        tools:
          - name: "tool_b"

steps:
  - name: "verify-sso-hint"
    description: "Auth notification should mention SSO opportunity"
    tool: "core_mcpserver_list"
    expected:
      success: true
      response_contains:
        - "same identity provider"
        - "shared-idp.example.com"
```

### 4. E2E Tests Against Spidertron

For testing against the real deployment:

#### 4.1 Prerequisites

```bash
# Verify connectivity
curl -s https://muster.k8s-internal.home.derstappen.com/.well-known/oauth-protected-resource

# Check OAuth configuration
curl -s https://muster.k8s-internal.home.derstappen.com/.well-known/oauth-authorization-server
```

#### 4.2 Manual Test Flow

```bash
# 1. Start agent without existing token
rm -rf ~/.config/muster/tokens/
muster agent --mcp-server --endpoint=https://muster.k8s-internal.home.derstappen.com/mcp

# 2. Connect with mcp-debug or Cursor
# Expected: authenticate_muster tool is visible

# 3. Call authenticate_muster
# Expected: Returns auth URL for Google OAuth

# 4. Open URL in browser, complete OAuth flow
# Expected: Callback received, success page shown

# 5. Retry tool listing
# Expected: Real tools from muster server visible

# 6. Verify token persisted
ls -la ~/.config/muster/tokens/
```

#### 4.3 Test with Remote MCP Server

Configure a protected MCP server in muster and test the proxy flow:

```yaml
# Test MCPServer that requires OAuth
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: protected-kubernetes
  namespace: muster
spec:
  type: remote
  remote:
    url: "https://mcp-kubernetes.example.com/mcp"
    transport: streamable-http
  oauth:
    required: true
    issuer: "https://dex.example.com"
```

## Test Infrastructure Requirements

### 1. Mock OAuth Server Package

```
internal/testing/mock/
├── oauth_server.go         # Mock OAuth 2.1 server
├── oauth_server_test.go    # Self-tests for mock
├── protected_mcp.go        # Mock MCP server with OAuth
└── templates/
    ├── authorize.html      # Mock authorize page
    └── consent.html        # Mock consent page
```

### 2. Test Fixtures

```
internal/testing/fixtures/oauth/
├── valid_token.json        # Sample valid token
├── expired_token.json      # Sample expired token
├── metadata.json           # Sample OAuth metadata
└── www_authenticate.txt    # Sample WWW-Authenticate headers
```

### 3. CI/CD Integration

```yaml
# .github/workflows/oauth-tests.yaml
name: OAuth Integration Tests

on: [push, pull_request]

jobs:
  oauth-tests:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      
      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.23'
      
      - name: Run Unit Tests
        run: go test ./internal/agent/oauth/... -v
      
      - name: Run Auth Wrapper Tests
        run: go test ./internal/agent/... -run TestBuildAuth -v
      
      - name: Run Integration Tests
        run: |
          go test ./tests/integration/... \
            -tags=integration \
            -timeout=10m \
            -v
```

## Priority Order

1. **High Priority (Week 1)**
   - Mock OAuth server implementation
   - Unit tests for auth_manager state transitions
   - Unit tests for token_store SSO lookup

2. **Medium Priority (Week 2)**
   - Integration tests with mock servers
   - Scenario tests for auth-meta propagation
   - Callback server timeout/error handling tests

3. **Lower Priority (Week 3+)**
   - E2E tests against spidertron
   - Performance testing (30s poll interval impact)
   - Token refresh flow testing

## Known Challenges

### 1. Browser Interaction

OAuth flows require browser interaction for the actual login. Options:
- **Headless browser automation**: Use Playwright/Puppeteer for E2E
- **Mock callback injection**: Simulate callback directly to agent's callback server
- **Skip browser in tests**: Mock the entire OAuth flow server-side

### 2. Token Expiry

Testing token refresh requires waiting for expiry or mocking time:
```go
// Use a clock interface for testability
type Clock interface {
    Now() time.Time
}

type RealClock struct{}
func (RealClock) Now() time.Time { return time.Now() }

type MockClock struct {
    current time.Time
}
func (m *MockClock) Advance(d time.Duration) { m.current = m.current.Add(d) }
```

### 3. Callback Port Conflicts

Multiple parallel tests using callback servers may conflict:
```go
// Use port 0 for random available port in tests
callbackServer := oauth.NewCallbackServer(0)
redirectURI, _ := callbackServer.Start(ctx)
// redirectURI will be http://localhost:<random>/callback
```

### 4. Cursor/VSCode Integration

Testing the full flow with Cursor requires:
- A way to programmatically interact with Cursor's MCP interface
- Or: Mock Cursor as an MCP client using `mcp-go`

## Success Criteria

- [ ] 100% coverage on auth state machine transitions
- [ ] Mock OAuth server can simulate full PKCE flow
- [ ] Integration tests pass without real IdP
- [ ] Scenario tests validate _meta field structure
- [ ] E2E test documented and runnable against spidertron
- [ ] CI pipeline runs auth tests on every PR

## References

- [ADR-004: OAuth Proxy](../explanation/decisions/004-oauth-proxy.md)
- [ADR-005: Muster Server Auth](../explanation/decisions/005-muster-auth.md)
- [ADR-008: Unified Authentication](../explanation/decisions/008-unified-authentication.md)
- [mcp-oauth Library](https://github.com/giantswarm/mcp-oauth)
- [OAuth 2.1 Specification](https://oauth.net/2.1/)
- [RFC 7636: PKCE](https://datatracker.ietf.org/doc/html/rfc7636)
- [RFC 9728: OAuth Protected Resource Metadata](https://datatracker.ietf.org/doc/html/rfc9728)
