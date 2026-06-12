# Muster Configuration Reference

Complete reference for configuring muster system settings, resources, and behavior.

## Overview

Muster uses a file-based configuration system with YAML files organized in a structured directory hierarchy. Configuration is loaded from `~/.config/muster/` by default, or from a custom path specified with `--config-path`.

### Configuration Philosophy

- **Simple**: Direct YAML files that are easy to edit
- **Predictable**: Standard directory structure with clear separation
- **Flexible**: Support for both API-driven and manual file editing

## Configuration Directory Structure

```
~/.config/muster/
├── config.yaml              # Main system configuration
├── mcpservers/              # MCP server definitions
│   ├── kubernetes.yaml
│   ├── github.yaml
│   └── prometheus.yaml
├── workflows/               # Workflow definitions
│   ├── deploy-app.yaml
│   └── backup-database.yaml
└── services/                # Service instances
    ├── my-web-app.yaml
    └── prod-database.yaml
```

## Main Configuration File

### Location
- **Default**: `~/.config/muster/config.yaml`
- **Custom**: Specified via `--config-path` flag

### Structure

```yaml
# ~/.config/muster/config.yaml
aggregator:
  port: 8090                    # Server port (default: 8090)
  host: "localhost"             # Bind address (default: localhost)
  transport: "streamable-http"  # MCP transport (default: streamable-http)
  enabled: true                 # Enable aggregator (default: true)

namespace: "default"            # Kubernetes namespace for CR discovery (default: default)
```

## Configuration Fields Reference

### Top-Level Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `namespace` | `string` | `"default"` | Kubernetes namespace for discovering MCPServer and Workflow CRs |
| `kubernetes` | `bool` | `false` | Enable Kubernetes CRD mode. When `true`, uses Kubernetes CRDs for resource storage. When `false`, uses filesystem YAML files. The Helm chart sets this to `true` by default. |
| `aggregator` | `AggregatorConfig` | see below | Aggregator service configuration |
| `auth` | `AuthConfig` | see below | Authentication settings for CLI |

### Aggregator Configuration

The aggregator manages the unified MCP interface and tool aggregation.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `port` | `int` | `8090` | Port for the aggregator HTTP/WebSocket server |
| `host` | `string` | `"localhost"` | Host address to bind the server to |
| `transport` | `string` | `"streamable-http"` | MCP transport protocol |
| `enabled` | `bool` | `true` | Whether to enable the aggregator service |

#### Transport Options

| Transport | Description | Use Case |
|-----------|-------------|----------|
| `streamable-http` | HTTP with streaming support | **Recommended** - Most compatible |
| `sse` | Server-Sent Events | Real-time updates |
| `stdio` | Standard I/O | Command-line clients |

### Auth Configuration

#### Session Duration

The `sessionDuration` field controls how long a user's session remains valid before
re-authentication is required. This sets the server-side refresh token TTL (the muster
refresh token's lifetime).

```yaml
aggregator:
  oauth:
    server:
      sessionDuration: "720h"  # 30 days (default)
```

| Value | Duration | Notes |
|-------|----------|-------|
| `720h` | 30 days | Default, aligned with Dex's `absoluteLifetime` |
| `168h` | 7 days | More restrictive for high-security environments |
| `2160h` | 90 days | Longer sessions (ensure Dex `absoluteLifetime` matches) |

> **Important:** Muster uses a **rolling** refresh token TTL (reset on each token
> rotation), while Dex's `absoluteLifetime` is an **absolute** limit measured from the
> original login that does **not** reset on rotation. If you increase `sessionDuration`
> beyond Dex's `absoluteLifetime`, the effective session will still be limited by Dex --
> the next token refresh after the absolute lifetime expires will fail, forcing
> re-authentication even if muster's session estimate shows time remaining.
>
> Always ensure `sessionDuration` does not exceed Dex's `absoluteLifetime`.

#### Access Token TTL

The access token TTL is not directly configurable; it defaults to 30 minutes
(`DefaultAccessTokenTTL`), matching Dex's `idTokens` expiry. The mcp-oauth library's
`capTokenExpiry` function automatically caps the effective access token lifetime to never
exceed the provider's token lifetime, so even if the default were higher, the actual
expiry would be capped to what Dex issues.

Access tokens are refreshed automatically using the refresh token -- users do not need to
re-authenticate when they expire. The CLI's `muster auth status` shows the current access
token expiry under "Expires" and the session duration under "Session".

For the full token lifecycle and how muster and Dex tokens interact, see the
[Security Configuration](../operations/security.md#token-lifecycle) guide.

The CLI's `muster auth status` displays an approximate session estimate based on the
default 30-day duration. Custom server-side values are not yet reflected in the CLI
estimate.

#### JWT Access Tokens

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enableJWTMode` | `bool` | `false` | Issue RFC 9068 signed JWTs as access tokens. Required when downstream services (e.g. agentgateway) validate tokens locally without calling the introspection endpoint. |
| `jwtSigningKeyFile` | `string` | `""` | Path to a PEM-encoded private key (EC P-256 → ES256, or RSA ≥2048 → RS256) used to sign JWT access tokens. **Required when `enableJWTMode` is true** — the server refuses to start otherwise. `kid` is derived from the RFC 7638 JWK thumbprint of the public key. In Helm, set the key contents via `muster.oauth.server.jwtSigningKey` (or supply it through `existingSecret` under key `jwt-signing-key` for production, so it never passes through Helm values/release history); the chart mounts it and wires this field automatically. `helm template` fails if neither is provided when JWT mode is on. |
| `resourceIdentifier` | `string` | `""` | RFC 8707 canonical URI for this muster instance as a resource server. Access tokens carry this value in their `aud` claim; tokens bound to a different resource are rejected, preventing replay across resource servers sharing the same IdP. Defaults to `baseUrl` when empty. |

#### DPoP and Trusted Proxies

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `trustedProxyCIDRs` | `[]string` | `[]` | CIDRs from which `X-Forwarded-Proto` and `X-Forwarded-Host` headers are trusted for DPoP htu URL reconstruction. Required when muster runs behind a reverse proxy that terminates TLS. |

#### RFC 8693 Token Exchange

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `trustedIssuers` | `[]TrustedIssuerConfig` | `[]` | Trusted external OIDC issuers. Tokens are accepted as `id_token`, `access_token`, or `jwt` subject_tokens. Use `allowedClaims` to express Kubernetes ServiceAccount or GitHub Actions trust. |

**TrustedIssuerConfig fields:**

| Field | Type | Description |
|-------|------|-------------|
| `issuer` | `string` | Expected `iss` claim value. |
| `jwksUrl` | `string` | JWKS endpoint. Independent of `issuer`. |
| `allowedAudiences` | `[]string` | Accepted `aud` values. Empty accepts any audience. |
| `allowedScopes` | `[]string` | Scope ceiling for tokens from this issuer. Nil means no restriction. |
| `allowedClaims` | `map[string]string` | Required claim name→pattern pairs. Keys are JWT claim names; values are exact strings or globs where `*` spans any chars including `/` and `?` matches one char. Absent or non-string claims are rejected. Empty means no restriction. |
| `allowPrivateIPJWKS` | `bool` | Allow `jwksUrl` to resolve to a private or loopback address. Required for in-cluster Kubernetes SA trust where the JWKS endpoint is `https://kubernetes.default.svc/openid/v1/jwks`. Emits a startup warning when set. Default: `false`. |

#### Brokered Token Exchange (`tokenExchangeBroker`)

Exposes muster's RFC 8693 token exchange to external confidential clients: a broker client POSTs a token-exchange request with an `audience` parameter to `/oauth/token` and receives a token minted by the audience's downstream Dex (instead of a muster-issued JWT). Subject tokens are validated against `trustedIssuers`, so at least one issuer entry covering the broker client's tokens is required.

Policy enforced by the broker path (mcp-oauth): client authentication is mandatory and only confidential clients are accepted; audiences are gated by the per-client allowlist; no refresh tokens are issued (`expires_in` is bounded by the downstream token's expiry — clients re-exchange); DPoP is rejected.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `clientAudiences` | `map[string][]string` | `{}` | Per-client audience allowlist: broker client ID → audiences it may request. A miss returns `invalid_target`. |
| `targets` | `map[string]BrokerTargetConfig` | `{}` | Audience name (e.g. a cluster name) → downstream Dex exchange target. |
| `allowPrivateIP` | `bool` | `false` | Allow downstream token endpoints to resolve to private/loopback IPs. Reduces SSRF protection; internal deployments only. |

**BrokerTargetConfig fields:**

| Field | Type | Description |
|-------|------|-------------|
| `dexTokenEndpoint` | `string` | Downstream Dex token endpoint URL (HTTPS). Required. |
| `expectedIssuer` | `string` | Expected `iss` claim of the exchanged token. Derived from `dexTokenEndpoint` when empty. |
| `connectorId` | `string` | Downstream Dex OIDC connector that trusts the subject token's issuer. Required. |
| `scopes` | `string` | Space-separated downstream scopes (default: `openid profile email groups`). Kubernetes-bound audiences must include the Dex cross-client scope for the apiserver's client, e.g. `audience:server:client_id:dex-k8s-authenticator` — without it the exchanged token's `aud` is the exchange client only, which the kube-apiserver rejects. The client-supplied RFC 8693 `scope` parameter is intentionally ignored. |
| `clientCredentialsSecretRef` | `object` | Kubernetes Secret with the downstream exchange client credentials: `name` (required), `namespace` (defaults to the muster namespace), `clientIdKey` (default `client-id`), `clientSecretKey` (default `client-secret`). |

Example:

```yaml
aggregator:
  oauth:
    server:
      trustedIssuers:
        - issuer: https://dex.main.example.com
          jwksUrl: https://dex.main.example.com/keys
          allowedAudiences: ["portal-frontend"]
      tokenExchangeBroker:
        clientAudiences:
          portal-backend: ["cluster-a"]
        targets:
          cluster-a:
            dexTokenEndpoint: https://dex.cluster-a.example.com/token
            connectorId: main-dex
            scopes: "openid profile email groups audience:server:client_id:dex-k8s-authenticator"
            clientCredentialsSecretRef:
              name: muster-token-exchange-cluster-a
```

#### Private-IP OIDC Discovery (Dex)

By default the SSRF guard rejects an OIDC issuer URL that resolves to a private or loopback address. On clusters where the public Dex hostname resolves to an RFC 1918 address (e.g. an Azure internal load balancer, or air-gapped environments), discovery fails with `context deadline exceeded` and the server starts in degraded mode.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `dex.allowPrivateIPOIDC` | `bool` | `false` | Allow the Dex issuer URL to resolve to a private/loopback IP during OIDC discovery. This is the discovery-path counterpart to `allowPrivateIPJWKS`. Emits a CWE-918 startup warning when set; only enable it when the issuer is genuinely fronted by an internal-only load balancer. |

#### Silent Re-Authentication (CLI Flag)

Silent re-authentication is controlled via CLI flags only, not configuration file.

By default, muster uses interactive authentication. If your IdP supports OIDC `prompt=none` (note: Dex does not), you can enable silent re-authentication with the `--silent` flag:

```bash
muster auth login --silent     # Attempt silent re-auth before interactive
muster agent --silent          # Enable silent auth for agent
```

When `--silent` is used:

1. If you have a previous session, muster opens the browser with OIDC `prompt=none`
2. If your IdP session is still valid, authentication completes without user interaction
3. If the IdP session has expired, muster falls back to interactive login

**Note:** Silent auth is disabled by default because Dex (the default IdP) does not support `prompt=none`. When silent auth fails with Dex, it causes two browser tabs to open.

**Security:** When enabled, silent re-authentication maintains full security:
- PKCE is enforced on every flow
- State parameter prevents CSRF attacks
- The IdP validates the session, not muster
- Any failure falls back to interactive authentication

### Example Configurations

#### Minimal Configuration
```yaml
# Uses all defaults (namespace: "default", aggregator defaults)
aggregator: {}
```

#### Development Configuration
```yaml
aggregator:
  port: 8091
  host: "0.0.0.0"
  transport: "streamable-http"
  enabled: true
```

#### Production Configuration (Kubernetes)
```yaml
namespace: "muster-system"      # Use dedicated namespace for muster CRs
kubernetes: true                # Use Kubernetes CRDs instead of filesystem
aggregator:
  port: 80
  host: "0.0.0.0"
  transport: "streamable-http"
  enabled: true
```

#### Multi-Tenant Configuration
```yaml
namespace: "team-alpha"         # Each team uses their own namespace
aggregator:
  port: 8090
  host: "localhost"
  transport: "streamable-http"
```

## MCP Server Configuration

MCP servers can be configured through YAML files or Kubernetes CRDs. Each server requires:

```yaml
# Local server example
mcpservers:
  - name: filesystem-tools
    description: File system operations
    toolPrefix: fs
    type: stdio              # Server execution type
    autoStart: true
    command: ["npx", "@modelcontextprotocol/server-filesystem", "/workspace"]
    env:
      DEBUG: "1"

  # Streamable HTTP server example
  - name: api-server
    description: Remote API tools
    toolPrefix: api
    type: streamable-http
    url: "https://api.example.com/mcp"
    timeout: 30
    headers:
      Authorization: "Bearer token"

  # SSE server example
  - name: sse-server
    description: SSE-based MCP server
    toolPrefix: sse
    type: sse
    url: "https://api.example.com/sse"
    timeout: 45
```

#### MCP Server Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `name` | `string` | ✅ | - | Unique server identifier |
| `description` | `string` | ❌ | - | Human-readable description |
| `toolPrefix` | `string` | ❌ | - | Tool name prefix |
| `type` | `string` | ✅ | - | Server type (`stdio`, `streamable-http`, or `sse`) |
| `autoStart` | `boolean` | ❌ | `false` | Auto-start server (stdio only) |
| `command` | `[]string` | ✅* | - | Command and args (*required for stdio) |
| `args` | `[]string` | ❌ | - | Command arguments (stdio only) |
| `env` | `map[string]string` | ❌ | `{}` | Environment variables |
| `url` | `string` | ✅* | - | Server URL (*required for streamable-http and sse) |
| `timeout` | `integer` | ❌ | `30` | Connection timeout in seconds |
| `headers` | `map[string]string` | ❌ | `{}` | HTTP headers (streamable-http and sse only) |

### Workflow Configuration

**Location**: `workflows/*.yaml`

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: deploy-application
  namespace: default
spec:
  description: "Deploy application with health checks"
  args:                          # Workflow arguments
    appName:
      type: string
      required: true
      description: "Application name to deploy"
    environment:
      type: string
      default: "staging"
      description: "Target environment"
  steps:                         # Workflow steps
    - id: "build"
      tool: "build_application"
      args:
        name: "{{.appName}}"
        env: "{{.environment}}"
      store: true
      description: "Build the application"

    - id: "deploy"
      tool: "deploy_application"
      args:
        name: "{{.appName}}"
        image: "{{.build.image}}"
        env: "{{.environment}}"
      condition:
        fromStep: "build"
        expect:
          success: true
      description: "Deploy to target environment"

    - id: "health-check"
      tool: "health_check"
      args:
        url: "{{.deploy.url}}"
      allowFailure: false
      description: "Verify deployment health"
```

#### Workflow Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `description` | `string` | ❌ | Human-readable description |
| `args` | `map[string]ArgDefinition` | ❌ | Workflow argument schema |
| `steps` | `[]WorkflowStep` | ✅ | Sequence of execution steps |

#### Workflow Step Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | `string` | ✅ | Unique step identifier |
| `tool` | `string` | ✅ | Tool name to execute |
| `args` | `map[string]any` | ❌ | Tool arguments (supports templating) |
| `condition` | `WorkflowCondition` | ❌ | Execution condition |
| `store` | `bool` | ❌ | Store result for later steps |
| `allowFailure` | `bool` | ❌ | Continue on failure |
| `outputs` | `map[string]any` | ❌ | Output mappings |
| `description` | `string` | ❌ | Step documentation |

### Service Configuration

**Location**: `services/*.yaml`

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Service
metadata:
  name: my-web-app
  namespace: default
spec:
  args:
    port: 3000
    replicas: 2
    environment: "production"
```

## Configuration Loading

### Loading Order

1. **Defaults**: Built-in default values
2. **Main Config**: `config.yaml` overrides defaults
3. **Resource Files**: Individual resource definitions

### Custom Configuration Path

Use `--config-path` to specify a custom configuration directory:

```bash
muster serve --config-path /etc/muster
muster create service --config-path ./project-config app-name
```

### Environment-Specific Configuration

Create different configuration directories for different environments:

```bash
# Development
muster serve --config-path ~/.config/muster-dev

# Staging
muster serve --config-path ~/.config/muster-staging

# Production
muster serve --config-path /etc/muster-prod
```

## Configuration Validation

### Automatic Validation

Muster validates configuration on startup and when resources are created:

- **Syntax**: YAML syntax validation
- **Schema**: Field types and required values
- **References**: Tool availability and dependencies

### Manual Validation

Check resource availability:

```bash
# Check specific resources
muster check mcpserver kubernetes
muster check workflow deploy-app
```

## Templating

### Template Syntax

Use Go template syntax for dynamic values:

```yaml
args:
  url: "https://{{.environment}}.example.com"
  replicas: "{{.replicas}}"
  config: "{{.baseConfig}}/{{.serviceName}}"
```

### Available Variables

In resource templates, these variables are available:

| Context | Variables | Description |
|---------|-----------|-------------|
| Workflow | `.args.*`, `.stepResults.*` | Workflow args and step outputs |
| Service | `.name`, `.args.*` | Service context |

## CLI Commands

### Available Commands

| Command | Description |
|---------|-------------|
| `muster serve` | Start the muster aggregator server |
| `muster agent` | MCP client for the aggregator server |
| `muster create` | Create resources (service, workflow) |
| `muster get` | Get detailed information about resources |
| `muster list` | List resources |
| `muster start` | Start services or execute workflows |
| `muster stop` | Stop services |
| `muster check` | Check resource availability |
| `muster test` | Execute test scenarios |

### Resource Types

| Resource Type | Create | Get | List | Check | Start |
|---------------|--------|-----|------|-------|-------|
| `service` | ✅ | ✅ | ✅ | ❌ | ✅ |
| `mcpserver` | ❌ | ✅ | ✅ | ✅ | ❌ |
| `workflow` | ✅ | ✅ | ✅ | ✅ | ✅ |
| `workflow-execution` | ❌ | ✅ | ✅ | ❌ | ❌ |

## See Also

- [CLI Reference](cli/) - Command-line interface documentation
- [CRDs Reference](crds.md) - Kubernetes resource specifications
- [MCP Tools Reference](mcp-tools.md) - Available tools and usage
- [API Reference](api/) - HTTP and MCP API documentation
