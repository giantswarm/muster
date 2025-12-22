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

### 3. Authentication Flow (Agent -> Muster)

Since the Muster Agent runs locally (e.g., in Cursor) and the Server is remote, the Agent must obtain a token.

1.  **Initiation**: Cursor calls `muster agent`.
2.  **Request**: Agent sends a request to `muster server` (e.g., `list_tools`).
3.  **Challenge**: Server responds with `401 Unauthorized` and `WWW-Authenticate` header pointing to its authorization endpoint (Dex).
4.  **User Interaction**:
    *   Agent detects the 401.
    *   Agent generates a local authorization URL (Authorization Code Flow with PKCE).
    *   Agent starts a temporary local listener (e.g., on port `3000`).
    *   Agent instructs the user to visit the URL: "Please log in to Muster: [Link]".
5.  **Browser Flow**:
    *   User clicks link -> IdP (Dex) -> User logs in.
    *   IdP redirects to `http://localhost:3000/callback` with code.
6.  **Token Exchange**:
    *   Agent receives code.
    *   Agent exchanges code for Access/Refresh tokens (direct to IdP).
    *   Agent stores tokens locally (in memory or secure file).
7.  **Retry**:
    *   Agent retries the request to `muster server`, adding `Authorization: Bearer <token>`.
8.  **Server Validation**:
    *   Muster Server validates the token (signature, issuer, audience) using `mcp-oauth` middleware.
    *   Request proceeds.

### 4. Relation to "OAuth Proxy" (Downstream Auth)

Once the request reaches the Aggregator (Step 8), the "OAuth Proxy" logic from [004](004-oauth-proxy.md) kicks in if the request is destined for a *remote* MCP server.

*   **Scenario A: Same IdP (SSO)**
    *   If Muster Server and Remote MCP Server share the same IdP and trust the same audiences/clients, Muster *might* be able to forward the user's token directly (Downstream OAuth / Token Exchange).
    *   However, to keep it robust and decoupled, the **Token Reuse Strategy** from [004](004-oauth-proxy.md) is preferred: Muster Server acts as a client. It checks if it has a token for this user/session for the downstream target.

*   **Scenario B: Different IdPs**
    *   Muster Server validates the *incoming* token (User -> Muster).
    *   Muster Server realizes it needs a *different* token for downstream (Muster -> Remote).
    *   Muster Server triggers the Proxy flow: returns a specialized "Remote Auth Required" tool response (not a 401) to the Agent.
    *   Agent displays *another* link: "Authenticate to Remote Cluster X".

## Implementation Steps

1.  **Agent**: Implement OAuth 2.1 Public Client logic (CIMD, PKCE, local listener). CIMD muster-agent.json hosted on GitHub Pages.
    *   *Note*: This moves some auth logic to the Agent, unlike the pure "Proxy" model where Server did everything. This is necessary because the Server *itself* is now protected.
2.  **Server**: Add `ValidateToken` middleware to the main HTTP entry point.
3.  **Docs**: Update deployment guide to include Dex client registration for the Agent.

## Consequences

*   **Agent Complexity**: The Agent is no longer just a dumb pipe; it must handle OAuth flows.
*   **Double Auth**: Users might need to auth twice (once to Muster, once to Remote), but SSO/Token Reuse should minimize this.
*   **Security**: Muster is now secure by default when exposed.