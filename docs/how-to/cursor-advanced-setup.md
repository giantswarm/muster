# Advanced Cursor Setup for Infrastructure Engineering

> **Note**: This guide covers advanced configuration for production environments and complex setups.
> For basic setup, see [AI Agent Quick Start](../getting-started/ai-agent-setup.md) which uses the simpler `muster standalone` mode.

This guide is for scenarios where you need:

- Multiple environment configurations
- Visible server logs for debugging
- Multiple MCP clients connecting to one server
- Production deployment patterns

Optimize Cursor for maximum productivity in infrastructure and platform engineering tasks with Muster.

## Quick Start for Advanced Users

### Basic Advanced Configuration

> **For simple setup**, use `muster standalone` instead. This section shows separate server/agent mode.

```json
// Cursor Settings (Cmd/Ctrl + ,)
{
  "mcpServers": {
    "muster": {
      "command": "muster",
      "args": ["agent", "--mcp-server"]
    }
  }
}
```

**Prerequisites**: Run `muster serve` in a separate terminal first.

## Advanced MCP Server Configurations

### Multiple Muster Instances

Configure different muster instances for different environments:

```json
// ~/.cursor/settings.json
{
  "mcpServers": {
    "muster-dev": {
      "command": "muster",
      "args": ["agent", "--mcp-server", "--config-path", "/path/to/dev/config"]
    },
    "muster-staging": {
      "command": "muster",
      "args": ["agent", "--mcp-server", "--config-path", "/path/to/staging/config"]
    },
    "muster-prod": {
      "command": "muster",
      "args": ["agent", "--mcp-server", "--config-path", "/path/to/prod/config"]
    }
  }
}
```

### Standalone Mode Configuration

For simpler setups, use standalone mode:

```json
{
  "mcpServers": {
    "muster-standalone": {
      "command": "muster",
      "args": ["standalone"]
    }
  }
}
```

## Environment-Specific Configurations

### Development Environment

Create a development configuration directory:

```bash
# Development config at ~/dev-muster/
mkdir -p ~/dev-muster/{mcpservers,workflows,serviceclasses,services}
```

```yaml
# ~/dev-muster/config.yaml
aggregator:
  port: 8090
  host: "localhost"
  enabled: true
```

```yaml
# ~/dev-muster/mcpservers/dev-tools.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: dev-tools
  namespace: default
spec:
  type: localCommand
  command: ["echo", "dev-mock-server"]
  autoStart: true
  description: "Development tools"
```

### Staging Environment

```bash
# Staging config at ~/staging-muster/
mkdir -p ~/staging-muster/{mcpservers,workflows,serviceclasses,services}
```

```yaml
# ~/staging-muster/config.yaml
aggregator:
  port: 8091
  host: "localhost"
  enabled: true
```

### Production Environment

```bash
# Production config at ~/prod-muster/
mkdir -p ~/prod-muster/{mcpservers,workflows,serviceclasses,services}
```

```yaml
# ~/prod-muster/config.yaml
aggregator:
  port: 8092
  host: "localhost"
  enabled: true
```

## Advanced Tool Usage

### Infrastructure Automation Workflows

Create workflows for common infrastructure tasks:

```yaml
# workflows/deploy-infrastructure.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: deploy-infrastructure
  namespace: default
spec:
  name: deploy-infrastructure
  description: "Deploy complete infrastructure stack"
  args:
    environment:
      type: string
      required: true
    app_name:
      type: string
      required: true
  steps:
    - id: create_service
      tool: core_service_create
      args:
        name: "{{.app_name}}-{{.environment}}"
        serviceClassName: "web-application"
    - id: verify_deployment
      tool: core_service_status
      args:
        name: "{{.app_name}}-{{.environment}}"
```

### ServiceClass Templates

Define reusable service templates:

```yaml
# serviceclasses/microservice.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: microservice
  namespace: default
spec:
  description: "Standard microservice deployment"
  args:
    service_name:
      type: string
      required: true
    port:
      type: integer
      default: 8080
  serviceConfig:
    lifecycleTools:
      start:
        tool: "deploy_microservice"
        args:
          name: "{{.service_name}}"
          port: "{{.port}}"
      stop:
        tool: "undeploy_microservice"
        args:
          name: "{{.service_name}}"
```

## Cursor Integration Tips

### Effective AI Prompts

Structure prompts to leverage muster's capabilities:

**Good**: "Create a web service called 'user-api' using the web-application serviceclass"
- AI will use: `core_service_create(name="user-api", serviceClassName="web-application")`

**Good**: "List all running services"
- AI will use: `core_service_list()`

**Good**: "Execute the deploy-app workflow for my-service"
- AI will use: `workflow_deploy-app(app_name="my-service")`

### Workspace Organization

Organize your workspace for effective muster usage:

```
project/
├── .cursor/
│   └── settings.json          # Muster MCP server config
├── muster-config/             # Project-specific muster config
│   ├── config.yaml
│   ├── mcpservers/
│   ├── workflows/
│   ├── serviceclasses/
│   └── services/
└── src/                       # Your application code
```

## Testing and Validation

### Test Your Configuration

Before using with AI, test your muster setup:

```bash
# Test basic connection
muster agent --repl

# In REPL:
list_tools()
core_service_list()
core_workflow_list()
```

### Validate ServiceClasses

```bash
# Check serviceclass availability
muster check serviceclass web-application

# List all serviceclasses
muster list serviceclass
```

### Test Workflows

```bash
# Check workflow availability
muster check workflow deploy-app

# List workflows
muster list workflow
```

## Troubleshooting

### Connection Issues

If Cursor can't connect to muster:

1. **Check muster is running**:

   ```bash
   # Start muster server
   muster serve
   ```

2. **Test agent connectivity**:

   ```bash
   muster agent --repl
   ```

3. **Verify configuration paths**:

   ```bash
   # Check config directory exists
   ls -la ~/.config/muster/
   ```

### Tool Availability Issues

If tools aren't available in Cursor:

1. **Check MCP servers**:

   ```bash
   muster list mcpserver
   ```

2. **Verify tool discovery**:

   ```bash
   muster agent --repl
   # Then: list tools
   ```

### Performance Optimization

For better performance:

1. **Use specific config paths** instead of default
2. **Keep configurations minimal** - only include needed MCP servers
3. **Use standalone mode** for simple setups

## Next Steps

1. **Create project-specific configs** for different codebases
2. **Build custom workflows** for your infrastructure patterns
3. **Set up ServiceClasses** for your common deployment patterns
4. **Test with your team** to establish conventions

For more examples, see the test scenarios in `internal/testing/scenarios/`.