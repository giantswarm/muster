# muster standalone

Start Muster in standalone mode (aggregator server + agent in single process).

## Synopsis

```
muster standalone [OPTIONS]
```

## Description

The `standalone` command starts both the Muster aggregator server and the agent in a single process. This mode is specifically designed for AI assistant integration, automatically configuring the agent to run as an MCP server with stdio transport.

This command combines the functionality of `muster serve` and `muster agent --mcp-server`, making it ideal for:
- AI assistant integration (Claude, Cursor, etc.)
- MCP server deployments
- Development environments where you need both components

**Key Behaviors:**
- Automatically enables MCP server mode for the agent (`--mcp-server`)
- Automatically disables serve logging (`--silent`) to prevent interference with MCP protocol
- Runs both components concurrently until one exits or encounters an error

## Options

The `standalone` command inherits **all flags** from both the `serve` and `agent` commands:

### Server Configuration (from `serve`)
- `--config-path` (string): Custom configuration directory path
  - Default: `~/.config/muster`
- `--debug`: Enable debug-level logging and verbose output
- `--yolo`: Disable denylist for destructive tool calls

### Agent Configuration (from `agent`)
- `--endpoint` (string): Aggregator MCP endpoint URL
- `--transport` (string): Transport protocol (`streamable-http`, `sse`)
- `--timeout` (duration): Timeout for operations
- `--verbose`: Enable verbose logging
- `--no-color`: Disable colored output
- `--json-rpc`: Enable full JSON-RPC message logging

**Note:** The `--silent` flag from `serve` and `--mcp-server` flag from `agent` are automatically enabled and cannot be overridden in standalone mode.

## Examples

### Basic MCP Server
```bash
# Start as MCP server for AI assistants
muster standalone

# The process will:
# 1. Start the aggregator server (silently)
# 2. Start the agent in MCP server mode
# 3. Communicate via stdio for MCP protocol
```

### Debug Mode
```bash
# Start with debug logging (server-side only)
muster standalone --debug

# Note: Agent output is automatically suppressed to maintain MCP protocol integrity
```

### Custom Configuration
```bash
# Use custom configuration directory
muster standalone --config-path /etc/muster

# Development with custom config
muster standalone --config-path ./dev-config --debug
```

### Advanced Transport Configuration
```bash
# Use SSE transport for real-time updates
muster standalone --transport sse --verbose

# Enable JSON-RPC debugging (careful with AI assistants)
muster standalone --json-rpc --debug
```

## AI Assistant Integration

### Cursor Setup
Add to your Cursor MCP settings:

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

### Claude Desktop Setup
Add to your Claude configuration:

```json
{
  "mcpServers": {
    "muster": {
      "command": "muster",
      "args": ["standalone"],
      "env": {
        "MUSTER_CONFIG_PATH": "/path/to/your/config"
      }
    }
  }
}
```

## Process Management

The standalone command manages two concurrent processes:

1. **Aggregator Server**: Loads configuration, starts MCP servers, manages services
2. **Agent**: Connects to aggregator and exposes tools via MCP protocol

The command exits when:
- Either process encounters a fatal error
- The parent process receives a termination signal (Ctrl+C)
- The AI assistant disconnects and terminates the MCP session

## Troubleshooting

### Connection Issues
```bash
# Check if both components are starting correctly
muster standalone --debug --verbose

# Verify configuration is valid
muster check --config-path ./your-config
```

### MCP Protocol Issues
```bash
# Enable JSON-RPC debugging (will interfere with normal operation)
muster standalone --json-rpc

# Check endpoint connectivity
muster agent --endpoint http://localhost:8080/mcp
```

### Configuration Problems
```bash
# Test configuration separately
muster serve --config-path ./your-config --debug
# Then in another terminal:
muster agent --mcp-server --endpoint http://localhost:8080/mcp
```

## Related Commands

- [`muster serve`](serve.md) - Start aggregator server only
- [`muster agent`](agent.md) - Start agent only (with MCP server option)
- [`muster check`](check.md) - Validate configuration before running standalone 