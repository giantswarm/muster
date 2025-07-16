# Quick Reference

Common command patterns and configuration for everyday Muster usage.

## Essential Commands

### Quick Start (Easiest)
```bash
# All-in-one mode for IDE integration
muster standalone

# All-in-one with debug output  
muster standalone --debug
```

### Advanced Server Management
```bash
# Separate server (for production/multiple clients)
muster serve

# Start with debug logging
muster serve --debug

# Start without output
muster serve --silent

# Use custom config directory
muster serve --config-path /custom/path
```

### MCP Server Operations
```bash
# List all MCP servers
muster list mcpserver

# Get specific server details  
muster get mcpserver <name>

# Check server availability
muster check mcpserver <name>
```

### Service Operations
```bash
# List services
muster list service

# Service lifecycle
muster create service <name> <serviceclass> [--args]
muster start service <name>
muster stop service <name>

# Service information
muster get service <name>
```

### Workflow Operations
```bash
# List workflows
muster list workflow

# Get workflow details
muster get workflow <name>

# Check workflow availability
muster check workflow <name>

# Execute workflow via agent
muster agent --repl
# then: workflow_<name>(arg1="value1")
```

### ServiceClass Operations
```bash
# List serviceclasses
muster list serviceclass

# Get serviceclass details
muster get serviceclass <name>

# Check serviceclass availability  
muster check serviceclass <name>
```

### Agent Operations
```bash
# Standalone mode (easiest - combined serve + agent)
muster standalone

# Interactive REPL mode
muster agent --repl

# MCP server mode for IDE integration (advanced - requires separate muster serve)
muster agent --mcp-server
```

### Testing
```bash
# Run all tests
muster test

# Run specific scenario
muster test --scenario <name>

# Run with debugging
muster test --debug

# Generate API schema
muster test --generate-schema
```

## Configuration Structure

### Main Configuration File
```yaml
# ~/.config/muster/config.yaml
aggregator:
  port: 8090                    # Default: 8090
  host: "localhost"             # Default: localhost  
  transport: "streamable-http"  # Default: streamable-http
  enabled: true                 # Default: true
  musterPrefix: "x"             # Default: "x"
```

### Directory Structure
```
~/.config/muster/
├── config.yaml              # Main configuration
├── mcpservers/              # MCP server definitions
│   └── example.yaml
├── workflows/               # Workflow definitions  
│   └── deploy.yaml
├── serviceclasses/          # ServiceClass definitions
│   └── web-app.yaml
└── services/                # Service instances
    └── my-service.yaml
```

## Common Configuration Examples

### Basic MCP Server
```yaml
# ~/.config/muster/mcpservers/git-tools.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: git-tools
  namespace: default
spec:
  type: localCommand
  command: ["npx", "@modelcontextprotocol/server-git"]
  autoStart: true
  env:
    GIT_ROOT: "/workspace"
    LOG_LEVEL: "info"
  description: "Git tools MCP server"
```

### Basic ServiceClass
```yaml
# ~/.config/muster/serviceclasses/web-app.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: web-application
  namespace: default
spec:
  description: "Web application service"
  args:
    port:
      type: integer
      default: 8080
      description: "Application port"
  serviceConfig:
    lifecycleTools:
      start:
        tool: "start_web_service"
        args:
          port: "{{.port}}"
      stop:
        tool: "stop_web_service"
        args:
          name: "{{.name}}"
```

### Basic Workflow
```yaml
# ~/.config/muster/workflows/deploy.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: deploy-app
  namespace: default
spec:
  name: deploy-app
  description: "Deploy application workflow"
  args:
    app_name:
      type: string
      required: true
  steps:
    - id: create_service
      tool: core_service_create
      args:
        name: "{{.app_name}}"
        serviceClassName: "web-application"
    - id: check_status
      tool: core_service_status
      args:
        name: "{{.app_name}}"
```

## Agent/REPL Commands

### Tool Discovery
```bash
# List all available tools
list_tools()

# Filter tools by pattern
filter_tools(pattern="core_")
filter_tools(pattern="workflow_")

# Get tool information
describe_tool("core_service_create")
```

### Core Tool Usage
```bash
# Service management
call_tool(name="core_service_create", arguments={"name": "my-app", "serviceClassName": "web-app"})
call_tool(name="core_service_list", arguments={})
call_tool(name="core_service_status", arguments={"name": "my-app"})
call_tool(name="core_service_start", arguments={"name": "my-app"})
call_tool(name="core_service_stop", arguments={"name": "my-app"})

# Workflow management  
call_tool(name="core_workflow_list", arguments={})
call_tool(name="core_workflow_create", arguments={"name": "my-workflow", "steps": [...]})
call_tool(name="workflow_my-workflow", arguments={"app_name": "test-app"})

# ServiceClass management
call_tool(name="core_serviceclass_list", arguments={})
call_tool(name="core_serviceclass_get", arguments={"name": "web-app"})
call_tool(name="core_serviceclass_check", arguments={"name": "web-app"})
```

## Output Formats

All list and get commands support output formatting:
```bash
# Table format (default)
muster list service

# JSON format
muster list service --output json

# YAML format  
muster get workflow deploy-app --output yaml

# Quiet mode (minimal output)
muster list service --quiet
``` 