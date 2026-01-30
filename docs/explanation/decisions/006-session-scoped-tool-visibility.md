# 006. Session-Scoped Tool Visibility for OAuth-Protected MCP Servers

## Context

With the introduction of OAuth authentication in [ADR 004](004-oauth-proxy.md) and [ADR 005](005-muster-auth.md), remote MCP servers can now require authentication before exposing their tools. However, the current aggregator architecture maintains a **global tool registry** that is shared across all users.

This creates a fundamental problem: **tool visibility depends on who has authenticated with which servers**, but the current implementation exposes the same tool list to everyone.

### Current Behavior (Problem)

```
User A authenticates with mcp-kubernetes → muster connects
User B authenticates with mcp-github → muster connects
User C has not authenticated with anything

Current: All three users see tools from BOTH mcp-kubernetes AND mcp-github
Expected: Each user should only see tools from servers THEY have authenticated with
```

### Scenarios Requiring Per-User Tool Visibility

1. **Different access levels**: User A has access to production Kubernetes, User B only has access to staging.

2. **Different tool permissions**: User A authenticated with full permissions, User B with read-only. The MCP server might expose different tools based on the user's authorization.

3. **Private servers**: Some MCP servers are only accessible to specific users based on their IdP group membership.

4. **Multi-tenant environments**: Each user may have access to a different set of backend systems.

## Decision

We will implement **session-scoped tool visibility**, where each user session maintains its own view of available tools based on their authentication status with each remote MCP server.

### 1. Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           Muster Server                                      │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                     Session Manager                                     │ │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐                  │ │
│  │  │ Session A    │  │ Session B    │  │ Session C    │                  │ │
│  │  │ (User A)     │  │ (User B)     │  │ (User C)     │                  │ │
│  │  │              │  │              │  │              │                  │ │
│  │  │ Token: K8s   │  │ Token: GH    │  │ Tokens: ∅    │                  │ │
│  │  │ Tool View:   │  │ Tool View:   │  │ Tool View:   │                  │ │
│  │  │  - K8s tools │  │  - GH tools  │  │  - auth_k8s  │                  │ │
│  │  │              │  │  - auth_k8s  │  │  - auth_gh   │                  │ │
│  │  └──────────────┘  └──────────────┘  └──────────────┘                  │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
│                                    │                                         │
│                                    ▼                                         │
│  ┌────────────────────────────────────────────────────────────────────────┐ │
│  │                 Session-Aware Registry                                  │ │
│  │                                                                         │ │
│  │  Global Servers:                 Per-Session Connections:               │ │
│  │  ┌─────────────────────────┐    ┌────────────────────────────────┐     │ │
│  │  │ mcp-kubernetes (OAuth)  │ ←──│ Session A: Connected (token)   │     │ │
│  │  │ mcp-github (OAuth)      │ ←──│ Session B: Connected (token)   │     │ │
│  │  │ mcp-local (no auth)     │    │ Session C: Pending auth        │     │ │
│  │  └─────────────────────────┘    └────────────────────────────────┘     │ │
│  └────────────────────────────────────────────────────────────────────────┘ │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 2. Key Concepts

#### 2.1 Server Registration Types

Servers are registered with the aggregator in two categories:

| Type | Authentication | Tool Visibility | Example |
|------|---------------|-----------------|---------|
| **Global** | None required | Visible to all sessions | mcp-local, core tools |
| **Session-Scoped** | OAuth required | Visible only after user authenticates | mcp-kubernetes, mcp-github |

#### 2.2 Session Tool View

Each session maintains its own "tool view" that is computed dynamically:

```
SessionToolView = GlobalTools + AuthenticatedServerTools + SyntheticAuthTools
```

Where:
- **GlobalTools**: Tools from servers that don't require authentication
- **AuthenticatedServerTools**: Tools from OAuth-protected servers where the user has a valid token
- **SyntheticAuthTools**: `authenticate_<server>` tools for OAuth-protected servers the user hasn't authenticated with yet

#### 2.3 Per-Session MCP Clients

For OAuth-protected servers, each session needs its **own MCP client connection** because:

1. OAuth tokens are user-specific and may grant different permissions
2. The remote MCP server may expose different tools based on the user's identity
3. Connection state (ping, health) is per-user

```
Session A → MCP Client A (token: user-a-token) → mcp-kubernetes
Session B → MCP Client B (token: user-b-token) → mcp-kubernetes
```

### 3. Implementation Architecture

#### 3.1 Session Registry

```go
// SessionRegistry manages per-session state for OAuth-protected servers.
type SessionRegistry struct {
    mu       sync.RWMutex
    sessions map[string]*SessionState  // sessionID -> state
}

// SessionState holds per-session connection state.
type SessionState struct {
    SessionID      string
    CreatedAt      time.Time
    LastActivity   time.Time
    
    // Per-server connection state for this session
    Connections map[string]*SessionConnection  // serverName -> connection
}

// SessionConnection represents a session's connection to a specific server.
type SessionConnection struct {
    ServerName  string
    Status      ConnectionStatus  // connected, pending_auth, failed
    Client      MCPClient         // Session-specific MCP client (with user's token)
    Token       *oauth.Token      // User's OAuth token for this server
    Tools       []mcp.Tool        // Tools available to THIS user (may differ by permissions)
    Resources   []mcp.Resource
    Prompts     []mcp.Prompt
}
```

#### 3.2 Tool Resolution Flow

When a session requests the tool list (`tools/list`):

```
1. Start with global tools (no auth required)
   └── Core tools, local MCP servers, etc.

2. For each OAuth-protected server:
   ├── If session has valid token AND connection:
   │   └── Include server's tools (from session-specific client)
   └── Else:
       └── Include synthetic authenticate_<server> tool

3. Return aggregated tool list specific to this session
```

#### 3.3 Tool Execution Flow

When a session executes a tool:

```
1. Identify the target server from the tool name

2. If server requires OAuth:
   ├── Get session-specific connection
   ├── Verify token is valid
   └── Execute via session's MCP client (with user's token)

3. If server is global:
   └── Execute via shared MCP client (no auth needed)
```

### 4. MCP Protocol Considerations

#### 4.1 Session Identification

The mcp-go library provides session IDs for HTTP-based transports:

```go
session := mcpserver.ClientSessionFromContext(ctx)
sessionID := session.SessionID()  // UUID per connection
```

Each MCP protocol message handler MUST use the session context to determine which user's view to present.

#### 4.2 Tool List Changed Notifications

When a user completes OAuth authentication:

```
1. Create session-specific MCP client with the new token
2. Fetch tools from the remote server (may be user-specific!)
3. Update session's tool view
4. Send tools/list_changed notification to THAT session only
```

This requires extending the aggregator to send targeted notifications:

```go
// Current: Broadcast to all sessions
a.mcpServer.NotifyToolsListChanged()

// New: Notify specific session
a.NotifySessionToolsChanged(sessionID)
```

#### 4.3 Lazy Tool Discovery

Since we can't know what tools a user has access to until they authenticate, the flow becomes:

```
Initial connection:
└── User sees: core_tools + authenticate_mcp_kubernetes + authenticate_mcp_github

After authenticating with mcp-kubernetes:
└── User sees: core_tools + kubernetes_* tools + authenticate_mcp_github

After authenticating with mcp-github:
└── User sees: core_tools + kubernetes_* tools + github_* tools
```

### 5. Token Injection for Tool Calls

When forwarding tool calls to remote OAuth-protected servers, the aggregator must inject the **user's token** from the session:

```go
func (a *AggregatorServer) forwardToolCall(ctx context.Context, serverName, toolName string, args map[string]any) (*mcp.CallToolResult, error) {
    sessionID := getSessionIDFromContext(ctx)
    
    // Get session-specific connection
    conn := a.sessionRegistry.GetConnection(sessionID, serverName)
    if conn == nil || conn.Status != StatusConnected {
        return nil, fmt.Errorf("not authenticated with %s", serverName)
    }
    
    // The session's client already has the token configured
    return conn.Client.CallTool(ctx, toolName, args)
}
```

### 6. Resource Sharing vs Isolation

Different resources have different sharing models:

| Resource | Sharing Model | Reason |
|----------|---------------|--------|
| Global server connections | Shared | No auth, same for all users |
| OAuth tokens | Per-session | User-specific credentials |
| MCP client connections | Per-session (OAuth servers) | Token embedded, may have different permissions |
| Tool lists | Per-session | May differ based on user permissions |
| OAuth metadata cache | Shared | Same IdP metadata for all users |

### 7. Session Lifecycle

#### 7.1 Session Creation

Sessions are created implicitly when a new MCP connection is established:

```go
func (a *AggregatorServer) handleInitialize(ctx context.Context, req mcp.InitializeRequest) {
    sessionID := getSessionIDFromContext(ctx)
    
    // Create session state
    a.sessionRegistry.CreateSession(sessionID)
    
    // Register unauthenticated session connections for OAuth servers
    for _, server := range a.getOAuthServers() {
        a.sessionRegistry.SetPendingAuth(sessionID, server.Name)
    }
}
```

#### 7.2 Session Cleanup

Sessions should be cleaned up when:
- MCP connection is closed
- Session has been idle for a configurable timeout (e.g., 30 minutes)
- Server is shutting down

```go
func (a *AggregatorServer) cleanupSession(sessionID string) {
    state := a.sessionRegistry.GetSession(sessionID)
    if state == nil {
        return
    }
    
    // Close session-specific MCP client connections
    for _, conn := range state.Connections {
        if conn.Client != nil {
            conn.Client.Close()
        }
    }
    
    // Remove session state
    a.sessionRegistry.DeleteSession(sessionID)
    
    // Optionally: Revoke OAuth tokens (if supported by IdP)
}
```

### 8. Backward Compatibility

To maintain backward compatibility with existing deployments:

1. **No OAuth servers configured**: Behavior unchanged; all tools visible to all users.

2. **OAuth disabled for a server**: Server's tools are globally visible (existing behavior).

3. **Mixed mode**: Global servers work as before; only OAuth-protected servers get session-scoped visibility.

### 9. SSO Mechanisms

The SSO mechanisms (ADR 004) work with session-scoped connections:

**Token Forwarding**: When muster is protected by OAuth and `forwardToken: true` is set, muster forwards its ID token to downstream servers that trust muster's client ID.

**Token Exchange**: For cross-cluster SSO where clusters have separate IdPs, muster exchanges its local token for one valid on the remote cluster's IdP using RFC 8693 Token Exchange.

### 10. Diagram: Complete OAuth + Session Flow

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                                Muster Agent                                      │
│                         (Cursor -> stdio -> Agent)                               │
└────────────────────────────────────────┬────────────────────────────────────────┘
                                         │ HTTP/SSE
                                         ▼
┌─────────────────────────────────────────────────────────────────────────────────┐
│                                Muster Server                                     │
│                                                                                  │
│  1. Agent connects → SessionID assigned (abc-123)                               │
│                                                                                  │
│  2. tools/list request from session abc-123:                                    │
│     ┌─────────────────────────────────────────────────────────────────────────┐ │
│     │ Tool View for Session abc-123:                                          │ │
│     │ ├── core_workflow_list          (global)                                │ │
│     │ ├── core_service_list           (global)                                │ │
│     │ ├── authenticate_mcp_kubernetes (synthetic - not authenticated yet)     │ │
│     │ └── authenticate_mcp_github     (synthetic - not authenticated yet)     │ │
│     └─────────────────────────────────────────────────────────────────────────┘ │
│                                                                                  │
│  3. User calls authenticate_mcp_kubernetes:                                     │
│     → Returns OAuth URL: https://dex.example.com/authorize?...                  │
│     → User completes browser auth flow                                          │
│     → Callback stores token for session abc-123                                 │
│                                                                                  │
│  4. Session abc-123 upgraded:                                                   │
│     → Create MCP client with user's token                                       │
│     → Fetch tools from mcp-kubernetes                                           │
│     → Update session tool view                                                  │
│     → Send tools/list_changed to session abc-123                                │
│     ┌─────────────────────────────────────────────────────────────────────────┐ │
│     │ Tool View for Session abc-123 (updated):                                │ │
│     │ ├── core_workflow_list          (global)                                │ │
│     │ ├── core_service_list           (global)                                │ │
│     │ ├── kubernetes_list_pods        (session-specific - authenticated!)     │ │
│     │ ├── kubernetes_get_deployment   (session-specific)                      │ │
│     │ ├── kubernetes_logs             (session-specific)                      │ │
│     │ └── authenticate_mcp_github     (synthetic - still not authenticated)   │ │
│     └─────────────────────────────────────────────────────────────────────────┘ │
│                                                                                  │
│  5. Meanwhile, Session xyz-789 (different user) connects:                        │
│     ┌─────────────────────────────────────────────────────────────────────────┐ │
│     │ Tool View for Session xyz-789:                                          │ │
│     │ ├── core_workflow_list          (global)                                │ │
│     │ ├── core_service_list           (global)                                │ │
│     │ ├── authenticate_mcp_kubernetes (synthetic - this user hasn't authed)   │ │
│     │ └── authenticate_mcp_github     (synthetic)                             │ │
│     └─────────────────────────────────────────────────────────────────────────┘ │
│     Note: Session xyz-789 does NOT see kubernetes tools!                        │
└─────────────────────────────────────────────────────────────────────────────────┘
```

## Consequences

### Benefits

1. **True multi-user support**: Each user sees only the tools they have access to.

2. **Per-user permissions**: Remote MCP servers can expose different tools based on user identity.

3. **Security isolation**: Users cannot see or call tools from servers they haven't authenticated with.

4. **Incremental authentication**: Users can authenticate with servers as needed, building up their tool set.

5. **Audit trail**: Tool access is traceable to specific authenticated users.

### Costs

1. **Increased complexity**: The aggregator now manages per-session state, not just global state.

2. **Memory overhead**: Each session maintains its own MCP client connections for OAuth servers.

3. **Connection scaling**: N users × M OAuth servers = N×M potential connections.

4. **Protocol extension**: Need to target notifications to specific sessions.

5. **Tool list latency**: First tool list for a new session requires checking all server auth states.

### Mitigations

| Cost | Mitigation |
|------|------------|
| Memory overhead | Session idle timeout, lazy connection establishment |
| Connection scaling | Connection pooling, shared connections for same-token users |
| Tool list latency | Cache global tools, async session connection establishment |
| Protocol complexity | Abstract behind SessionAwareRegistry interface |

## Implementation Steps

1. **Create SessionRegistry**: Implement session-scoped state management alongside the existing global registry.

2. **Extend token storage**: Link tokens to session connections, not just session IDs.

3. **Session-aware tool listing**: Modify `GetAllTools()` to accept a session context and compute per-session views.

4. **Per-session MCP clients**: Create and manage MCP client instances per session for OAuth servers.

5. **Targeted notifications**: Extend the MCP server wrapper to send notifications to specific sessions.

6. **Session cleanup**: Implement idle session detection and cleanup with connection pooling.

7. **Testing**: Add comprehensive tests for multi-session scenarios with different auth states.

## Open Questions

1. **Connection pooling**: Can we share MCP client connections when multiple users have the same token (e.g., service accounts)?

2. **Token refresh coordination**: When a token is refreshed, how do we update all session connections using it?

3. **Stdio transport**: Stdio is inherently single-user; should we skip session management entirely for stdio?

4. **Tool list caching**: How long can we cache a session's tool list before re-checking server connections?

