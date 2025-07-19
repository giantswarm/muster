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
```

## Configuration Fields Reference

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

### Example Configurations

#### Minimal Configuration
```yaml
# Uses all defaults
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

#### Production Configuration
```yaml
aggregator:
  port: 80
  host: "0.0.0.0"
  transport: "streamable-http"
  enabled: true
```

## Resource Configuration Files

Resources are stored as individual YAML files in type-specific subdirectories.

### MCPServer Configuration

**Location**: `mcpservers/*.yaml`

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: kubernetes
  namespace: default
spec:
  type: localCommand              # Server execution type
  autoStart: true                 # Auto-start on system init
  toolPrefix: "k8s"              # Optional tool prefix
  command: ["mcp-kubernetes"]     # Command to execute
  env:                           # Environment variables
    KUBECONFIG: "/path/to/config"
    LOG_LEVEL: "info"
  description: "Kubernetes management server"
```

#### MCPServer Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `type` | `string` | ✅ | - | Server type (`localCommand` only currently) |
| `autoStart` | `bool` | ❌ | `false` | Start automatically on system init |
| `toolPrefix` | `string` | ❌ | - | Prefix for all tools from this server |
| `command` | `[]string` | ✅* | - | Command and args (*required for localCommand) |
| `env` | `map[string]string` | ❌ | `{}` | Environment variables |
| `description` | `string` | ❌ | - | Human-readable description |

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