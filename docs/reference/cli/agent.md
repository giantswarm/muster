# muster agent

MCP Client for the muster aggregator server

## Synopsis

The agent command connects to the MCP aggregator as a client agent,
logs all JSON-RPC communication, and demonstrates dynamic tool updates.

This is useful for connecting the aggregator's behavior, filtering
tools, and ensuring that the agent can execute tools.

The agent command can run in three modes:
1. Normal mode (default): Connects, lists tools, and waits for notifications
2. REPL mode (--repl): Provides an interactive interface to explore and execute tools
3. MCP Server mode (--mcp-server): Runs an MCP server that exposes REPL functionality via stdio;
   the aggregator's list_changed notifications reach the assistant as the bridge's own

Transport options:
- streamable-http (default): Fast HTTP-based transport with notification support, compatible with muster serve
- sse: Server-Sent Events transport with real-time notification support

In REPL mode, you can:
- List available tools, resources, and prompts
- Get detailed information about specific items
- Execute tools interactively with JSON arguments
- View resources and retrieve their contents
- Execute prompts with arguments
- Toggle notification display

In MCP Server mode:
- The agent command acts as an MCP server using stdio transport
- It exposes all REPL functionality as MCP tools
- It's designed for integration with AI assistants like Claude or Cursor
- Configure it in your AI assistant's MCP settings

By default, it connects to the aggregator endpoint configured in your
muster configuration file. You can override this with the --endpoint flag.

Note: The aggregator server must be running (use 'muster serve') before using this command.

```
muster agent [flags]
```

## Options

```
      --auth string          Authentication mode: none (default: fail with auth_required), prompt, or auto (env: MUSTER_AUTH_MODE)
      --config-path string   Configuration directory (default "~/.config/muster")
      --context string       Use a specific context (env: MUSTER_CONTEXT)
      --disable-auto-sso     Disable automatic authentication with remote MCP servers after muster auth
      --endpoint string      Aggregator MCP endpoint URL (default: from config)
  -h, --help                 help for agent
      --json-rpc             Enable full JSON-RPC message logging
      --login                Open the browser to sign in when authentication is required (same as --auth auto)
      --mcp-server           Run as MCP server (stdio transport)
      --no-color             Disable colored output
      --repl                 Start interactive REPL mode
      --silent               Attempt silent re-auth using OIDC prompt=none (requires IdP support, not supported by Dex) (default true)
      --timeout duration     Timeout for a tool call made through the REPL or the MCP server bridge (call_tool's timeout argument overrides it for one call) (default 5m0s)
      --transport string     Transport to use (streamable-http, sse) (default "streamable-http")
      --verbose              Enable verbose logging (show keepalive messages)
```

## SEE ALSO

* [muster](README.md)	 - Aggregate MCP servers behind one authenticated endpoint
