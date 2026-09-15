# muster serve

Start the muster aggregator server.

## Synopsis

Start the muster aggregator: the process that connects to the registered
MCP servers, aggregates their tools and serves them over one MCP endpoint
(http://localhost:8090/mcp by default).

  - Registered MCP servers are connected, or started when they are stdio
    servers with autoStart, and reconnected when they fail.
  - Every other muster command (list, get, create, call, start, stop, check,
    events) talks to this endpoint.
  - An MCP client connects to the endpoint directly, or through
    'muster agent --mcp-server' when it needs a stdio transport.

Configuration is read from ~/.config/muster unless --config-path names another
directory. The directory holds config.yaml, mcpservers/ with one MCPServer
definition per file and workflows/ with one Workflow per file. With
'kubernetes: true' in config.yaml the definitions are read from the MCPServer
and Workflow custom resources of the configured namespace instead.

OAuth protection of the endpoint and OAuth towards remote MCP servers are
configured in config.yaml; the --oauth-* flags switch them on for a quick
local trial. See https://giantswarm.github.io/muster/ for the reference.

```
muster serve [flags]
```

## Options

```
      --config-path string                   Configuration directory (default "~/.config/muster")
      --debug                                Enable general debug logging
      --extra-ca-file string                 PEM file whose certificates are appended to the system trust pool at startup
  -h, --help                                 help for serve
      --oauth-mcp-client                     Enable OAuth MCP client/proxy for remote MCP server authentication
      --oauth-mcp-client-id string           OAuth client identifier (CIMD URL). If empty, auto-derived from public URL
      --oauth-mcp-client-public-url string   Publicly accessible URL of the Muster Server for OAuth callbacks
      --oauth-server                         Enable OAuth 2.1 protection for Muster Server (requires config file for full setup)
      --oauth-server-base-url string         Base URL of the Muster Server for OAuth (e.g., https://muster.example.com)
      --silent                               Disable console log output. Does not silence OTLP — unset OTEL_EXPORTER_OTLP_* or set OTEL_SDK_DISABLED=true for that.
```

## SEE ALSO

* [muster](README.md)	 - Aggregate MCP servers behind one authenticated endpoint
