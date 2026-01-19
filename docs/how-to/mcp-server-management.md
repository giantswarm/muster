# MCP Server Management

This guide covers how to create, configure, and manage MCP (Model Context Protocol) servers in muster.

## Overview

MCP servers provide structured access to tools and resources for AI assistants. Muster supports three types of MCP servers:

- **Stdio servers**: Execute as local processes with configurable command lines
- **Streamable HTTP servers**: Connect to external MCP servers via HTTP
- **SSE servers**: Connect to external MCP servers via Server-Sent Events

### Goal

Add a new MCP server to extend Muster's tool capabilities.

### Prerequisites

- Muster control plane running
- MCP server binary available
- Understanding of the tool's requirements

## Creating MCP Servers

### Stdio Command Servers

Create a stdio server that runs as a local process:

1. **Create MCP server configuration**

   ```yaml
   # example-server.yaml
   apiVersion: muster.giantswarm.io/v1alpha1
   kind: MCPServer
   metadata:
     name: example-tool
     namespace: default
   spec:
     type: localCommand
     command: ["mcp-example-tool"]
     autoStart: true
     env:
       TOOL_CONFIG: "/path/to/config"
       LOG_LEVEL: "info"
     description: "Example MCP server providing custom tools"
   ```

2. **Register the server**

   ```bash
   muster create mcpserver example-server.yaml
   ```

3. **Verify server status**

   ```bash
   muster get mcpserver example-tool
   ```

4. **Test tool availability**

   ```bash
   muster agent --repl
   # In REPL:
   list tools
   # Or filter tools to see ones from this server:
   filter tools example
   ```

### Verification

- Server shows status "running"
- Tools from the server appear in tool listings
- Tools can be executed successfully

## Configure Auto-Start Behavior

### Goal

Control when MCP servers start automatically.

### Steps

1. **Enable auto-start** (start with Muster)

   ```yaml
   apiVersion: muster.giantswarm.io/v1alpha1
   kind: MCPServer
   metadata:
     name: auto-start-server
     namespace: default
   spec:
     autoStart: true
     # ... other configuration
   ```

2. **Disable auto-start** (manual control)

   ```yaml
   apiVersion: muster.giantswarm.io/v1alpha1
   kind: MCPServer
   metadata:
     name: manual-server
     namespace: default
   spec:
     autoStart: false
     # ... other configuration
   ```

3. **Apply configuration**

   ```bash
   muster create mcpserver server-config.yaml
   ```

4. **Manual server control**

   ```bash
   # Check server status
   muster get mcpserver example-tool

   # List all servers
   muster list mcpserver

   # Check server availability
   muster check mcpserver example-tool
   ```

## Monitor MCP Server Health

### Goal

Set up monitoring and health checks for MCP servers.

### Steps

1. **Check server status**

   ```bash
   # List all servers with status
   muster list mcpserver

   # Get detailed server info
   muster get mcpserver example-tool
   ```

2. **Test server communication**

   ```bash
   # Check if server is available
   muster check mcpserver example-tool
   ```

3. **Set up health monitoring**

   ```yaml
   apiVersion: muster.giantswarm.io/v1alpha1
   kind: MCPServer
   metadata:
     name: monitored-server
     namespace: default
   spec:
     type: localCommand
     command: ["mcp-example-tool"]
     healthCheck:
       enabled: true
       interval: "30s"
       timeout: "10s"
       command: ["health-check"]
   ```

4. **Configure alerting** (if monitoring system available)

   ```bash
   # Export server metrics
   muster metrics mcpserver --format prometheus
   ```

## Troubleshoot Server Startup Issues

### Goal

Diagnose and fix common MCP server startup problems.

## Advanced Configuration

### Environment Variables
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

## SSO Authentication

Muster supports Single Sign-On (SSO) for MCP servers, allowing users to authenticate once and access multiple servers without separate authentication flows.

### SSO Mechanisms

Muster supports two SSO mechanisms:

| Mechanism | Description | Configuration |
|-----------|-------------|---------------|
| **Token Forwarding** | Muster forwards its ID token to downstream servers | `auth.forwardToken: true` |
| **Token Reuse** | Servers share an OAuth issuer, tokens are reused automatically | Default behavior when servers share an issuer |

### Token Forwarding (Recommended for Trusted Servers)

When Token Forwarding is enabled, muster forwards its ID token to the downstream MCP server. This provides seamless SSO without requiring users to authenticate to each server individually.

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: internal-api
spec:
  description: "Internal API with SSO"
  toolPrefix: "api"
  type: streamable-http
  url: "https://internal-api.example.com/mcp"
  auth:
    forwardToken: true  # Enable SSO via token forwarding
```

**How it works:**
1. User runs `muster auth login` to authenticate to muster
2. On first MCP request, muster proactively connects to all SSO-enabled servers
3. User can immediately access SSO servers without additional authentication
4. The CLI shows the SSO type for each server: `mcp-kubernetes  connected [Token Forwarding]`

**Requirements:**
- The downstream MCP server must trust muster's OAuth client ID
- Both muster and the downstream server must use the same identity provider (issuer)

### Token Reuse (Automatic SSO)

When multiple MCP servers share the same OAuth issuer, muster automatically reuses tokens across servers. This is the default behavior and requires no additional configuration.

```yaml
# Both servers use the same Dex issuer - tokens are automatically reused
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: server-a
spec:
  type: streamable-http
  url: "https://server-a.example.com/mcp"
  auth:
    type: oauth
    issuer: "https://dex.example.com"
---
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: server-b
spec:
  type: streamable-http
  url: "https://server-b.example.com/mcp"
  auth:
    type: oauth
    issuer: "https://dex.example.com"  # Same issuer = automatic SSO
```

### Disabling SSO Token Reuse

In some cases, you may want to disable token reuse for a specific server (e.g., for security isolation):

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: isolated-server
spec:
  type: streamable-http
  url: "https://isolated.example.com/mcp"
  auth:
    type: oauth
    issuer: "https://dex.example.com"
    sso: false  # Disable token reuse for this server
```

### Checking SSO Status

Use `muster auth status` to see which servers are using SSO:

```bash
$ muster auth status

Muster: authenticated
  Endpoint: https://muster.example.com
  Expires:  in 23 hours

MCP Servers:
  mcp-kubernetes  connected [Token Forwarding]
  internal-api    connected [Token Reuse]
  isolated-server auth_required   Run: muster auth login --server isolated-server
```

### Troubleshooting SSO

**SSO server not connecting automatically:**
- Verify `forwardToken: true` is set in the MCPServer spec
- Check that the downstream server trusts muster's OAuth client ID
- Run with `--debug` to see detailed SSO connection logs

**Token reuse not working:**
- Ensure both servers use the exact same `issuer` URL
- Verify `sso: false` is not set on the server
- Check that both servers require the same OAuth scopes

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

```yaml
spec:
  toolPrefix: "custom"  # Tools will be prefixed as "x_custom_*"
```

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

## Configuration Best Practices

### 1. Naming Conventions

- Use descriptive names: `git-tools`, `k8s-cluster-prod`
- Include environment in name for multi-env setups
- Avoid special characters and spaces

### 2. Configuration Management

- Store configurations in version control
- Use environment-specific overlays
- Document required environment variables

### 3. Monitoring

- Always enable health checks for production
- Set appropriate timeouts
- Monitor resource usage

### 4. Security

- Run with minimal required permissions
- Use read-only filesystems where possible
- Regularly update server binaries

## Related Documentation

- [Configuration Reference](../reference/configuration.md) - Detailed configuration options
- [API Reference](../reference/api.md) - Programmatic server management  
- [CRD Reference](../reference/crds.md) - Kubernetes CRD schema
- [Architecture](../explanation/architecture.md) - How MCP servers fit into muster
- [MCP Server Reference](../reference/mcpserver.md)
- [Server Configuration Schema](../reference/configuration.md#mcpserver)
- [Troubleshooting Guide](troubleshooting.md)
- [Getting Started with MCP Servers](../getting-started/mcp-server-setup.md)
