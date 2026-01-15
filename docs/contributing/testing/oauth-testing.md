# OAuth BDD Testing

This document covers the OAuth BDD testing infrastructure for muster, including architecture, test scenarios, debugging tips, and implementation guidance.

## Overview

The OAuth testing infrastructure enables comprehensive testing of muster's authentication implementation:

- **ADR-004**: OAuth Proxy (Muster Server → Remote MCP Servers)
- **ADR-005**: Muster Server Auth (Agent → Muster Server)
- **ADR-008**: Unified Authentication (auth status polling, `_meta` fields, SSO detection)

## Architecture

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

### Authentication Layers

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

### Token Stores

There are TWO token stores in the test environment:

| Store | Location | Purpose | Accessible By |
|-------|----------|---------|---------------|
| Mock OAuth Server | `mock.OAuthServer.issuedTokens` | Validate tokens for protected MCP servers | Mock infrastructure |
| Muster Token Store | `oauth.TokenStore.tokens` | Store tokens for aggregator to use | Muster process only |

The `test_simulate_oauth_callback` tool bridges these by completing the full OAuth flow, which stores the token in BOTH locations.

### Complete OAuth Flow

```
1. Test calls protected tool          → Aggregator proxies to protected MCP server
2. Protected MCP returns 401          → Aggregator marks server as auth_required
3. Aggregator exposes authenticate_X  → Synthetic tool appears in tool list
4. Test calls authenticate_X          → Muster calls CreateAuthChallenge()
                                         - Generates PKCE verifier
                                         - Stores state in StateStore
                                         - Returns auth URL with real state
5. Test parses auth URL               → Extracts state, redirect_uri, etc.
6. Test generates auth code           → Mock OAuth server stores code
7. Test calls muster callback         → GET /callback?code=XXX&state=YYY
8. Muster validates state             → Finds it in StateStore ✓
9. Muster exchanges code              → POST to mock OAuth /token endpoint
10. Mock OAuth returns token          → Muster stores in TokenStore
11. Test retries protected tool       → Aggregator finds token, sends it
12. Protected MCP validates token     → Against mock OAuth server ✓
13. Tool executes successfully        → Protected tools now available
```

## Implementation Components

| Component | Location | Purpose |
|-----------|----------|---------|
| Mock OAuth Server | `internal/testing/mock/oauth_server.go` | OAuth 2.1 server for testing |
| Mock Clock | `internal/testing/mock/clock.go` | Time manipulation for expiry tests |
| Protected MCP Server | `internal/testing/mock/protected_mcp_server.go` | MCP server with OAuth protection |
| Test Fixtures | `internal/testing/fixtures/oauth/` | Sample tokens and metadata |
| `test_simulate_oauth_callback` | `internal/testing/test_tools.go` | Complete OAuth flow simulation |
| `test_inject_token` | `internal/testing/test_tools.go` | Direct token injection |
| `test_get_oauth_server_info` | `internal/testing/test_tools.go` | OAuth server state inspection |

## Current OAuth Scenarios

| Scenario | Description | Status |
|----------|-------------|--------|
| `oauth-auth-meta-propagation` | Auth status in responses | ✅ Passing |
| `oauth-full-stack` | End-to-end with protected MCPs | ✅ Passing |
| `oauth-mock-server-basic` | Basic mock OAuth functionality | ✅ Passing |
| `oauth-protected-mcp-server` | Protected MCP with simulated auth | ✅ Passing |
| `oauth-sso-detection` | SSO hint for same-issuer servers | ✅ Passing |
| `oauth-sso-token-reuse` | Token reuse across servers | ✅ Passing |
| `oauth-token-injection` | Direct token injection testing | ✅ Passing |
| `oauth-token-refresh-flow` | Token refresh behavior | ✅ Passing |

## Key Concepts

### Session-Scoped Tool Visibility (ADR-006)

The aggregator maintains **per-session** views of available tools. Each session only sees tools from OAuth-protected servers they've authenticated with.

**Key code path:**
1. Client calls `tools/list` via MCP
2. `sessionToolFilter()` intercepts the request
3. `GetAllToolsForSession()` computes session-specific tools
4. For OAuth servers: check connection status
5. Return session's tools OR synthetic auth tool based on auth state

### Token Lookup by Issuer

The aggregator's SSO mechanism uses **issuer-based** token lookup:

```go
token := oauthHandler.GetTokenByIssuer(sessionID, authInfo.Issuer)
```

SSO works because:
- Server A and Server B both use issuer `https://idp.example.com`
- User authenticates to Server A → token stored under `(sessionID, issuer)`
- User tries Server B → aggregator calls `GetTokenByIssuer(sessionID, issuer)`
- Finds existing token → SSO works!

### Server Status States

- `StatusConnected` - normal, fully connected servers
- `StatusDisconnected` - connection lost
- `StatusAuthRequired` - OAuth required, not yet authenticated

## Test Tool Reference

| Tool | Injects Into | Use Case |
|------|--------------|----------|
| `test_inject_token` | Mock OAuth Server only | Testing token validation, 401 behavior |
| `test_simulate_oauth_callback` | Both (via full flow) | Testing complete OAuth integration |
| `test_get_oauth_server_info` | N/A (read-only) | Debugging OAuth server state |

### When to Use Each Tool

**`test_inject_token`:**
- Testing mock OAuth server's token validation
- Testing protected MCP server's 401/200 behavior
- NOT for testing the full OAuth flow through muster

**`test_simulate_oauth_callback`:**
- Testing the complete OAuth integration
- Verifying SSO token reuse
- Any scenario where muster needs to use the token

## Writing OAuth Test Scenarios

### Basic Protected MCP Server Test

```yaml
name: "oauth-protected-mcp-server"
category: "behavioral"
concept: "mcpserver"
tags: ["oauth", "authentication"]
timeout: "2m"

pre_configuration:
  mock_oauth_servers:
    - name: "mock-idp"
      scopes: ["openid", "profile", "mcp:read"]
      auto_approve: true
      pkce_required: true
      token_lifetime: "1h"

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
    tool: "x_protected-server_authenticate"
    args: {}
    expected:
      success: true
      contains: ["http"]

  - id: complete-oauth-flow
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

### SSO Detection Test

```yaml
name: "oauth-sso-detection"
category: "behavioral"
concept: "mcpserver"
tags: ["oauth", "sso"]
timeout: "2m"

pre_configuration:
  mock_oauth_servers:
    - name: "shared-idp"
      scopes: ["openid", "profile", "mcp:admin"]
      auto_approve: true

  mcp_servers:
    - name: "sso-server-a"
      config:
        type: "streamable-http"
        oauth:
          required: true
          mock_oauth_server_ref: "shared-idp"
        tools:
          - name: "op_a"
            responses:
              - response: { source: "server-a" }

    - name: "sso-server-b"
      config:
        type: "streamable-http"
        oauth:
          required: true
          mock_oauth_server_ref: "shared-idp"  # Same issuer
        tools:
          - name: "op_b"
            responses:
              - response: { source: "server-b" }

steps:
  - id: authenticate-server-a
    tool: "test_simulate_oauth_callback"
    args:
      server: "sso-server-a"
    expected:
      success: true

  - id: call-server-a-tool
    tool: "x_sso-server-a_op_a"
    args: {}
    expected:
      success: true
      contains: ["server-a"]

  # SSO should work for second server
  - id: sso-authenticate-server-b
    tool: "x_sso-server-b_authenticate"
    args: {}
    expected:
      success: true
      contains: ["Successfully connected"]

  - id: call-server-b-tool
    tool: "x_sso-server-b_op_b"
    args: {}
    expected:
      success: true
      contains: ["server-b"]
```

## Debugging OAuth Tests

### Run with Debug Output

```bash
muster test --scenario oauth-protected-mcp-server --verbose --debug
```

### Common Pitfalls

#### "Tool not found" Instead of "Auth required"

**Symptom:** Calling a protected tool returns "tool not found" instead of an authentication error.

**Cause:** The server might not be properly registered in `StatusAuthRequired` state.

**Debug steps:**
1. Check if the mock OAuth server is running (`test_get_oauth_server_info`)
2. Verify `/.well-known/oauth-protected-resource` is accessible
3. Verify the server appears in `core_mcpserver_list` output
4. Enable debug logging

#### Token Stored But Protected Tool Still Fails

**Symptom:** `test_simulate_oauth_callback` succeeds but protected tools fail.

**Cause:** Token is in mock OAuth server but not in muster's token store.

**Debug steps:**
1. Check if callback URL was correct
2. Verify the state parameter matches
3. Check muster logs for callback handling errors

#### SSO Not Working Between Servers

**Symptom:** Have to authenticate separately to each server even with same issuer.

**Cause:** Servers might be using different mock OAuth servers.

**Debug steps:**
1. Verify both servers reference the SAME `mock_oauth_server_ref`
2. Check that the issuer URL is identical
3. Confirm the first authentication completed fully

#### Session ID Mismatch

**Symptom:** Token exists but `GetTokenByIssuer` returns nil.

**Cause:** Different session IDs between callback and tool call.

**Debug steps:**
1. Check if session ID is propagated correctly
2. Look for `sessionID` in debug logs

### Debugging Commands

```bash
# Run with full debug output
muster test --scenario oauth-protected-mcp-server --verbose --debug 2>&1 | tee /tmp/oauth-debug.log

# Search for specific events
grep "Session" /tmp/oauth-debug.log
grep "GetTokenByIssuer" /tmp/oauth-debug.log
grep "callback" /tmp/oauth-debug.log
```

## Running OAuth Tests

```bash
# Rebuild after code changes
go install

# Run specific OAuth scenario
muster test --scenario oauth-protected-mcp-server --verbose

# Run all OAuth scenarios
muster test --scenario oauth-auth-meta-propagation --verbose
muster test --scenario oauth-full-stack --verbose
muster test --scenario oauth-mock-server-basic --verbose
muster test --scenario oauth-protected-mcp-server --verbose
muster test --scenario oauth-sso-detection --verbose
muster test --scenario oauth-sso-token-reuse --verbose
muster test --scenario oauth-token-injection --verbose
muster test --scenario oauth-token-refresh-flow --verbose

# Full test suite
make test
muster test --parallel 50
```

## Diagrams

Architecture diagrams are available in the `diagrams/` subdirectory:

- `oauth-test-setup.png` - Overview diagram
- `oauth-test-setup-system-view.png` - System context
- `oauth-test-setup-container-view.png` - Container architecture
- `oauth-test-setup-components-view.png` - Component details
- `oauth-test-setup-sequence.png` - Sequence diagram
- `oauth-test-setup-code-view.png` - Code-level view

## References

- [ADR-004: OAuth Proxy](../../explanation/decisions/004-oauth-proxy.md)
- [ADR-005: Muster Server Auth](../../explanation/decisions/005-muster-auth.md)
- [ADR-008: Unified Authentication](../../explanation/decisions/008-unified-authentication.md)
- [OAuth 2.1 Specification](https://oauth.net/2.1/)
- [RFC 7636: PKCE](https://datatracker.ietf.org/doc/html/rfc7636)
- [RFC 9728: OAuth Protected Resource Metadata](https://datatracker.ietf.org/doc/html/rfc9728)
