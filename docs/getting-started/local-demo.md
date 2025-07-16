# Local Demo (2 minutes)

Experience Muster's core concepts with minimal setup and understand the two-layer architecture.

## Prerequisites
- Go 1.21+ installed
- 2 minutes of time
- No other dependencies!

## Step 1: Quick Install
```bash
git clone https://github.com/giantswarm/muster.git
cd muster
go install
```

## Step 2: Start Muster
```bash
# Start the aggregator server
muster serve &

# Start interactive agent  
muster agent --repl
```

## Step 3: Understanding Muster's Architecture (2 minutes)

**Key Concept**: Muster has two layers with different tool sets. Understanding this is crucial.

### Layer 1: Agent Meta-Tools (What You Use)

In the REPL, you have access to **11 meta-tools** for interacting with the aggregator:

#### Tool Discovery & Management
```bash
# See all available tools from the aggregator
list_tools()

# Get detailed information about any tool
describe_tool(name="core_service_list")

# Filter tools by pattern
filter_tools(pattern="core_service_*")

# List core Muster tools specifically
list_core_tools()
```

#### Tool Execution
```bash
# Execute any tool from the aggregator
call_tool(name="core_config_get", arguments={})

# Execute service management tools
call_tool(name="core_service_list", arguments={})

# Execute workflows
call_tool(name="workflow_auth-workflow", arguments={
  "cluster": "my-cluster"
})
```

#### Resource & Prompt Access
```bash
# List available resources
list_resources()

# Get resource content
get_resource(uri="config://muster.yaml")

# List available prompts
list_prompts()

# Execute prompts
get_prompt(name="deploy-template", arguments={})

# Get detailed resource/prompt information
describe_resource(uri="config://settings")
describe_prompt(name="deployment")
```

### Layer 2: Aggregator Tools (What Gets Executed)

When you use `call_tool`, you're accessing the aggregator's tools:

#### Core Business Logic (36 tools)
```bash
# Configuration management
call_tool(name="core_config_get", arguments={})
call_tool(name="core_config_get_aggregator", arguments={})

# Service management  
call_tool(name="core_service_list", arguments={})
call_tool(name="core_service_status", arguments={"name": "mcp-aggregator"})

# ServiceClass templates
call_tool(name="core_serviceclass_list", arguments={})
call_tool(name="core_serviceclass_available", arguments={
  "name": "service-k8s-connection"
})

# Workflow orchestration
call_tool(name="core_workflow_list", arguments={})
call_tool(name="core_workflow_execution_list", arguments={})

# MCP server management
call_tool(name="core_mcpserver_list", arguments={})
```

#### Dynamic & External Tools
```bash
# Auto-generated workflow execution tools
call_tool(name="workflow_auth-workflow", arguments={
  "cluster": "my-cluster",
  "profile": "default"
})

# External MCP server tools (varies by configuration)
call_tool(name="x_kubernetes_list_pods", arguments={
  "namespace": "default"
})
```

## What You've Experienced

✅ **Two-Layer Architecture** - Understood agent vs aggregator separation  
✅ **Meta-Tool Interface** - Used the 11 agent tools to access everything  
✅ **Tool Discovery** - Found available tools using `list_tools` and `filter_tools`  
✅ **Tool Execution** - Executed aggregator tools via `call_tool`  
✅ **Resource Management** - Accessed resources and prompts  
✅ **Dynamic Capabilities** - Saw how workflows and external tools integrate  

## Architecture Summary

You've now experienced Muster's **two distinct tool layers**:

### Agent Layer (11 meta-tools)
What you directly interact with:
- **Discovery**: `list_tools`, `describe_tool`, `filter_tools`, `list_core_tools`
- **Execution**: `call_tool`
- **Resources**: `list_resources`, `get_resource`, `describe_resource`
- **Prompts**: `list_prompts`, `get_prompt`, `describe_prompt`

### Aggregator Layer (36+ tools)
What gets executed via `call_tool`:
- **Core Tools** (36): `core_config_*`, `core_service_*`, `core_workflow_*`, etc.
- **Workflow Tools** (dynamic): `workflow_auth-workflow`, `workflow_connect-monitoring`, etc.
- **External Tools** (variable): `x_kubernetes_*`, `x_teleport_*`, etc.

### Usage Pattern
```bash
# ✅ How it works
list_tools()                                # Discover available tools
call_tool(name="core_service_list", arguments={})  # Execute tools

# ❌ This doesn't exist at the agent layer
core_service_list()                         # Not available
```

## Real Examples from Your Configuration

Based on the current `.muster` setup, try these examples:

```bash
# Explore available ServiceClasses
call_tool(name="core_serviceclass_list", arguments={})

# Check what workflows are available
filter_tools(pattern="workflow_*")

# Execute the authentication workflow
call_tool(name="workflow_auth-workflow", arguments={
  "cluster": "my-cluster", 
  "profile": "default"
})

# Create a Kubernetes connection service
call_tool(name="core_service_create", arguments={
  "serviceClassName": "service-k8s-connection",
  "name": "demo-connection",
  "args": {
    "cluster_name": "demo-cluster",
    "role": "management"
  }
})
```

## Next Steps

1. **Connect Your IDE**: Use [ai-agent-setup.md](ai-agent-setup.md) for AI agent integration
2. **Follow Platform Setup**: [platform-setup.md](platform-setup.md) for real infrastructure
3. **Create ServiceClasses**: Define your own service templates in `.muster/serviceclasses/`
4. **Build Workflows**: Chain operations for automation in `.muster/workflows/`
5. **Add MCP Servers**: Integrate with external tools in `.muster/mcpservers/`

## Key Takeaway

**Remember the separation**: AI agents use meta-tools to access aggregator functionality. This pattern enables unified access to core tools, workflows, and external MCP servers through a consistent interface.

For comprehensive examples, explore the test scenarios in `internal/testing/scenarios/`. 