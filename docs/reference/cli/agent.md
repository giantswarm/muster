# muster agent

MCP client for the Muster aggregator server with interactive and server modes.

## Synopsis

```bash
muster agent [OPTIONS]
```

## Description

The `agent` command connects to the Muster aggregator server, providing multiple ways to interact with tools, resources, and workflows. In MCP server mode, it acts as a **transport bridge** that forwards MCP messages between stdio (for AI assistants) and HTTP/SSE (to the server), handling OAuth authentication when needed.

**Prerequisites**: The aggregator server must be running (`muster serve`) before using this command.

**Architecture Note**: The server exposes meta-tools (`list_tools`, `call_tool`, etc.) as its only interface. The agent does not process these meta-tools locally - it forwards all MCP messages to the server. OAuth-capable MCP clients can connect directly to the server, bypassing the agent.

## Operation Modes

### 1. Normal Mode (Default)
Connects to the aggregator, and monitors for real-time changes.

```bash
muster agent
# Connects and displays:
# - Available tools from all MCP servers
# - Real-time notifications of changes
# - Connection status and health
```

### 2. REPL Mode (`--repl`)
Provides an interactive command-line interface for exploring and executing tools.

```bash
muster agent --repl
# Opens interactive shell with commands:
# > list tools
# > describe tool kubernetes_get_pods
# > call kubernetes_get_pods {"namespace": "default"}
```

### 3. MCP Server Mode (`--mcp-server`)
Runs as an MCP transport bridge for AI assistant integration.

```bash
muster agent --mcp-server
# Runs as stdio MCP server, bridging to the aggregator via HTTP/SSE
```

**Transport Bridge Role:**
- Bridges stdio ↔ HTTP/SSE transport protocols
- Handles OAuth authentication when the server requires it
- Forwards all MCP protocol messages to the aggregator server
- The server exposes meta-tools (`list_tools`, `call_tool`, etc.) - the agent does not process them locally

## Options

### Connection Configuration

- `--endpoint` (string): Aggregator MCP endpoint URL
  - Default: Auto-detected from configuration
  - Format: `http://localhost:8080/mcp` (streamable-http) or `http://localhost:8080/sse` (SSE)
- `--transport` (string): Transport protocol to use
  - Options: `streamable-http` (default), `sse`
  - `sse`: Real-time bidirectional communication with notifications
  - `streamable-http`: Request-response pattern for compatibility
- `--timeout` (duration): Timeout for operations
  - Default: `5m`
  - Format: `30s`, `5m`, `1h`

### Output and Logging

- `--verbose`: Enable verbose logging including keepalive messages
  - Default: `false`
  - Shows detailed connection and operation information
- `--no-color`: Disable colored output
  - Default: `false`
  - Useful for scripting or terminals without color support
- `--json-rpc`: Enable full JSON-RPC message logging
  - Default: `false`
  - Shows complete protocol messages for debugging

### Configuration

- `--config-path` (string): Custom configuration directory path
  - Default: `~/.config/muster`
  - Used for endpoint auto-detection

### Mode Selection

- `--repl`: Start interactive REPL mode
  - Mutually exclusive with `--mcp-server`
- `--mcp-server`: Run as MCP server (stdio transport)
  - Mutually exclusive with `--repl`

## Examples

### Basic Monitoring

```bash
# Connect and monitor tool changes
muster agent

# With verbose logging
muster agent --verbose

# Using SSE transport for real-time updates
muster agent --transport sse --verbose
```

### Interactive Exploration (REPL Mode)

```bash
# Start interactive session
muster agent --repl

# Interactive commands:
> help                           # Show available commands
> list tools                     # List all available tools
> filter tools kubernetes        # Filter tools by pattern
> describe tool x_kubernetes_get_pods  # Get tool details
> call x_kubernetes_get_pods {"namespace": "default"}  # Execute tool
> list workflows                 # Show available workflows
> workflow deploy-app env=prod   # Execute workflow
> exit                          # Exit REPL
```

### AI Assistant Integration

```bash
# Run as MCP server for AI assistants
muster agent --mcp-server

# With custom endpoint
muster agent --mcp-server --endpoint http://localhost:8080/mcp
```

### Debug and Development

```bash
# Custom configuration
muster agent --config-path ./dev-config --debug
```

## REPL Commands

When using `--repl` mode, the following commands are available:

### Information Commands

- `help`, `?` - Show command help
- `list tools` - List all available tools
- `list resources` - List all available resources
- `list prompts` - List all available prompts
- `list workflows` - List workflows with parameters
- `list core-tools` - List built-in Muster tools

### Tool Interaction

- `describe tool <name>` - Show detailed tool information
- `call <tool> {json}` - Execute tool with JSON arguments
- `filter tools [pattern] [desc] [case] [detailed]` - Filter tools by criteria

### Resource Management

- `describe resource <uri>` - Show resource details
- `get <resource-uri>` - Retrieve resource content

### Workflow Execution

- `workflow <name> [param=val]` - Execute workflow with parameters
- `describe prompt <name>` - Show prompt details
- `prompt <name> {json}` - Execute prompt with arguments

### Context Management

- `context` - Show current context
- `context list`, `context ls` - List all available contexts with endpoints
- `context use <name>` - Switch to a different context (preferred)
- `ctx` - Alias for `context` command

When switching contexts, the REPL automatically reconnects to the new endpoint. If the new endpoint requires authentication (or the token has expired), the REPL will automatically initiate the OAuth login flow and retry the connection after successful authentication.

The current context is displayed in the prompt:

```
𝗺 production » list tools
𝗺 staging [AUTH REQUIRED] » call my_tool
```

**Prompt indicators:**
- `[AUTH REQUIRED]` - One or more servers need authentication (run `auth login`) - displayed prominently in uppercase

**Auto-reconnect with re-authentication:**
```
𝗺 local » context use production
Switched to production (https://muster.example.com/mcp)
Connecting...
Authentication required for new endpoint
Starting OAuth login flow...
A browser window will open for authentication.
Authentication successful
Retrying connection...
Connected

𝗺 production »
```

Long context names are truncated using smart ellipsis that preserves both the start and end of the name (e.g., `production-...cluster`).

**Terminal compatibility:** The prompt uses unicode characters (𝗺 ») by default. Falls back to ASCII (`m >`) on terminals without unicode support.

### Session Control

- `notifications <on|off>` - Toggle notification display
- `exit`, `quit` - Exit the REPL

### Keyboard Shortcuts

- `TAB` - Auto-complete commands and arguments
- `↑/↓` - Navigate command history
- `Ctrl+R` - Search command history
- `Ctrl+C` - Cancel current line

## Transport Types

### streamable-http (Server-Sent Events) - Recommended

Real-time bidirectional communication with full notification support:

```bash
muster agent --transport sse
```

**Features:**

- Persistent connection for continuous monitoring
- Real-time notifications of tool/resource changes
- Ideal for interactive use and development
- Event streaming for immediate updates

**Use Cases:**

- Interactive REPL sessions
- AI assistant integration
- Development and debugging
- Real-time monitoring

### Streamable HTTP - Compatibility

Request-response pattern for restricted environments:

```bash
muster agent --transport streamable-http
```

**Features:**

- No persistent connection
- Stateless operations
- Better for automation scripts
- Compatible with restrictive network environments

**Use Cases:**

- CLI automation scripts
- Batch processing
- Network-restricted environments
- Simple tool execution

## Configuration

### Endpoint Auto-Detection

The agent automatically detects the aggregator endpoint from configuration:

```yaml
# ~/.config/muster/config.yaml
aggregator:
  endpoint: "http://localhost:8080"
  transport: "sse"
```

### Override via Environment

```bash
export MUSTER_ENDPOINT="http://localhost:9090"
muster agent
```

## AI Assistant Integration

### Cursor/VSCode Configuration

Add to your MCP settings:

```json
{
  "mcpServers": {
    "muster": {
      "command": "muster",
      "args": ["agent", "--mcp-server"],
      "env": {}
    }
  }
}
```

### Claude Desktop Configuration

Add to `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "muster": {
      "command": "muster",
      "args": ["agent", "--mcp-server", "--endpoint", "http://localhost:8080/sse"]
    }
  }
}
```

### Custom Endpoint

```json
{
  "mcpServers": {
    "muster": {
      "command": "muster",
      "args": ["agent", "--mcp-server", "--endpoint", "http://remote-server:8080/sse"]
    }
  }
}
```

## MCP Server Tools

When running in `--mcp-server` mode, the agent bridges to the aggregator server which exposes the following meta-tools:

| Tool Name | Description | Arguments |
|-----------|-------------|-----------|
| `list_tools` | List all available tools | `{}` |
| `list_resources` | List all available resources | `{}` |
| `list_prompts` | List all available prompts | `{}` |
| `list_core_tools` | List built-in Muster tools | `{}` |
| `describe_tool` | Get detailed tool information | `{"name": "tool_name"}` |
| `describe_resource` | Get resource details | `{"uri": "resource_uri"}` |
| `describe_prompt` | Get prompt details | `{"name": "prompt_name"}` |
| `call_tool` | Execute any available tool | `{"name": "tool_name", "arguments": {}}` |
| `get_resource` | Retrieve resource content | `{"uri": "resource_uri"}` |
| `get_prompt` | Execute prompt with arguments | `{"name": "prompt_name", "arguments": {}}` |
| `filter_tools` | Filter tools by criteria | `{"pattern": "...", "description": "..."}` |

**Note**: These meta-tools are exposed by the aggregator server, not by the agent. The agent forwards all MCP messages to the server transparently. All actual tools (core_*, workflow_*, x_*) are accessed via the `call_tool` meta-tool.

## Error Handling

### Connection Errors

```bash
# Server not running
muster agent
# Error: failed to connect to aggregator: connection refused

# Solution: Start the server first
muster serve  # In another terminal
```

### Transport Issues

```bash
# SSE not supported
muster agent --transport sse
# Warning: SSE not available, falling back to streamable-http

# Solution: Use compatible transport
muster agent --transport streamable-http
```

### Endpoint Detection

```bash
# Configuration not found
muster agent
# Warning: Could not detect endpoint, using default: http://localhost:8090/mcp

# Solution: Specify endpoint explicitly
muster agent --endpoint http://localhost:8080/mcp
```

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Command completed successfully |
| 1 | Connection error or invalid arguments |
| 2 | Transport error or protocol issue |
| 3 | Configuration error |
| 130 | Interrupted by signal (Ctrl+C) |

## Troubleshooting

### Connection Issues

```bash
# Test connection manually
curl http://localhost:8080/api/v1/status

# Check if server is running
ps aux | grep "muster serve"

# Verify endpoint in config
cat ~/.config/muster/config.yaml
```

### REPL Issues

```bash
# Enable verbose mode for debugging
muster agent --repl --verbose --json-rpc

# Check for tool caching issues
> list tools    # Should refresh tool cache
```

### MCP Server Mode Issues

```bash
# Test MCP server locally
echo '{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2024-11-05", "capabilities": {}}}' | muster agent --mcp-server
```

### Performance Issues

```bash
# Use streamable-http for better performance in some cases
muster agent --transport streamable-http

# Increase timeout for slow operations
muster agent --timeout 10m
```

## Related Commands

- **[serve](serve.md)** - Start the aggregator server (prerequisite)
- **[list](list.md)** - List resources without interactive mode
- **[get](get.md)** - Get specific resource information
- **[test](test.md)** - Test functionality with scenarios

## Advanced Usage

### Scripting with Agent

```bash
# Get tools list programmatically
muster agent --transport streamable-http --timeout 30s

# Monitor changes in background
muster agent --transport sse --verbose > agent.log 2>&1 &
```

### Development Workflows

```bash
# Development with debug output
muster agent --repl --verbose --json-rpc --transport sse

# Testing with custom config
muster agent --config-path ./test-config --endpoint http://test:8080/sse
```

### AI Assistant Debugging

```bash
# Test MCP server mode manually
muster agent --mcp-server --verbose --json-rpc > mcp-debug.log 2>&1
```