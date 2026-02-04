# ADR-010: Server-Side Meta-Tools Migration

## Status

Accepted

## Context

Prior to this migration, muster's architecture had a split responsibility for tool management:

1. **Aggregator** exposed 36+ core tools directly to MCP clients
2. **Agent** (`muster agent --mcp-server`) provided 11 meta-tools for AI assistants

This created several problems:

- **Duplicated logic**: Tool listing, filtering, and execution logic existed in both places
- **Inconsistent visibility**: Session-scoped tool visibility was harder to enforce
- **Complex agent**: The agent had significant responsibility for tool management
- **Testing complexity**: Meta-tools needed separate testing from core tools

## Decision

We migrate all meta-tools to the server-side (aggregator) with the following architecture:

### Tool Exposure

The aggregator now exposes **ONLY meta-tools** to MCP clients:

| Meta-tool | Description |
|-----------|-------------|
| `list_tools` | List all available tools for the session |
| `describe_tool` | Get detailed schema for a specific tool |
| `call_tool` | Execute any tool by name |
| `list_resources` | List available MCP resources |
| `describe_resource` | Get resource metadata |
| `get_resource` | Read resource contents |
| `list_prompts` | List available prompts |
| `describe_prompt` | Get prompt details |
| `get_prompt` | Execute a prompt |
| `filter_tools` | Search tools by pattern |
| `list_core_tools` | List muster core tools only |

### Tool Execution Flow

All tool calls now go through the `call_tool` meta-tool:

```
AI Agent → call_tool(name="core_workflow_list", args={})
         → Aggregator.CallToolInternal()
         → callCoreToolDirectly() or backend server
         → Result wrapped in structured JSON response
```

### Session-Scoped Visibility

The `list_tools` response now includes:

1. **Available tools**: Tools from connected/authenticated servers
2. **Servers requiring auth**: Information about OAuth-protected servers that need authentication

```json
{
  "tools": [
    {"name": "list_tools", "description": "..."},
    {"name": "call_tool", "description": "..."}
  ],
  "servers_requiring_auth": [
    {"name": "github", "status": "auth_required", "auth_tool": "core_auth_login"}
  ]
}
```

### Implementation Components

1. **`internal/metatools/`** - Meta-tool definitions and handlers
2. **`internal/api/metatools.go`** - Interfaces for data provider and handler
3. **`internal/aggregator/tool_factory.go`** - Creates only meta-tools for MCP exposure
4. **`internal/aggregator/server.go`** - Implements `MetaToolsDataProvider` interface

## Consequences

### Positive

1. **Simpler agent**: Agent becomes a thin OAuth shim + transport bridge (~200 lines vs ~700)
2. **Direct access**: OAuth-capable clients can connect to aggregator without agent
3. **Centralized logic**: Meta-tool logic lives in one place (server)
4. **Session consistency**: Server-side meta-tools use session-scoped tool visibility
5. **Better testability**: Meta-tools can be tested as part of server tests
6. **Auth visibility**: Users see which servers need authentication via `list_tools`

### Negative

1. **Breaking change**: All tool calls must use `call_tool` wrapper
2. **Response wrapping**: Tool results are wrapped in JSON, requiring unwrapping
3. **Migration required**: Test clients and integrations need updating

### Neutral

- CLI commands continue working (transparent to end users)
- Agent REPL continues working
- BDD test scenarios continue working (after test client update)
- MCP native protocol methods (`tools/list`, `tools/call`) continue working

## Implementation Notes

### Test Client Changes

The BDD test client (`internal/testing/mcp_client.go`) wraps all tool calls through `call_tool`:

```go
func (c *mcpTestClient) CallTool(ctx context.Context, toolName string, toolArgs map[string]interface{}) (interface{}, error) {
    // Wrap through call_tool meta-tool
    metaToolArgs := map[string]interface{}{
        "name":      toolName,
        "arguments": toolArgs,
    }
    result, err := c.client.CallTool(callCtx, request)
    // Unwrap the nested response
    return c.unwrapMetaToolResponse(result, toolName)
}
```

### Error Message Changes

When a tool's server is disconnected, the error message changed from "tool not found" to "server not found" for better clarity.

## Related Issues

- Epic: #341 - Server-Side Meta-Tools Migration
- Issue: #342 - Create internal/metatools/ package (completed)
- Issue: #343 - Core Integration (this ADR)
- Issue: #344 - Agent Simplification (future)
- Issue: #345 - Documentation & ADR (this document)

## References

- ADR-006: Session-Scoped Tool Visibility
- ADR-008: Unified Authentication
