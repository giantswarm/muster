# AI Agent Integration Guide (Advanced)

> **Looking for the easiest setup?** See [AI Agent Quick Start](ai-agent-setup.md) which uses `muster standalone` for single-command integration.

## What we're doing here

This **advanced guide** walks you through setting up Muster with separate server and agent processes for production environments. This approach provides visible logs, supports multiple MCP clients, and is ideal for complex deployments.

**Use this guide when you need:**

- Visible server logs for debugging
- Multiple IDE/MCP clients connecting to one server
- Production deployment patterns
- Environment-specific configurations

**For simple IDE integration, use `muster standalone` instead.**

## Understanding Muster's Two-Layer Architecture

**Critical Concept**: Before setting up AI integration, understand that Muster operates in two distinct layers:

### Layer 1: Aggregator Server (`muster serve`)

- **Contains**: 36 core tools (`core_service_list`, etc.) + external MCP tools
- **Purpose**: Business logic, tool execution, service management
- **Direct access**: Internal to Muster or via direct MCP connection

### Layer 2: Agent (`muster agent --mcp-server`)

- **Contains**: 11 meta-tools (`list_tools`, `call_tool`, etc.)
- **Purpose**: Bridge between AI agents and aggregator
- **AI agent access**: This is what your IDE connects to

**Your AI agent uses meta-tools to access aggregator functionality.**

## Before we start

### What you'll need

- **OS**: Linux, macOS, or Windows
- **Go**: Version 1.21 or later
- **IDE**: VSCode, Cursor, or another MCP-compatible environment
- **Network**: Ability to run local processes

### What you should know

- Basic JSON configuration
- How to configure extensions in your IDE
- Basic command-line usage
- Understanding of Muster's two-layer architecture

## Getting Muster installed

### Build from source

```bash
# Clone the repo
git clone https://github.com/giantswarm/muster.git
cd muster

# Build and install
go install

# Verify installation
muster version
```

## Setting things up

### Step 1: Configure and Start Muster Server

Muster uses a configuration directory at `.muster/` by default:

```bash
# Create the config directory (optional - created automatically)
mkdir -p .muster/{mcpservers,serviceclasses,workflows}

# Start Muster server (keep this running)
muster serve
```

**Note**: Keep this terminal open - the server needs to stay running for the agent to connect.

### Step 2: Add MCP servers (optional)

You can add MCP servers to extend muster's capabilities:

```yaml
# .muster/mcpservers/filesystem.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: filesystem-tools
  namespace: default
spec:
  type: localCommand
  command: ["npx", "@modelcontextprotocol/server-filesystem", "/workspace"]
  autoStart: true
  description: "Filesystem operations"
```

### Step 3: Test Muster

```bash
# Test interactive mode
muster agent --repl

# In the REPL, try meta-tools:
list tools                                         # Discover all available tools
call core_config_get {}     # Execute core tools
call core_service_list {}   # Execute service tools
call core_mcpserver_list {} # Execute MCP server tools
```

## Connecting your IDE

### VSCode setup

**Configure MCP extension**
Add this to your VSCode settings:

```json
{
  "mcpServers": {
    "muster": {
      "command": "muster",
      "args": ["agent", "--mcp-server"]
    }
  }
}
```

### Cursor setup

**Add Muster as MCP server**

```json
{
  "mcpServers": {
    "muster": {
      "command": "muster",
      "args": ["agent", "--mcp-server"]
    }
  }
}
```

### Other MCP clients

For other tools that support MCP, use the same configuration pattern:

```json
{
  "mcpServers": {
    "muster": {
      "command": "muster",
      "args": ["agent", "--mcp-server"],
      "description": "Muster unified tool interface"
    }
  }
}
```

## What your AI agent can do

Once configured, your AI agent will have access to **11 meta-tools** that provide access to all Muster functionality:

### Agent Meta-Tools (What AI Agents Use)

**Tool Discovery & Management:**

- `list_tools` - Discover all available tools from the aggregator
- `describe_tool` - Get detailed information about any tool
- `filter_tools` - Filter tools by name patterns or descriptions
- `list_core_tools` - List built-in Muster tools specifically

**Tool Execution:**

- `call_tool` - Execute any tool from the aggregator with arguments

**Resource & Prompt Access:**

- `list_resources` - List available resources
- `get_resource` - Retrieve resource content
- `describe_resource` - Get resource details
- `list_prompts` - List available prompts
- `get_prompt` - Execute prompt templates
- `describe_prompt` - Get prompt details

### Aggregator Tools (Accessed via call_tool)

When your AI agent uses `call_tool`, it can execute any of the **36+ aggregator tools**:

**Configuration Management (5 tools):**

- `core_config_get` - Get complete system configuration
- `core_config_get_aggregator` - Get aggregator-specific settings
- `core_config_update_aggregator` - Modify aggregator configuration
- `core_config_save` - Persist configuration changes
- `core_config_reload` - Reload from configuration files

**Service Management (9 tools):**

- `core_service_list` - List all services (static and ServiceClass-based)
- `core_service_create` - Create service instances from ServiceClasses
- `core_service_get` - Get detailed service information
- `core_service_start/stop/restart` - Control service lifecycle
- `core_service_status` - Monitor service health
- `core_service_delete` - Remove ServiceClass instances
- `core_service_validate` - Validate service configurations

**ServiceClass Management (7 tools):**

- `core_serviceclass_list` - List available service templates
- `core_serviceclass_create` - Define new service types
- `core_serviceclass_get` - Get ServiceClass details
- `core_serviceclass_available` - Check template dependencies
- `core_serviceclass_update` - Modify existing templates
- `core_serviceclass_delete` - Remove templates
- `core_serviceclass_validate` - Validate template configurations

**Workflow Orchestration (9 tools):**

- `core_workflow_list` - List available workflows
- `core_workflow_create` - Define multi-step processes
- `core_workflow_get` - Get workflow details
- `core_workflow_available` - Check workflow dependencies
- `core_workflow_update/delete` - Modify or remove workflows
- `core_workflow_validate` - Validate workflow configurations
- `core_workflow_execution_list` - View execution history
- `core_workflow_execution_get` - Get execution details
- `workflow_<name>` - Execute specific workflows (auto-generated)

**MCP Server Management (6 tools):**

- `core_mcpserver_list` - List external tool providers
- `core_mcpserver_create` - Add new MCP servers
- `core_mcpserver_get` - Get server details
- `core_mcpserver_update/delete` - Modify or remove servers
- `core_mcpserver_validate` - Validate server configurations

**External Tools (Variable):**

- Tools from configured MCP servers (e.g., `x_kubernetes_*`, `x_teleport_*`)

## Example AI interactions

Your AI agent can now help you with infrastructure tasks using the correct two-layer pattern:

**"Show me the current system configuration"**

```bash
AI executes: call core_config_get {}
```

**"Create a Kubernetes connection service"**

```bash
AI executes: call core_service_create {
  "serviceClassName": "service-k8s-connection",
  "name": "my-k8s-conn",
  "args": {"cluster_name": "prod", "role": "management"}
}
```

**"List all running services"**

```bash
AI executes: call core_service_list {}
```

**"Execute the authentication workflow"**

```bash
AI executes: call workflow_auth-workflow {
  "cluster": "my-cluster",
  "profile": "default"
})
```

**"Check workflow execution history"**

```bash
AI executes: call core_workflow_execution_list {
  "workflow_name": "auth-workflow"
}
```

**"Discover available tools"**

```bash
AI executes: list tools
```

**"Filter tools for service management"**

```bash
AI executes: filter tools workflow_*
```

## Troubleshooting

### Connection issues

- Ensure `muster serve` is running
- Check that the agent can connect: `muster agent --repl`
- Verify IDE MCP extension is properly installed

### Tool not available

- Check if MCP servers are running: Ask agent to run `call core_mcpserver_list {})`
- Verify tool availability by testing meta-tools: Ask agent to run `list_tools()`
- Check aggregator status: Ask agent to run `call core_service_status {"name": "mcp-aggregator"})`

### Configuration problems

- Check configuration directory: `.muster/`
- Verify YAML syntax in configuration files
- Check muster logs for error messages
- Test configuration: Ask agent to run `call core_config_get {})`

## Next steps

1. **Explore meta-tools**: Use `muster agent --repl` to try all 11 meta-tools
2. **Create ServiceClasses**: Define templates in `.muster/serviceclasses/`
3. **Build workflows**: Automate processes in `.muster/workflows/`
4. **Add MCP servers**: Integrate external tools in `.muster/mcpservers/`

### Real Examples from Current Configuration

Based on your `.muster` setup, you can try:

- **ServiceClasses**: `service-k8s-connection`, `mimir-port-forward`
- **Workflows**: `auth-workflow`, `login-workload-cluster`, `connect-monitoring`
- **MCP Servers**: `kubernetes`, `prometheus`, `grafana`, `teleport`

### Key Usage Patterns for AI Agents

Remember these patterns when working with AI agents:

```bash
# ✅ Correct: How AI agents work with Muster
list tools                                     # Discover tools
call core_service_list {}  # Execute tools
filter tools workflow_*             # Filter tools

# ❌ Wrong: AI agents can't do this
core_service_list()                             # Doesn't exist at agent layer
workflow_auth-workflow()                        # Must use call_tool
```

This two-layer architecture enables:

- **Unified access** to all tool types (core, workflow, external)
- **Dynamic discovery** of available capabilities
- **Consistent interface** regardless of underlying tool source
- **Transparent routing** to appropriate tool handlers

For comprehensive examples, see the test scenarios in `internal/testing/scenarios/`.