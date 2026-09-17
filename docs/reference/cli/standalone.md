# muster standalone

Start the muster in standalone mode

## Synopsis

Standalone mode starts the muster aggregator server and the agent in a single process.
It enforces the MCP server mode for the agent and disables serve logging.

```
muster standalone [flags]
```

## Options

```
      --auth string                          Authentication mode: none (default: fail with auth_required), prompt, or auto (env: MUSTER_AUTH_MODE)
      --config-path string                   Configuration directory (default "~/.config/muster")
      --context string                       Use a specific context (env: MUSTER_CONTEXT)
      --debug                                Enable general debug logging
      --disable-auto-sso                     Disable automatic authentication with remote MCP servers after muster auth
      --endpoint string                      Aggregator MCP endpoint URL (default: from config)
      --extra-ca-file string                 PEM file whose certificates are appended to the system trust pool at startup
  -h, --help                                 help for standalone
      --json-rpc                             Enable full JSON-RPC message logging
      --login                                Open the browser to sign in when authentication is required (same as --auth auto)
      --mcp-server                           Run as MCP server (stdio transport)
      --no-color                             Disable colored output
      --oauth-mcp-client                     Enable OAuth MCP client/proxy for remote MCP server authentication
      --oauth-mcp-client-id string           OAuth client identifier (CIMD URL). If empty, auto-derived from public URL
      --oauth-mcp-client-public-url string   Publicly accessible URL of the muster server for OAuth callbacks
      --oauth-server                         Enable OAuth 2.1 protection for muster server (requires config file for full setup)
      --oauth-server-base-url string         Base URL of the muster server for OAuth (e.g., https://muster.example.com)
      --repl                                 Start interactive REPL mode
      --silent                               Attempt silent re-auth using OIDC prompt=none (requires IdP support, not supported by Dex) (default true)
      --timeout duration                     Timeout for waiting for notifications (default 5m0s)
      --transport string                     Transport to use (streamable-http, sse) (default "streamable-http")
      --verbose                              Enable verbose logging (show keepalive messages)
```

## SEE ALSO

* [muster](README.md)	 - Aggregate MCP servers behind one authenticated endpoint
