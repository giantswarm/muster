# OAuth BDD Testing

This document covers the OAuth BDD testing infrastructure for muster, including architecture, test scenarios, debugging tips, and implementation guidance.

## Overview

The OAuth testing infrastructure enables comprehensive testing of muster's authentication implementation:

- **ADR-004**: OAuth Proxy (muster server → Remote MCP Servers)
- **ADR-005**: muster server Auth (Agent → muster server)
- **ADR-008**: Unified Authentication (auth status polling, `_meta` fields, SSO detection)

## Architecture

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           BDD Test Environment                               │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌──────────────┐     ┌──────────────┐     ┌──────────────────────────────┐ │
│  │  Test Runner │────▶│   muster     │────▶│  Protected Mock MCP Server   │ │
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
│  Layer 2: Agent → muster server (HTTP/SSE) [ADR-005]                            │
│     - Google OAuth (or Dex) protects muster server endpoints                    │
│     - Agent detects 401, creates synthetic `authenticate_muster` tool           │
│     - Local callback server on port 3000                                        │
│     - Token stored: ~/.config/muster/tokens/                                    │
│                                                                                 │
├─────────────────────────────────────────────────────────────────────────────────┤
│                                                                                 │
│  Layer 3: muster server → Remote MCP Servers (OAuth Proxy) [ADR-004]            │
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
| muster Token Store | `oauth.TokenStore.tokens` | Store tokens for aggregator to use | muster process only |

The `test_simulate_oauth_callback` tool bridges these by completing the full OAuth flow, which stores the token in BOTH locations.

### Complete OAuth Flow

```
1. Test calls protected tool          → Aggregator proxies to protected MCP server
2. Protected MCP returns 401          → Aggregator marks server as auth_required
3. Aggregator exposes authenticate_X  → Synthetic tool appears in tool list
4. Test calls authenticate_X          → muster calls CreateAuthChallenge()
                                         - Generates PKCE verifier
                                         - Stores state in StateStore
                                         - Returns auth URL with real state
5. Test parses auth URL               → Extracts state, redirect_uri, etc.
6. Test generates auth code           → Mock OAuth server stores code
7. Test calls muster callback         → GET /callback?code=XXX&state=YYY
8. muster validates state             → Finds it in StateStore ✓
9. muster exchanges code              → POST to mock OAuth /token endpoint
10. Mock OAuth returns token          → muster stores in TokenStore
11. Test retries protected tool       → Aggregator finds token, sends it
12. Protected MCP validates token     → Against mock OAuth server ✓
13. Tool executes successfully        → Protected tools now available
```

## Implementation Components

| Component | Location | Purpose |
|-----------|----------|---------|
| Mock OAuth Server | `internal/testing/mock/oauth_server.go` | OAuth 2.1 server for testing |
| Mock Clock | `internal/testing/mock/clock.go` | Time manipulation for expiry tests |
| Authorization-server profiles | `internal/testing/mock/oauth_profile.go` | `github` / `dex` / `pro`: one bundle of a real authorization server's quirks, see "Authorization-server profiles" |
| Protected MCP Server | `internal/testing/mock/protected_mcp_server.go` | MCP server with OAuth protection |
| Test Fixtures | `internal/testing/fixtures/oauth/` | Sample tokens and metadata |
| `test_simulate_oauth_callback` | `internal/testing/test_tools.go` | Complete OAuth flow simulation |
| `test_inject_token` | `internal/testing/test_tools.go` | Direct token injection |
| `test_get_oauth_server_info` | `internal/testing/test_tools.go` | OAuth server state inspection |
| `test_resolve_auth_redirect` | `internal/testing/test_tools_authorization_server.go` | `core_auth_login` challenge followed one hop: which mock AS the sign-in goes to |
| `test_pin_mcpserver_authorization_server` | `internal/testing/test_tools_authorization_server.go` | Rewrite an MCPServer's `spec.auth.authorizationServer` at runtime (pin, re-pin, clear) |

## Current OAuth Scenarios

| Scenario | Description | Status |
|----------|-------------|--------|
| `oauth-auth-meta-propagation` | Auth status in responses | ✅ Passing |
| `oauth-full-stack` | End-to-end with protected MCPs | ✅ Passing |
| `oauth-mock-server-basic` | Basic mock OAuth functionality | ✅ Passing |
| `oauth-protected-mcp-server` | Protected MCP with simulated auth | ✅ Passing |
| `oauth-sso-detection` | SSO hint for same-issuer servers | ✅ Passing |
| `oauth-sso-shared-issuer` | SSO across servers with same issuer | ✅ Passing |
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
| `test_get_oauth_server_info` | N/A (read-only) | Debugging OAuth server state; `profile`, `dcr_registrations` / `dcr_registered_clients` for DCR assertions |
| `test_forget_oauth_registrations` | Mock OAuth Server only | Drops every RFC 7591 registration the mock holds — the loss alone, without the restart |
| `test_restart_mock_oauth_server` | Mock OAuth Server only | Replaces the authorization server's process behind its port and issuer (a pod replaced): tokens, codes and the configured client stay; under `profile: pro` (or `forget_registrations_on_restart`) the RFC 7591 registrations go with the old process, so muster presents a `client_id` the server no longer knows. Result: `forgotten`, `dcr_registered_clients`, `port` |
| `test_restart_instance` | muster serve (process) | Restarts `muster serve` on the same configuration while the Valkey stand-in and every mock server keep running, then reconnects every user client with its bearer — the sessions that lived through a rollout. Needs `pre_configuration.storage.type: valkey` for anything to survive (see "Storage backend and process restart" in scenarios.md) |
| `test_stop_valkey` / `test_start_valkey` | Valkey stand-in | A Valkey outage with the data kept, and its recovery, while muster runs |
| `test_patch_cr` / `test_get_cr` | envtest API server (Kubernetes mode) | A merge patch on a CR the scenario applied (e.g. `spec.suspended`, labels, `spec.auth.authorizationServer`) and a read of the CR with the status muster wrote; needs `pre_configuration.mode: kubernetes` (see "Kubernetes mode" in scenarios.md) |
| `test_set_apiserver_reachable` | API server proxy (Kubernetes mode) | The API server gone mid-run (`reachable: false`) and back (`true`) for this instance alone |
| `test_redeploy_mock_server` | Mock MCP server (plain or protected) | A fresh backend process behind the same port: every MCP session forgotten, no refused connection in between, the tools kept or changed as `add_tools` / `remove_tools` say (a new image, announced to nobody) |
| `test_set_mock_server_auth` | Protected mock MCP server | Flips the backend between anonymous and 401-with-metadata while it runs (a rollover from an anonymous pod to an OAuth resource server, or back); needs a token validator on the mock, `oauth.required` is the state at start |
| `test_get_mock_server_rejections` | N/A (read-only) | How many requests a protected mock MCP server answered with 401 (`rejected`), and how many of them carried a bearer past its exp (`rejected_expired`, JWKS-validated backends): the backend's own count of what muster sent it with a dead token |
| `test_measure_meta_tool` | N/A (read-only) | Calls a meta-tool through the current session and reports its duration, the bytes of its answer and the store commands it cost (by name), for the budgets of an installation-shaped scenario (see "Installation scale" in scenarios.md) |
| `test_valkey_footprint` | N/A (read-only) | What the Valkey stand-in holds, by key prefix, and the capability store's bytes per session; needs `pre_configuration.storage.type: valkey` |
| `test_advance_clock` | muster serve's clock and every mock OAuth server's clock | Moves time forward on both sides at once: backoffs, the orchestrator's ticks, the catalogue age and token lifetimes (see "Faults and time" in scenarios.md); `test_advance_oauth_clock` moves the authorization server alone |

### When to Use Each Tool

**`test_inject_token`:**
- Testing mock OAuth server's token validation
- Testing protected MCP server's 401/200 behavior
- NOT for testing the full OAuth flow through muster

**`test_simulate_oauth_callback`:**
- Testing the complete OAuth integration
- Verifying SSO (Token Forwarding, Token Exchange)
- Any scenario where muster needs to use the token
- With `auth_url`, the browser leg of a challenge an earlier step obtained
  (`auth_url: "{{ .login_step }}"`, the text of that step's `core_auth_login`
  answer): the tool skips its own login call, for a server that would refuse
  to start a flow now — deactivated after the challenge

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
    tool: "core_auth_login"
    args:
      server: "protected-server"
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
    tool: "core_auth_login"
    args:
      server: "sso-server-b"
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

### Subject-Scoped Grants (GitHub-Style Connectors)

`oauth.grant_scope` pins the referenced mock OAuth server as the MCPServer's
authorization server (`spec.auth.authorizationServer`, with the mock server's
issuer and the server's `scope` as `scopes`) and sets that `grantScope`.
`"subject"` files the grant under the person instead of the login session:
another session of the same subject (`test_muster_auth_login` with the same
`subject`, then `test_create_user`) connects with `core_auth_login` and no
sign-in link, and `core_auth_logout` on any server of the issuer revokes the
grant for every server and session of the person. Needs a mock OAuth server
with `use_as_muster_oauth_server: true` so sessions carry a subject.

```yaml
  mcp_servers:
    - name: "gh-hosted"
      config:
        type: "streamable-http"
        oauth:
          required: true
          mock_oauth_server_ref: "github-as"
          scope: "repo"
          grant_scope: "subject"   # spec.auth.authorizationServer.grantScope
```

See `oauth-subject-grant-logout-shared-issuer.yaml` for the full flow: two
servers on one subject-scoped issuer, two sessions of one person, a logout
that disconnects both servers in both sessions, and a session-scoped pair for
contrast.

A session of the same person that never calls `core_auth_login` is connected
with the grant on its first tool call, and `list_tools` shows the server's
tools to it; `oauth-subject-grant-tool-call.yaml` covers that for four
sessions of one subject and the unchanged failure for another. Two things to
keep in mind when writing such a scenario: keep the connector's mock issuer
separate from the `use_as_muster_oauth_server` one (the muster login stores an
ID-only token under muster's issuer per session, and the session entry is
consulted before the person's grant, so a shared issuer never reaches the
grant), and remember that the first resolution of a cached tool records its
name in the aggregator's registry -- a step meant to exercise the "tool not
found" path has to run before any session calls that tool.

`oauth.omit_resource_metadata: true` makes the mock backend publish no RFC 9728
metadata: a bare `WWW-Authenticate: Bearer` on 401 and a 404 for
`/.well-known/oauth-protected-resource`. The aggregator then registers the
server without an issuer and learns it only from the pin -- the state of a
restarted muster before anyone logs in. `profile: github` on the referenced
mock OAuth server implies it, together with `grant_scope: subject` and the
endpoints pin (see "Authorization-server profiles"). See
`oauth-subject-grant-logout-without-resource-metadata.yaml`.

### Authorization-server profiles

A scenario against a named authorization server selects its **profile** on
the mock OAuth server instead of listing that server's quirks flag by flag --
a scenario about GitHub that forgets one flag passes against a GitHub that
does not exist. `mock_oauth_servers[].profile` is `github`, `dex` or `pro`
(`mock.Profile` in `internal/testing/mock/oauth_profile.go`; each bundle's
wire behaviour is asserted by a unit test there):

| profile | the authorization server | the resources that verify its tokens |
|---|---|---|
| `github` | no RFC 8414 / OIDC discovery document (404 on both well-known paths); no Client ID Metadata Documents and no RFC 7591 registration -- only the pre-registered `client_id`, refused directly at `/authorize` otherwise; `scope` omitted from token responses; access tokens without an expiry (`expires_in` absent) | answer a bare 401 without RFC 9728 metadata (`omit_resource_metadata`); grants are the person's (`grant_scope: subject`); muster is pinned to the issuer with explicit `authorizationEndpoint` / `tokenEndpoint` (`pin_authorization_server` with `pin_endpoints_ref` naming the server itself), since nothing can be discovered |
| `dex` | a discovery document; `scope` omitted from token responses (RFC 6749 §5.1); an id_token with every token; RFC 7591 registration | RFC 9728 metadata as usual |
| `pro` (the MCP TypeScript SDK's authorization server) | a discovery document; RFC 7591 registration whose response carries no `registration_client_uri` / `registration_access_token`, so muster cannot check a registration through RFC 7592 and probes the authorization endpoint instead; a `client_id` it does not know answered directly at `/authorize` with a JSON `invalid_client`, never redirected; the token endpoint refuses unregistered clients; registrations held in memory and gone with `test_restart_mock_oauth_server` | RFC 9728 metadata as usual |

Without a profile the mock is a well-behaved authorization server: a
discovery document, `scope` and `expires_in` in every token response,
registration responses with the RFC 7592 pair (`GET registration_client_uri`
answers 200 while the client is known and 401 once it is forgotten), and a
restart that keeps its registrations.

Every individual flag still works and overrides the profile for that flag
alone: `supports_dcr: false` beside `profile: dex` is a Dex without
registration; `authorize_accepts_any_client: true` beside `profile: pro` is a
pro whose authorization endpoint reveals nothing about an unknown client
(`oauth-dcr-reregister-after-token-endpoint-invalid-client.yaml`). The
resource-side defaults are overridden the same way on the MCP server's `oauth`
block (`omit_resource_metadata: false`, `grant_scope: session`,
`pin_endpoints_ref`, `pin_authorization_server: false`). The flags a profile
reaches on the mock OAuth server: `omit_discovery`, `supports_dcr`,
`require_registered_client`, `omit_token_scope`, `omit_token_expiry`,
`omit_registration_client_uri`, `forget_registrations_on_restart`; on the MCP
server: `omit_resource_metadata`, `grant_scope`, `pin_authorization_server`,
`pin_endpoints_ref`.

```yaml
pre_configuration:
  mock_oauth_servers:
    - name: "github-as"
      profile: "github"
      scopes: ["repo", "read:org"]
      auto_approve: true
      client_id: "github-client"

  mcp_servers:
    - name: "gh-hosted"
      config:
        type: "streamable-http"
        oauth:
          required: true
          mock_oauth_server_ref: "github-as"   # bare 401, subject-scoped grant and the endpoints pin follow from the profile
          scope: "repo"
```

`test_get_oauth_server_info` reports a server's `profile`. The scenarios that
reproduce a named server's bug carry its profile: `dex` in
`oauth-pinned-issuer-is-the-grant-key.yaml` (a server's grant survives the
login id_token mirror, muster#1174), `pro` in
`oauth-dcr-reregister-after-as-forgets-client.yaml` (a registration a restart
forgot is detected and made again, muster#1128), `github` in
`oauth-pinned-server-connectable-after-restart.yaml` (a pinned bare-401
server is connectable after a muster restart with no login in the new
process, muster#1150).

One thing the `github` bundle does not model: on an installation muster
identifies itself to GitHub with a client registered out of band
(`spec.auth.authorizationServer.clientCredentialsSecretRef`, a Kubernetes
Secret), which a filesystem-mode scenario cannot provide. The mock's token
endpoint therefore accepts muster's client identification under `github`;
only its authorization endpoint refuses a `client_id` it does not know.

### Pinned authorization servers that differ from the advertised one

`oauth.pin_authorization_server: true` pins `mock_oauth_server_ref` as the
MCPServer's authorization server without a grant scope (`grant_scope` implies
the pin). `oauth.pin_endpoints_ref` adds another mock server's `/authorize`
and `/token` as `authorizationEndpoint`/`tokenEndpoint` -- the GitHub shape,
explicit endpoints under the pinned issuer. `oauth.advertised_issuer_ref`
makes the backend's RFC 9728 metadata name that mock server as its
authorization server while tokens are still validated against
`mock_oauth_server_ref`: a backend that accepts tokens from an authorization
server other than the one it advertises (muster's own `/mcp` trusts its IdP's
tokens but names muster's OAuth server). On a mock OAuth server,
`omit_token_scope: true` leaves `scope` out of token responses, as Dex does
(`profile: dex` implies it).

`oauth.forward_identity: true` next to the pin sets `auth.forwardIdentity`:
the session's ID token in `X-Muster-Id-Token` next to the pinned grant. A mock
tool's `echo_headers` reports the headers it received and, for one that
carries a JWT, its decoded claims under `received_header_claims.<header>`
(signature unchecked), so a step can assert whose token a header held with
`json_path` (`oauth-pinned-server-forwards-identity`).

`oauth.pin_identity_path` pins the MCPServer under an identity other than the
mock server's issuer -- that issuer URL with the path appended, the GitHub App
shape (`https://github.com/apps/<slug>`) -- so two MCPServers at one mock
server keep separate grants. `oauth.expected_issuer_ref` names the mock server
whose issuer the pin carries as `expectedIssuer`, which a callback that
carries `iss` (`test_simulate_oauth_callback` with `send_iss: true`) then has
to match. Both imply the pin and need the server's endpoints
(`pin_endpoints_ref`). See `oauth-pinned-identity-expected-issuer.yaml`: two
clients of one authorization server, a logout per client, and a third pinned
without `expectedIssuer` whose callback with `iss` is refused.

`test_resolve_auth_redirect` (`server`) runs `core_auth_login` and follows the
challenge's start URL one hop; its result names the mock OAuth server the
sign-in is sent to (`authorization_server`) plus `authorization_endpoint`,
`client_id` and `scope`. `test_pin_mcpserver_authorization_server` (`server`,
`issuer_ref`, optional `endpoints_ref`, `scopes`, `grant_scope`, or `clear:
true`) rewrites the MCPServer's `spec.auth.authorizationServer` in the
filesystem definition, which the reconciler applies like a CR update; pinning
drops `auth.forwardToken` and `auth.tokenExchange` and sets `auth.type: oauth`,
the switch of a server connected through SSO to a pinned authorization server.
See `oauth-pinned-issuer-is-the-grant-key.yaml` (muster#1174),
`oauth-pinned-authorization-server-change-takes-effect.yaml` (muster#1175) and
`oauth-auth-config-change-resets-live-sessions.yaml` (a forwardToken server
switched to a pinned authorization server underneath a connected session,
muster#1276).

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
muster test --scenario oauth-sso-shared-issuer --verbose
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
- [ADR-005: muster server Auth](../../explanation/decisions/005-muster-auth.md)
- [ADR-008: Unified Authentication](../../explanation/decisions/008-unified-authentication.md)
- [OAuth 2.1 Specification](https://oauth.net/2.1/)
- [RFC 7636: PKCE](https://datatracker.ietf.org/doc/html/rfc7636)
- [RFC 9728: OAuth Protected Resource Metadata](https://datatracker.ietf.org/doc/html/rfc9728)
