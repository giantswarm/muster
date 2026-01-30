# Muster Custom Resource Definitions (CRDs)

Complete reference for muster's Kubernetes Custom Resource Definitions. These CRDs define the core resources managed by the muster system for orchestrating MCP servers, service lifecycle management, and workflow execution.

## Overview

Muster provides three primary CRDs:

| CRD | API Version | Kind | Short Name | Purpose |
|-----|-------------|------|------------|---------|
| **MCPServer** | `muster.giantswarm.io/v1alpha1` | `MCPServer` | `mcps` | Manages MCP (Model Context Protocol) servers that provide tools |
| **ServiceClass** | `muster.giantswarm.io/v1alpha1` | `ServiceClass` | `sc` | Defines reusable service templates with lifecycle management |
| **Workflow** | `muster.giantswarm.io/v1alpha1` | `Workflow` | `wf` | Defines multi-step processes for automated task execution |

## MCPServer

MCPServer resources define and manage MCP (Model Context Protocol) servers that provide tools for various operations like Git, filesystem, database management, etc.

### Resource Definition

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: <server-name>
  namespace: <namespace>
spec:
  # Required: Server execution type
  type: stdio|streamable-http|sse
  
  # Optional: Tool name prefix (applies to all types)
  toolPrefix: "<prefix>"
  
  # Optional: Human-readable description
  description: "<description>"
  
  # For stdio servers: Auto-start behavior
  autoStart: false
  
  # For stdio servers: Command to execute (required when type: stdio)
  command: "<executable>"
  
  # For stdio servers: Command arguments (optional when type: stdio)
  args: ["<arg1>", "<arg2>"]
  
  # For remote servers: Server endpoint (required when type: streamable-http or sse)
  url: "https://api.example.com/mcp"
  
  # Optional: Environment variables (stdio servers) or headers (remote servers)
  env:        # For stdio servers
    KEY1: "value1"
    KEY2: "value2"
  headers:    # For remote servers
    Authorization: "Bearer token"
    Content-Type: "application/json"
  
  # Optional: Connection timeout in seconds (all types)
  timeout: 30
  
  # Optional: Authentication configuration (remote servers)
  auth:
    type: oauth|none          # Authentication type
    forwardToken: true        # Forward muster's ID token for SSO
    requiredAudiences:        # Audiences needed in forwarded token (e.g., for Kubernetes OIDC)
      - "dex-k8s-authenticator"
    fallbackToOwnAuth: true   # Fallback to separate OAuth if forwarding fails

# Status is managed automatically by muster (via reconciliation)
status:
  state: running          # unknown|starting|running|stopping|stopped|failed
  health: healthy         # unknown|healthy|unhealthy|checking
  lastError: ""           # Any error from recent operations
  lastConnected: ""       # When the server was last successfully connected
  restartCount: 0         # Number of times the server has been restarted
  conditions: []          # Kubernetes standard conditions
```

### Field Reference

#### Spec Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `type` | `string` | Yes | Execution method for the MCP server | Must be `stdio`, `streamable-http`, or `sse` |
| `toolPrefix` | `string` | No | Prefix for all tool names from this server | Pattern: `^[a-zA-Z][a-zA-Z0-9_-]*$` |
| `description` | `string` | No | Human-readable description | Max 500 characters |
| `autoStart` | `boolean` | No | Auto-start when system initializes | Default: `false`, only for stdio servers |
| `command` | `string` | Yes* | Executable path for stdio servers | Required when `type` is `stdio` |
| `args` | `[]string` | No | Command line arguments for stdio servers | Only for stdio servers |
| `url` | `string` | Yes* | Endpoint URL for remote servers | Required when `type` is `streamable-http` or `sse` |
| `env` | `map[string]string` | No | Environment variables for stdio servers | Only for stdio servers |
| `headers` | `map[string]string` | No | HTTP headers for remote servers | Only for streamable-http and sse servers |
| `timeout` | `integer` | No | Connection timeout in seconds | Min: 1, Max: 300, Default: 30 |
| `auth` | `MCPServerAuth` | No | Authentication configuration | Only for streamable-http and sse servers |

#### MCPServerAuth Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `type` | `string` | No | Authentication type | Must be `oauth`, `teleport`, or `none` |
| `forwardToken` | `boolean` | No | Forward muster's ID token for SSO | Default: `false` |
| `requiredAudiences` | `[]string` | No | Additional audiences to request from IdP for token forwarding | Only used when `forwardToken: true` |
| `fallbackToOwnAuth` | `boolean` | No | Fallback to separate OAuth flow if forwarding/exchange fails | Default: `true` |
| `sso` | `boolean` | No | Enable SSO token reuse between servers with same issuer | Default: `true` |
| `tokenExchange` | `TokenExchangeConfig` | No | RFC 8693 token exchange for cross-cluster SSO | See below |
| `teleport` | `TeleportAuth` | No | Teleport authentication settings (when `type: teleport`) | See below |

**Note on `requiredAudiences`**: When forwarding tokens to downstream servers that require specific audience claims (e.g., Kubernetes OIDC authentication), specify the required audiences here. Muster will request these audiences from the upstream IdP (e.g., Dex) using cross-client scopes (`audience:server:client_id:<audience>`). The resulting multi-audience token is forwarded to all downstream servers. Example: `requiredAudiences: ["dex-k8s-authenticator"]`. Note that required audiences are collected at muster startup and during user authentication - if you add MCPServers with new audiences after users have authenticated, they must re-authenticate to obtain tokens with the new audiences.

#### TeleportAuth Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `identityDir` | `string` | No* | Filesystem path to tbot identity directory | Mutually exclusive with `identitySecretName` |
| `identitySecretName` | `string` | No* | Name of Kubernetes Secret with tbot identity | Mutually exclusive with `identityDir` |
| `identitySecretNamespace` | `string` | No | Namespace of identity secret | Default: same as MCPServer |
| `appName` | `string` | No | Teleport application name for routing | Used in Host header |

*Note: Either `identityDir` or `identitySecretName` must be specified

#### TokenExchangeConfig Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `enabled` | `boolean` | No | Enable token exchange | Default: `false` |
| `dexTokenEndpoint` | `string` | Yes* | URL to access Dex's token endpoint (may be via proxy) | Must be HTTPS, required when enabled |
| `expectedIssuer` | `string` | No | Expected issuer URL in exchanged token's `iss` claim | Must be HTTPS if specified. Default: derived from `dexTokenEndpoint` by removing `/token` suffix |
| `connectorId` | `string` | Yes* | ID of OIDC connector on remote Dex | Required when enabled |
| `scopes` | `string` | No | Scopes to request for exchanged token | Default: `openid profile email groups` |
| `clientCredentialsSecretRef` | `ClientCredentialsSecretRef` | No | Reference to secret containing OAuth client credentials | See below |

**Security Note**: Muster validates that the exchanged token's `iss` claim matches `expectedIssuer` using constant-time comparison. This prevents token substitution attacks in proxied access scenarios. When `expectedIssuer` is not specified, the issuer is derived from `dexTokenEndpoint` by removing the `/token` suffix (backward compatible). Set `expectedIssuer` explicitly when accessing Dex through a proxy where the access URL differs from Dex's configured issuer.

#### ClientCredentialsSecretRef Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `name` | `string` | Yes | Name of the Kubernetes Secret | Must exist in the specified namespace |
| `namespace` | `string` | No | Namespace of the secret | Default: same as MCPServer |
| `clientIdKey` | `string` | No | Key in secret for client ID | Default: `client-id` |
| `clientSecretKey` | `string` | No | Key in secret for client secret | Default: `client-secret` |

**Usage Note**: Client credentials are required when the remote Dex's token exchange endpoint requires client authentication. The secret should be created before the MCPServer and should contain the OAuth client ID and secret registered on the remote Dex.

**RBAC Requirements**: Muster's service account requires `get` permission on `secrets` resources in the namespace where credentials are stored. For cross-namespace access (when `namespace` differs from the MCPServer's namespace), ensure RBAC policies explicitly grant access. Cross-namespace secret access is logged as a warning to aid security auditing.

Example RBAC configuration for secret access:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: muster-secret-reader
  namespace: secrets-namespace
rules:
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get"]
  resourceNames: ["token-exchange-credentials"]  # Optional: restrict to specific secrets
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: muster-secret-reader
  namespace: secrets-namespace
subjects:
- kind: ServiceAccount
  name: muster
  namespace: muster
roleRef:
  kind: Role
  name: muster-secret-reader
  apiGroup: rbac.authorization.k8s.io
```

**Secret Rotation Best Practices**:

1. **Zero-downtime rotation**: Update the secret with new credentials while keeping old credentials valid on the remote Dex. Muster loads credentials at connection time, so new connections will use updated credentials.

2. **Rotation procedure**:
   - Register new client credentials on the remote Dex (keeping old credentials active)
   - Update the Kubernetes secret with new credentials
   - Verify new connections succeed with new credentials
   - Revoke old credentials on the remote Dex

3. **Monitoring**: After rotation, monitor logs for authentication failures. Muster logs token exchange attempts (with client_id, not secrets) for troubleshooting.

4. **Automation**: Consider using external secrets management (e.g., External Secrets Operator, Vault) for automated rotation.

#### Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `state` | `string` | Current operational state: `unknown`, `starting`, `running`, `stopping`, `stopped`, `failed` |
| `health` | `string` | Health status: `unknown`, `healthy`, `unhealthy`, `checking` |
| `lastError` | `string` | Error message from the most recent operation |
| `lastConnected` | `*metav1.Time` | When the server was last successfully connected |
| `restartCount` | `int` | Number of times the server has been restarted |
| `conditions` | `[]metav1.Condition` | Standard Kubernetes conditions |

> **Note**: Available tools are computed per-session at runtime based on user authentication. See [ADR 007](../explanation/decisions/007-crd-status-reconciliation.md) for details on session-scoped tool visibility.

### Examples

#### Basic Git Tools Server (Stdio)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: git-tools
  namespace: default
spec:
  type: stdio
  description: "Git tools MCP server for repository operations"
  autoStart: true
  command: "npx"
  args: ["@modelcontextprotocol/server-git"]
  env:
    GIT_ROOT: "/workspace"
    LOG_LEVEL: "info"
```

#### Python Tools with Prefix (Stdio)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: python-tools
  namespace: default
spec:
  type: stdio
  toolPrefix: "py"
  description: "Python-based MCP server with custom tools"
  autoStart: true
  command: "python"
  args: ["-m", "mcp_server.custom"]
  env:
    PYTHONPATH: "/usr/local/lib/python3.9/site-packages"
    DEBUG: "true"
```

#### Streamable HTTP Server (Remote)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: api-tools
  namespace: default
spec:
  type: streamable-http
  description: "Remote API tools server"
  url: "https://api.example.com/mcp"
  timeout: 30
  headers:
    Authorization: "Bearer your-api-token"
    X-API-Version: "v1"
```

#### SSE Remote Server (Remote)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: sse-tools
  namespace: default
spec:
  type: sse
  toolPrefix: "remote"
  description: "Server-Sent Events MCP server"
  url: "https://mcp.example.com/sse"
  timeout: 60
  headers:
    Authorization: "Bearer sse-token"
    Accept: "text/event-stream"
```

#### OAuth-Protected Server with SSO Token Forwarding
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: mcp-kubernetes
  namespace: default
spec:
  type: streamable-http
  toolPrefix: "k8s"
  description: "Kubernetes MCP server with SSO authentication"
  url: "https://mcp-kubernetes.example.com/mcp"
  timeout: 30
  auth:
    type: oauth
    forwardToken: true           # Forward muster's ID token for SSO
    fallbackToOwnAuth: true      # If forwarding fails, trigger separate auth
```

When `forwardToken: true` is configured:
1. User authenticates to muster once via OAuth
2. When calling this MCP server, muster forwards the user's ID token
3. The downstream server validates the token (must configure `TrustedAudiences`)
4. If forwarding fails and `fallbackToOwnAuth: true`, a separate OAuth flow is triggered

Downstream servers must be configured to trust muster's client ID:
- **mcp-kubernetes**: Set `oauth.trustedAudiences: ["muster-client"]` in Helm values
- **inboxfewer**: Set `oauthSecurity.trustedAudiences: ["muster-client"]` in Helm values

#### Cross-Cluster SSO with Token Exchange (RFC 8693)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: remote-cluster-k8s
  namespace: default
spec:
  type: streamable-http
  toolPrefix: "remote_k8s"
  description: "Kubernetes tools on remote cluster via token exchange"
  url: "https://mcp-kubernetes.remote-cluster.example.com/mcp"
  timeout: 30
  auth:
    type: oauth
    tokenExchange:
      enabled: true
      dexTokenEndpoint: "https://dex.remote-cluster.example.com/token"
      connectorId: "local-cluster-dex"
      scopes: "openid profile email groups"
    fallbackToOwnAuth: false
```

When `tokenExchange.enabled: true` is configured:
1. User authenticates to muster once via OAuth (gets token from local cluster's Dex)
2. When calling this MCP server, muster exchanges the local token for one valid on the remote cluster
3. The remote Dex must have an OIDC connector configured that trusts the local cluster's Dex
4. The exchanged token is used to authenticate to the remote MCP server

The remote Dex must be configured with an OIDC connector:
```yaml
# On remote cluster's Dex
connectors:
  - type: oidc
    id: local-cluster-dex
    name: "Local Cluster"
    config:
      issuer: https://dex.local-cluster.example.com
      getUserInfo: true
      insecureEnableGroups: true
```

#### Token Exchange with Client Credentials

When the remote Dex requires client authentication for token exchange, you can reference a Kubernetes secret containing the client credentials:

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: remote-cluster-k8s-authenticated
  namespace: default
spec:
  type: streamable-http
  toolPrefix: "remote_k8s"
  description: "Kubernetes tools on remote cluster with authenticated token exchange"
  url: "https://mcp-kubernetes.remote-cluster.example.com/mcp"
  timeout: 30
  auth:
    type: oauth
    tokenExchange:
      enabled: true
      dexTokenEndpoint: "https://dex.remote-cluster.example.com/token"
      connectorId: "local-cluster-dex"
      scopes: "openid profile email groups"
      # Reference to secret containing OAuth client credentials
      clientCredentialsSecretRef:
        name: remote-cluster-token-exchange-credentials
        namespace: muster  # Optional, defaults to MCPServer namespace
        clientIdKey: client-id      # Optional, defaults to "client-id"
        clientSecretKey: client-secret  # Optional, defaults to "client-secret"
    fallbackToOwnAuth: false
```

The referenced secret should be created before the MCPServer:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: remote-cluster-token-exchange-credentials
  namespace: muster
type: Opaque
stringData:
  client-id: muster-token-exchange
  client-secret: <your-client-secret>
```

The client credentials must be registered as a static client on the remote Dex:

```yaml
# On remote cluster's Dex
staticClients:
  - id: muster-token-exchange
    name: "Muster Token Exchange"
    secret: <your-client-secret>
    # No redirect URIs needed for token exchange
```

#### Cross-Cluster SSO via Proxy (OAuth Token Forwarding)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: private-cluster-k8s
  namespace: default
spec:
  type: streamable-http
  toolPrefix: "private_k8s"
  description: "Kubernetes tools on private cluster accessed via Teleport"
  url: "https://mcp-kubernetes.private-cluster.teleport.example.com/mcp"
  timeout: 60
  auth:
    type: oauth
    tokenExchange:
      enabled: true
      # Access URL goes through proxy
      dexTokenEndpoint: "https://dex.private-cluster.proxy.example.com/token"
      # Expected issuer is the actual Dex issuer (not the proxy URL)
      expectedIssuer: "https://dex.private-cluster.internal.example.com"
      connectorId: "management-cluster-dex"
    fallbackToOwnAuth: true
```

When accessing Dex through a proxy (e.g., VPN, HTTP proxy):
- `dexTokenEndpoint`: The proxy URL used to reach Dex's token endpoint
- `expectedIssuer`: The actual issuer URL configured in Dex (used for token validation)

This is necessary because Dex's tokens contain the configured issuer URL in the `iss` claim, not the proxy URL used to access it. Muster validates that the exchanged token's issuer matches `expectedIssuer` for security.

> **Warning**: When accessing Dex through a proxy, you **MUST** set `expectedIssuer` explicitly. If omitted, muster derives the expected issuer from `dexTokenEndpoint` (the proxy URL), which will cause token validation to fail because the token's `iss` claim contains the actual Dex issuer URL, not the proxy URL. This validation failure is intentional - it ensures you explicitly configure the expected issuer for proxied scenarios.

#### Teleport Application Access with Token Exchange (Private Installations)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: private-cluster-mcp
  namespace: default
spec:
  type: streamable-http
  toolPrefix: "private"
  description: "MCP server on private cluster via Teleport with cross-cluster SSO"
  # MCP server URL is accessed via Teleport
  url: "https://mcp-kubernetes.private-cluster.teleport.example.com/mcp"
  timeout: 60
  auth:
    # Teleport provides the network access layer (mutual TLS)
    type: teleport
    teleport:
      # Kubernetes Secret containing tbot identity files
      identitySecretName: tbot-private-cluster
      identitySecretNamespace: muster-system
      # Teleport application name for routing
      appName: mcp-kubernetes-private
    # Token exchange provides user identity (cross-cluster SSO)
    tokenExchange:
      enabled: true
      # Token endpoint accessed via Teleport
      dexTokenEndpoint: "https://dex.private-cluster.teleport.example.com/token"
      # Expected issuer is the actual Dex issuer URL
      expectedIssuer: "https://dex.private-cluster.internal.example.com"
      connectorId: "management-cluster-dex"
      scopes: "openid profile email groups"
    fallbackToOwnAuth: false
```

This configuration combines:

1. **Teleport Authentication (`auth.type: teleport`)**: Provides encrypted network access to private installations
   - Uses Machine ID certificates for mutual TLS authentication
   - Supports both filesystem identity directories and Kubernetes Secrets
   - Routes requests through Teleport Application Access

2. **Token Exchange (`auth.tokenExchange`)**: Provides user identity via RFC 8693
   - Exchanges local user token for one valid on remote cluster's Dex
   - Preserves user identity for RBAC on the remote cluster
   - Token exchange request uses the Teleport HTTP client

The complete flow:
```
User authenticates to muster (local Dex)
    ↓
Muster loads Teleport identity certificates
    ↓
Token exchange request via Teleport → Remote Dex
    ↓
Remote Dex validates token via OIDC connector
    ↓
Remote Dex issues new token with user identity
    ↓
MCP server call via Teleport with exchanged token
    ↓
Remote MCP server authenticates user for RBAC
```

**Prerequisites for Teleport + Token Exchange:**

1. **tbot (Teleport Machine ID)** configured to output identity files:
   ```yaml
   # Kubernetes Secret created by tbot
   apiVersion: v1
   kind: Secret
   metadata:
     name: tbot-private-cluster
     namespace: muster-system
   data:
     tlscert: <base64-encoded-client-cert>
     key: <base64-encoded-private-key>
     teleport-application-ca.pem: <base64-encoded-ca-cert>
   ```

2. **Remote Dex** configured with OIDC connector for local cluster:
   ```yaml
   connectors:
     - type: oidc
       id: management-cluster-dex
       name: "Management Cluster"
       config:
         issuer: https://dex.management-cluster.example.com
         getUserInfo: true
         insecureEnableGroups: true
   ```

3. **Teleport Application Access** configured for both Dex and MCP server

#### Troubleshooting Token Exchange

**Common Issues and Solutions:**

| Symptom | Likely Cause | Solution |
|---------|--------------|----------|
| `issuer mismatch: expected "proxy-url", got "actual-issuer"` | Missing `expectedIssuer` for proxied access | Set `expectedIssuer` to the actual Dex issuer URL shown in the error |
| `token exchange failed: connection refused` | Wrong `dexTokenEndpoint` URL | Verify the endpoint is reachable from muster's network |
| `token exchange failed: 401 Unauthorized` | Remote Dex doesn't trust local Dex | Configure OIDC connector on remote Dex (see example above) |
| `token exchange failed: invalid_grant` | Token expired or connector misconfigured | Check token lifetime and connector ID matches |

**Debugging Steps:**

1. **Verify configuration**: Check the MCPServer's token exchange config
   ```bash
   kubectl get mcpserver <name> -o yaml | grep -A10 tokenExchange
   ```

2. **Check muster logs**: Look for token exchange events
   ```bash
   kubectl logs -l app=muster | grep -i "token.*exchange"
   ```

3. **Verify remote Dex connectivity**: Test the token endpoint is reachable
   ```bash
   curl -s -o /dev/null -w "%{http_code}" https://<dex-endpoint>/.well-known/openid-configuration
   ```

4. **Check Kubernetes events**: Muster emits events for token exchange
   ```bash
   kubectl get events --field-selector involvedObject.kind=MCPServer
   ```

**Events Reference:**
- `MCPServerTokenExchanged`: Token exchange succeeded
- `MCPServerTokenExchangeFailed`: Token exchange failed (check message for details)

### CLI Usage

```bash
# List all MCP servers
kubectl get mcpservers
# or using short name
kubectl get mcps

# Get detailed information
kubectl describe mcpserver git-tools

# Check logs
kubectl logs mcpserver/git-tools

# Create from file
kubectl apply -f mcpserver.yaml
```

---

## ServiceClass

ServiceClass resources define reusable templates for service instances with complete lifecycle management including start, stop, health checking, and dependency management.

### Resource Definition

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: <serviceclass-name>
  namespace: <namespace>
spec:
  # Optional: Human-readable description
  description: "<description>"
  
  # Optional: Argument schema for service instantiation
  args:
    <arg_name>:
      type: string|integer|boolean|number|object|array
      required: true|false
      default: <default_value>
      description: "<description>"
  
  # Required: Service configuration template
  serviceConfig:
    # Optional: Default name template for instances
    defaultName: "<name_template>"
    
    # Optional: ServiceClass dependencies
    dependencies: ["<serviceclass1>", "<serviceclass2>"]
    
    # Required: Lifecycle tool definitions
    lifecycleTools:
      # Required: Start tool
      start:
        tool: "<tool_name>"
        args:
          <key>: <value_template>
        outputs:
          <var_name>: "<json_path>"
      
      # Required: Stop tool  
      stop:
        tool: "<tool_name>"
        args:
          <key>: <value_template>
        outputs:
          <var_name>: "<json_path>"
      
      # Optional: Restart tool
      restart:
        tool: "<tool_name>"
        args:
          <key>: <value_template>
        outputs:
          <var_name>: "<json_path>"
      
      # Optional: Health check tool
      healthCheck:
        tool: "<tool_name>"
        args:
          <key>: <value_template>
        expect:
          success: true|false
          jsonPath:
            <path>: <expected_value>
        expectNot:
          success: true|false
          jsonPath:
            <path>: <unexpected_value>
      
      # Optional: Status tool
      status:
        tool: "<tool_name>"
        args:
          <key>: <value_template>
        outputs:
          <var_name>: "<json_path>"
    
    # Optional: Health check configuration
    healthCheck:
      enabled: true|false
      interval: "<duration>"
      failureThreshold: <number>
      successThreshold: <number>
    
    # Optional: Operation timeouts
    timeout:
      create: "<duration>"
      delete: "<duration>"
      healthCheck: "<duration>"
    
    # Optional: Output templates
    outputs:
      <output_name>: "<template>"

# Status is managed automatically by muster (via reconciliation)
status:
  valid: true|false                  # Spec passes structural validation
  validationErrors: []               # Any spec validation error messages
  referencedTools: []                # Tools mentioned in lifecycle definitions (informational)
  conditions: []                     # Kubernetes standard conditions
```

### Field Reference

#### Spec Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `description` | `string` | No | Human-readable description | Max 1000 characters |
| `args` | `map[string]ArgDefinition` | No | Argument schema for service instances | - |
| `serviceConfig` | `ServiceConfig` | Yes | Core service configuration template | - |

#### ArgDefinition Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `type` | `string` | Yes | Data type of the argument | `string`, `integer`, `boolean`, `number`, `object`, `array` |
| `required` | `boolean` | No | Whether argument is mandatory | Default: `false` |
| `default` | `any` | No | Default value if not provided | - |
| `description` | `string` | No | Usage explanation | Max 500 characters |

#### ServiceConfig Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `defaultName` | `string` | No | Template for generating instance names | Supports templating |
| `dependencies` | `[]string` | No | Required ServiceClasses | - |
| `lifecycleTools` | `LifecycleTools` | Yes | Tool definitions for lifecycle management | - |
| `healthCheck` | `HealthCheckConfig` | No | Health monitoring configuration | - |
| `timeout` | `TimeoutConfig` | No | Operation timeout settings | - |
| `outputs` | `map[string]string` | No | Output templates for instances | Supports templating |

#### LifecycleTools Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `start` | `ToolCall` | Yes | Tool for starting service instances |
| `stop` | `ToolCall` | Yes | Tool for stopping service instances |
| `restart` | `ToolCall` | No | Tool for restarting service instances |
| `healthCheck` | `HealthCheckToolCall` | No | Tool for health checking |
| `status` | `ToolCall` | No | Tool for querying service status |

#### ToolCall Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `tool` | `string` | Yes | Name of the tool to execute |
| `args` | `map[string]any` | No | Arguments for tool execution (supports templating) |
| `outputs` | `map[string]string` | No | Maps tool result paths to variable names |

#### HealthCheckToolCall Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `tool` | `string` | Yes | Name of the health check tool |
| `args` | `map[string]any` | No | Arguments for tool execution |
| `expect` | `HealthCheckExpectation` | No | Positive health check expectations |
| `expectNot` | `HealthCheckExpectation` | No | Negative health check expectations |

#### HealthCheckExpectation Fields

| Field | Type | Description |
|-------|------|-------------|
| `success` | `boolean` | Whether the tool call should succeed |
| `jsonPath` | `map[string]any` | JSON path conditions to check in results |

#### HealthCheckConfig Fields

| Field | Type | Default | Description | Constraints |
|-------|------|---------|-------------|-------------|
| `enabled` | `boolean` | `false` | Whether health checking is active | - |
| `interval` | `string` | `"30s"` | How often to perform health checks | Duration format: `^[0-9]+(ns\|us\|ms\|s\|m\|h)$` |
| `failureThreshold` | `integer` | `3` | Failures before marking unhealthy | Min: 1 |
| `successThreshold` | `integer` | `1` | Successes to mark healthy | Min: 1 |

#### TimeoutConfig Fields

| Field | Type | Description | Constraints |
|-------|------|-------------|-------------|
| `create` | `string` | Timeout for service creation | Duration format: `^[0-9]+(ns\|us\|ms\|s\|m\|h)$` |
| `delete` | `string` | Timeout for service deletion | Duration format: `^[0-9]+(ns\|us\|ms\|s\|m\|h)$` |
| `healthCheck` | `string` | Timeout for individual health checks | Duration format: `^[0-9]+(ns\|us\|ms\|s\|m\|h)$` |

### Examples

#### PostgreSQL Database ServiceClass
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: postgres-database
  namespace: default
spec:
  description: "PostgreSQL database service with lifecycle management"
  args:
    database_name:
      type: string
      required: true
      description: "Name of the database to create"
    port:
      type: integer
      required: false
      default: 5432
      description: "Port number for the database"
    replicas:
      type: integer
      required: false
      default: 1
      description: "Number of database replicas"
  serviceConfig:
    defaultName: "postgres-{{.database_name}}"
    dependencies: []
    lifecycleTools:
      start:
        tool: "docker_run"
        args:
          image: "postgres:13"
          env:
            POSTGRES_DB: "{{.database_name}}"
            POSTGRES_PORT: "{{.port}}"
        outputs:
          containerId: "result.container_id"
      stop:
        tool: "docker_stop"
        args:
          container_id: "{{.containerId}}"
      healthCheck:
        tool: "postgres_health_check"
        args:
          port: "{{.port}}"
        expect:
          success: true
          jsonPath:
            status: "healthy"
    healthCheck:
      enabled: true
      interval: "30s"
      failureThreshold: 3
      successThreshold: 1
    timeout:
      create: "5m"
      delete: "2m"
      healthCheck: "10s"
    outputs:
      connection_string: "postgresql://user:pass@localhost:{{.port}}/{{.database_name}}"
      port: "{{.port}}"
```

#### Web Application ServiceClass
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: web-application
  namespace: default
spec:
  description: "Generic web application service"
  args:
    image:
      type: string
      required: true
      description: "Container image to deploy"
    port:
      type: integer
      default: 8080
      description: "Application port"
    replicas:
      type: integer
      default: 1
      description: "Number of replicas"
    environment:
      type: object
      default: {}
      description: "Environment variables"
  serviceConfig:
    defaultName: "web-{{.image | replace '/' '-' | replace ':' '-'}}"
    lifecycleTools:
      start:
        tool: "kubernetes_deploy"
        args:
          image: "{{.image}}"
          port: "{{.port}}"
          replicas: "{{.replicas}}"
          env: "{{.environment}}"
        outputs:
          deploymentName: "metadata.name"
          serviceName: "service.metadata.name"
      stop:
        tool: "kubernetes_delete"
        args:
          deployment: "{{.deploymentName}}"
          service: "{{.serviceName}}"
      status:
        tool: "kubernetes_status"
        args:
          deployment: "{{.deploymentName}}"
        outputs:
          readyReplicas: "status.readyReplicas"
      healthCheck:
        tool: "http_check"
        args:
          url: "http://{{.serviceName}}:{{.port}}/health"
        expect:
          success: true
    healthCheck:
      enabled: true
      interval: "30s"
    outputs:
      url: "http://{{.serviceName}}:{{.port}}"
      deployment: "{{.deploymentName}}"
```

### CLI Usage

```bash
# List all ServiceClasses
kubectl get serviceclasses
# or using short name
kubectl get sc

# Get detailed information
kubectl describe serviceclass postgres-database

# Create from file
kubectl apply -f serviceclass.yaml
```

---

## Workflow

Workflow resources define multi-step processes that can be executed to automate complex tasks like application deployment, data migration, or system configuration.

### Resource Definition

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: <workflow-name>
  namespace: <namespace>
spec:
  # Optional: Human-readable description
  description: "<description>"
  
  # Optional: Argument schema for workflow execution
  args:
    <arg_name>:
      type: string|integer|boolean|number|object|array
      required: true|false
      default: <default_value>
      description: "<description>"
  
  # Required: Workflow steps
  steps:
    - id: "<step_id>"
      tool: "<tool_name>"
      args:
        <key>: <value_template>
      condition:
        tool: "<condition_tool>"
        args:
          <key>: <value>
        fromStep: "<step_id>"
        expect:
          success: true|false
          jsonPath:
            <path>: <expected_value>
        expectNot:
          success: true|false
          jsonPath:
            <path>: <unexpected_value>
      store: true|false
      allowFailure: true|false
      outputs:
        <var_name>: <output_template>
      description: "<step_description>"

# Status is managed automatically by muster (via reconciliation)
status:
  valid: true|false                  # Spec passes structural validation
  validationErrors: []               # Any spec validation error messages
  referencedTools: []                # Tools mentioned in steps (informational)
  stepCount: 0                       # Number of steps in the workflow
  conditions: []                     # Kubernetes standard conditions
```

### Field Reference

#### Spec Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `description` | `string` | No | Human-readable description | Max 1000 characters |
| `args` | `map[string]ArgDefinition` | No | Argument schema for execution validation | - |
| `steps` | `[]WorkflowStep` | Yes | Sequence of workflow steps | Min 1 item |

#### WorkflowStep Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `id` | `string` | Yes | Unique step identifier within workflow | Pattern: `^[a-zA-Z0-9_-]+$`, Max 63 chars |
| `tool` | `string` | Yes | Name of the tool to execute | Min 1 character |
| `args` | `map[string]any` | No | Arguments for tool execution (supports templating) | - |
| `condition` | `WorkflowCondition` | No | Optional execution condition | - |
| `store` | `boolean` | No | Store step result for later steps | Default: `false` |
| `allowFailure` | `boolean` | No | Continue on step failure | Default: `false` |
| `outputs` | `map[string]any` | No | Output mappings for subsequent steps | - |
| `description` | `string` | No | Human-readable step documentation | Max 500 characters |

#### WorkflowCondition Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `tool` | `string` | No* | Tool for condition evaluation |
| `args` | `map[string]any` | No | Arguments for condition tool |
| `fromStep` | `string` | No* | Reference step for condition evaluation |
| `expect` | `WorkflowConditionExpectation` | No | Positive expectations |
| `expectNot` | `WorkflowConditionExpectation` | No | Negative expectations |

*Note: Either `tool` or `fromStep` should be specified

#### WorkflowConditionExpectation Fields

| Field | Type | Description |
|-------|------|-------------|
| `success` | `boolean` | Whether the tool call should succeed |
| `jsonPath` | `map[string]any` | JSON path conditions to check |

#### Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `valid` | `boolean` | Whether the spec passes structural validation |
| `validationErrors` | `[]string` | Any spec validation error messages |
| `referencedTools` | `[]string` | Tools mentioned in workflow steps (informational only) |
| `stepCount` | `int` | Number of steps in the workflow |
| `conditions` | `[]metav1.Condition` | Standard Kubernetes conditions |

> **Note**: Tool availability is computed per-session at runtime based on user authentication. A workflow may show all referenced tools but only be executable by users with access to those tools. See [ADR 007](../explanation/decisions/007-crd-status-reconciliation.md) for details.

### Examples

#### Application Deployment Workflow
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: deploy-application
  namespace: default
spec:
  name: deploy-application
  description: "Deploy application to production environment with validation"
  args:
    app_name:
      type: string
      required: true
      description: "Name of the application to deploy"
    environment:
      type: string
      default: "production"
      description: "Target deployment environment"
    replicas:
      type: integer
      default: 3
      description: "Number of application replicas"
  steps:
    - id: build_image
      tool: docker_build
      args:
        name: "{{.app_name}}"
        tag: "{{.environment}}-latest"
      store: true
      description: "Build container image for deployment"
    
    - id: deploy_service
      tool: core_service_create
      args:
        name: "{{.app_name}}-{{.environment}}"
        serviceClassName: "web-application"
        args:
          image: "{{.results.build_image.image_id}}"
          replicas: "{{.replicas}}"
      store: true
      description: "Create service instance"
    
    - id: verify_deployment
      tool: health_check
      args:
        service_name: "{{.results.deploy_service.name}}"
        timeout: "5m"
      store: true
      description: "Verify deployment health"
```

#### Conditional Database Migration Workflow
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: database-migration
  namespace: default
spec:
  name: database-migration
  description: "Perform database migration with rollback capability"
  args:
    database_name:
      type: string
      required: true
      description: "Target database name"
    migration_version:
      type: string
      required: true
      description: "Target migration version"
    dry_run:
      type: boolean
      default: false
      description: "Perform dry run without applying changes"
  steps:
    - id: check_database
      tool: postgres_status
      args:
        database: "{{.database_name}}"
      store: true
      description: "Verify database connectivity"
    
    - id: backup_database
      tool: postgres_backup
      args:
        database: "{{.database_name}}"
        backup_name: "pre-migration-{{.migration_version}}"
      condition:
        fromStep: "check_database"
        expect:
          success: true
          jsonPath:
            status: "connected"
      store: true
      description: "Create backup before migration"
    
    - id: run_migration
      tool: postgres_migrate
      args:
        database: "{{.database_name}}"
        version: "{{.migration_version}}"
        dry_run: "{{.dry_run}}"
      condition:
        fromStep: "backup_database"
        expect:
          success: true
      allowFailure: false
      store: true
      description: "Execute database migration"
    
    - id: verify_migration
      tool: postgres_verify
      args:
        database: "{{.database_name}}"
        expected_version: "{{.migration_version}}"
      condition:
        fromStep: "run_migration"
        expect:
          success: true
      description: "Verify migration completed successfully"
    
    - id: rollback_migration
      tool: postgres_restore
      args:
        database: "{{.database_name}}"
        backup_name: "{{.results.backup_database.backup_name}}"
      condition:
        fromStep: "verify_migration"
        expectNot:
          success: true
      description: "Rollback on migration failure"
```

#### Multi-Environment Deployment Workflow
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: progressive-deployment
  namespace: default
spec:
  name: progressive-deployment
  description: "Deploy application progressively across environments"
  args:
    app_name:
      type: string
      required: true
      description: "Application name"
    version:
      type: string
      required: true
      description: "Application version"
    environments:
      type: array
      default: ["staging", "production"]
      description: "Target environments in order"
  steps:
    - id: deploy_staging
      tool: core_service_create
      args:
        name: "{{.app_name}}-staging"
        serviceClassName: "web-application"
        args:
          image: "{{.app_name}}:{{.version}}"
          environment: "staging"
      store: true
      description: "Deploy to staging environment"
    
    - id: test_staging
      tool: integration_test
      args:
        service_name: "{{.results.deploy_staging.name}}"
        test_suite: "smoke-tests"
      condition:
        fromStep: "deploy_staging"
        expect:
          success: true
      store: true
      description: "Run integration tests on staging"
    
    - id: deploy_production
      tool: core_service_create
      args:
        name: "{{.app_name}}-production"
        serviceClassName: "web-application"
        args:
          image: "{{.app_name}}:{{.version}}"
          environment: "production"
          replicas: 5
      condition:
        fromStep: "test_staging"
        expect:
          success: true
          jsonPath:
            test_result: "passed"
      store: true
      description: "Deploy to production environment"
    
    - id: monitor_production
      tool: health_monitor
      args:
        service_name: "{{.results.deploy_production.name}}"
        duration: "10m"
      condition:
        fromStep: "deploy_production"
        expect:
          success: true
      description: "Monitor production deployment"
```

### CLI Usage

```bash
# List all workflows
kubectl get workflows
# or using short name
kubectl get wf

# Get detailed information
kubectl describe workflow deploy-application

# Create from file
kubectl apply -f workflow.yaml
```

---

## Templating

All CRDs support Go template syntax for dynamic values. Templates can reference:

### Available Variables

| Context | Variables | Description |
|---------|-----------|-------------|
| **ServiceClass** | `.` | All args passed during service creation |
| | `.<arg_name>` | Specific argument values |
| | `.containerId`, `.deploymentName`, etc. | Outputs from previous tools |
| **Workflow** | `.` | All args passed during workflow execution |
| | `.<arg_name>` | Specific argument values |
| | `.results.<step_id>.<output>` | Results from previous steps |

### Template Functions

| Function | Description | Example |
|----------|-------------|---------|
| `replace` | String replacement | `{{.image | replace "/" "-"}}` |
| `lower` | Convert to lowercase | `{{.name | lower}}` |
| `upper` | Convert to uppercase | `{{.env | upper}}` |
| `trim` | Remove whitespace | `{{.value | trim}}` |

### Examples

```yaml
# ServiceClass templating
defaultName: "{{.app_name}}-{{.environment}}"
args:
  image: "{{.registry}}/{{.app_name}}:{{.version}}"
  env:
    APP_NAME: "{{.app_name | upper}}"
    DATABASE_URL: "{{.database_connection_string}}"

# Workflow templating  
args:
  service_name: "{{.app_name}}-{{.environment}}"
  image: "{{.results.build_image.image_id}}"
  replicas: "{{.replicas}}"
```

---

## Tool Availability and Dependencies

### Tool Discovery

Muster automatically discovers available tools from:
1. **Core Tools**: Built-in muster tools (`core_service_*`, `core_workflow_*`, etc.)
2. **MCP Server Tools**: Tools provided by registered MCPServer resources
3. **Dynamic Tools**: Workflow execution tools (`workflow_<name>`)

### Session-Scoped Tool Visibility

**Important**: With session-scoped tool visibility (see [ADR 007](../explanation/decisions/007-crd-status-reconciliation.md)), tool availability is no longer a global property stored in CRD status. Instead:

- Tool availability is computed **per-session at runtime**
- Different users may have access to different tools based on their OAuth authentication
- CRD status fields show `referencedTools` (what tools the resource uses), not availability
- Actual executability depends on the user's authenticated session

This means:
- A Workflow requiring `kubernetes_deploy` is executable by users authenticated with `mcp-kubernetes`
- The same Workflow is not executable by users who haven't authenticated
- The `valid` status field indicates spec correctness, not tool availability

### Dependency Resolution

#### ServiceClass Dependencies
```yaml
spec:
  serviceConfig:
    dependencies: ["database", "cache"]  # Must be available ServiceClasses
```

### Status Monitoring

CRDs provide status information about validation and referenced tools:

```bash
# Check ServiceClass validation status
kubectl get serviceclass postgres-database -o jsonpath='{.status.valid}'

# Check referenced tools (informational)
kubectl get workflow deploy-app -o jsonpath='{.status.referencedTools}'

# Check MCP Server runtime state
kubectl get mcpserver git-tools -o jsonpath='{.status.state}'
```

### Reconciliation

Muster automatically reconciles CRD status with runtime state:

- **MCPServer**: Status reflects actual process state (running, stopped, healthy, unhealthy)
- **ServiceClass**: Status reflects spec validation and lists referenced tools
- **Workflow**: Status reflects spec validation and lists referenced tools

Reconciliation works in both filesystem mode (watching YAML files) and Kubernetes mode (using informers). See the [reconciler package documentation](../../internal/reconciler/doc.go) for implementation details.

---

## Best Practices

### Naming Conventions

| Resource | Pattern | Examples |
|----------|---------|----------|
| **MCPServer** | `<tool-category>-tools` | `git-tools`, `filesystem-tools`, `database-tools` |
| **ServiceClass** | `<service-type>` | `postgres-database`, `web-application`, `message-queue` |
| **Workflow** | `<action>-<target>` | `deploy-application`, `backup-database`, `scale-service` |

### Resource Organization

```yaml
# Use consistent namespacing
metadata:
  namespace: muster-system    # For system resources
  namespace: applications     # For application resources
  namespace: default         # For development resources

# Use labels for grouping
metadata:
  labels:
    component: database
    environment: production
    version: v1.2.3
```

### Security Considerations

```yaml
# MCPServer environment variables
env:
  # Avoid storing secrets directly
  DATABASE_URL: "postgresql://user:pass@localhost:5432/db"  # ❌ BAD
  
  # Use references to secrets
  DATABASE_URL_FROM_SECRET: "database-credentials"         # ✅ GOOD

# ServiceClass security
serviceConfig:
  lifecycleTools:
    start:
      args:
        # Use secure defaults
        security_context:
          runAsNonRoot: true
          readOnlyRootFilesystem: true
```

#### SSO Token Forwarding Security

When using `forwardToken: true` for SSO:

1. **ID Tokens Only**: Muster forwards ID tokens (containing identity claims), not access tokens
2. **TLS Required**: All communication must be over HTTPS
3. **Tokens Not Logged**: Tokens are never logged in plaintext
4. **Downstream Opt-In**: Downstream servers must explicitly configure `TrustedAudiences`
5. **Constant-Time Comparison**: mcp-oauth uses `subtle.ConstantTimeCompare` for audience matching
6. **Audit Logging**: Both muster and downstream servers log when cross-client tokens are used

Events emitted for SSO:
- `MCPServerTokenForwarded`: Successful SSO token forwarding
- `MCPServerTokenForwardingFailed`: Token forwarding failed
- `MCPServerTokenExchanged`: Successful RFC 8693 token exchange
- `MCPServerTokenExchangeFailed`: Token exchange failed

### Error Handling

```yaml
# Workflow error handling
steps:
  - id: risky_operation
    tool: external_api_call
    allowFailure: true    # Continue on failure
    
  - id: cleanup
    tool: cleanup_resources
    condition:
      fromStep: "risky_operation"
      expectNot:
        success: true     # Only run if previous step failed
```

### Performance Optimization

```yaml
# Health check tuning
healthCheck:
  enabled: true
  interval: "30s"        # Balance between responsiveness and load
  failureThreshold: 3    # Avoid false positives
  successThreshold: 1    # Quick recovery

# Timeout configuration
timeout:
  create: "5m"          # Generous for complex deployments
  delete: "2m"          # Quick cleanup
  healthCheck: "10s"    # Fast feedback
```

---

## Related Documentation

- **[CLI Reference](cli/)** - Command-line tools for managing CRDs
- **[MCP Tools Reference](mcp-tools.md)** - Available tools for use in ServiceClasses and Workflows
- **[Workflow Creation Guide](../how-to/workflow-creation.md)** - Step-by-step workflow development
- **[Service Configuration Guide](../how-to/service-configuration.md)** - ServiceClass development patterns
- **[Architecture Overview](../explanation/architecture.md)** - How CRDs fit into the muster system 
