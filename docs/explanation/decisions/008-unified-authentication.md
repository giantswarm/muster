# 008. Explicit Authentication State

## Status

Accepted

## Context

### The Problem

When the Muster Agent connects to a Muster Server, some remote MCP servers may require OAuth authentication. Currently, the agent detects this by **scanning tool names for `_authenticate` suffixes** (e.g., looking for `server_authenticate` tools in the tool list).

This is fragile because:
1. **Information Loss**: The tool name doesn't carry issuer URL, scope, or SSO hints
2. **Pattern Matching**: Relies on naming conventions that could break
3. **No SSO Intelligence**: Can't detect that two servers use the same IdP
4. **Requires Explicit Query**: AI must know to look for auth tools
5. **Pollutes Server Namespace**: Synthetic auth tools are injected into each server's tool list

### Current Flow (Fragile)

```
1. AI calls list_tools
2. Agent scans tool names for "_authenticate" suffix
3. Agent infers: "server_X needs auth" (loses issuer info)
4. AI must call x_server_X_authenticate without knowing if SSO is possible
```

### Desired Flow (Proactive)

```
1. AI calls any tool (e.g., list_tools, call_tool)
2. Response includes _meta with auth status:
   - server_X: needs auth, issuer=https://dex.example.com
   - server_Y: needs auth, issuer=https://dex.example.com (same issuer = SSO!)
3. AI sees this in EVERY response and can act on it proactively
4. AI calls core_auth_login with server parameter to authenticate
```

## Decision

### Core Principle

**Authentication is a muster platform concern, not an MCP server concern.**

MCP servers should expose their actual tools unchanged. Muster handles authentication orchestration through:
- The `auth://status` **resource** for status (data/context)
- Core **tools** for actions (`core_auth_login`, `core_auth_logout`)

This follows MCP philosophy:
- **Resources** are application-driven - the Muster Agent reads `auth://status` and decides how to present it to the model
- **Tools** are model-controlled - the AI can invoke `core_auth_login` when it decides authentication is needed

### MCP _meta Specification

Per the MCP specification, `_meta` is reserved for additional metadata. We use a namespaced key following the format:

```
giantswarm.io/auth_required
```

This follows the MCP key naming rules:
- Prefix: `giantswarm.io/` (our namespace)
- Name: `auth_required` (alphanumeric with underscores)

### Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                                                                         │
│   User → LLM (Cursor) → MCP Client (IDE)                                │
│                              │                                          │
│                              │ calls tools                              │
│                              ▼                                          │
│   ┌───────────────────────────────────────────────────────────────┐     │
│   │ Muster Agent (MCP Server to IDE)                              │     │
│   │                                                               │     │
│   │  - Exposes core tools to IDE                                  │     │
│   │  - Polls auth://status from aggregator                        │     │
│   │  - Adds _meta to every response                               │     │
│   │                                                               │     │
│   └───────────────────────────────────────────────────────────────┘     │
│                              │                                          │
│                              │ MCP Client connection                    │
│                              ▼                                          │
│   ┌───────────────────────────────────────────────────────────────┐     │
│   │ Muster Aggregator (MCP Server)                                │     │
│   │                                                               │     │
│   │  Resources:                                                   │     │
│   │    auth://status    → Auth state for all servers              │     │
│   │                                                               │     │
│   │  Core Tools:                                                  │     │
│   │    core_auth_login  → Initiate OAuth for a server             │     │
│   │    core_auth_logout → Clear session for a server              │     │
│   │                                                               │     │
│   └───────────────────────────────────────────────────────────────┘     │
│                              │                                          │
│              ┌───────────────┼───────────────┐                          │
│              ▼               ▼               ▼                          │
│   ┌──────────────┐  ┌──────────────┐  ┌──────────────┐                  │
│   │ gitlab       │  │ jira         │  │ github       │                  │
│   │ (OAuth)      │  │ (OAuth)      │  │ (no auth)    │                  │
│   │              │  │              │  │              │                  │
│   │ Tools:       │  │ Tools:       │  │ Tools:       │                  │
│   │ - create_mr  │  │ - create_tkt │  │ - create_pr  │                  │
│   │ - list_repos │  │ - search     │  │ - list_repos │                  │
│   │ (pure, no    │  │ (pure, no    │  │              │                  │
│   │  synthetic)  │  │  synthetic)  │  │              │                  │
│   └──────────────┘  └──────────────┘  └──────────────┘                  │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

### 1. Auth Status Resource (Aggregator)

The aggregator exposes `auth://status` as an MCP resource.

```go
// internal/aggregator/auth_resource.go

const AuthStatusResourceURI = "auth://status"

type AuthStatusResponse struct {
    Servers []ServerAuthStatus `json:"servers"`
}

type ServerAuthStatus struct {
    Name   string `json:"name"`
    Status string `json:"status"` // "connected", "auth_required", "disconnected"
    Issuer string `json:"issuer,omitempty"`
    Scope  string `json:"scope,omitempty"`
    Error  string `json:"error,omitempty"`
}
```

### 2. Core Auth Tools (Aggregator)

The aggregator exposes core tools for authentication actions.

| Tool | Description | Input Schema |
|------|-------------|--------------|
| `core_auth_login` | Initiate OAuth login flow for a specific MCP server | `{ "server": string }` |
| `core_auth_logout` | Clear authentication session for a specific MCP server | `{ "server": string }` |

```go
// internal/api/core_auth.go

// core_auth_login initiates OAuth for the specified server
// Returns the OAuth URL for the user to open in their browser
func handleCoreAuthLogin(ctx context.Context, server string) (*mcp.CallToolResult, error) {
    // Trigger OAuth flow for the server
    // Return URL for user to complete authentication
}

// core_auth_logout clears the session for the specified server
func handleCoreAuthLogout(ctx context.Context, server string) (*mcp.CallToolResult, error) {
    // Clear session tokens for the server
    // Server tools will be hidden until re-authentication
}
```

### 3. Auth Status Polling and Caching (Agent)

The agent polls `auth://status` periodically and caches the result.

```go
// internal/agent/auth_poller.go

const AuthPollInterval = 30 * time.Second

type AuthRequiredInfo struct {
    Server string `json:"server"`
    Issuer string `json:"issuer"`
    Scope  string `json:"scope,omitempty"`
}

func (s *MCPServer) pollAuthStatus(ctx context.Context) {
    resource, err := s.musterClient.GetResource(ctx, "auth://status")
    if err != nil {
        return // Silently continue with cached data
    }
    
    var status AuthStatusResponse
    if err := json.Unmarshal([]byte(resource.Contents), &status); err != nil {
        return
    }
    
    var authRequired []AuthRequiredInfo
    for _, srv := range status.Servers {
        if srv.Status == "auth_required" {
            authRequired = append(authRequired, AuthRequiredInfo{
                Server: srv.Name,
                Issuer: srv.Issuer,
                Scope:  srv.Scope,
            })
        }
    }
    
    s.authCacheMu.Lock()
    s.authRequiredCache = authRequired
    s.authCacheMu.Unlock()
}
```

### 4. Adding Auth Notification to Tool Responses (Agent)

Every tool response includes:
1. **Human-readable notification** in content (tells AI what to do)
2. **Structured data** in `_meta` (for programmatic use)

```go
// internal/agent/server_mcp_handlers.go

func (s *MCPServer) wrapToolResult(result *mcp.CallToolResult) *mcp.CallToolResult {
    s.authCacheMu.RLock()
    authRequired := s.authRequiredCache
    s.authCacheMu.RUnlock()
    
    if len(authRequired) == 0 {
        return result
    }
    
    // Add human-readable notification
    notification := s.buildAuthNotification(authRequired)
    result.Content = append(result.Content, mcp.TextContent{
        Type: "text",
        Text: notification,
    })
    
    // Add structured data in _meta
    if result.Meta == nil {
        result.Meta = make(map[string]interface{})
    }
    result.Meta["giantswarm.io/auth_required"] = authRequired
    
    return result
}

func (s *MCPServer) buildAuthNotification(authRequired []AuthRequiredInfo) string {
    var sb strings.Builder
    sb.WriteString("\n---\n")
    sb.WriteString("Authentication Required:\n")
    
    for _, auth := range authRequired {
        sb.WriteString(fmt.Sprintf("- %s: use core_auth_login tool with server='%s'\n", 
            auth.Server, auth.Server))
    }
    
    // Group by issuer for SSO hints
    issuerServers := make(map[string][]string)
    for _, auth := range authRequired {
        issuerServers[auth.Issuer] = append(issuerServers[auth.Issuer], auth.Server)
    }
    
    for issuer, servers := range issuerServers {
        if len(servers) > 1 {
            sb.WriteString(fmt.Sprintf("\nNote: %s use the same identity provider (%s). ",
                strings.Join(servers, " and "), issuer))
            sb.WriteString("Signing in to one will authenticate all of them.\n")
        }
    }
    
    return sb.String()
}
```

### 5. Tool Visibility

| Server State | What's Visible |
|--------------|----------------|
| `auth_required` (session not authenticated) | No tools from that server |
| `connected` (session authenticated) | All actual tools from that server, unchanged |
| Core tools | Always visible (`core_auth_*`, `core_mcpserver_*`, etc.) |

No synthetic `x_<server>_authenticate` tools are injected. Server tool lists remain pure.

### Example Tool Response

When the AI calls any tool, the response includes clear instructions:

```json
{
  "content": [
    {
      "type": "text",
      "text": "Available tools: github_create_issue, github_list_repos, ..."
    },
    {
      "type": "text",
      "text": "\n---\nAuthentication Required:\n- gitlab: use core_auth_login tool with server='gitlab'\n- jira: use core_auth_login tool with server='jira'\n\nNote: gitlab and jira use the same identity provider (dex.example.com). Signing in to one will authenticate all of them.\n"
    }
  ],
  "_meta": {
    "giantswarm.io/auth_required": [
      {
        "server": "gitlab",
        "issuer": "https://dex.example.com"
      },
      {
        "server": "jira", 
        "issuer": "https://dex.example.com"
      }
    ]
  }
}
```

### What the AI Sees and Does

The AI reads the human-readable notification and understands:

1. **What needs auth**: "gitlab and jira require authentication"
2. **How to authenticate**: "use core_auth_login tool with server parameter"
3. **SSO opportunity**: "same identity provider, sign in once for both"

**Typical AI response to user:**
> "I notice that gitlab and jira require authentication before I can use their tools. Good news: they use the same identity provider, so you only need to sign in once. Would you like me to start the authentication process?"

When the user agrees, the AI invokes the tool:

```json
{
  "name": "core_auth_login",
  "arguments": { "server": "gitlab" }
}
```

The tool returns an OAuth URL for the user to open in their browser.

## What This ADR Does NOT Include

Previous iterations proposed complexity that is not needed:

- **Synthetic per-server authenticate tools**: Replaced by `core_auth_login`
- **Shared pkg/oauth package**: Code deduplication is a separate concern
- **Issuer-keyed agent token storage**: Nice-to-have but not required for explicit auth state
- **Agent-initiated SSO**: The AI can orchestrate SSO based on the issuer info

## Implementation Steps

1. Add `core_auth_login` tool to `internal/api/`
2. Add `core_auth_logout` tool to `internal/api/`
3. Remove synthetic `x_<server>_authenticate` tool injection
4. Remove `IsSyntheticAuthTool()` and related logic
5. Update `_meta` notifications to reference `core_auth_login`
6. Ensure tool visibility hides unauthenticated server tools (no synthetic replacement)

## Consequences

### Positive

- **Clean Separation**: MCP servers expose only their actual capabilities
- **Stable Interface**: `core_auth_*` tools are always available, predictable
- **Discoverable**: Auth is in the `core_*` namespace where users expect muster functionality
- **Logout Support**: Natural place for session management via `core_auth_logout`
- **Consistent Patterns**: Matches `core_mcpserver_*`, `core_config_*`
- **Simpler Implementation**: No synthetic tool injection/hiding logic
- **SSO Awareness**: AI can see which servers share an issuer and suggest SSO
- **MCP Compliant**: Uses standard `_meta` field per specification

### Negative

- **Polling Overhead**: Agent polls server every 30 seconds (lightweight, ~100 bytes)
- **Response Size**: Every response includes auth info (negligible for typical 0-3 servers)

### Future Enhancements

If needed later:
- Agent-side issuer-keyed token storage for automatic SSO
- Server-side SSE notifications when auth state changes (avoid polling)

## Related Decisions

- [ADR-004: OAuth Proxy](004-oauth-proxy.md) - Server-side OAuth implementation
- [ADR-005: Muster Auth](005-muster-auth.md) - Agent-side OAuth implementation
- [ADR-006: Session-Scoped Tool Visibility](006-session-scoped-tool-visibility.md) - Auth affects tool visibility
