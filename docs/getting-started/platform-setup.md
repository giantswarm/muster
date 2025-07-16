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
mkdir -p .muster/{mcpservers,serviceclasses,workflows}

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
list_tools()                                # Discover available tools
list_core_tools()                          # List core Muster tools

# Test core functionality (aggregator layer via meta-tools)
call_tool(name="core_config_get", arguments={})          # Check system config
call_tool(name="core_mcpserver_list", arguments={})      # List MCP servers
call_tool(name="core_serviceclass_list", arguments={})   # List ServiceClasses
```

## Step 2: Configure Infrastructure Tools (5 minutes)

### Add an MCP Server
Create an MCP server configuration file:

```yaml
# .muster/mcpservers/example-tools.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: example-tools
  namespace: default
spec:
  type: localCommand
  command: ["echo", "mock-mcp-server"]
  autoStart: true
  description: "Example MCP server for demo"
```

### For Real Kubernetes Integration
If you have `mcp-kubernetes` installed:

```yaml
# .muster/mcpservers/kubernetes.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: kubernetes
  namespace: default
spec:
  type: localCommand
  command: ["mcp-kubernetes"]
  autoStart: true
  env:
    KUBECONFIG: "/path/to/your/kubeconfig"
  description: "Kubernetes management tools"
```

### Verify MCP Server Registration
```bash
# Using meta-tools to check registration
muster agent --repl

# In REPL:
call_tool(name="core_mcpserver_list", arguments={})
call_tool(name="core_mcpserver_get", arguments={"name": "example-tools"})
```

## Step 3: Create Your First ServiceClass (3 minutes)

### Define a Kubernetes Connection Service
Based on the current `.muster` configuration pattern:

```yaml
# .muster/serviceclasses/web-app.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: web-application
  namespace: default
spec:
  description: "Deploys a web application with Kubernetes"
  args:
    image:
      type: string
      required: true
      description: "Container image to deploy"
    replicas:
      type: integer
      default: 3
      description: "Number of replicas"
    namespace:
      type: string
      default: "default"
      description: "Kubernetes namespace"
  serviceConfig:
    lifecycleTools:
      start:
        tool: "api_kubernetes_create_deployment"
        args:
          image: "{{ .image }}"
          replicas: "{{ .replicas }}"
          namespace: "{{ .namespace }}"
          name: "{{ .name }}"
        outputs:
          deploymentName: "name"
          status: "status"
      stop:
        tool: "api_kubernetes_delete_deployment"
        args:
          name: "{{ .deploymentName }}"
          namespace: "{{ .namespace }}"
      status:
        tool: "api_kubernetes_get_deployment_status"
        args:
          name: "{{ .deploymentName }}"
          namespace: "{{ .namespace }}"
    healthCheck:
      enabled: true
      interval: "30s"
      failureThreshold: 3
```

### Test ServiceClass Availability
```bash
# Using meta-tools in REPL
call_tool(name="core_serviceclass_available", arguments={"name": "web-application"})
call_tool(name="core_serviceclass_list", arguments={})
```

## Step 4: Create and Execute a Workflow (2 minutes)

### Define a Deployment Workflow
Following the pattern from current `.muster/workflows/`:

```yaml
# .muster/workflows/deploy.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: deploy-app
  namespace: default
spec:
  name: deploy-app
  description: "Deploy application with health checks"
  args:
    app_name:
      type: string
      required: true
      description: "Name of the application to deploy"
    image:
      type: string
      required: true
      description: "Container image to deploy"
    namespace:
      type: string
      default: "default"
      description: "Kubernetes namespace"
  steps:
    - id: create_service
      tool: core_service_create
      description: "Create the application service instance"
      args:
        name: "{{ .app_name }}"
        serviceClassName: "web-application"
        args:
          image: "{{ .image }}"
          namespace: "{{ .namespace }}"
        persist: true
        autoStart: true
      outputs:
        serviceName: "{{ .app_name }}"
    - id: start_service
      tool: core_service_start
      description: "Start the application service"
      args:
        name: "{{ create_service.serviceName }}"
    - id: verify_deployment
      tool: core_service_status
      description: "Verify service is healthy"
      args:
        name: "{{ create_service.serviceName }}"
      store: true
```

### Execute the Workflow
```bash
# Start interactive agent
muster agent --repl

# In the REPL, execute using the meta-tool pattern:
call_tool(name="workflow_deploy-app", arguments={
  "app_name": "my-app",
  "image": "nginx:latest", 
  "namespace": "default"
})

# Check workflow execution status
call_tool(name="core_workflow_execution_list", arguments={"workflow_name": "deploy-app"})
```

## Step 5: Connect Your IDE

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

**Service Management (9 tools):**
- `core_service_list` - List all services
- `core_service_create` - Create service instances from ServiceClasses
- `core_service_start/stop/restart` - Control service lifecycle
- `core_service_status` - Monitor service health

**ServiceClass Management (7 tools):**
- `core_serviceclass_list` - List available service templates
- `core_serviceclass_create` - Define new service types
- `core_serviceclass_available` - Check template dependencies

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
call_tool(name="core_service_create", arguments={
  "serviceClassName": "web-application",
  "name": "my-service",
  "args": {"image": "nginx:latest"}
})

# AI checks results
call_tool(name="core_service_status", arguments={"name": "my-service"})
```

## Next Steps

1. **Add Real MCP Servers**: Configure actual infrastructure tools (Kubernetes, Prometheus, etc.)
2. **Create More ServiceClasses**: Define templates for databases, monitoring, networking
3. **Build Complex Workflows**: Chain multiple operations with conditional logic
4. **Explore Testing**: Use `muster test` to validate configurations

### Real-World Examples
Based on the current `.muster` configuration, you already have examples for:
- **ServiceClasses**: `service-k8s-connection`, `mimir-port-forward`
- **Workflows**: `auth-workflow`, `login-workload-cluster`, `connect-monitoring`
- **MCP Servers**: `kubernetes`, `prometheus`, `grafana`, `teleport`

### Understanding the Architecture
**Remember**: AI assistants use the 11 meta-tools to access the 36+ aggregator tools. This separation enables:
- **Unified access** to all tool types (core, workflow, external)
- **Dynamic discovery** of capabilities
- **Consistent interface** regardless of tool source
- **Transparent routing** to appropriate handlers

For more examples, see the test scenarios in `internal/testing/scenarios/`. 