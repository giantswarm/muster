# Connect MCP clients

muster is an MCP server. Any client that speaks the Model Context Protocol connects to it and
receives the meta-tools; the aggregated catalogue is reached through them. This guide has the
configuration for the common clients, locally and against a remote muster.

## Which entry point

| Situation | Configure the client with |
|---|---|
| One IDE on a laptop, nothing else running | `muster standalone`: aggregator and stdio bridge in one process |
| A `muster serve` you keep running, or several clients on one machine | `muster agent --mcp-server`: stdio bridge to the local aggregator |
| A remote muster and a client that cannot do OAuth itself | `muster agent --mcp-server --endpoint https://muster.example.com/mcp`: the bridge handles the browser login |
| A remote muster and a client that speaks MCP over HTTP with OAuth | the endpoint URL directly, no bridge |

`standalone` and `agent --mcp-server` speak stdio towards the client, which every desktop client
supports. The bridge forwards MCP messages unchanged; the meta-tools come from the aggregator.

## Cursor

`~/.cursor/mcp.json` (global) or `.cursor/mcp.json` in a project:

```json
{
  "mcpServers": {
    "muster": {
      "command": "muster",
      "args": ["standalone"]
    }
  }
}
```

For a remote muster replace the arguments with
`["agent", "--mcp-server", "--endpoint", "https://muster.example.com/mcp"]`.

## VS Code

`.vscode/mcp.json` in the workspace, or the user-level `mcp.json` from the command palette
(`MCP: Open User Configuration`):

```json
{
  "servers": {
    "muster": {
      "type": "stdio",
      "command": "muster",
      "args": ["standalone"]
    }
  }
}
```

VS Code also connects over HTTP: `{"type": "http", "url": "http://localhost:8090/mcp"}` against a
local `muster serve`, or the URL of a remote muster, where VS Code runs the OAuth login itself.

## Claude Code

```bash
claude mcp add muster -- muster standalone
```

or, against a running aggregator, over HTTP:

```bash
claude mcp add --transport http muster http://localhost:8090/mcp
claude mcp add --transport http muster https://muster.example.com/mcp   # remote, OAuth login in the browser
```

## Claude Desktop

`claude_desktop_config.json` (macOS: `~/Library/Application Support/Claude/`, Windows:
`%APPDATA%\Claude\`):

```json
{
  "mcpServers": {
    "muster": {
      "command": "muster",
      "args": ["standalone"]
    }
  }
}
```

## Any client, over HTTP

The aggregator serves MCP over streamable HTTP at `/mcp` (and the older SSE transport at `/sse`).
A client that implements the MCP authorization flow connects to a protected muster without a
bridge: muster publishes the protected-resource metadata (`/.well-known/oauth-protected-resource`),
accepts clients that identify themselves with a client metadata document (CIMD) and offers
dynamic client registration where the deployment allows it. The
[HTTP endpoints](../reference/api.md) reference lists every path.

Clients that can set request headers can declare a [toolset](../reference/toolsets.md) with
`X-muster-Toolset`, for example `preset:read-only`, and see and call only that part of the
catalogue.

## Remote muster through the bridge

For clients that only speak stdio, `muster agent --mcp-server` is the bridge to a remote muster.
On the first request that needs it, the bridge opens the browser for the login, stores the
tokens under `~/.config/muster/tokens/` and refreshes them from then on.

```bash
muster context add prod --endpoint https://muster.example.com/mcp --use
muster auth login
```

With a context in place, `["agent", "--mcp-server"]` needs no `--endpoint`; the bridge uses the
current context. `--context <name>` or `MUSTER_CONTEXT` select another one, and
`--disable-auto-sso` stops the bridge from also signing you in to the remote MCP servers that
muster forwards your identity to. [Authenticate the CLI](authenticate-the-cli.md) has the details.

## Checking the connection

Ask the assistant which tools are available: it should call `list_tools` and describe a paged
catalogue. If the client shows no tools, run the same command the client runs in a terminal
(`muster standalone --mcp-server` or `muster agent --mcp-server`) and read its output on stderr;
`muster agent --repl` against the same endpoint shows whether the aggregator itself is
reachable. [Troubleshooting AI agents](ai-troubleshooting.md) continues from there.
