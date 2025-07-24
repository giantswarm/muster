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
list tools

# Get detailed information about any tool
describe tool core_service_list

# Filter tools by pattern
filter tools core_service_*

# List core Muster tools specifically
list core-tools
```

#### Tool Execution

```bash
# Execute any tool from the aggregator
call core_config_get {}

# Execute service management tools
call core_service_list {}

# Execute workflows
call workflow_auth-workflow {
  "cluster": "my-cluster"
}
```

#### Resource & Prompt Access

```bash
# List available resources
list resources

# Get resource content
get config://muster.yaml

# List available prompts
list prompts

# Execute prompts
prompt deploy-template {}

# Get detailed resource/prompt information
describe resource config://settings
describe prompt deployment
```

### Layer 2: Aggregator Tools (What Gets Executed)

When you use `call <tool>`, you're accessing the aggregator's tools:

#### Core Business Logic (36 tools)

```bash
# Configuration management
call core_config_get {}
call core_config_get_aggregator {}

# Service management
call core_service_list {})
call core_service_status {"name": "mcp-aggregator"}

# ServiceClass templates
call core_serviceclass_list {}
call core_serviceclass_available {
  "name": "service-k8s-connection"
}

# Workflow orchestration
call core_workflow_list {}
call core_workflow_execution_list {}

# MCP server management
call core_mcpserver_list {}
```

#### Dynamic & External Tools
```bash
# Auto-generated workflow execution tools
call workflow_auth-workflow {
  "cluster": "my-cluster",
  "profile": "default"
}

# External MCP server tools (varies by configuration)
call x_kubernetes_list_pods {
  "namespace": "default"
}
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
call core_serviceclass_list {}

# Check what workflows are available
filter tools workflow_*

# Execute the authentication workflow
call workflow_auth-workflow {
  "cluster": "my-cluster",
  "profile": "default"
}

# Create a Kubernetes connection service
call core_service_create {
  "serviceClassName": "service-k8s-connection",
  "name": "demo-connection",
  "args": {
    "cluster_name": "demo-cluster",
    "role": "management"
  }
}
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