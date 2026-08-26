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

   Muster exports logs, traces, and metrics via OpenTelemetry (OTLP). Point the
   standard `OTEL_EXPORTER_OTLP_*` environment variables at your collector and
   build alerts there; there is no `muster metrics` command.

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

| Mechanism | What You Do | What Happens | Configuration |
|-----------|-------------|--------------|---------------|
| **Token Forwarding** | Authenticate once to muster | Muster forwards its ID token to downstream servers | `auth.forwardToken: true` |
| **Token Exchange** | Authenticate once to muster | Muster exchanges its token for one valid on the remote IdP | `auth.tokenExchange` config |

### Token Forwarding (Recommended for Trusted Servers)

When Token Forwarding is enabled, muster forwards its ID token to the downstream MCP server. This provides seamless SSO without requiring users to authenticate to each server individually.

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: mcp-kubernetes
spec:
  description: "Kubernetes MCP with SSO"
  toolPrefix: "k8s"
  type: streamable-http
  url: "https://mcp-kubernetes.example.com/mcp"
  auth:
    forwardToken: true  # Enable SSO via token forwarding
    # Specify audiences required by downstream server (e.g., for Kubernetes OIDC)
    requiredAudiences:
      - "dex-k8s-authenticator"
```

**How it works:**
1. User runs `muster auth login` to authenticate to muster
2. Muster requests tokens with all `requiredAudiences` from the IdP via cross-client scopes
3. On first MCP request, muster proactively connects to all SSO-enabled servers using the multi-audience token
4. User can immediately access SSO servers without additional authentication
5. The CLI shows the SSO type for each server: `mcp-kubernetes  Connected [SSO: Forwarded]`

**Requirements:**
- The downstream MCP server must trust muster's OAuth client ID
- Both muster and the downstream server must use the same identity provider (issuer)
- For Kubernetes OIDC auth, the IdP must support cross-client authentication (`audience:server:client_id:*` scopes)

**Important:** Required audiences are collected at muster startup and during user authentication. If you add or modify MCPServers with `requiredAudiences` after users have authenticated, those users must re-authenticate (`muster auth logout` followed by `muster auth login`) to obtain tokens with the new audiences.

**Security Note:** Access control for `requiredAudiences` is enforced at two levels:
1. **Kubernetes RBAC**: Only users with permissions to create/modify MCPServer CRDs can configure `requiredAudiences`
2. **IdP Cross-Client Configuration**: The identity provider (e.g., Dex) must be configured to allow cross-client authentication for the specified audiences. Unauthorized audience requests will be rejected by the IdP.

### Token Exchange (Cross-Cluster SSO)

When clusters have separate Identity Providers, muster can use RFC 8693 Token Exchange to obtain a token valid on the remote cluster's IdP. This enables cross-cluster SSO without requiring shared trust.

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: remote-cluster-mcp
spec:
  description: "MCP on remote cluster with Token Exchange"
  type: streamable-http
  url: "https://mcp.remote-cluster.example.com/mcp"
  auth:
    tokenExchange:
      enabled: true
      tokenEndpoint: "https://dex.remote-cluster.example.com/token"
      audience: "mcp-server"
```

**How it works:**
1. User authenticates to muster
2. When accessing the remote server, muster exchanges its token at the remote IdP
3. Remote IdP validates the token and issues a new one valid for that cluster
4. Muster uses the exchanged token for downstream requests

### Checking SSO Status

Use `muster auth status` to see which servers are using SSO:

```bash
$ muster auth status

Muster: authenticated
  Endpoint: https://muster.example.com
  Expires:  in 23 hours

MCP Servers:
  mcp-kubernetes  Connected [SSO: Forwarded]
  remote-cluster  Connected [SSO: Exchanged]
  isolated-server Not authenticated          Run: muster auth login --server isolated-server
```

### Troubleshooting SSO

**SSO server not connecting automatically:**
- Verify `forwardToken: true` is set in the MCPServer spec
- Check that the downstream server trusts muster's OAuth client ID
- Run with `--debug` to see detailed SSO connection logs

## AWS SigV4 Request Signing

An AWS-hosted MCP server accepts no bearer token. It authenticates each request
by an AWS Signature Version 4 signature. Set `auth.type: sigv4` to make muster
sign every request that it sends to such a server.

This is not SSO. The signature carries muster's own machine identity, not the
identity of the user who made the call, so all users of the server share one AWS
identity. CloudTrail records muster, not a named engineer.

`auth.sigv4` is only valid with `type: streamable-http`. Muster rejects it
together with `forwardToken`, `tokenExchange` or `authorizationServer`, because
none of them apply to a machine identity. These rules hold in both Kubernetes
and filesystem mode.

A connection failure reports itself as such. A 401 from a SigV4 server is not
"Auth Required" — there is no login flow to send a user to — so muster keeps the
server in `Failed` and retries with backoff. Check the signing region, the
assumed role and its policy.

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: aws-root
spec:
  type: streamable-http
  url: "https://aws-mcp.eu-central-1.api.aws/mcp"
  timeout: 120
  auth:
    type: sigv4
    sigv4:
      region: eu-central-1
  meta:
    AWS_REGION: eu-central-1
```

**Fields:**

| Field | Required | Purpose |
|-------|----------|---------|
| `auth.sigv4.region` | yes | The signing region. It must match the region in `url`, because the endpoint checks the credential scope of the signature. |
| `auth.sigv4.service` | no | The signing service name. It defaults to the first hostname label of `url`, so `aws-mcp.eu-central-1.api.aws` signs as `aws-mcp`. |
| `auth.sigv4.roleArn` | no | An IAM role that muster assumes before it signs. Leave it empty to sign as muster's own identity. |
| `meta` | see below | Entries merged into `params._meta` of every request that carries `params`. It is not a SigV4 field: see [Request metadata](#request-metadata). |

Muster gets its base credentials from the default AWS credential chain. In
Kubernetes that means IRSA: the pod identity webhook injects `AWS_ROLE_ARN` and
`AWS_WEB_IDENTITY_TOKEN_FILE`, and the chain exchanges the projected token for
credentials. Set `roleArn` to chain one more hop from there, which is how one
muster reaches many accounts. Each account gets its own MCPServer, and a shared
`family` puts them all behind one tool name with an account selector.

**Set `meta` for the AWS-hosted server.** It takes the region that it operates
in from `params._meta.AWS_REGION`. This region is a different value from the
signing region, even when the two strings match: the signing region belongs to
the endpoint, and the operating region belongs to the resources that the call
reads. A value that a caller puts in `_meta` itself wins over the one in `meta`.

Set it even though calls succeed without it. The backend falls back to its own
region rather than failing, so a missing entry does not raise an error — it
returns a correct-looking answer about the wrong region. Measured against
`aws-mcp.eu-central-1.api.aws`: the same query for CloudWatch log groups
returned nothing with no `meta`, and the one log group that exists in the
account with `meta: {AWS_REGION: eu-north-1}`. An agent reads the first result
as "there are none".

## Request metadata

`meta` is a remote-server field, not a SigV4 field. Muster merges its entries
into the `params._meta` object of every outbound JSON-RPC request that carries
`params`. Use it for a backend that reads call-scoped configuration from the MCP
metadata field instead of from tool arguments.

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: regional-tools
spec:
  type: streamable-http
  url: "https://mcp.example.com/mcp"
  meta:
    AWS_REGION: eu-central-1
```

Rules:

- The merge applies to `type: streamable-http` and `type: sse`, with any auth
  type and with none. A server that needs a login keeps its entries after the
  login: the per-session connection carries them too.
- An entry that the request already has in `_meta` wins, so a caller can
  override one per call.
- A request without `params` is left untouched. `initialize` does carry
  `params`, so the handshake gets the entries as well.
- `type: stdio` rejects the field. A stdio server speaks over a pipe, so no HTTP
  transport can inject the entries, and muster refuses the definition instead of
  accepting the map and dropping it.

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

Inspect server state and logs:
```bash
# Server status and any startup error
muster get mcpserver <server-name> -o yaml
muster list mcpserver --all --verbose

# Aggregator logs (servers log through it) — there is no `muster logs`
muster serve --debug
```

## Integration Examples

### With Cursor/VS Code
Configure Cursor to use muster MCP servers:

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
