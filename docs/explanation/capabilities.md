# Core Capabilities of Muster

Muster provides **comprehensive platform management capabilities** through core built-in tools organized into functional categories, plus dynamic workflow execution tools and external tools from your configured MCP servers.

## 🧰 Core Tool Categories

### **Configuration Tools** (5 tools)
System configuration management and aggregator settings:
```bash
# Get current system configuration
core_config_get

# Update aggregator settings (port, host, transport)
core_config_update_aggregator

# Save configuration changes persistently
core_config_save
```

### **MCP Server Tools** (6 tools)
Manage external MCP server definitions and lifecycle:
```bash
# List all configured MCP servers (like kubernetes, prometheus, grafana)
core_mcpserver_list

# Create new MCP server definitions
core_mcpserver_create

# Validate server configurations before deployment
core_mcpserver_validate
```

### **Service Tools**
Manage the lifecycle of static services (aggregator, MCP servers):
```bash
# Discover what's running in your system
core_service_list

# Start, stop, restart services
core_service_start
core_service_stop
core_service_restart

# Monitor service health and status
core_service_status
```

### **Workflow Tools**
Define and execute multi-step orchestrated processes:
```bash
# List available workflows
core_workflow_list

# Create complex multi-step workflows
core_workflow_create

# Track workflow execution history
core_workflow_execution_list
core_workflow_execution_get
```

## 🚀 Dynamic Tool Generation

### **Workflow Execution Tools**
For each workflow you define, Muster automatically generates a `workflow_<name>` execution tool. Based on this instance's configuration:

```bash
# Generated from your actual workflows:
workflow_connect-monitoring           # Connect to Giant Swarm monitoring
workflow_check-cilium-health         # Check Cilium network health
workflow_login-management-cluster    # Login to management cluster
workflow_login-workload-cluster      # Login to workload cluster
workflow_discovery                   # Service discovery workflow
workflow_auth                        # Authentication workflow
```

## 🧠 Intelligent Tool Discovery

Your agent can now discover and use tools dynamically:

```bash
# Discover available tools by category
agent: "What configuration tools are available?"
→ Shows core_config_get, core_config_update_aggregator, etc.

# Find tools by functionality
agent: "I need to manage Kubernetes connections"
→ Discovers a workflow that wires up the relevant tools

# Execute complex operations
agent: "Connect to monitoring in cluster"
→ workflow_connect-monitoring(cluster="foo-bar.k8s.mydomain.com")
```

## 🏗️ Advanced Service Management

### **Multi-Step Workflows**
Orchestrate complex operations with built-in error handling:

**Real Example - Monitoring Connection Workflow:**
```yaml
# From .muster/workflows/connect-monitoring.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: connect-monitoring
spec:
  description: "Connect to monitoring in a Giant Swarm installation"
  args:
    cluster:
      type: "string"
      description: "Cluster domain (e.g., 'my-k8s.my-domain.com')"
      required: true
    localPort:
      type: "string"
      default: "18000"
      description: "Local port for forwarding"
  steps:
    - id: "login-cluster"
      tool: "x_teleport_kube_login"
      args:
        kubeCluster: "{{.input.cluster}}"
    - id: "port-forward-prometheus"
      tool: "x_kubernetes_port_forward"
      args:
        cluster: "{{.input.cluster}}"
        localPort: "{{.input.localPort}}"
```

**Benefits:**
- **Reduce AI costs** - Deterministic execution without re-discovery
- **Faster results** - No need to figure out prerequisites each time
- **Consistent operations** - Same reliable process across team members
- **Error handling** - Built-in failure recovery and cleanup

## 🛡️ Smart Access Control & Context Optimization

### **Tool Filtering**
- **Denylist Protection**: Block destructive tools by default (override with `--yolo`)
- **Context-Aware Loading**: Only load tools when needed to minimize agent context
- **Project-Based Control**: Different tool sets for different projects

## 📊 Real-World Integration Example

**Complete End-to-End Scenario:**
1. **Agent Request**: "I need to check Cilium health in the gazelle installation"
2. **Workflow Discovery**: Finds `workflow_check-cilium-health`
3. **Authentication**: Handles Teleport authentication automatically
4. **Health Check**: Executes comprehensive Cilium health verification
5. **Result Delivery**: Returns structured health status and recommendations

This demonstrates how Muster transforms complex, multi-step platform operations into simple, one-command executions for AI agents while maintaining full control and observability.
