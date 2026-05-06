# muster create

Create a new resource in the muster environment.

## Synopsis

```
muster create <resource-type> <name> [flags]
```

## Description

The `create` command persists a new resource definition. The aggregator
server (`muster serve`) must be running.

## Resource Types

| Resource Type | Description | Example |
|---------------|-------------|---------|
| `workflow` | Workflow definition | `muster create workflow deploy-flow` |
| `mcpserver` | MCP server definition (stdio, streamable-http, or sse) | `muster create mcpserver my-server --type=stdio --command=mcp-foo` |

## Options

### Output Control
- `--output`, `-o` (string): Output format (`table`, `json`, `yaml`). Default: `table`.
- `--quiet`, `-q`: Suppress non-essential output.

### Configuration
- `--config-path` (string): Custom configuration directory path. Default: `~/.config/muster`.

### MCPServer Flags
- `--type` (string, required for `mcpserver`): One of `stdio`, `streamable-http`, `sse`.
- `--command` (string, stdio only): Executable to run.
- `--args` (string, stdio only): Command-line arguments.
- `--url` (string, streamable-http / sse only): Endpoint URL.
- `--timeout` (integer): Connection timeout in seconds.
- `--autoStart` (boolean): Auto-start at server boot.

Any unknown flags are forwarded as MCPServer parameters.

## Examples

### Workflows

```bash
muster create workflow deploy-flow
muster create workflow backup-db --output json
```

### MCP Servers

```bash
# stdio server
muster create mcpserver my-stdio-server \
  --type=stdio \
  --command=npx \
  --args="@modelcontextprotocol/server-git" \
  --autoStart=true

# streamable-http server
muster create mcpserver my-http-server \
  --type=streamable-http \
  --url=https://api.example.com/mcp \
  --timeout=30

# sse server
muster create mcpserver my-sse-server \
  --type=sse \
  --url=https://sse.example.com/mcp \
  --timeout=60
```

## Error Handling

### Resource Already Exists

```bash
muster create workflow deploy-flow
# Error: workflow 'deploy-flow' already exists
```

### Connection Error

```bash
muster create workflow deploy-flow
# Error: failed to connect to aggregator
#
# Solution: ensure `muster serve` is running.
```

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Resource created successfully |
| 1 | General error or invalid arguments |
| 2 | Resource already exists |
| 4 | Connection error (server not running) |

## Related Commands

- **[get](get.md)** - Retrieve created resource details
- **[list](list.md)** - List all resources of a type
- **[start](start.md)** - Start services or execute workflows
- **[check](check.md)** - Check resource availability
