# MCP Server Management

This guide covers how to create, configure, and manage MCP (Model Context Protocol) servers in muster.

## Overview

MCP servers provide structured access to tools and resources for AI assistants. Muster supports three types of MCP servers:

- **Stdio servers**: Execute as local processes with configurable command lines
- **Streamable HTTP servers**: Connect to external MCP servers via HTTP
- **SSE servers**: Connect to external MCP servers via Server-Sent Events

## Creating MCP Servers

### Stdio Command Servers

Create a stdio server that runs as a local process:

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: filesystem-tools
spec:
  description: "File system operations"
  toolPrefix: "fs"
  type: stdio
  autoStart: true
  command: "npx"
  args: ["@modelcontextprotocol/server-filesystem", "/workspace"]
  env:
    DEBUG: "1"
    LOG_LEVEL: "info"
```

### Remote Servers

Connect to external MCP servers:

#### Streamable HTTP Transport
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: remote-api
spec:
  description: "Remote API tools"
  toolPrefix: "api"
  type: streamable-http
  url: "https://api.example.com/mcp"
  timeout: 60
  headers:
    Authorization: "Bearer your-token-here"
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
  type: sse
  url: "https://sse.example.com/mcp"
  timeout: 90
  headers:
    Authorization: "Bearer your-token-here"
```

## Using the CLI

### Creating Servers via CLI

Create a stdio server:
```bash
muster create mcpserver filesystem-tools \
  --type stdio \
  --command "npx" \
  --args "@modelcontextprotocol/server-filesystem,/workspace" \
  --auto-start \
  --tool-prefix fs \
  --description "File system operations"
```

Create a streamable HTTP server:
```bash
muster create mcpserver remote-api \
  --type streamable-http \
  --url "https://api.example.com/mcp" \
  --timeout 60 \
  --tool-prefix api \
  --description "Remote API tools"
```

Create an SSE server:
```bash
muster create mcpserver sse-server \
  --type sse \
  --url "https://sse.example.com/mcp" \
  --timeout 90 \
  --tool-prefix sse \
  --description "SSE MCP server"
```

### Listing Servers
```bash
muster list mcpserver
```

### Getting Server Details
```bash
muster get mcpserver filesystem-tools
```

### Updating Servers
```bash
# Update stdio server
muster update mcpserver filesystem-tools \
  --auto-start=false \
  --description "Updated file system tools"

# Update remote server
muster update mcpserver remote-api \
  --url "https://new-api.example.com/mcp" \
  --timeout 120
```

### Deleting Servers
```bash
muster delete mcpserver filesystem-tools
```

## Configuration Best Practices

### Stdio Servers
- Use absolute paths for commands when possible
- Set appropriate environment variables for configuration
- Enable auto-start for critical servers
- Use descriptive tool prefixes to avoid conflicts

### Remote Servers (Streamable HTTP and SSE)
- Use HTTPS endpoints when possible for security
- Set appropriate timeouts based on server response times
- Test connectivity before deploying to production
- Monitor server availability and health
- Include necessary authentication headers

### Tool Prefixes
- Use short but descriptive prefixes (e.g., `k8s`, `git`, `fs`)
- Avoid generic prefixes like `tools` or `server`
- Be consistent across related servers

## Troubleshooting

### Stdio Server Issues

**Command not found:**
```bash
# Check if the command is available
which npx
npm install -g @modelcontextprotocol/server-filesystem

# Verify the server definition
muster get mcpserver filesystem-tools
```

**Permission errors:**
```bash
# Check file permissions
ls -la /workspace
chmod +x /path/to/mcp-server

# Run with appropriate user
sudo -u mcpuser muster start mcpserver filesystem-tools
```

### Remote Server Issues

**Connection timeouts:**
```bash
# Test connectivity
curl -v https://api.example.com/mcp

# Increase timeout
muster update mcpserver remote-api --timeout 120
```

**Transport errors:**
```bash
# Check server type and endpoint
muster get mcpserver remote-api

# For SSE servers, ensure endpoint supports Server-Sent Events
# For HTTP servers, ensure endpoint supports streaming HTTP
```

**Authentication errors:**
```bash
# Update headers for authentication
muster update mcpserver remote-api \
  --header "Authorization=Bearer new-token"
```

## Advanced Configuration

### Environment Variables for Stdio Servers
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: custom-tools
spec:
  type: stdio
  command: "python"
  args: ["-m", "my_mcp_server"]
  env:
    PYTHONPATH: "/usr/local/lib/python3.9/site-packages"
    API_KEY: "your-api-key"
    DEBUG: "true"
    LOG_LEVEL: "info"
```

### Custom Headers for Remote Servers
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: authenticated-api
spec:
  type: streamable-http
  url: "https://secure-api.example.com/mcp"
  headers:
    Authorization: "Bearer jwt-token-here"
    X-API-Version: "v2"
    Content-Type: "application/json"
  timeout: 45
```

### Monitoring and Health Checks

Check server status:
```bash
# List all servers with status
muster list mcpserver

# Get detailed server information
muster get mcpserver <server-name>

# Check if server is available
muster check mcpserver <server-name>
```

Monitor logs:
```bash
# View server logs (when available)
muster logs mcpserver <server-name>
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
