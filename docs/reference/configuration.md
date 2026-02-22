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
├── serviceclasses/          # ServiceClass templates
│   ├── web-app.yaml
│   └── database.yaml
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
| `namespace` | `string` | `"default"` | Kubernetes namespace for discovering MCPServer, ServiceClass, and Workflow CRs |
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
re-authentication is required. This sets the server-side refresh token TTL.

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

> **Important:** Muster uses a rolling refresh token TTL (reset on each token rotation),
> while Dex's `absoluteLifetime` is measured from the original login and does **not**
> reset. If you increase `sessionDuration` beyond Dex's `absoluteLifetime`, the effective
> session will still be limited by Dex. Ensure both values are aligned.

The CLI's `muster auth status` displays an approximate session estimate based on the
default 30-day duration. Custom server-side values are not yet reflected in the CLI
estimate.

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

### ServiceClass Configuration

**Location**: `serviceclasses/*.yaml`

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: web-application
  namespace: default
spec:
  description: "Web application service template"
  args:                          # Argument definitions
    port:
      type: integer
      default: 8080
      description: "Application port"
      required: false
    replicas:
      type: integer
      default: 1
      description: "Number of replicas"
      required: true
  serviceConfig:                 # Service configuration template
    lifecycleTools:
      start:
        tool: "start_web_service"
        args:
          port: "{{.port}}"
          replicas: "{{.replicas}}"
      stop:
        tool: "stop_web_service"
        args:
          name: "{{.name}}"
    healthCheck:
      tool: "check_web_service"
      args:
        port: "{{.port}}"
```

#### ServiceClass Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `description` | `string` | ❌ | Human-readable description |
| `args` | `map[string]ArgDefinition` | ❌ | Argument schema for instantiation |
| `serviceConfig` | `ServiceConfig` | ✅ | Service configuration template |

#### Argument Definition

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | `string` | ✅ | Data type (`string`, `integer`, `boolean`, `number`, `object`, `array`) |
| `required` | `bool` | ❌ | Whether argument must be provided |
| `default` | `any` | ❌ | Default value if not specified |
| `description` | `string` | ❌ | Argument documentation |

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
  serviceClassName: "web-application"
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
muster create service --config-path ./project-config app-name web-app
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
muster check serviceclass web-app
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
| ServiceClass | `.name`, `.args.*` | Service name and arguments |
| Workflow | `.args.*`, `.stepResults.*` | Workflow args and step outputs |
| Service | `.name`, `.args.*`, `.serviceClass.*` | Service context |

## CLI Commands

### Available Commands

| Command | Description |
|---------|-------------|
| `muster serve` | Start the muster aggregator server |
| `muster agent` | MCP client for the aggregator server |
| `muster create` | Create resources (service, serviceclass, workflow) |
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
| `serviceclass` | ✅ | ✅ | ✅ | ✅ | ❌ |
| `mcpserver` | ❌ | ✅ | ✅ | ✅ | ❌ |
| `workflow` | ✅ | ✅ | ✅ | ✅ | ✅ |
| `workflow-execution` | ❌ | ✅ | ✅ | ❌ | ❌ |

## See Also

- [CLI Reference](cli/) - Command-line interface documentation
- [CRDs Reference](crds.md) - Kubernetes resource specifications
- [MCP Tools Reference](mcp-tools.md) - Available tools and usage
- [API Reference](api/) - HTTP and MCP API documentation 
