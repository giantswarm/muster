# Muster Reference Documentation

Technical reference for commands, APIs, and configurations. Find exact syntax, parameters, and specifications here.

## Quick Navigation

### Command Line Interface
- **[CLI Reference](cli/)** - Complete command documentation
  - [CLI Overview](cli/README.md) - All commands with quick reference
  - [serve](cli/serve.md) - Start the Muster control plane
  - [agent](cli/agent.md) - Interactive MCP client and REPL
  - [create](cli/create.md) - Create resources (services, workflows, serviceclasses)
  - [get](cli/get.md) - Get detailed resource information
  - [list](cli/list.md) - List multiple resources
  - [start](cli/start.md) - Start services and execute workflows
  - [stop](cli/stop.md) - Stop running services
  - [check](cli/check.md) - Check resource availability
  - [test](cli/test.md) - Execute test scenarios
  - [version](cli/version.md) - Show version info
  - [self-update](cli/self-update.md) - Update binary from GitHub releases

### API Documentation
- **[API Reference](api.md)** - HTTP and MCP API specifications
  - HTTP REST API - Resource management via HTTP
  - MCP Aggregator API - Tool execution via MCP protocol
  - WebSocket/SSE Events - Real-time notifications
  - SDK Examples - Integration code for JavaScript, Python, Go

### Kubernetes Resources
- **[Custom Resource Definitions (CRDs)](crds.md)** - Complete reference for muster's Kubernetes resources
  - [MCPServer](crds.md#mcpserver) - MCP server management and configuration
  - [ServiceClass](crds.md#serviceclass) - Service templates with lifecycle management
  - [Workflow](crds.md#workflow) - Multi-step process definitions
  - [Templating](crds.md#templating) - Dynamic value templating in resources
  - [Best Practices](crds.md#best-practices) - Resource design patterns and guidelines

### MCP Tools Reference
- **[Core MCP Tools](mcp-tools.md)** - Complete reference for AI agents and MCP clients
  - [Configuration Tools](mcp-tools.md#configuration-tools) - System configuration management
  - [MCP Server Tools](mcp-tools.md#mcp-server-tools) - MCP server lifecycle management
  - [Service Tools](mcp-tools.md#service-tools) - Service instance management  
  - [ServiceClass Tools](mcp-tools.md#serviceclass-tools) - ServiceClass definition management
  - [Workflow Tools](mcp-tools.md#workflow-tools) - Workflow definition and execution management
  - [Dynamic Workflow Execution](mcp-tools.md#dynamic-workflow-execution-tools) - `workflow_<name>` tools
  - [External Tools](mcp-tools.md#external-tools) - Tools from connected MCP servers

### Configuration
- **[Configuration Reference](configuration.md)** - Complete system configuration documentation
  - [Main Configuration](configuration.md#main-configuration-file) - Core system settings (aggregator, ports, transport)
  - [Resource Configuration](configuration.md#resource-configuration-files) - MCPServer, ServiceClass, Workflow, and Service definitions
  - [Directory Structure](configuration.md#configuration-directory-structure) - File organization and locations
  - [Configuration Loading](configuration.md#configuration-loading) - Loading order, custom paths, environment-specific setup
  - [Templating](configuration.md#templating) - Dynamic value templating in configurations
  - [Validation & Troubleshooting](configuration.md#configuration-validation) - Config validation and common issues
  - Environment variables and runtime settings
  - Service and workflow configuration schemas

## Key Reference Materials

### CRD Quick Reference
| Resource | Purpose | Example Usage |
|----------|---------|---------------|
| **MCPServer** | Manage MCP tool providers | `kubectl apply -f git-tools-mcpserver.yaml` |
| **ServiceClass** | Service templates | `kubectl get serviceclass postgres-database` |
| **Workflow** | Multi-step processes | `kubectl get workflow deploy-application` |

> **→ See [CRD Reference](crds.md) for complete resource documentation**

### Command Quick Reference
| Command | Purpose | Example |
|---------|---------|---------|
| `muster serve` | Start control plane | `muster serve --port 8080` |
| `muster agent --repl` | Interactive exploration | `muster agent --repl` |
| `muster list service` | List services | `muster list service` |
| `muster create service` | Create service | `muster create service my-app web-service` |
| `muster start workflow` | Execute workflow | `muster start workflow deploy-app --env=prod` |
| `muster test` | Run tests | `muster test --parallel 4` |

### Core Tools Quick Reference
| Tool Category | Common Tools | Purpose |
|---------------|--------------|---------|
| **Configuration** | `core_config_get`, `core_config_save` | System configuration |
| **MCP Servers** | `core_mcpserver_list`, `core_mcpserver_create` | MCP server management |
| **Services** | `core_service_list`, `core_service_start`, `core_service_status` | Service lifecycle |
| **ServiceClasses** | `core_serviceclass_list`, `core_serviceclass_create` | Service templates |
| **Workflows** | `core_workflow_list`, `core_workflow_create`, `workflow_<name>` | Workflow management |

> **→ See [Core MCP Tools Reference](mcp-tools.md) for complete tool documentation**

### API Quick Reference
| Endpoint Type | Example | Purpose |
|---------------|---------|---------|
| **Core Tools** | `POST /mcp {"method": "tools/call", "params": {"name": "core_service_list"}}` | Execute built-in Muster tools |
| **Workflow Tools** | `POST /mcp {"method": "tools/call", "params": {"name": "workflow_login-management-cluster"}}` | Execute workflows |
| MCP Tools | `POST /mcp` | Execute tools via MCP protocol |
| REST Services | `GET /api/v1/services` | List services via HTTP |
| SSE Events | `GET /sse` | Real-time event streaming |
| Configuration | `GET /api/v1/config` | System configuration |

### Global Options
Most commands support these options:
- `--output`, `-o` - Output format (table\|json\|yaml)
- `--config-path` - Custom configuration directory
- `--quiet`, `-q` - Suppress non-essential output

## Common Usage Patterns

### Getting Started
```bash
# 1. Start Muster
muster serve

# 2. Explore interactively
muster agent --repl

# 3. List available resources
muster list serviceclass
muster list workflow
```

### Resource Management
```bash
# Create and manage services
muster create service my-app web-service --image=nginx
muster start service my-app
muster get service my-app
muster stop service my-app
```

### Workflow Execution
```bash
# Execute workflows with parameters
muster start workflow deploy-app \
  --app-name=my-app \
  --environment=production \
  --replicas=3
```

### System Monitoring
```bash
# Check system health
muster check serviceclass web-service
muster check mcpserver kubernetes
muster list service --output json
```

### Interactive Tool Discovery
```bash
# Use the agent REPL to discover and execute core tools
muster agent --repl

# In the REPL:
list tools                    # See all available tools
describe core_service_list    # Get tool documentation
call core_service_list        # Execute tools directly
```

## Resource Types

| Type | Description | CLI Commands | API Endpoints | Kubernetes CRD |
|------|-------------|--------------|---------------|----------------|
| **Service** | Service instances | `create`, `start`, `stop`, `get`, `list` | `/api/v1/services` | - |
| **ServiceClass** | Service templates | `create`, `get`, `list`, `check` | `/api/v1/serviceclasses` | [ServiceClass](crds.md#serviceclass) |
| **Workflow** | Multi-step procedures | `create`, `start`, `get`, `list`, `check` | `/api/v1/workflows` | [Workflow](crds.md#workflow) |
| **MCPServer** | Tool providers | `get`, `list`, `check` | `/api/v1/mcpservers` | [MCPServer](crds.md#mcpserver) |

## Output Formats

All commands support multiple output formats:

### Table Format (Default)
Human-readable tabular output for console use.

### JSON Format
```bash
muster list service --output json
```
Structured data for scripts and programmatic use.

### YAML Format
```bash
muster get serviceclass web-app --output yaml
```
Configuration-friendly format for infrastructure as code.

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `MUSTER_CONFIG_PATH` | Configuration directory | `~/.config/muster` |
| `MUSTER_ENDPOINT` | Aggregator server endpoint | `http://localhost:8080` |
| `MUSTER_LOG_LEVEL` | Logging level | `info` |
| `MUSTER_OUTPUT_FORMAT` | Default output format | `table` |

## Error Codes

Standard exit codes across all commands:

| Code | Meaning |
|------|---------|
| 0 | Success |
| 1 | General error |
| 2 | Resource not found |
| 3 | Configuration error |
| 4 | Connection error |
| 130 | Interrupted (Ctrl+C) |

## Integration Examples

### CI/CD Integration
```bash
# Health check before deployment
muster check serviceclass web-app || exit 1

# Deploy application
muster start workflow deploy-app --env=production

# Verify deployment
muster get service production-app --output json | jq '.status'
```

### Monitoring Scripts
```bash
# Check critical services
for service in web-app database cache; do
  if ! muster check serviceclass "$service" --quiet; then
    echo "WARNING: $service is not available"
  fi
done
```

### Development Automation
```bash
# Start development environment
muster start workflow setup-dev-env --local-storage=./data

# Run tests
muster test --concept serviceclass --parallel 4
```

## Architecture Integration

### MCP Protocol
Muster implements the Model Context Protocol (MCP) for tool integration:
- **Tools**: Executable functions from MCP servers
- **Resources**: Files and data sources
- **Prompts**: Template-based interactions

### Service Locator Pattern
All components communicate through the central API layer:
- No direct inter-package dependencies
- Handler-based service registration
- Thread-safe operation

## Related Documentation

### Getting Started
- [Quick Start](../getting-started/quick-start.md) - Basic setup and first steps
- [AI Agent Setup](../getting-started/ai-agent-setup.md) - IDE integration
- [Platform Setup](../getting-started/platform-setup.md) - Production deployment

### How-to Guides
- [Workflow Creation](../how-to/workflow-creation.md) - Build custom workflows
- [Service Configuration](../how-to/service-configuration.md) - Configure services
- [Troubleshooting](../how-to/troubleshooting.md) - Common issues and solutions

### Architecture
- [System Architecture](../explanation/architecture.md) - How components interact
- [Design Principles](../explanation/design-principles.md) - Why Muster works this way
- [Component Overview](../explanation/components/) - Detailed component docs

### Resource Management
- [Custom Resource Definitions](crds.md) - Complete CRD reference
- [Configuration Examples](../explanation/configuration-examples.md) - Sample configurations

## Support and Feedback

### Getting Help
- Review the [troubleshooting guide](../how-to/troubleshooting.md)
- Check [GitHub issues](https://github.com/giantswarm/muster/issues)
- Use `muster agent --repl` for interactive exploration

### Contributing
- Missing documentation? [Create an issue](https://github.com/giantswarm/muster/issues/new)
- Found an error? Submit a correction via pull request
- Want to improve examples? Contributions welcome! 