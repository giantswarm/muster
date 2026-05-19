# Platform Engineering Quick Start (15 minutes)

Set up Muster for infrastructure management and workflow orchestration.

## Prerequisites

- Go 1.21+ installed
- 15 minutes focused time

## Step 1: Installation & Basic Setup (5 minutes)

### Install Muster

```bash
# Clone and build from source
git clone https://github.com/giantswarm/muster.git
cd muster
go install
```

### Initialize Configuration

```bash
# Create basic configuration directory
mkdir -p .muster/{mcpservers,workflows}

# Initialize basic config (optional - will be created automatically)
cat > .muster/config.yaml << EOF
aggregator:
  port: 8090
  host: localhost
  transport: streamable-http
  enabled: true
EOF

# Start the server
muster serve &

# Test the setup using the agent REPL
muster agent --repl
```

In the REPL, test the **two-layer architecture**:

```bash
# Test meta-tools (agent layer)
list tools                                # Discover available tools
list core_tools                          # List core Muster tools

# Test core functionality (aggregator layer via meta-tools)
call core_config_get {}          # Check system config
call core_mcpserver_list {}      # List MCP servers
```

## Step 2: Configure Infrastructure Tools (5 minutes)

### Add an MCP Server

Create an MCP server configuration file:

```yaml
# filesystem-server.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: filesystem-tools
  namespace: default
spec:
  description: "File system operations"
  toolPrefix: "fs"
  type: sdtio
  autoStart: true
  command: ["npx", "@modelcontextprotocol/server-filesystem", "/workspace"]
  env:
    DEBUG: "1"
```

### For Real Kubernetes Integration

If you have `mcp-kubernetes` installed:

```yaml
# streamable-http-server.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: api-server
  namespace: default
spec:
  description: "External API tools"
  toolPrefix: "api"
  type: streamable-http
  url: "https://api.example.com/mcp"
  timeout: 60
  headers:
    Authorization: "Bearer your-token"
```

### Verify MCP Server Registration

```bash
# Using meta-tools to check registration
muster agent --repl

# In REPL:
call core_mcpserver_list {}
call core_mcpserver_get {"name": "example-tools"}
```

## Step 3: Connect Your IDE

### Configure Cursor/VSCode

Add to your IDE settings:

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

Now your AI assistant can use **Muster's two-layer architecture**:

### Agent Layer (What AI Assistants Use)

Your AI assistant gets access to **11 meta-tools**:

**Tool Discovery & Management:**

- `list_tools` - Discover all available tools from aggregator
- `describe_tool` - Get detailed tool information
- `filter_tools` - Filter tools by name/description patterns
- `list_core_tools` - List built-in Muster tools specifically

**Tool Execution:**

- `call_tool` - Execute any aggregator tool with arguments

**Resource & Prompt Access:**

- `list_resources` - List available resources
- `get_resource` - Retrieve resource content
- `describe_resource` - Get resource details
- `list_prompts` - List available prompts
- `get_prompt` - Execute prompt templates
- `describe_prompt` - Get prompt details

### Aggregator Layer (What Gets Executed via call_tool)

The aggregator provides **36+ core tools** plus dynamic capabilities:

**Configuration Management (5 tools):**

- `core_config_get` - Get system configuration
- `core_config_save` - Save configuration changes
- `core_config_update_aggregator` - Modify aggregator settings

**Service Management:**

- `core_service_list` - List all services
- `core_service_start/stop/restart` - Control service lifecycle
- `core_service_status` - Monitor service health

**Workflow Orchestration (9 tools):**

- `core_workflow_list` - List available workflows
- `core_workflow_create` - Define multi-step processes
- `workflow_<name>` - Execute specific workflows (auto-generated)
- `core_workflow_execution_list` - View execution history

**MCP Server Management (6 tools):**

- `core_mcpserver_list` - List external tool providers
- `core_mcpserver_create` - Add new MCP servers
- `core_mcpserver_start/stop` - Control MCP server lifecycle

### AI Assistant Usage Pattern

Your AI assistant will use this pattern:

```bash
# AI discovers available tools
list_tools()

# AI executes aggregator tools via meta-tool
call_tool(name="core_service_status", arguments={"name": "my-service"})
```

## Next Steps

1. **Add Real MCP Servers**: Configure actual infrastructure tools (Kubernetes, Prometheus, etc.)
2. **Build Complex Workflows**: Chain multiple operations with conditional logic
3. **Explore Testing**: Use `muster test` to validate configurations

### Real-World Examples

Based on the current `.muster` configuration, you already have examples for:

- **Workflows**: `auth-workflow`, `login-workload-cluster`, `connect-monitoring`
- **MCP Servers**: `kubernetes`, `prometheus`, `grafana`

### Understanding the Architecture

**Remember**: AI assistants use the 11 meta-tools to access the 36+ aggregator tools. This separation enables:

- **Unified access** to all tool types (core, workflow, external)
- **Dynamic discovery** of capabilities
- **Consistent interface** regardless of tool source
- **Transparent routing** to appropriate handlers

For more examples, see the test scenarios in `internal/testing/scenarios/`.
