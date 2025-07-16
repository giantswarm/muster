# Quick Start Guide

## Easiest Way: Standalone Mode (Recommended)

### 1. Install Muster
```bash  
git clone https://github.com/giantswarm/muster.git
cd muster && go install
```

### 2. Connect to Your IDE
Configure your IDE to use Muster in standalone mode:

**Cursor/VSCode settings.json**:
```json
{
  "mcpServers": {
    "muster": {
      "command": "muster",
      "args": ["standalone"]
    }
  }
}
```

**That's it!** Muster will automatically start its server and agent in a single process.

### 3. Test the Connection
Ask your AI agent: "What tools are available through Muster?"

Your agent will use the `list_tools` meta-tool to show all available tools from the aggregator.

## Advanced: Separate Server/Agent Mode

For production use or when you need to see logs:

### 1. Start Muster Server
```bash
muster serve
```

### 2. Configure Agent
**Cursor/VSCode settings.json**:
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

**Benefits**: Visible server logs, multiple MCP clients can connect, production-ready

### 3. Explore Available Tools

Start the interactive agent to understand the **two-layer architecture**:
```bash
muster agent --repl
```

In the REPL, you can explore both layers:

#### Agent Meta-Tools (What AI Agents Use)
The agent exposes 11 meta-tools for accessing the aggregator:

```bash
# Tool Discovery
list_tools()              # List all tools from aggregator
describe_tool(name="core_service_list")  # Get tool details
filter_tools(pattern="core_service_*")   # Filter by pattern
list_core_tools()         # List core Muster tools specifically

# Tool Execution  
call_tool(name="core_service_list", arguments={})  # Execute any tool

# Resource & Prompt Access
list_resources()          # List available resources
get_resource(uri="config://settings")   # Get resource content
list_prompts()           # List available prompts
get_prompt(name="deploy", arguments={}) # Execute prompts
```

#### Aggregator Tools (What Gets Executed)
The aggregator has the actual business logic tools:

```bash
# Configuration Tools (5 tools)
call_tool(name="core_config_get", arguments={})
call_tool(name="core_config_save", arguments={})

# Service Management Tools (9 tools)  
call_tool(name="core_service_list", arguments={})
call_tool(name="core_service_create", arguments={
  "serviceClassName": "service-k8s-connection",
  "name": "my-connection"
})

# ServiceClass Tools (7 tools)
call_tool(name="core_serviceclass_list", arguments={})
call_tool(name="core_serviceclass_available", arguments={
  "name": "service-k8s-connection"
})

# MCP Server Tools (6 tools)
call_tool(name="core_mcpserver_list", arguments={})

# Workflow Tools (9 tools)
call_tool(name="core_workflow_list", arguments={})
call_tool(name="workflow_auth-workflow", arguments={
  "cluster": "my-cluster",
  "profile": "default"
})
```

### 4. Try Real Examples
Based on the current `.muster` configuration:

```bash
# Discover available tools
list_tools()

# Create a Kubernetes connection service
call_tool(
  name="core_service_create",
  arguments={
    "serviceClassName": "service-k8s-connection",
    "name": "my-k8s-connection",
    "args": {
      "cluster_name": "my-cluster",
      "role": "management"
    }
  }
)

# Execute authentication workflow
call_tool(
  name="workflow_auth-workflow", 
  arguments={
    "cluster": "my-cluster"
  }
)

# Check service status
call_tool(
  name="core_service_status",
  arguments={
    "name": "my-k8s-connection"
  }
)
```

## Key Architectural Understanding

### Two Layers, Different Tools

**Layer 1: Agent (`muster agent --mcp-server`)**
- **What AI agents connect to**
- **11 meta-tools**: `list_tools`, `call_tool`, `describe_tool`, etc.
- **Purpose**: Bridge between AI agents and aggregator

**Layer 2: Aggregator (`muster serve`)**  
- **Contains the actual business logic**
- **36+ tools**: `core_service_list`, `workflow_*`, `x_kubernetes_*`, etc.
- **Purpose**: Unified tool execution and service management

### Usage Pattern for AI Agents

AI agents **never** directly call aggregator tools. They always use meta-tools:

```bash
# ✅ Correct: How AI agents work
list_tools()                                    # Discover tools
call_tool(name="core_service_list", arguments={})  # Execute tools

# ❌ Wrong: AI agents can't do this
core_service_list()                             # Doesn't exist at agent layer
```

This pattern enables:
- **Unified access** to all tool types (core, workflow, external)
- **Dynamic discovery** of available capabilities  
- **Consistent interface** regardless of underlying tool source
- **Transparent routing** to appropriate tool handlers 