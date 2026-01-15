# OAuth BDD Testing Completion Plan

## Executive Summary

This document outlines the remaining work to complete the OAuth BDD testing infrastructure for muster. The core infrastructure is in place and all 8 OAuth scenarios currently pass. However, several critical testing gaps need to be addressed to ensure robust coverage of OAuth authentication flows.

**Why This Matters:**
OAuth authentication is a security-critical feature. Without comprehensive BDD tests, we cannot:
- Catch regressions when refactoring the OAuth flow
- Verify edge cases like token expiry, SSO behavior, and error handling
- Ensure the aggregator correctly exposes authentication state to clients
- Validate that ADR-004, ADR-005, and ADR-008 are correctly implemented

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           BDD Test Environment                               │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌──────────────┐     ┌──────────────┐     ┌──────────────────────────────┐ │
│  │  Test Runner │────▶│   Muster     │────▶│  Protected Mock MCP Server   │ │
│  │  + MCP Client│     │   Serve      │     │  (validates against OAuth)   │ │
│  └──────────────┘     │  (separate   │     └───────────────┬──────────────┘ │
│         │             │   process)   │                     │                │
│         │             └──────┬───────┘                     │                │
│         │                    │                             │                │
│         │                    ▼                             ▼                │
│         │             ┌──────────────────────────────────────┐              │
│         └────────────▶│        Mock OAuth Server              │              │
│                       │  (validates tokens for protected MCP) │              │
│                       └──────────────────────────────────────┘              │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

**Key Insight:** There are TWO token stores in the test environment:
1. **Mock OAuth Server** (`mock.OAuthServer.issuedTokens`) - validates tokens for protected MCP servers
2. **Muster's OAuth Manager** (`oauth.Manager.tokenStore`) - stores tokens for the aggregator to use

The `test_simulate_oauth_callback` tool bridges these by completing the full OAuth flow, which stores the token in BOTH locations.

## Critical Learnings from Investigation

These learnings should inform future development and debugging of OAuth tests.

### 1. Session-Scoped Tool Visibility (ADR-006)

The aggregator maintains **per-session** views of available tools. Each session only sees tools from OAuth-protected servers they've authenticated with.

**Key code path:**
1. Client calls `tools/list` via MCP
2. `sessionToolFilter()` in `server.go` intercepts the request
3. `GetAllToolsForSession()` in `registry.go` computes session-specific tools
4. For OAuth servers: check `session.GetConnection(serverName).Status == StatusSessionConnected`
5. Return session's tools OR synthetic auth tool based on auth state

**Why this matters for tests:**
- After `test_simulate_oauth_callback` succeeds, `tools/list` should return the protected server's tools
- Different sessions can have different tool views
- Calling `list_tools` without a session context will not show session-authenticated tools

### 2. The OAuth Metadata Discovery Flow

When the aggregator encounters an OAuth-protected server, it discovers the OAuth configuration via RFC 9728:

1. Protected MCP server returns `401 Unauthorized` with `WWW-Authenticate` header
2. Aggregator calls `/.well-known/oauth-protected-resource` on the server
3. This returns `authorization_servers` (the issuer) and `scopes_supported`
4. Aggregator stores this in `ServerInfo.AuthInfo`
5. Sets server status to `StatusAuthRequired`
6. Exposes synthetic `x_<server>_authenticate` tool

**Why this matters for tests:**
- Mock protected MCP server MUST expose `/.well-known/oauth-protected-resource` endpoint
- The response must contain the mock OAuth server's URL as the authorization server
- Without this, the aggregator can't discover where to send users for auth

### 3. Token Lookup is by Issuer, Not Server Name

The aggregator's SSO mechanism uses **issuer-based** token lookup:

```go
// internal/aggregator/server.go:1558
token := oauthHandler.GetTokenByIssuer(sessionID, authInfo.Issuer)
```

**Why this enables SSO:**
- Server A and Server B both use issuer `https://idp.example.com`
- User authenticates to Server A → token stored under `(sessionID, "https://idp.example.com")`
- User tries Server B → aggregator calls `GetTokenByIssuer(sessionID, "https://idp.example.com")`
- Finds existing token → SSO works!

**Why this matters for tests:**
- SSO only works if servers share the SAME mock OAuth server (same issuer URL)
- To test NO-SSO, use DIFFERENT mock OAuth servers with different URLs
- The `test_inject_token` tool doesn't understand issuers - it just injects into the mock server

### 4. `test_inject_token` Only Injects into Mock OAuth Server

The current `test_inject_token` implementation (lines 346-407 in `test_tools.go`):

```go
// Line 396
oauthServer.AddToken(token, refreshToken, scope, oauthServer.GetClientID(), expiresAt)
```

**What this means:**
- Token is valid for the **protected MCP server** to verify (it checks with mock OAuth server)
- Token is NOT in muster's `oauth.Manager.tokenStore`
- Aggregator's `GetTokenByIssuer()` will return `nil`
- Aggregator won't send `Authorization: Bearer` header
- Protected tools remain unavailable

**When to use `test_inject_token`:**
- Testing mock OAuth server's token validation
- Testing protected MCP server's 401/200 behavior
- NOT for testing the full OAuth flow through muster

**When to use `test_simulate_oauth_callback`:**
- Testing the complete OAuth integration
- Verifying SSO token reuse
- Any scenario where muster needs to use the token

### 5. The Session Connection Upgrade Flow

After successful OAuth callback, muster upgrades the session connection:

1. OAuth callback arrives at muster with `code` and `state`
2. `oauth.Manager.HandleCallback()` exchanges code for token
3. Token is stored in `tokenStore` by `(sessionID, issuer)`
4. Later, when `authenticate` is called again (or a protected tool):
5. Aggregator finds token via `GetTokenByIssuer()`
6. Calls `tryConnectWithToken()` which creates authenticated MCP client
7. Fetches tools from protected server
8. Upgrades session connection: `session.SetConnection(serverName, conn)`
9. Sends `tools/list_changed` notification to client

**Why this matters for tests:**
- After callback succeeds, the session connection must be established
- This requires either calling a protected tool OR calling authenticate again
- The second authenticate call in `test_simulate_oauth_callback` was a workaround for this
- Proper fix: tests should call a protected tool after `test_simulate_oauth_callback`

### 6. Protected MCP Server Behavior

The mock protected MCP server (`internal/testing/mock/protected_mcp_server.go`):

1. Exposes `/.well-known/oauth-protected-resource` → returns issuer info
2. Requires `Authorization: Bearer <token>` on MCP requests
3. Validates tokens by calling mock OAuth server's introspection
4. Returns 401 if token is invalid/missing
5. Returns normal MCP responses if token is valid

**Testing implications:**
- Protected server won't work without valid token in mock OAuth server
- The token muster sends must match what the protected server expects
- Token validation happens on EVERY request to protected server

### 7. The `StatusAuthRequired` State

Servers can be in different states:
- `StatusConnected` - normal, fully connected servers
- `StatusDisconnected` - connection lost
- `StatusAuthRequired` - OAuth required, not yet authenticated

**What happens in `StatusAuthRequired`:**
- Synthetic `x_<server>_authenticate` tool is exposed
- Protected server's actual tools are NOT exposed (they can't be fetched yet)
- Calling any tool for this server (except authenticate) should fail with auth error

**Testing implications:**
- This is the state we should verify in pre-auth behavior tests
- The error message should guide users to call the authenticate tool

## Current State

### What Works

| Component | Status | Location |
|-----------|--------|----------|
| Mock OAuth Server | ✅ Complete | `internal/testing/mock/oauth_server.go` |
| Mock Clock | ✅ Complete | `internal/testing/mock/clock.go` |
| Protected MCP Server | ✅ Complete | `internal/testing/mock/protected_mcp_server.go` |
| Test Fixtures | ✅ Complete | `internal/testing/fixtures/oauth/` |
| `test_simulate_oauth_callback` | ✅ Working | `internal/testing/test_tools.go` |
| `test_inject_token` | ✅ Working | `internal/testing/test_tools.go` |
| `test_get_oauth_server_info` | ✅ Working | `internal/testing/test_tools.go` |

### Passing OAuth Scenarios

All 8 OAuth scenarios currently pass:

1. `oauth-auth-meta-propagation` - Verifies auth status is included in responses
2. `oauth-full-stack` - End-to-end test with protected MCP servers
3. `oauth-mock-server-basic` - Basic mock OAuth server functionality
4. `oauth-protected-mcp-server` - Protected MCP server with simulated auth
5. `oauth-sso-detection` - SSO hint detection for same-issuer servers
6. `oauth-sso-token-reuse` - Token reuse across servers (uses injection)
7. `oauth-token-injection` - Direct token injection testing
8. `oauth-token-refresh-flow` - Token refresh behavior

## Gaps to Address

### 1. Remove Second Authenticate Call in `test_simulate_oauth_callback`

**Priority:** High  
**Files:** `internal/testing/test_tools.go` (lines 200-215)

#### Why This Is a Problem

The current implementation calls the authenticate tool TWICE:
1. First call: Gets the auth URL with proper state parameter
2. Second call (after callback): Attempts to "establish connection"

**Why the second call is wrong:**

1. **It masks aggregator bugs:** The aggregator SHOULD automatically use the stored token on the next protected tool call. If it doesn't, we have a bug in the aggregator - not something to work around in tests.

2. **It doesn't match real user behavior:** Real users don't call authenticate twice. They complete the OAuth flow once, then call their protected tool.

3. **It can hide token storage issues:** If the callback doesn't properly store the token in muster's OAuth manager, the second authenticate call might succeed anyway (by creating a new auth flow), masking the original bug.

4. **It creates test-specific behavior:** Test code should simulate real usage, not create special paths that only exist in tests.

#### Current Code
```go
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
```

#### What Should Happen Instead

After the callback succeeds:
1. The token is stored in muster's OAuth manager (tied to session + issuer)
2. The NEXT call to any protected tool should automatically use this token
3. The aggregator's `handleSyntheticAuthTool` should find the token via `GetTokenByIssuer()`

**If this doesn't work**, the bug is in one of:
- `oauth.Manager.HandleCallback()` - not storing token correctly
- `aggregator.handleSyntheticAuthTool()` - not finding stored token
- Session management - session ID mismatch between callback and tool call

#### Implementation

1. Remove lines 200-215 from `test_tools.go`
2. Update scenarios to explicitly call a protected tool after `test_simulate_oauth_callback` to verify the flow works
3. If tests fail after this change, investigate the aggregator/OAuth manager - don't add the second call back

---

### 2. Pre-Authentication Behavior Testing

**Priority:** High  
**Files:** New scenario `oauth-pre-auth-behavior.yaml`

#### Why This Is Missing and Why It Matters

Currently, NO scenario tests what happens when you call a protected tool BEFORE authenticating. This is a critical gap because:

1. **User experience:** When a user tries to use a protected tool without auth, they need a clear error message telling them to authenticate, not a confusing "tool not found" error.

2. **ADR-008 compliance:** The ADR specifies that unauthenticated calls should return auth status in the response, allowing the LLM client to prompt the user to authenticate.

3. **Security:** We need to verify that protected tools actually reject unauthenticated requests, not just assume it works.

4. **Synthetic tool exposure:** The aggregator should expose `x_<server>_authenticate` tools for OAuth-protected servers. Without a test, we can't be sure this works.

#### Expected Behavior

When calling a protected tool without authentication:
1. The protected MCP server returns 401 with `WWW-Authenticate` header
2. Muster's aggregator marks the server as `StatusAuthRequired`
3. The aggregator exposes a synthetic `x_<server>_authenticate` tool
4. Calling the protected tool returns an error indicating authentication is required
5. The error should be actionable (e.g., "call x_<server>_authenticate to log in")

#### New Scenario

```yaml
name: "oauth-pre-auth-behavior"
category: "behavioral"
concept: "mcpserver"
description: "Verify protected tools return auth challenge before authentication"
tags: ["oauth", "pre-auth", "error-handling", "adr-008"]
timeout: "2m"

pre_configuration:
  mock_oauth_servers:
    - name: "pre-auth-idp"
      scopes: ["openid", "profile"]
      auto_approve: true
      pkce_required: false
      token_lifetime: "1h"

  mcp_servers:
    - name: "pre-auth-server"
      config:
        type: "streamable-http"
        oauth:
          required: true
          mock_oauth_server_ref: "pre-auth-idp"
        tools:
          - name: "protected_operation"
            responses:
              - response:
                  status: "ok"

steps:
  # Step 1: Verify server is registered but requires auth
  - id: list-servers
    description: "Server should be listed even if not authenticated"
    tool: "core_mcpserver_list"
    args: {}
    expected:
      success: true
      contains:
        - "pre-auth-server"

  # Step 2: Try to call protected tool before auth
  # This is the KEY test - we expect a meaningful error, not "tool not found"
  - id: call-before-auth
    description: "Calling protected tool before authentication should return auth error"
    tool: "x_pre-auth-server_protected_operation"
    args: {}
    expected:
      success: false
      error_contains:
        - "not authenticated"

  # Step 3: Verify authenticate tool is available and returns auth URL
  - id: call-authenticate-tool
    description: "Authenticate tool should be available and return auth URL"
    tool: "x_pre-auth-server_authenticate"
    args: {}
    expected:
      success: true
      contains:
        - "http"  # Auth URL should contain http(s)

  # Step 4: Complete authentication
  - id: authenticate
    tool: "test_simulate_oauth_callback"
    args:
      server: "pre-auth-server"
    expected:
      success: true

  # Step 5: Now the protected tool should work
  - id: call-after-auth
    description: "After authentication, protected tool should succeed"
    tool: "x_pre-auth-server_protected_operation"
    args: {}
    expected:
      success: true
      contains:
        - "status"
        - "ok"
```

---

### 3. Fix `oauth-sso-token-reuse.yaml` to Use Proper OAuth Flow

**Priority:** High  
**Files:** `internal/testing/scenarios/oauth-sso-token-reuse.yaml`

#### Why This Is Wrong

The current scenario uses `test_inject_token`, which:
1. Only adds the token to the mock OAuth server's internal store
2. Does NOT put the token in muster's OAuth manager
3. Therefore does NOT actually test SSO token reuse

**What the scenario claims to test:**
> "Verify SSO token reuse when multiple servers share the same OAuth issuer"

**What it actually tests:**
> "The mock OAuth server accepts injected tokens"

This is a false positive - the scenario passes, but it's not testing what we think it's testing.

#### The Real SSO Flow

SSO (Single Sign-On) in muster works like this:

1. User authenticates to `server-a` (issuer: `enterprise-idp`)
2. Token is stored in muster's OAuth manager: `tokenStore[sessionID][enterprise-idp]`
3. User tries to access `server-b` (also issuer: `enterprise-idp`)
4. Aggregator calls `GetTokenByIssuer(sessionID, "enterprise-idp")`
5. Finds existing token → uses it without requiring new auth
6. User gets seamless access to `server-b`

**The key:** SSO works because `GetTokenByIssuer` looks up tokens by ISSUER, not by server name.

#### Fixed Scenario

```yaml
name: "oauth-sso-token-reuse"
category: "behavioral"
concept: "mcpserver"
description: "Verify SSO token reuse - authenticating to one server works for other servers with same issuer"
tags: ["oauth", "sso", "token-reuse", "adr-008"]
timeout: "2m"

pre_configuration:
  mock_oauth_servers:
    - name: "shared-idp"
      scopes: ["openid", "profile", "mcp:admin"]
      auto_approve: true
      pkce_required: false
      token_lifetime: "1h"

  mcp_servers:
    - name: "sso-server-a"
      config:
        type: "streamable-http"
        oauth:
          required: true
          mock_oauth_server_ref: "shared-idp"
          scope: "mcp:admin"
        tools:
          - name: "op_a"
            responses:
              - response: { source: "server-a" }

    - name: "sso-server-b"
      config:
        type: "streamable-http"
        oauth:
          required: true
          mock_oauth_server_ref: "shared-idp"
          scope: "mcp:admin"
        tools:
          - name: "op_b"
            responses:
              - response: { source: "server-b" }

steps:
  # Step 1: Complete FULL OAuth flow for first server
  # This stores token in BOTH mock OAuth server AND muster's OAuth manager
  - id: authenticate-server-a
    description: "Complete OAuth flow for server-a"
    tool: "test_simulate_oauth_callback"
    args:
      server: "sso-server-a"
    expected:
      success: true

  # Step 2: Verify first server works
  - id: call-server-a-tool
    description: "Protected tool on server-a should work after auth"
    tool: "x_sso-server-a_op_a"
    args: {}
    expected:
      success: true
      contains:
        - "server-a"

  # Step 3: Call authenticate on server-b
  # Because both servers use "shared-idp", the aggregator should:
  # 1. Call GetTokenByIssuer(sessionID, "shared-idp") 
  # 2. Find the token we stored when authenticating to server-a
  # 3. Use that token to connect to server-b
  # 4. Return "Successfully connected" without requiring new auth
  - id: sso-authenticate-server-b
    description: "SSO should detect existing token for same issuer"
    tool: "x_sso-server-b_authenticate"
    args: {}
    expected:
      success: true
      contains:
        - "Successfully connected"

  # Step 4: Verify second server works via SSO
  - id: call-server-b-tool
    description: "Protected tool on server-b should work via SSO"
    tool: "x_sso-server-b_op_b"
    args: {}
    expected:
      success: true
      contains:
        - "server-b"
```

---

### 4. Multiple Different Issuers (No SSO Should Apply)

**Priority:** Medium  
**Files:** New scenario `oauth-different-issuers.yaml`

#### Why This Test Is Important

SSO works because tokens are looked up by ISSUER, not by server name. We need to verify the NEGATIVE case: servers with DIFFERENT issuers should NOT share tokens.

**Why this could break:**
1. Bug in `GetTokenByIssuer` that returns any token
2. Session ID confusion that crosses issuer boundaries
3. Token storage key collision

If this test fails, it's a security issue - users could accidentally gain access to servers they shouldn't.

#### New Scenario

```yaml
name: "oauth-different-issuers"
category: "behavioral"
concept: "mcpserver"
description: "Verify servers with different OAuth issuers do NOT share tokens"
tags: ["oauth", "multi-issuer", "no-sso", "security"]
timeout: "2m"

pre_configuration:
  mock_oauth_servers:
    # Two completely separate identity providers
    - name: "idp-alpha"
      scopes: ["openid", "profile"]
      auto_approve: true

    - name: "idp-beta"
      scopes: ["openid", "profile"]
      auto_approve: true

  mcp_servers:
    - name: "server-alpha"
      config:
        type: "streamable-http"
        oauth:
          required: true
          mock_oauth_server_ref: "idp-alpha"  # Uses idp-alpha
        tools:
          - name: "alpha_op"
            responses:
              - response: { source: "alpha" }

    - name: "server-beta"
      config:
        type: "streamable-http"
        oauth:
          required: true
          mock_oauth_server_ref: "idp-beta"  # Uses DIFFERENT idp-beta
        tools:
          - name: "beta_op"
            responses:
              - response: { source: "beta" }

steps:
  # Authenticate to alpha
  - id: authenticate-alpha
    tool: "test_simulate_oauth_callback"
    args:
      server: "server-alpha"
    expected:
      success: true

  # Alpha should work
  - id: call-alpha-tool
    tool: "x_server-alpha_alpha_op"
    args: {}
    expected:
      success: true
      contains:
        - "alpha"

  # CRITICAL: Beta uses DIFFERENT IdP - should NOT work without separate auth
  # If this passes, we have a security bug!
  - id: call-beta-without-auth
    description: "Beta uses different IdP - MUST fail without its own auth"
    tool: "x_server-beta_beta_op"
    args: {}
    expected:
      success: false
      error_contains:
        - "not authenticated"

  # Now authenticate to beta separately
  - id: authenticate-beta
    tool: "test_simulate_oauth_callback"
    args:
      server: "server-beta"
    expected:
      success: true

  # Beta should work now
  - id: call-beta-tool
    tool: "x_server-beta_beta_op"
    args: {}
    expected:
      success: true
      contains:
        - "beta"
```

---

### 5. Token Expiry Testing with MockClock

**Priority:** Medium  
**Files:** `internal/testing/test_tools.go`, new scenario

#### Why Real Token Expiry Testing Is Complex

The current `oauth-token-refresh-flow` scenario doesn't actually test token expiry because:

1. **No time advancement:** The mock OAuth server uses `time.Now()` by default. Even with a 30-second token lifetime, the test completes before expiry.

2. **No protected tool call after wait:** The scenario doesn't call a protected tool after the "wait" period.

3. **No way to control time:** We need the MockClock to be used by the OAuth server, and a test tool to advance it.

#### Why MockClock Is The Right Solution

We already have `internal/testing/mock/clock.go` with a proper MockClock implementation:

```go
type MockClock struct {
    mu      sync.RWMutex
    current time.Time
}

func (m *MockClock) Advance(d time.Duration) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.current = m.current.Add(d)
}
```

The mock OAuth server already supports injecting a Clock:

```go
type OAuthServerConfig struct {
    // ...
    Clock Clock  // Set this to a MockClock for testing token expiry
}
```

#### New Test Tool: `test_advance_oauth_clock`

Add to `internal/testing/test_tools.go`:

```go
func (h *TestToolsHandler) handleAdvanceOAuthClock(ctx context.Context, args map[string]interface{}) (interface{}, error) {
    duration, ok := args["duration"].(string)
    if !ok || duration == "" {
        return nil, fmt.Errorf("duration argument required (e.g., '5m', '1h')")
    }
    
    d, err := time.ParseDuration(duration)
    if err != nil {
        return nil, fmt.Errorf("invalid duration: %w", err)
    }
    
    // Get OAuth server and advance its clock
    serverName, _ := args["server"].(string)
    
    if serverName != "" {
        // Advance specific server's clock
        server := h.instanceManager.GetMockOAuthServer(h.currentInstance.ID, serverName)
        if server == nil {
            return nil, fmt.Errorf("OAuth server %s not found", serverName)
        }
        if mockClock, ok := server.GetClock().(*mock.MockClock); ok {
            mockClock.Advance(d)
        }
    } else {
        // Advance all OAuth servers' clocks
        for name := range h.currentInstance.MockOAuthServers {
            server := h.instanceManager.GetMockOAuthServer(h.currentInstance.ID, name)
            if server != nil {
                if mockClock, ok := server.GetClock().(*mock.MockClock); ok {
                    mockClock.Advance(d)
                }
            }
        }
    }
    
    return map[string]interface{}{
        "success": true,
        "message": fmt.Sprintf("Advanced OAuth clock by %s", duration),
        "advanced_by": d.String(),
    }, nil
}
```

**Why this approach:**
1. Uses existing MockClock infrastructure
2. Doesn't require waiting real time
3. Can test any expiry scenario instantly
4. Allows testing specific servers or all servers

#### Implementation Notes

1. The mock OAuth server's config must use MockClock instead of RealClock for expiry tests
2. The muster_manager needs to inject MockClock when creating OAuth servers for expiry scenarios
3. Consider adding a scenario config option: `use_mock_clock: true`

---

### 6. OAuth Error Scenarios

**Priority:** Low  
**Files:** New scenarios

#### Why Error Handling Tests Matter

OAuth is complex and many things can go wrong. Without error tests, we can't verify that:
1. Errors are reported clearly to users
2. The system recovers gracefully
3. Security isn't compromised by error conditions

#### OAuth Server Unavailable During Auth

**Scenario:** User starts auth, but OAuth server goes down before token exchange.

```yaml
name: "oauth-server-unavailable"
pre_configuration:
  mock_oauth_servers:
    - name: "flaky-idp"
      simulate_error: "connection refused"  # Token exchange fails
```

#### Invalid Token Rejection

**Scenario:** Attacker tries to use a forged token.

The mock OAuth server validates tokens against its `issuedTokens` map. An injected fake token should be rejected when used.

---

### 7. ADR-008 Unified Authentication Features

**Priority:** Medium  
**Files:** Investigation needed, possibly new scenarios

#### What ADR-008 Specifies

The ADR describes several features that should be tested:

1. **`_meta["giantswarm.io/auth_required"]` in responses:**
   - Every tool response should include auth status
   - Shows which servers need authentication
   - Includes auth tool name and issuer

2. **`auth://status` resource:**
   - Polling this resource returns current auth state
   - Used by agents to periodically check if re-auth is needed

3. **SSO detection:**
   - When multiple servers share an issuer, indicate this to the user
   - "Authenticate once for gitlab and jira (same identity provider)"

#### Investigation Needed

Before writing tests, verify these features exist:

1. Check `internal/aggregator/` for `_meta` field injection
2. Check for `auth://` resource registration
3. Check `internal/agent/auth_wrapper.go` for SSO detection logic

---

## Implementation Order

| Order | Task | Priority | Effort | Why This Order |
|-------|------|----------|--------|----------------|
| 1 | Remove second authenticate call | High | Small | Quick fix, improves test accuracy |
| 2 | Fix `oauth-sso-token-reuse.yaml` | High | Small | Critical for SSO verification |
| 3 | Add `oauth-pre-auth-behavior` | High | Small | Validates user experience on auth failure |
| 4 | Add `oauth-different-issuers` | Medium | Small | Security verification |
| 5 | Add `test_advance_oauth_clock` | Medium | Medium | Enables expiry testing |
| 6 | Add token expiry scenario | Medium | Small | Depends on clock tool |
| 7 | Investigate ADR-008 features | Medium | Medium | Research before implementation |
| 8 | Add error scenarios | Low | Small | Edge cases |

## Files to Modify

| File | Change | Priority |
|------|--------|----------|
| `internal/testing/test_tools.go` | Remove second auth call (lines 200-215) | High |
| `internal/testing/test_tools.go` | Add `test_advance_oauth_clock` tool | Medium |
| `internal/testing/scenarios/oauth-sso-token-reuse.yaml` | Rewrite to use proper flow | High |
| `internal/testing/scenarios/oauth-pre-auth-behavior.yaml` | New file | High |
| `internal/testing/scenarios/oauth-different-issuers.yaml` | New file | Medium |
| `internal/testing/scenarios/oauth-token-expiry-handling.yaml` | New file | Medium |

## Testing Checklist

After implementation:

- [ ] All existing OAuth scenarios still pass
- [ ] `test_simulate_oauth_callback` no longer calls authenticate twice
- [ ] Pre-auth behavior returns proper challenge, not "tool not found"
- [ ] SSO scenario uses full OAuth flow, not token injection
- [ ] Different issuers don't share tokens (security test)
- [ ] Clock advancement causes token validation to fail
- [ ] `make test` passes
- [ ] `muster test --parallel 50` passes

## Running Tests

```bash
# Rebuild after code changes
go install

# Run all OAuth scenarios individually
muster test --scenario oauth-auth-meta-propagation --verbose
muster test --scenario oauth-full-stack --verbose
muster test --scenario oauth-mock-server-basic --verbose
muster test --scenario oauth-protected-mcp-server --verbose
muster test --scenario oauth-sso-detection --verbose
muster test --scenario oauth-sso-token-reuse --verbose
muster test --scenario oauth-token-injection --verbose
muster test --scenario oauth-token-refresh-flow --verbose

# Run new scenarios
muster test --scenario oauth-pre-auth-behavior --verbose
muster test --scenario oauth-different-issuers --verbose

# Full test suite
make test
muster test --parallel 50
```

## Common Pitfalls and Debugging Tips

### Pitfall 1: "Tool not found" Instead of "Auth required"

**Symptom:** Calling a protected tool returns "tool not found" instead of an authentication error.

**Cause:** The server might not be properly registered in `StatusAuthRequired` state.

**Debug steps:**
1. Check if the mock OAuth server is running (`test_get_oauth_server_info`)
2. Check if the protected MCP server's `/.well-known/oauth-protected-resource` is accessible
3. Verify the server appears in `core_mcpserver_list` output
4. Enable debug logging: `muster test --scenario <name> --verbose --debug`

### Pitfall 2: Token Stored But Protected Tool Still Fails

**Symptom:** `test_simulate_oauth_callback` succeeds but protected tools fail.

**Cause:** Token is in mock OAuth server but not in muster's token store.

**Debug steps:**
1. Check if callback URL was correct (should be muster's callback, not mock OAuth)
2. Verify the state parameter matches what muster generated
3. Check muster logs for callback handling errors

### Pitfall 3: SSO Not Working Between Servers

**Symptom:** Have to authenticate separately to each server even with same issuer.

**Cause:** The servers might be using different mock OAuth servers (different issuer URLs).

**Debug steps:**
1. Verify both servers reference the SAME `mock_oauth_server_ref`
2. Check that the issuer URL is identical for both
3. Confirm the first authentication completed fully

### Pitfall 4: Session ID Mismatch

**Symptom:** Token exists but `GetTokenByIssuer` returns nil.

**Cause:** Different session IDs between callback and tool call.

**Debug steps:**
1. Check if session ID is being propagated correctly through MCP context
2. Look for `sessionID` in debug logs
3. Verify the MCP client maintains the same session across calls

### Debugging Commands

```bash
# Run with full debug output
muster test --scenario oauth-protected-mcp-server --verbose --debug 2>&1 | tee /tmp/oauth-debug.log

# Search for specific events
grep "Session" /tmp/oauth-debug.log
grep "GetTokenByIssuer" /tmp/oauth-debug.log
grep "callback" /tmp/oauth-debug.log

# Check OAuth server state
grep "mock OAuth" /tmp/oauth-debug.log
```

### Test Tool Comparison

| Tool | Injects Into | Use Case |
|------|--------------|----------|
| `test_inject_token` | Mock OAuth Server only | Testing token validation, 401 behavior |
| `test_simulate_oauth_callback` | Both (via full flow) | Testing complete OAuth integration |
| `test_get_oauth_server_info` | N/A (read-only) | Debugging OAuth server state |
| `test_advance_oauth_clock` | Mock OAuth Server | Testing token expiry (needs implementation) |

## References

- [ADR-004: OAuth Proxy](../../explanation/decisions/004-oauth-proxy.md) - How muster proxies OAuth for remote MCP servers
- [ADR-005: Muster Server Auth](../../explanation/decisions/005-muster-auth.md) - How muster server itself is protected
- [ADR-008: Unified Authentication](../../explanation/decisions/008-unified-authentication.md) - Unified auth experience across layers
- [Original Auth Testing Plan](./auth-testing-plan.md)
- [OAuth BDD Testing Plan](./oauth-bdd-testing-plan.md)
- [OAuth Implementation Plan](./oauth-implementation-plan.md)
