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

### 3. MCP Roles and Architecture

Each component plays dual roles in the MCP protocol:

```
┌────────────────┐         ┌────────────────┐            ┌────────────────┐            ┌────────────────┐
│     Cursor     │  stdio  │  Muster Agent  │  HTTP/SSE  │ Muster Server  │  HTTP/SSE  │   Remote MCP   │
│                │ <-----> │                │ ---------> │                │ ---------> │                │
│   MCP Host     │         │   MCP Server   │            │   MCP Server   │            │   MCP Server   │
│   MCP Client   │         │   MCP Client   │            │   MCP Client   │            │                │
└────────────────┘         └────────────────┘            └────────────────┘            └────────────────┘
```

**Cursor configuration** (e.g., `.cursor/mcp.json`):
```json
{
  "muster": {
    "command": "muster",
    "args": ["agent", "--mcp-server", "--endpoint=http://localhost:8090/mcp"]
  }
}
```

**Key points**:
1. Cursor **starts** `muster agent` as a local stdio MCP server (subprocess)
2. Cursor is an MCP Host and MCP Client; it communicates with the Agent via stdio
3. Muster Agent is an MCP Server (for Cursor) AND an MCP Client (for Muster Server)
4. Muster Server is an MCP Server (for Agent) AND an MCP Client (for Remote MCPs)
5. The Agent proxies tool calls, resources, and prompts between Cursor and Server

### 4. Key Insight: Same Pattern as Downstream Auth (Lazy Initialization)

**Critical Learning from ADR 004 Implementation**: Authentication happens during the MCP handshake (`initialize` request), not during a tool call. This is **exactly the same** situation for both:
- **Agent -> Muster Server**: Agent (as MCP Client) gets 401 when connecting to protected Server
- **Muster Server -> Remote MCP**: Server (as MCP Client) gets 401 when connecting to protected Remote

**The solution is the same**: Use lazy initialization with synthetic authentication tools.

Since the Agent is an MCP Server (for Cursor), it can expose synthetic tools even when its upstream connection to Muster Server fails. This provides a consistent user experience where auth URLs always appear as tool results in Cursor.

### 5. Authentication Flow (Agent -> Muster)

1.  **Startup**: Cursor starts `muster agent --mcp-server --endpoint=<server-url>` as a stdio subprocess.
2.  **Agent Connects to Server**: Agent (as MCP Client) attempts to establish connection to `muster server` (SSE/Streamable-HTTP).
3.  **Handshake Rejection**: Server responds with `401 Unauthorized` and `WWW-Authenticate` header pointing to its IdP.
4.  **Lazy Initialization (Pending Auth State)**:
    *   Agent detects the 401 during connection.
    *   Agent parses `WWW-Authenticate` header to extract issuer/realm.
    *   Agent enters "pending auth" state (similar to `RegisterPendingAuth()` in ADR 004).
    *   Agent exposes a synthetic `authenticate_muster` tool to Cursor.
    *   Agent completes MCP handshake with Cursor (stdio) - Cursor sees the synthetic tool.
5.  **User Calls Synthetic Auth Tool**:
    *   User (via Cursor) calls `authenticate_muster` tool.
    *   Agent generates authorization URL (Authorization Code Flow with PKCE).
    *   Agent starts a temporary local listener (e.g., on port `3000`).
    *   Tool result contains the auth URL: "Please sign in: [Link]".
6.  **Browser Flow**:
    *   User clicks link -> IdP (Dex) -> User logs in.
    *   IdP redirects to `http://localhost:3000/callback` with code.
7.  **Token Exchange**:
    *   Agent receives code via the local listener.
    *   Agent exchanges code for Access/Refresh tokens (direct to IdP).
    *   Agent stores tokens locally (in memory or XDG-compliant secure file).
8.  **Upgrade to Connected**:
    *   Agent retries MCP connection to Server with `Authorization: Bearer <token>` header.
    *   Server validates token; handshake succeeds.
    *   Agent receives tools, resources, and prompts from Server.
    *   Agent replaces synthetic `authenticate_muster` tool with real tools (similar to `UpgradeToConnected()`).
    *   Agent sends `tools/list_changed` notification to Cursor.
    *   User can now interact with Muster through Cursor.

### 6. Consistency with Downstream Auth

| Aspect | Agent -> Muster (this ADR) | Muster -> Remote (ADR 004) |
|--------|---------------------------|---------------------------|
| **When 401 occurs** | Agent connecting to Server | Server connecting to Remote MCP |
| **Who is the MCP Client?** | Agent | Muster Server |
| **Who is the MCP Server (for user)?** | Agent (for Cursor) | Muster Server (for Agent/Cursor) |
| **Synthetic tools?** | Yes - `authenticate_muster` | Yes - `authenticate_<server>` |
| **Lazy init pattern** | Same: pending auth -> upgrade | `RegisterPendingAuth()` + `UpgradeToConnected()` |
| **Token storage** | Agent-side (local filesystem) | Server-side (session store) |
| **User experience** | Auth URL in tool result | Auth URL in tool result |

### 7. Relation to "OAuth Proxy" (Downstream Auth)

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

1.  **Agent**: Implement lazy initialization with synthetic auth tool:
    *   Handle 401 responses during SSE/Streamable-HTTP connection to Muster Server
    *   Parse `WWW-Authenticate` header to discover the authorization server
    *   Enter "pending auth" state and expose synthetic `authenticate_muster` tool to Cursor
    *   Implement Authorization Code Flow with PKCE when synthetic tool is called
    *   Start a temporary local HTTP listener for the callback (e.g., port `3000`)
    *   Store tokens locally (in memory or XDG-compliant secure file)
    *   Retry connection with `Authorization: Bearer <token>` header
    *   Replace synthetic tool with real tools from Server (send `tools/list_changed`)
    *   CIMD `muster-agent.json` hosted on GitHub Pages

2.  **Server**: Add `ValidateToken` middleware to the main HTTP entry point:
    *   Return `401 Unauthorized` with proper `WWW-Authenticate` header for unauthenticated requests
    *   Validate token signature, issuer, and audience using `mcp-oauth` library
    *   Extract session identity from validated token for downstream auth reuse

3.  **Docs**: Update deployment guide to include Dex client registration for the Agent.

## Consequences

*   **Consistent UX**: Auth flows work identically whether authenticating to Muster Server or Remote MCPs - users always see auth URLs as tool results in Cursor.
*   **Agent Complexity**: The Agent must implement the same lazy initialization pattern as Muster Server (pending auth state, synthetic tools, upgrade to connected).
*   **Code Reuse**: The Agent can reuse much of the OAuth and lazy initialization logic from the Server implementation.
*   **Double Auth**: Users might need to auth twice (once to Muster, once to Remote), but SSO/Token Reuse should minimize this.
*   **Security**: Muster is now secure by default when exposed.
