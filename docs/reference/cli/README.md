# CLI Reference

Complete reference for all Muster command-line interface commands.

## Quick Reference

| Command | Purpose | Common Usage |
|---------|---------|--------------|
| [`muster serve`](serve.md) | Start Muster control plane | `muster serve --port 8080` |
| [`muster agent`](agent.md) | Interactive tool interface | `muster agent --repl` |
| [`muster standalone`](standalone.md) | MCP server mode | `muster standalone` |
| [`muster create`](create.md) | Create resources | `muster create service my-app web-service` |
| [`muster get`](get.md) | Retrieve resources | `muster get service my-app` |
| [`muster list`](list.md) | List resources | `muster list services` |
| [`muster start`](start.md) | Start resources | `muster start service my-app` |
| [`muster stop`](stop.md) | Stop resources | `muster stop service my-app` |
| [`muster check`](check.md) | Check availability | `muster check serviceclass web-service` |
| [`muster test`](test.md) | Run tests | `muster test --scenario basic-crud` |
| [`muster version`](version.md) | Show version info | `muster version` |
| [`muster self-update`](self-update.md) | Update from GitHub | `muster self-update` |

## Command Categories

### Server Operations
Commands for managing the Muster control plane server.

- **[serve](serve.md)** - Start the Muster aggregator server
  ```bash
  muster serve                    # Start with defaults
  muster serve --port 9090        # Custom port
  muster serve --debug            # Debug mode
  ```

### Interactive Interfaces
Commands for interactive tool usage and MCP server modes.

- **[agent](agent.md)** - Interactive MCP client for tool exploration
  ```bash
  muster agent                    # Connect and list tools
  muster agent --repl             # Interactive REPL mode
  muster agent --mcp-server       # Run as MCP server
  ```

- **[standalone](standalone.md)** - Standalone MCP server mode
  ```bash
  muster standalone               # Basic MCP server
  ```

### Utility Commands
Commands for version management and updates.

- **[version](version.md)** - Display version information
  ```bash
  muster version                  # Show current version
  ```

- **[self-update](self-update.md)** - Update to latest version
  ```bash
  muster self-update              # Update from GitHub
  ```

### Resource Management
Commands for creating, retrieving, and managing Muster resources.

- **[create](create.md)** - Create new resources
  ```bash
  muster create serviceclass web-app
  muster create service my-app web-app --image=nginx:latest
  muster create workflow deploy-flow
  ```

- **[get](get.md)** - Retrieve resource details
  ```bash
  muster get service my-app
  muster get workflow deploy-flow
  muster get serviceclass web-app --output yaml
  ```

- **[list](list.md)** - List multiple resources
  ```bash
  muster list service             # All services
  muster list workflow            # All workflows
  muster list mcpserver           # All MCP servers
  ```

### Resource Control
Commands for starting, stopping, and checking resources.

- **[start](start.md)** - Start services and execute workflows
  ```bash
  muster start service my-app
  muster start workflow deploy-flow --env=prod
  ```

- **[stop](stop.md)** - Stop running services
  ```bash
  muster stop service my-app
  ```

- **[check](check.md)** - Check resource availability
  ```bash
  muster check serviceclass web-app
  muster check mcpserver kubernetes
  muster check workflow deploy-flow
  ```

### Testing and Validation
Commands for testing and validating configurations.

- **[test](test.md)** - Execute test scenarios
  ```bash
  muster test                     # Run all tests
  muster test --scenario basic-crud
  muster test --parallel 4        # Parallel execution
  ```

## Global Options

All commands support these global options:

| Option | Short | Description | Default |
|--------|-------|-------------|---------|
| `--config-path` | | Configuration directory path | `~/.config/muster` |
| `--output` | `-o` | Output format (json\|yaml\|table) | `table` |
| `--quiet` | `-q` | Suppress non-essential output | `false` |
| `--help` | `-h` | Show command help | - |
| `--version` | | Show version information | - |

## Configuration

Muster uses configuration files located in `~/.config/muster/` by default:

```
~/.config/muster/
├── config.yaml           # Main configuration
├── mcpservers/           # MCP server definitions
├── workflows/            # Workflow definitions
├── serviceclasses/       # Service class templates
└── services/             # Service instances
```

### Custom Configuration Directory

Use `--config-path` to specify a custom configuration directory:

```bash
muster serve --config-path /etc/muster
muster list service --config-path ./local-config
```

## Resource Types

Muster manages these resource types:

| Resource Type | Description | Examples |
|---------------|-------------|----------|
| **mcpserver** | MCP server configurations | `kubernetes`, `prometheus`, `github` |
| **serviceclass** | Service templates | `web-app`, `database`, `monitoring` |
| **service** | Service instances | `my-web-app`, `prod-db`, `grafana-instance` |
| **workflow** | Workflow definitions | `deploy-app`, `backup-db`, `scale-service` |
| **workflow-execution** | Workflow run history | Execution results and logs |

## Common Usage Patterns

### Getting Started
```bash
# 1. Start Muster
muster serve

# 2. Check what's available
muster list mcpserver
muster list serviceclass

# 3. Create a service
muster create service my-app web-service --replicas=3

# 4. Check service status
muster get service my-app
```

### Resource Management
```bash
# Create resources from templates
muster create serviceclass my-template
muster create service instance-1 my-template

# Monitor and control
muster list service
muster start service instance-1
muster stop service instance-1
```

### Workflow Execution
```bash
# List available workflows
muster list workflow

# Execute with parameters
muster start workflow deploy-app \
  --environment=production \
  --replicas=5 \
  --image=myapp:v1.2.3

# Check execution history
muster list workflow-execution
muster get workflow-execution abc123-def456
```

### Debugging and Testing
```bash
# Interactive exploration
muster agent --repl

# Run tests
muster test --verbose --debug
muster test --scenario service-lifecycle

# Check resource availability
muster check serviceclass web-app
muster check workflow deploy-app
```

## Output Formats

Most commands support multiple output formats:

### Table Format (Default)
```bash
muster list service
# NAME        STATUS    SERVICECLASS    CREATED
# my-app      Running   web-service     2m ago
# my-db       Stopped   database        1h ago
```

### JSON Format
```bash
muster list service --output json
# {
#   "services": [
#     {
#       "name": "my-app",
#       "status": "Running",
#       "serviceClass": "web-service",
#       "created": "2024-01-07T10:00:00Z"
#     }
#   ]
# }
```

### YAML Format
```bash
muster get service my-app --output yaml
# apiVersion: muster.giantswarm.io/v1alpha1
# kind: Service
# metadata:
#   name: my-app
# spec:
#   serviceClass: web-service
#   replicas: 3
```

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `MUSTER_CONFIG_PATH` | Configuration directory | `~/.config/muster` |
| `MUSTER_ENDPOINT` | Aggregator server endpoint | `http://localhost:8080` |
| `MUSTER_LOG_LEVEL` | Log level for commands | `info` |
| `MUSTER_OUTPUT_FORMAT` | Default output format | `table` |

## Error Handling

Common exit codes across commands:

| Code | Meaning |
|------|---------|
| 0 | Command completed successfully |
| 1 | General error or invalid arguments |
| 2 | Resource not found |
| 3 | Configuration error |
| 4 | Connection error (server not running) |
| 130 | Interrupted by signal (Ctrl+C) |

## Prerequisites

Before using most commands:

1. **Start the server**: Run `muster serve` in a separate terminal
2. **Verify connection**: Commands will fail if the aggregator server isn't running
3. **Check configuration**: Ensure `~/.config/muster/config.yaml` exists

## Related Documentation

- **[Configuration Reference](../configuration.md)** - Detailed configuration options
- **[MCP Tools documentation](reference/mcp-tools.md)** - All the core mcp tools
- **[CRD reference](reference/crds.md/)** - Kubernetes Custom Resource definitions
- **[API Reference](../api.md)** - HTTP API and programmatic access
- **[Getting Started](../../getting-started/)** - Setup and initial usage
- **[How-to Guides](../../how-to/)** - Task-oriented guides

## Tips and Best Practices

### Performance
- Use `--quiet` flag in scripts to reduce output
- Use JSON output for programmatic processing
- Consider `--parallel` option for test commands

### Debugging
- Add `--verbose` flag for detailed output
- Use `muster agent --repl` to explore tools interactively
- Check `muster test --debug` for troubleshooting

### Automation
- Use environment variables for consistent configuration
- Leverage `--output json` for script processing
- Consider workflow definitions for complex operations 