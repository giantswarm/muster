# muster check

Check if resources are available and properly configured.

## Synopsis

```
muster check <resource-type> <name> [flags]
```

## Description

The `check` command verifies that an MCP server or workflow is registered
and ready to use. The aggregator server (`muster serve`) must be running.

## Resource Types

| Resource Type | Description | Example |
|---------------|-------------|---------|
| `mcpserver` | Check MCP server status and connectivity | `muster check mcpserver kubernetes` |
| `workflow` | Check if a workflow is available (all required tools present) | `muster check workflow deploy-app` |

## Options

- `--output`, `-o` (string): Output format (`table`, `json`, `yaml`). Default: `table`.
- `--quiet`, `-q`: Suppress non-essential output.
- `--config-path` (string): Custom configuration directory path. Default: `~/.config/muster`.

## Examples

### MCP Server

```bash
muster check mcpserver kubernetes

# NAME         STATUS    RESPONSIVE   TOOLS   LAST_CHECK
# kubernetes   Healthy   Yes          15      30s ago
```

### Workflow

```bash
muster check workflow deploy-application

# NAME                 STATUS      TOOLS_AVAILABLE   DEPENDENCIES
# deploy-application   Available   5/5               All met

# If dependencies are missing:
# NAME                 STATUS        TOOLS_AVAILABLE   DEPENDENCIES
# deploy-application   Unavailable   4/5               x_helm_install missing
```

## Status Indicators

### Availability Status

- **Available**: Resource is ready to use
- **Unavailable**: Resource has missing dependencies
- **Degraded**: Resource is partially available
- **Error**: Resource has configuration errors

### MCP Server Health

- **Healthy**: Server is running and responsive
- **Unhealthy**: Server is running but not responding correctly
- **Error**: Server has errors or is not running
- **Starting**: Server is in startup phase

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Resource is available and healthy |
| 1 | General error or invalid arguments |
| 2 | Resource not found |
| 3 | Resource is unavailable or unhealthy |
| 4 | Partial availability (degraded) |
| 5 | Connection error (server not running) |

## Related Commands

- **[list](list.md)** - List resources to find what to check
- **[get](get.md)** - Get detailed information after check fails
- **[start](start.md)** - Start resources after confirming availability
