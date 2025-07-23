# MCP Server Management

This guide covers how to create, configure, and manage MCP (Model Context Protocol) servers in muster.

## Overview

MCP servers provide structured access to tools and resources for AI assistants. Muster supports two types of MCP servers:

- **Local servers**: Execute as local processes with configurable command lines
- **Remote servers**: Connect to external MCP servers via HTTP or SSE

## Creating MCP Servers

### Local Command Servers

Create a local server that runs as a process:

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: filesystem-tools
spec:
  description: "File system operations"
  toolPrefix: "fs"
  type: local
  local:
    autoStart: true
    command: ["npx", "@modelcontextprotocol/server-filesystem", "/workspace"]
    env:
      DEBUG: "1"
      LOG_LEVEL: "info"
```

### Remote Servers

Connect to external MCP servers:

#### HTTP Transport
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: remote-api
spec:
  description: "Remote API tools"
  toolPrefix: "api"
  type: remote
  remote:
    endpoint: "https://api.example.com/mcp"
    transport: "http"
    timeout: 60
```

#### Server-Sent Events (SSE) Transport
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: sse-server
spec:
  description: "SSE MCP server"
  toolPrefix: "sse"
  type: remote
  remote:
    endpoint: "https://sse.example.com/mcp"
    transport: "sse"
    timeout: 90
```

## Using the CLI

### Creating Servers via CLI

Create a local server:
```bash
muster mcpserver create filesystem-tools \
  --type local \
  --command "npx" \
  --command "@modelcontextprotocol/server-filesystem" \
  --command "/workspace" \
  --auto-start \
  --tool-prefix fs \
  --description "File system operations"
```

Create a remote server:
```bash
muster mcpserver create remote-api \
  --type remote \
  --endpoint "https://api.example.com/mcp" \
  --transport http \
  --timeout 60 \
  --tool-prefix api \
  --description "Remote API tools"
```

### Listing Servers
```bash
muster mcpserver list
```

### Getting Server Details
```bash
muster mcpserver get filesystem-tools
```

### Updating Servers
```bash
# Update local server
muster mcpserver update filesystem-tools \
  --auto-start=false \
  --description "Updated file system tools"

# Update remote server
muster mcpserver update remote-api \
  --endpoint "https://new-api.example.com/mcp" \
  --timeout 120
```

### Deleting Servers
```bash
muster mcpserver delete filesystem-tools
```

## Configuration Best Practices

### Local Servers
- Use absolute paths for commands when possible
- Set appropriate environment variables for configuration
- Enable auto-start for critical servers
- Use descriptive tool prefixes to avoid conflicts

### Remote Servers
- Use HTTPS endpoints when possible for security
- Set appropriate timeouts based on server response times
- Test connectivity before deploying to production
- Monitor server availability and health

### Tool Prefixes
- Use short but descriptive prefixes (e.g., `k8s`, `git`, `fs`)
- Avoid generic prefixes like `tools` or `server`
- Be consistent across related servers

## Troubleshooting

### Local Server Issues

**Command not found:**
```bash
# Check if the command is available
which npx
npm install -g @modelcontextprotocol/server-filesystem

# Verify the server definition
muster mcpserver get filesystem-tools
```

**Permission errors:**
```bash
# Check file permissions
ls -la /workspace
chmod +x /path/to/mcp-server

# Run with appropriate user
sudo -u mcpuser muster mcpserver start filesystem-tools
```

### Remote Server Issues

**Connection timeouts:**
```bash
# Test connectivity
curl -v https://api.example.com/mcp

# Increase timeout
muster mcpserver update remote-api --timeout 120
```

**Transport errors:**
```bash
# Verify transport support
curl -H "Accept: text/event-stream" https://sse.example.com/mcp
```

### Common Issues

**Tool name conflicts:**
```bash
# List tools to identify conflicts
muster tool list | grep conflicting-name

# Update tool prefix
muster mcpserver update server-name --tool-prefix unique-prefix
```

**Server not starting:**
```bash
# Check server logs
muster logs mcpserver/server-name

# Verify configuration
muster mcpserver validate server-name
```

## Integration Examples

### With Cursor/VS Code
Configure Cursor to use muster MCP servers:

```json
{
  "mcpServers": {
    "muster-aggregator": {
      "command": "curl",
      "args": ["-N", "http://localhost:8090/sse"]
    }
  }
}
```

### With Other AI Assistants
Most MCP-compatible assistants can connect to muster's aggregator endpoint at `http://localhost:8090/mcp`.

## Related Documentation

- [Configuration Reference](../reference/configuration.md) - Detailed configuration options
- [API Reference](../reference/api.md) - Programmatic server management  
- [CRD Reference](../reference/crds.md) - Kubernetes CRD schema
- [Architecture](../explanation/architecture.md) - How MCP servers fit into muster 
