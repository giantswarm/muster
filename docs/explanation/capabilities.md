# Core Capabilities of Muster

Muster provides **comprehensive platform management capabilities** through 36 core built-in tools organized into 5 functional categories, plus dynamic workflow execution tools and external tools from your configured MCP servers.

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

### **Service Tools** (9 tools)
Manage service instances throughout their lifecycle (aggregator, MCP servers, ServiceClass instances):
```bash
# Discover what's running in your system
core_service_list

# Create services from ServiceClass templates
core_service_create

# Start, stop, restart services dynamically
core_service_start
core_service_stop
core_service_restart

# Monitor service health and status
core_service_status
```

### **ServiceClass Tools** (7 tools)
Manage ServiceClass definitions that serve as reusable service templates:
```bash
# List available service templates
core_serviceclass_list

# Create new service templates for common patterns
core_serviceclass_create

# Check if templates have all required tools available
core_serviceclass_available
```

### **Workflow Tools** (9 tools)
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
→ Discovers serviceclass 'service-k8s-connection' and related tools

# Execute complex operations
agent: "Connect to monitoring in cluster"
→ workflow_connect-monitoring(cluster="foo-bar.k8s.mydomain.com")
```

## 🏗️ Advanced Service Management

### **ServiceClass Templates** 
This instance provides reusable service templates:

**Real Example - Kubernetes Connection Service:**
```yaml
# From .muster/serviceclasses/k8s-connection.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: service-k8s-connection
spec:
  description: "Dynamic Kubernetes cluster connections with authentication"
  args:
    cluster_name:
      type: "string"
      required: true
      description: "Name of the Kubernetes cluster"
    role:
      type: "string" 
      required: true
      description: "Role for the connection (management, workload, etc.)"
    auth_provider:
      type: "string"
      default: "teleport"
      description: "Authentication provider"
  serviceConfig:
    lifecycleTools:
      start:
        tool: "api_kubernetes_connect"
        args:
          clusterName: "{{ .cluster_name }}"
          role: "{{ .role }}"
          authProvider: "{{ .auth_provider }}"
      healthCheck:
        tool: "api_kubernetes_connection_status"
        args:
          connectionId: "{{ .service_id }}"
```

**Usage:**
```bash
# Create a managed Kubernetes connection
core_service_create {
  "serviceClassName": "service-k8s-connection",
  "name": "prod-cluster-connection",
  "args": {
    "cluster_name": "production",
    "role": "admin",
    "auth_provider": "teleport"
  }
}
```

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
    - id: "setup-prometheus-access"
      tool: "core_service_create"
      args:
        serviceClassName: "prometheus-port-forward"
        name: "prometheus-port-forward-{{.input.cluster}}"
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

### **Prerequisites Management**
ServiceClasses automatically handle complex prerequisites:
- **Port Forwarding**: Automatically set up when accessing remote services
- **Authentication**: Handle cluster logins and token management
- **Health Checking**: Continuous monitoring of service availability
- **Cleanup**: Automatic resource cleanup when services stop

## 📊 Real-World Integration Example

**Complete End-to-End Scenario:**
1. **Agent Request**: "I need to check Cilium health in the gazelle installation"
2. **Workflow Discovery**: Finds `workflow_check-cilium-health`
3. **Service Creation**: Creates required port-forwarding services
4. **Authentication**: Handles Teleport authentication automatically
5. **Health Check**: Executes comprehensive Cilium health verification
6. **Result Delivery**: Returns structured health status and recommendations
7. **Cleanup**: Automatically cleans up temporary resources

This demonstrates how Muster transforms complex, multi-step platform operations into simple, one-command executions for AI agents while maintaining full control and observability. 