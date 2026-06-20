# Muster Custom Resource Definitions (CRDs)

Complete reference for muster's Kubernetes Custom Resource Definitions. These CRDs define the core resources managed by the muster system for orchestrating MCP servers, service lifecycle management, and workflow execution.

## Overview

Muster provides two primary CRDs:

| CRD | API Version | Kind | Short Name | Purpose |
|-----|-------------|------|------------|---------|
| **MCPServer** | `muster.giantswarm.io/v1alpha1` | `MCPServer` | `mcps` | Manages MCP (Model Context Protocol) servers that provide tools |
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

  # Optional: Family grouping. When set, the aggregator exposes tools as
  # x_<family.name>_<toolName> with a required parameter (named by
  # family.instanceArg) selecting which instance handles the call. Multiple
  # MCPServer CRs sharing the same family.name (for example, several kubernetes
  # MCP servers pointed at different clusters) appear to clients as a single
  # deduplicated tool surface. Both name and instanceArg are required when
  # family is set.
  family:
    name: "<family-name>"
    instanceArg: "<parameter-name>"  # e.g. management_cluster, country, model

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
| `toolPrefix` | `string` | No | Per-server tool prefix used when `family` is unset | Pattern: `^[a-zA-Z][a-zA-Z0-9_-]*$` |
| `family` | `object` | No | Family grouping for equivalent servers under a shared tool surface | `name` and `instanceArg` both required when set |
| `family.name` | `string` | Yes (in `family`) | Family identifier | Pattern: `^[a-zA-Z][a-zA-Z0-9_-]*$` |
| `family.instanceArg` | `string` | Yes (in `family`) | Name of the required parameter the LLM uses to select an instance (e.g. `management_cluster`, `country`, `model`) | Pattern: `^[a-zA-Z][a-zA-Z0-9_]*$` |
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
| `type` | `string` | No | Authentication type | Must be `oauth` or `none` |
| `forwardToken` | `boolean` | No | Forward muster's ID token for SSO | Default: `false` |
| `requiredAudiences` | `[]string` | No | Additional audiences to request from IdP for SSO | Used with `forwardToken` or `tokenExchange` |
| `tokenExchange` | `TokenExchangeConfig` | No | RFC 8693 token exchange for cross-cluster SSO | See below |

**Note on `requiredAudiences`**: When using SSO (token forwarding or token exchange) with downstream servers that require specific audience claims (e.g., Kubernetes OIDC authentication), specify the required audiences here.

- **Token Forwarding** (`forwardToken: true`): Muster requests these audiences from its upstream IdP (e.g., Dex) using cross-client scopes (`audience:server:client_id:<audience>`). The resulting multi-audience token is forwarded to downstream servers. Required audiences are collected at muster startup - if you add MCPServers with new audiences after users have authenticated, they must re-authenticate.
- **Token Exchange** (`tokenExchange.enabled: true`): The audiences are appended as cross-client scopes to the token exchange request to the remote IdP. This ensures the exchanged token contains the audiences needed by the downstream server on the remote cluster.

Example: `requiredAudiences: ["dex-k8s-authenticator"]`.

**Security**: Access control for `requiredAudiences` relies on two layers: (1) Kubernetes RBAC controls who can create/modify MCPServer CRDs, and (2) the IdP's cross-client configuration determines which audiences are allowed. Audience values must not contain whitespace characters and are validated before use.

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
| `state` | `string` | Current infrastructure state (see table below) |
| `health` | `string` | Health status: `unknown`, `healthy`, `unhealthy`, `checking` |
| `lastError` | `string` | Error message from the most recent operation |
| `lastConnected` | `*metav1.Time` | When the server was last successfully connected |
| `restartCount` | `int` | Number of times the server has been restarted |
| `conditions` | `[]metav1.Condition` | Standard Kubernetes conditions |

##### CRD State Values

The MCPServer CRD status reflects **infrastructure state** (network reachability and authentication status):

| CRD State | Meaning | Server Response |
|-----------|---------|-----------------|
| `Connected` | Server is reachable and authenticated | 200 OK |
| `Auth Required` | Server is reachable but requires authentication | 401 Unauthorized |
| `Connecting` | Attempting to establish connection | Connection in progress |
| `Disconnected` | Not connected (intentionally) | N/A |
| `Failed` | Server cannot be reached | Connection refused, DNS failure, timeout |
| `Running` | Process is running (stdio servers) | N/A |
| `Starting` | Process is starting (stdio servers) | N/A |
| `Stopped` | Process is stopped (stdio servers) | N/A |

**Key point**: A 401 Unauthorized response indicates the server IS reachable (at the network level), so the CRD state is `Auth Required`, not `Failed`. This gives operators clear visibility into which servers need authentication.

##### Session State in CLI

The `muster list mcpserver` command shows both infrastructure state and session-specific authentication state:

```
NAME          STATE          SESSION         TYPE
server-a      Connected      Authenticated   streamable-http
server-b      Auth Required  Pending Auth    streamable-http
server-c      Failed         -               streamable-http
```

- **STATE**: Infrastructure state from CRD (reachability and auth status)
  - `Connected`: Server is fully operational
  - `Auth Required`: Server is reachable but needs authentication
  - `Failed`: Server cannot be reached
- **SESSION**: Per-user authentication state (only shown when logged in to muster)
  - `Authenticated`: User has successfully authenticated to this server
  - `Pending Auth`: Server requires authentication, user has not authenticated
  - `Expired`: User's token has expired
  - `-`: No session state (server unreachable or no auth required)

This aligns with the output from `muster auth status`, which shows per-server session state.

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
```

When `forwardToken: true` is configured:
1. User authenticates to muster once via OAuth
2. When calling this MCP server, muster forwards the user's ID token
3. The downstream server validates the token (must configure `TrustedAudiences`)

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

#### Cross-Cluster SSO via Proxy (OAuth Token Exchange)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: private-cluster-k8s
  namespace: default
spec:
  type: streamable-http
  toolPrefix: "private_k8s"
  description: "Kubernetes tools on a private cluster accessed via a proxy"
  url: "https://mcp-kubernetes.private-cluster.proxy.example.com/mcp"
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
```

When accessing Dex through a proxy (e.g., VPN, HTTP proxy):
- `dexTokenEndpoint`: The proxy URL used to reach Dex's token endpoint
- `expectedIssuer`: The actual issuer URL configured in Dex (used for token validation)

This is necessary because Dex's tokens contain the configured issuer URL in the `iss` claim, not the proxy URL used to access it. Muster validates that the exchanged token's issuer matches `expectedIssuer` for security.

> **Warning**: When accessing Dex through a proxy, you **MUST** set `expectedIssuer` explicitly. If omitted, muster derives the expected issuer from `dexTokenEndpoint` (the proxy URL), which will cause token validation to fail because the token's `iss` claim contains the actual Dex issuer URL, not the proxy URL. This validation failure is intentional - it ensures you explicitly configure the expected issuer for proxied scenarios.

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

  # Required: Workflow steps. Each step is exactly one of: a tool call (tool),
  # a sequential loop (forEach), or a concurrent group (parallel).
  steps:
    # 1) A plain tool call
    - id: "<step_id>"
      tool: "<tool_name>"
      args:
        <key>: <value_template>      # e.g. "{{.input.namespace}}"
      condition:
        # Catalog of fields — set exactly ONE source (template, tool, or fromStep).
        # template: a boolean Go-template gate, stands alone:
        template: "{{ eq .input.env \"production\" }}"
        # ...or tool/fromStep, which REQUIRE an expect or expectNot block:
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
      output: true|false              # include this step's result in the returned document
      store: true|false               # deprecated alias for output
      allowFailure: true|false
      description: "<step_description>"

    # 2) A sequential loop over a list (body is a flat list of sub-steps)
    - id: "<step_id>"
      forEach:
        items: "{{ .input.<list_arg> }}"  # must resolve to an array
        as: item                          # loop variable -> {{.vars.item}} (default "item")
        steps:
          - id: "<sub_step_id>"
            tool: "<tool_name>"
            args:
              <key>: "{{ .vars.item.<field> }}"

    # 3) A concurrent group (sub-steps run in parallel; siblings are independent)
    - id: "<step_id>"
      parallel:
        - id: "<sub_step_id>"
          tool: "<tool_name>"
        - id: "<sub_step_id>"
          tool: "<tool_name>"

  # Optional: best-effort cleanup/rollback steps run when the workflow fails
  # on a step that does not allow failure.
  onFailure:
    - id: "<sub_step_id>"
      tool: "<rollback_tool>"

  # Optional: a templated projection rendered once after all steps complete and
  # returned in place of the default envelope. Each leaf is a Go-template/sprig
  # expression evaluated against .input/.results/.vars; JSON structure (objects,
  # arrays, numbers) is preserved.
  output:
    <key>: "{{ .results.<step_id>.<field> }}"

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
| `onFailure` | `[]WorkflowSubStep` | No | Cleanup/rollback steps run when the workflow fails on a non-`allowFailure` step | - |
| `output` | `map[string]any` | No | Templated projection rendered after all steps complete, returned in place of the default envelope. Each leaf is evaluated against `.input`/`.results`/`.vars` with JSON structure preserved | - |

#### WorkflowStep Fields

A step is exactly one of: a tool call (`tool`), a sequential loop (`forEach`), or a concurrent group (`parallel`).

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `id` | `string` | Yes | Unique step identifier within workflow | Pattern: `^[a-zA-Z0-9_-]+$`, Max 63 chars |
| `tool` | `string` | No* | Name of the tool to execute | Mutually exclusive with `forEach`/`parallel` |
| `args` | `map[string]any` | No | Arguments for tool execution (supports templating) | - |
| `condition` | `WorkflowCondition` | No | Optional execution condition | - |
| `forEach` | `WorkflowForEach` | No* | Run a body of sub-steps once per list item | Mutually exclusive with `tool`/`parallel` |
| `parallel` | `[]WorkflowSubStep` | No* | Sub-steps executed concurrently | Mutually exclusive with `tool`/`forEach` |
| `output` | `boolean` | No | Include this step's result in the returned document. Every step result is referenceable by later steps (`{{.results.<id>}}`) regardless of this flag | Default: `false` |
| `store` | `boolean` | No | Deprecated alias for `output`; kept for backwards compatibility | Default: `false` |
| `allowFailure` | `boolean` | No | Continue on step failure | Default: `false` |
| `description` | `string` | No | Human-readable step documentation | Max 500 characters |

*Exactly one of `tool`, `forEach`, or `parallel` must be set. This is enforced by the CRD at apply time (a CEL validation rule), so `kubectl apply` rejects a step that sets none or more than one.

> **Referencing vs. returning**: Every step's result is referenceable by later
> steps as `{{.results.<step_id>}}` without any flag. The `output` flag (and its
> deprecated `store` alias) only controls whether the step's result is included
> in the document returned to the caller. To shape that document further, use the
> workflow-level [`output` projection](#workflow-output-projection).
>
> **Migration**: A previously documented `outputs:` field never did anything and
> has been removed. The `store` flag still works as a backwards-compatible alias
> for `output`; prefer `output`.

#### WorkflowForEach Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `items` | `string` | Yes | Template expression resolving to an array, e.g. `"{{ .input.clusters }}"` |
| `as` | `string` | No | Loop variable name, exposed as `{{ .vars.<as> }}` (default `item`); the zero-based index is `{{ .vars.<as>_index }}` |
| `steps` | `[]WorkflowSubStep` | Yes | Flat body executed once per item (no nested `forEach`/`parallel`) |

#### WorkflowSubStep Fields

Used by `forEach.steps`, `parallel`, and `onFailure`. A sub-step is a plain tool call.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | `string` | Yes | Unique sub-step identifier |
| `tool` | `string` | Yes | Name of the tool to execute |
| `args` | `map[string]any` | No | Arguments for tool execution (supports templating) |
| `condition` | `WorkflowCondition` | No | Optional execution condition |
| `output` | `boolean` | No | Include this sub-step's result in the returned document (default `false`). The result is referenceable by later steps regardless of this flag. Inside `forEach`, each iteration is also addressable as `{{.results.<id>_<index>}}` (the plain `{{.results.<id>}}` keeps the last iteration). |
| `store` | `boolean` | No | Deprecated alias for `output`; kept for backwards compatibility (default `false`) |
| `allowFailure` | `boolean` | No | Continue on failure (default `false`) |
| `description` | `string` | No | Human-readable documentation |

#### WorkflowCondition Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `template` | `string` | No* | Boolean Go-template gate, e.g. `"{{ eq .input.env \"production\" }}"`. The step runs when it renders to `true`. |
| `tool` | `string` | No* | Tool for condition evaluation |
| `args` | `map[string]any` | No | Arguments for condition tool |
| `fromStep` | `string` | No* | Reference step for condition evaluation |
| `expect` | `WorkflowConditionExpectation` | No | Positive expectations (with `tool`/`fromStep`) |
| `expectNot` | `WorkflowConditionExpectation` | No | Negative expectations (with `tool`/`fromStep`) |

*Note: Specify exactly one of `template`, `tool`, or `fromStep`. A `tool`/`fromStep` condition must declare `expect` or `expectNot` (without one, the engine defaults to expecting the call to fail). With `template`, `expect`/`expectNot` are ignored. Both rules are enforced at `kubectl apply` time via CEL. There are no `and`/`or` combinators.

#### WorkflowConditionExpectation Fields

| Field | Type | Description |
|-------|------|-------------|
| `success` | `boolean` | Whether the tool call should succeed |
| `jsonPath` | `map[string]any` | Path conditions to check against the result. Each key uses the workflow's expression language: a dotted/bracketed path navigated from the result (with array indexing, e.g. `items[0].name`), or a full Go-template expression where the result is exposed as `.result` (e.g. `"{{ (index .result.items 0).name }}"`). |

#### Workflow Output Projection

The optional workflow-level `output` field shapes the document returned to the
caller. It is a templated object rendered once after all steps complete, against
the same `.input` / `.results` / `.vars` context used by step args, and returned
in place of the default `{execution_id, workflow, status, input, steps[], ...}`
envelope.

```yaml
spec:
  steps:
    - id: pods
      tool: x_kubernetes_list
      args: { kind: Pod }
    - id: events
      tool: x_kubernetes_list
      args: { kind: Event }
  output:
    cluster: "{{ .input.management_cluster }}"
    notRunning: "{{ .results.pods.items }}"
    backoffCount: "{{ len .results.events.items }}"
```

- Each leaf is a Go-template/sprig expression. JSON structure is preserved:
  `notRunning` stays an array and `backoffCount` stays a number.
- Nested objects and arrays in the projection are rendered recursively.
- Every step result is referenceable here regardless of its `output` flag. When a
  projection is declared it replaces the envelope, so per-step `output`/`store`
  flags no longer affect the returned document (the create/validate path and the
  reconciler warn when such flags are left set and become inert).
- **Type coercion + escape hatch**: a *bare reference path* leaf
  (`"{{ .results.pods.items }}"`) keeps its exact JSON type. A *computed* leaf
  (e.g. `"{{ len .results.events.items }}"`) renders to a string and is then
  coerced to a number when it looks numeric. To keep a computed string whose form
  matters (versions, IDs, zero-padded values like `"08"`/`"1.20"`), reference it
  as a bare path or pipe it through sprig `quote`
  (`'{{ printf "%02d" .n | quote }}'`). Non-finite values (`NaN`/`Inf`) stay
  strings.
- When `output` is omitted, the default envelope is returned unchanged.

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

#### Conditional Database Migration Workflow
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: database-migration
  namespace: default
spec:
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
        database: "{{.input.database_name}}"
      store: true
      description: "Verify database connectivity"

    - id: backup_database
      tool: postgres_backup
      args:
        database: "{{.input.database_name}}"
        backup_name: "pre-migration-{{.input.migration_version}}"
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
        database: "{{.input.database_name}}"
        version: "{{.input.migration_version}}"
        dry_run: "{{.input.dry_run}}"
      condition:
        fromStep: "backup_database"
        expect:
          success: true
      store: true
      description: "Execute database migration"

    - id: verify_migration
      tool: postgres_verify
      args:
        database: "{{.input.database_name}}"
        expected_version: "{{.input.migration_version}}"
      condition:
        fromStep: "run_migration"
        expect:
          success: true
      description: "Verify migration completed successfully"
  onFailure:
    - id: rollback_migration
      tool: postgres_restore
      args:
        database: "{{.input.database_name}}"
        backup_name: "{{.results.backup_database.backup_name}}"
      description: "Restore the pre-migration backup if the workflow fails"
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

Workflow step arguments support Go template syntax (rendered with [sprig](https://masterminds.github.io/sprig/) functions). Templates are resolved server-side at execution time with `missingkey=error`, so referencing an undefined key fails the step.

### Available Variables

The template context exposes exactly these top-level keys:

| Key | Description |
|-----|-------------|
| `.input.<arg_name>` | Arguments passed during workflow execution |
| `.results.<step_id>` | Result of any previous step (navigate fields with `.results.<step_id>.<field>`; no flag required) |
| `.vars.<name>` | Loop variables inside `forEach` (e.g. `.vars.item`, `.vars.item_index`) |
| `.context.<step_id>` | Alias for `.results` |

> There is no bare `.<arg_name>`; always use `.input.<arg_name>`.

### Template Functions

The full [sprig](https://masterminds.github.io/sprig/) function set is available, e.g.:

| Function | Description | Example |
|----------|-------------|---------|
| `eq` / `ne` / `gt` | Comparisons (useful in `condition.template`) | `{{ eq .input.env "production" }}` |
| `replace` | String replacement | `{{ .input.image | replace "/" "-" }}` |
| `lower` / `upper` | Case conversion | `{{ .input.name | lower }}` |
| `trim` | Remove whitespace | `{{ .input.value | trim }}` |

### Examples

```yaml
# Workflow step args
args:
  service_name: "{{.input.app_name}}-{{.input.environment}}"
  image: "{{.results.build_image.image_id}}"
  replicas: "{{.input.replicas}}"
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

### Status Monitoring

CRDs provide status information about validation and referenced tools:

```bash
# Check referenced tools (informational)
kubectl get workflow deploy-app -o jsonpath='{.status.referencedTools}'

# Check MCP Server runtime state
kubectl get mcpserver git-tools -o jsonpath='{.status.state}'
```

### Reconciliation

Muster automatically reconciles CRD status with runtime state:

- **MCPServer**: Status reflects actual process state (running, stopped, healthy, unhealthy)
- **Workflow**: Status reflects spec validation and lists referenced tools

Reconciliation works in both filesystem mode (watching YAML files) and Kubernetes mode (using informers). See the [reconciler package documentation](../../internal/reconciler/doc.go) for implementation details.

---

## Best Practices

### Naming Conventions

| Resource | Pattern | Examples |
|----------|---------|----------|
| **MCPServer** | `<tool-category>-tools` | `git-tools`, `filesystem-tools`, `database-tools` |
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
- **[MCP Tools Reference](mcp-tools.md)** - Available tools for use in Workflows
- **[Workflow Creation Guide](../how-to/workflow-creation.md)** - Step-by-step workflow development
- **[Architecture Overview](../explanation/architecture.md)** - How CRDs fit into the muster system
