# 005. OAuth Protection for Muster Server

## Context

Muster is being deployed to a central Management Cluster to aggregate MCP servers from other clusters. This public exposure requires protecting the Muster Server itself with authentication.

Previously, in [004-oauth-proxy.md](004-oauth-proxy.md), we defined how Muster acts as an OAuth **Proxy** to handle authentication *for* remote MCP servers. Now, we must address how to authenticate users *to* the Muster Server itself.

## Decision

We will implement OAuth 2.1 protection for the Muster Server using the `mcp-oauth` library, similar to `mcp-kubernetes`.

### 1. Dual Role of Muster Server

Muster Server will play two distinct OAuth roles:

1.  **OAuth Resource Server**: It protects its own endpoints (e.g., `/mcp`, `/workflows`). Users (via the Agent) must present a valid Access Token issued by the trusted IdP (eg via Dex, or Github, Google etc) to access these resources.
2.  **OAuth Client (Proxy)**: As defined in [004](004-oauth-proxy.md), it acts as a client to obtain tokens for *downstream* remote MCP servers.

### 2. Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                      Muster Server                          │
│                                                             │
│  [ OAuth Middleware (Resource Server) ]                     │
│       Validates Token from Agent                            │
│               │                                             │
│               ▼                                             │
│  [ Aggregator / Tool Handler ]                              │
│               │                                             │
│               ▼                                             │
│  [ OAuth Proxy (Client) ]                                   │
│       Injects Token for Remote MCPs                         │
│               │                                             │
│               ▼                                             │
│        Remote MCP Server                                    │
└─────────────────────────────────────────────────────────────┘
```

### 3. Key Insight: MCP Handshake vs Tool Call Authentication

**Critical Learning from ADR 004 Implementation**: Authentication does not start with a tool call - it starts with the MCP protocol handshake (`initialize` request). When the Agent connects to Muster Server using SSE or Streamable-HTTP transport, the server returns a 401 *during the transport/connection phase*, before any tools are available.

This is fundamentally different from the downstream OAuth proxy flow (ADR 004) where:
- The aggregator can register a "pending auth" server with synthetic `authenticate_<server>` tools
- Users call the synthetic tool to trigger the OAuth flow

For Agent->Muster authentication, there are no tools yet - the connection itself fails. The Agent must handle this at the transport layer.

### 4. Authentication Flow (Agent -> Muster)

Since the Muster Agent runs locally (e.g., in Cursor) and the Server is remote, the Agent must obtain a token. The key difference from downstream auth is that the 401 occurs during the MCP handshake, not during a tool call.

1.  **Initiation**: Cursor calls `muster agent`.
2.  **Connection Attempt**: Agent attempts to establish MCP connection to `muster server` (SSE/Streamable-HTTP transport).
3.  **Handshake Rejection**: During the HTTP transport phase (before MCP `initialize` completes), Server responds with `401 Unauthorized` and `WWW-Authenticate` header pointing to its authorization endpoint (Dex).
4.  **Agent Handles Transport-Level 401**:
    *   Agent detects the 401 during connection (not tool call).
    *   Agent parses `WWW-Authenticate` header to extract issuer/realm.
    *   Agent generates a local authorization URL (Authorization Code Flow with PKCE).
    *   Agent starts a temporary local listener (e.g., on port `3000`).
    *   Agent outputs a message to the MCP client: "Authentication required. Please log in: [Link]".
5.  **Browser Flow**:
    *   User clicks link -> IdP (Dex) -> User logs in.
    *   IdP redirects to `http://localhost:3000/callback` with code.
6.  **Token Exchange**:
    *   Agent receives code.
    *   Agent exchanges code for Access/Refresh tokens (direct to IdP).
    *   Agent stores tokens locally (in memory or secure file).
7.  **Retry Connection**:
    *   Agent retries the MCP connection to `muster server`, adding `Authorization: Bearer <token>` header to the transport.
8.  **Server Validation**:
    *   Muster Server validates the token (signature, issuer, audience) using `mcp-oauth` middleware.
    *   MCP handshake completes successfully.
    *   Tools, resources, and prompts become available.

### 5. Comparison: Agent->Muster vs Muster->Remote Auth

| Aspect | Agent -> Muster (this ADR) | Muster -> Remote (ADR 004) |
|--------|---------------------------|---------------------------|
| **When 401 occurs** | During transport/connection | During `Initialize()` handshake |
| **Synthetic tools?** | No - connection fails before tools exist | Yes - `authenticate_<server>` tool |
| **Who handles auth?** | Agent (OAuth client) | Server (OAuth proxy) |
| **Token storage** | Agent-side (local) | Server-side (session store) |
| **Lazy init pattern** | Agent retries connection after auth | `RegisterPendingAuth()` + `UpgradeToConnected()` |

### 6. Relation to "OAuth Proxy" (Downstream Auth)

Once the Agent is authenticated and connected (Step 8), the "OAuth Proxy" logic from [004](004-oauth-proxy.md) kicks in if a request is destined for a *remote* MCP server.

*   **Scenario A: Same IdP (SSO)**
    *   If Muster Server and Remote MCP Server share the same IdP and trust the same audiences/clients, Muster *might* be able to forward the user's token directly (Downstream OAuth / Token Exchange).
    *   However, to keep it robust and decoupled, the **Token Reuse Strategy** from [004](004-oauth-proxy.md) is preferred: Muster Server acts as a client. It checks if it has a token for this user/session for the downstream target.

*   **Scenario B: Different IdPs**
    *   Muster Server validates the *incoming* token (User -> Muster).
    *   Muster Server realizes it needs a *different* token for downstream (Muster -> Remote).
    *   Muster Server triggers the Proxy flow: returns a specialized "Remote Auth Required" tool response (not a 401) to the Agent.
    *   Agent displays *another* link: "Authenticate to Remote Cluster X".

## Implementation Steps

1.  **Agent**: Implement OAuth 2.1 Public Client logic at the transport layer:
    *   Handle 401 responses during SSE/Streamable-HTTP connection establishment
    *   Parse `WWW-Authenticate` header to discover the authorization server
    *   Implement Authorization Code Flow with PKCE
    *   Start a temporary local HTTP listener for the callback (e.g., port `3000`)
    *   Store tokens locally (in memory or XDG-compliant secure file)
    *   Retry connection with `Authorization: Bearer <token>` header
    *   CIMD `muster-agent.json` hosted on GitHub Pages
    *   *Note*: This moves auth logic to the Agent (unlike the pure "Proxy" model where Server handles everything). This is necessary because the Server *itself* is now protected.

2.  **Server**: Add `ValidateToken` middleware to the main HTTP entry point:
    *   Return `401 Unauthorized` with proper `WWW-Authenticate` header for unauthenticated requests
    *   Validate token signature, issuer, and audience using `mcp-oauth` library
    *   Extract session identity from validated token for downstream auth reuse

3.  **Docs**: Update deployment guide to include Dex client registration for the Agent.

## Consequences

*   **Agent Complexity**: The Agent is no longer just a dumb pipe; it must handle OAuth flows at the transport layer. This is more complex than the downstream auth (ADR 004) where synthetic tools provide a user-friendly interface.
*   **No Synthetic Tools for Agent Auth**: Unlike downstream auth, there are no `authenticate_muster` tools. The Agent must handle the 401 during connection and present the auth URL directly to the user (via stdout/stderr or MCP logging).
*   **Double Auth**: Users might need to auth twice (once to Muster, once to Remote), but SSO/Token Reuse should minimize this.
*   **Security**: Muster is now secure by default when exposed.
