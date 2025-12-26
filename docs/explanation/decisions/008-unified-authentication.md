# 008. Explicit Authentication State

## Status

Proposed

## Context

### The Problem

When the Muster Agent connects to a Muster Server, some remote MCP servers may require OAuth authentication. Currently, the agent detects this by **scanning tool names for `_authenticate` suffixes** (e.g., looking for `server_authenticate` tools in the tool list).

This is fragile because:
1. **Information Loss**: The tool name doesn't carry issuer URL, scope, or SSO hints
2. **Pattern Matching**: Relies on naming conventions that could break
3. **No SSO Intelligence**: Can't detect that two servers use the same IdP
4. **Requires Explicit Query**: AI must know to look for auth tools

### Current Flow (Fragile)

```
1. AI calls list_tools
2. Agent scans tool names for "_authenticate" suffix
3. Agent infers: "server_X needs auth" (loses issuer info)
4. AI must call authenticate_server_X without knowing if SSO is possible
```

### Desired Flow (Proactive)

```
1. AI calls any tool (e.g., list_tools, call_tool)
2. Response includes _meta with auth status:
   - server_X: needs auth, issuer=https://dex.example.com
   - server_Y: needs auth, issuer=https://dex.example.com (same issuer = SSO!)
3. AI sees this in EVERY response and can act on it proactively
```

## Decision

Use the MCP `_meta` field to proactively communicate authentication status in every tool response. The agent polls `auth://status` from the server and includes the result in all responses.

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
┌─────────────────────────────────────────────────────────────────────┐
│                                                                     │
│   AI (Claude/GPT)                                                   │
│        │                                                            │
│        │ calls any tool                                             │
│        ▼                                                            │
│   ┌─────────────┐         ┌─────────────────────────────────────┐   │
│   │ Muster Agent│────────►│ Muster Server                       │   │
│   │             │  polls  │                                     │   │
│   │  Caches     │ auth:// │  ┌─────────────────────────┐        │   │
│   │  auth state │ status  │  │ auth://status resource  │        │   │
│   │             │ (30s)   │  └─────────────────────────┘        │   │
│   │             │         │                                     │   │
│   │  Adds _meta │         │                                     │   │
│   │  to EVERY   │         │                                     │   │
│   │  response   │         │                                     │   │
│   └─────────────┘         └─────────────────────────────────────┘   │
│        │                                                            │
│        │ response includes _meta["giantswarm.io/auth_required"]     │
│        ▼                                                            │
│   AI sees auth hints in EVERY tool response                         │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### 1. Auth Status Resource (Server)

The server exposes `auth://status` as an MCP resource (~50 lines).

```go
// internal/aggregator/auth_resource.go

const AuthStatusResourceURI = "auth://status"

type AuthStatusResponse struct {
    Servers []ServerAuthStatus `json:"servers"`
}

type ServerAuthStatus struct {
    Name     string `json:"name"`
    Status   string `json:"status"` // "connected", "auth_required", "error"
    Issuer   string `json:"issuer,omitempty"`
    Scope    string `json:"scope,omitempty"`
    AuthTool string `json:"auth_tool,omitempty"`
    Error    string `json:"error,omitempty"`
}

func (a *AggregatorServer) registerAuthStatusResource() {
    a.mcpServer.AddResource(mcp.NewResource(
        AuthStatusResourceURI,
        "Authentication status for all MCP servers",
        "application/json",
    ), a.handleAuthStatusResource)
}

func (a *AggregatorServer) handleAuthStatusResource(ctx context.Context, req mcp.ReadResourceRequest) (string, error) {
    servers := a.registry.GetAllServers()
    response := AuthStatusResponse{Servers: make([]ServerAuthStatus, 0)}
    
    for name, info := range servers {
        status := ServerAuthStatus{
            Name:   name,
            Status: string(info.Status),
        }
        if info.Status == StatusAuthRequired && info.AuthInfo != nil {
            status.Issuer = info.AuthInfo.Issuer
            status.Scope = info.AuthInfo.Scope
            status.AuthTool = "authenticate_" + name
        }
        response.Servers = append(response.Servers, status)
    }
    
    data, _ := json.Marshal(response)
    return string(data), nil
}
```

### 2. Auth Status Polling and Caching (Agent)

The agent polls `auth://status` periodically and caches the result (~40 lines).

```go
// internal/agent/auth_poller.go

const AuthPollInterval = 30 * time.Second

type AuthRequiredInfo struct {
    Server   string `json:"server"`
    Issuer   string `json:"issuer"`
    Scope    string `json:"scope,omitempty"`
    AuthTool string `json:"auth_tool"`
}

func (s *MCPServer) startAuthPoller(ctx context.Context) {
    ticker := time.NewTicker(AuthPollInterval)
    defer ticker.Stop()
    
    s.pollAuthStatus(ctx) // Initial poll
    
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            s.pollAuthStatus(ctx)
        }
    }
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
                Server:   srv.Name,
                Issuer:   srv.Issuer,
                Scope:    srv.Scope,
                AuthTool: srv.AuthTool,
            })
        }
    }
    
    s.authCacheMu.Lock()
    s.authRequiredCache = authRequired
    s.authCacheMu.Unlock()
}
```

### 3. Adding Auth Notification to Tool Responses (Agent)

Every tool response includes:
1. **Human-readable notification** in content (tells AI what to do)
2. **Structured data** in `_meta` (for future automation)

```go
// internal/agent/server_mcp_handlers.go

func (s *MCPServer) wrapToolResult(result *mcp.CallToolResult) *mcp.CallToolResult {
    s.authCacheMu.RLock()
    authRequired := s.authRequiredCache
    s.authCacheMu.RUnlock()
    
    if len(authRequired) == 0 {
        return result
    }
    
    // Add human-readable notification that tells the AI what to do
    notification := s.buildAuthNotification(authRequired)
    result.Content = append(result.Content, mcp.TextContent{
        Type: "text",
        Text: notification,
    })
    
    // Add structured data in _meta for programmatic use
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
    
    // Group by issuer for SSO hints
    issuerServers := make(map[string][]string)
    for _, auth := range authRequired {
        issuerServers[auth.Issuer] = append(issuerServers[auth.Issuer], auth.Server)
    }
    
    for _, auth := range authRequired {
        sb.WriteString(fmt.Sprintf("- %s: call '%s' to sign in\n", auth.Server, auth.AuthTool))
    }
    
    // Add SSO hints
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
      "text": "\n---\nAuthentication Required:\n- gitlab: call 'authenticate_gitlab' to sign in\n- jira: call 'authenticate_jira' to sign in\n\nNote: gitlab and jira use the same identity provider (dex.example.com). Signing in to one will authenticate all of them.\n"
    }
  ],
  "_meta": {
    "giantswarm.io/auth_required": [
      {
        "server": "gitlab",
        "issuer": "https://dex.example.com",
        "auth_tool": "authenticate_gitlab"
      },
      {
        "server": "jira", 
        "issuer": "https://dex.example.com",
        "auth_tool": "authenticate_jira"
      }
    ]
  }
}
```

### What the AI Sees and Does

The AI reads the human-readable notification and understands:

1. **What needs auth**: "gitlab and jira require authentication"
2. **How to authenticate**: "call 'authenticate_gitlab' or 'authenticate_jira'"
3. **SSO opportunity**: "same identity provider, sign in once for both"

**Typical AI response to user:**
> "I notice that gitlab and jira require authentication before I can use their tools. Good news: they use the same identity provider, so you only need to sign in once. Would you like me to start the authentication process?"

When the user agrees, the AI calls `authenticate_gitlab`, which returns an OAuth URL for the user to open.

## What This ADR Does NOT Include

Previous iterations proposed complexity that is not needed:

- **Shared pkg/oauth package**: Code deduplication is a separate concern, not required for this feature
- **submit_auth_token tool**: Not needed; existing `authenticate_X` tools work fine
- **Issuer-keyed agent token storage**: Nice-to-have but not required for explicit auth state
- **Agent-initiated SSO**: The AI can orchestrate SSO based on the issuer info

**Estimated implementation: ~110 lines of code**

## Implementation Steps

1. Add `auth://status` resource to `internal/aggregator/server.go`
2. Add auth poller to agent in `internal/agent/auth_poller.go`
3. Wrap all tool handlers to add `_meta` in `internal/agent/server_mcp_handlers.go`
4. Ensure `ServerInfo.AuthInfo` contains issuer from 401 WWW-Authenticate header

## Consequences

### Positive

- **Proactive Notification**: AI gets auth info in every response without asking
- **SSO Awareness**: AI can see which servers share an issuer and suggest SSO
- **Minimal Change**: ~110 lines of code, no architectural changes
- **Backwards Compatible**: Existing `authenticate_X` tools still work
- **MCP Compliant**: Uses standard `_meta` field per specification

### Negative

- **Polling Overhead**: Agent polls server every 30 seconds (lightweight, ~100 bytes)
- **Response Size**: Every response includes auth info (negligible for typical 0-3 servers)

### Future Enhancements

If needed later:
- Agent-side issuer-keyed token storage for automatic SSO
- Server-side SSE notifications when auth state changes (avoid polling)
- `get_auth_status` tool for explicit queries

## Related Decisions

- [ADR-004: OAuth Proxy](004-oauth-proxy.md) - Server-side OAuth implementation
- [ADR-005: Muster Auth](005-muster-auth.md) - Agent-side OAuth implementation
- [ADR-006: Session-Scoped Tool Visibility](006-session-scoped-tool-visibility.md) - Auth affects tool visibility
