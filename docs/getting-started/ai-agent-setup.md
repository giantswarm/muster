# AI Agent Quick Start (2 minutes)

Get Muster working with your AI agent in 2 minutes using the easiest setup method.

## Two Ways to Run Muster

### 🟢 Standalone Mode (Easiest - Recommended)
- **Single command**: `muster standalone` 
- **Perfect for**: Cursor, VSCode, Claude Desktop integration
- **Benefits**: No separate processes, automatic setup, works immediately

### 🟡 Separate Mode (Advanced)  
- **Two commands**: `muster serve` + `muster agent --mcp-server`
- **Perfect for**: Production deployments, multiple MCP clients, debugging
- **Benefits**: Visible logs, can connect multiple IDEs, production-ready

**This guide covers both, starting with the easiest approach.**

## Understanding Muster's Architecture

**Important:** Muster has two layers, and understanding this is key to using it effectively:

1. **Aggregator Server (`muster serve`)**: Provides 36 core tools (`core_service_list`, etc.) + external tools
2. **Agent (`muster agent --mcp-server`)**: Provides 11 meta-tools to access the aggregator (`list_tools`, `call_tool`, etc.)

**Your AI agent connects to the Agent layer and uses meta-tools to access the underlying functionality.**

## Prerequisites
- Cursor, VSCode with MCP extension, or Claude Desktop
- Go 1.21+ installed
- 2 minutes of your time

## Step 1: Install Muster (2 minutes)

### Option A: Download Binary
```bash
# Download latest release
curl -L https://github.com/giantswarm/muster/releases/latest/download/muster-linux-amd64 -o muster
chmod +x muster
sudo mv muster /usr/local/bin/
```

### Option B: Build from Source
```bash
git clone https://github.com/giantswarm/muster.git
cd muster
go build .
sudo mv muster /usr/local/bin/
```

### Verify Installation
```bash
muster version
# Should show version information
```

## Step 2: Configure Your AI Agent (Standalone Mode)

### For Cursor/VSCode
Add to your `settings.json`:

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

### For Claude Desktop
Add to your Claude config:

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

**That's it!** This single configuration automatically handles both the server and agent components.

## Step 3: Test the Connection (1 minute)

### Start a Conversation
Ask your AI agent:
```
What tools are available through Muster?
```

### Expected Response
Your AI agent should list the **11 agent meta-tools** it actually has access to:

**Tool Discovery & Execution:**
- `list_tools` - Discover all available tools from the aggregator
- `describe_tool` - Get detailed information about any tool
- `call_tool` - Execute any tool from the aggregator
- `filter_tools` - Filter tools by name patterns or descriptions
- `list_core_tools` - List built-in Muster tools specifically

**Resource & Prompt Access:**
- `list_resources` - List available resources
- `get_resource` - Retrieve resource content
- `describe_resource` - Get resource details
- `list_prompts` - List available prompts  
- `get_prompt` - Execute prompt templates
- `describe_prompt` - Get prompt details

### Try Tool Discovery
Ask your AI agent:
```
Use list_tools to show me what tools are available in the aggregator
```

Your agent will execute `list_tools()` and show all available tools including:
- **Core Tools**: `core_service_list`, `core_workflow_create`, etc. (36 tools)
- **Workflow Tools**: `workflow_auth-workflow`, `workflow_connect-monitoring`, etc.
- **External Tools**: `x_kubernetes_*`, `x_teleport_*`, etc. (varies by configuration)

### Try Tool Execution
Ask your AI agent:
```
Use call_tool to execute core_service_list and show me current services
```

Your agent will execute:
```json
call_tool(name="core_service_list", arguments={})
```

This demonstrates the key pattern: **AI agents use meta-tools to access aggregator functionality.**

## Step 4: Execute Your First Workflow (Optional)

Ask your AI agent:
```
First use list_tools to find available workflows, then execute one using call_tool
```

Your agent will:
1. Run `list_tools()` to discover workflows like `workflow_auth-workflow`
2. Use `call_tool(name="workflow_auth-workflow", arguments={...})` to execute it
3. Show you the workflow results

## What You've Accomplished

✅ **Muster is connected** to your AI agent  
✅ **Meta-tool access** - your agent can use the 11 agent tools to access everything  
✅ **Tool discovery** - your agent can find and explore all available tools  
✅ **Tool execution** - your agent can execute any aggregator tool via `call_tool`  
✅ **Resource access** - your agent can access resources and prompts

## Alternative: Advanced Separate Mode

If you need visible server logs or want to connect multiple MCP clients:

### Start Muster Server Separately
```bash
# In a terminal (keeps running)
muster serve
```

### Configure Agent Mode
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

**When to use**: Production deployments, debugging, multiple IDEs connecting to one server.

## Key Concepts to Remember

### The Two-Layer Architecture
1. **`muster serve`** (Aggregator): Has the actual tools and business logic
2. **`muster agent --mcp-server`** (Agent): Provides meta-tools to access the aggregator

### How AI Agents Work with Muster
- AI agents **never** directly call `core_service_list`
- AI agents **always** call `call_tool(name="core_service_list", arguments={})`
- This pattern works for all tools: core, workflows, and external MCP tools

### Common Usage Patterns
```bash
# Discover what's available
list_tools()

# Get details about a specific tool  
describe_tool(name="core_service_create")

# Execute any tool
call_tool(name="core_service_create", arguments={...})

# Filter tools by pattern
filter_tools(pattern="core_service_*")
```

## Next Steps

### Learn Core Concepts
- [Two-Layer Architecture](../explanation/architecture.md#two-layer-architecture-server-vs-agent)
- [Tool Integration Patterns](../explanation/architecture.md#tool-integration-pattern)
- [Understanding Workflows](../explanation/workflows.md)

### Try Common Tasks
- [Integrate with Kubernetes](../how-to/kubernetes-integration.md)
- [Create Custom Workflows](../how-to/workflow-creation.md)
- [Set Up Service Monitoring](../how-to/monitoring-setup.md)

### Advanced Configuration
- [Complete AI Agent Integration Guide](ai-agent-integration.md)
- [Configure Multiple MCP Servers](../how-to/mcp-server-management.md)
- [Set Up Production Deployment](../operations/deployment.md)

## Troubleshooting

### Agent Can't Find Muster
**Problem**: "No MCP servers available" or similar

**Solutions**:
1. Check Muster is in PATH: `which muster`
2. Test Muster directly: `muster standalone --help`
3. Check agent configuration file syntax
4. Restart your IDE/agent

### Tools Not Showing
**Problem**: Agent meta-tools not available

**Solutions**:
1. Test meta-tools: Ask agent to run `list_tools()`
2. Check for permission issues
3. Try `muster serve` in a separate terminal first

### Need Help?
- [Complete Troubleshooting Guide](../how-to/troubleshooting.md)
- [Architecture Documentation](../explanation/architecture.md)
- [Agent CLI Reference](../reference/cli/agent.md)
- [GitHub Issues](https://github.com/giantswarm/muster/issues)
- [Community Discussions](https://github.com/giantswarm/muster/discussions) 