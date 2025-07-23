# Workflow Orchestration Component

## Purpose
Executes multi-step workflows with advanced templating, state tracking, and dynamic tool generation. The workflow engine transforms complex, multi-step platform operations into simple, single-command executions.

## Key Responsibilities
- **Workflow Definition Management**: Parse and validate YAML workflow definitions
- **Dynamic Tool Generation**: Generate `workflow_<name>` execution tools for each defined workflow
- **Step Execution**: Execute workflow steps with dependency management and error handling
- **Template Processing**: Advanced argument templating with variable substitution
- **Execution Tracking**: Persistent execution state and comprehensive history
- **Cross-Tool Integration**: Orchestrate core tools, external MCP tools, and services

## Component Architecture

### Key Files
- `executor.go`: Workflow execution engine with step orchestration
- `execution_tracker.go`: Real-time execution state management
- `execution_storage.go`: Persistent execution history and results
- `api_adapter.go`: API integration for tool access

### Tool Generation Pattern
Each workflow definition automatically generates a corresponding execution tool:

```
.muster/workflows/connect-monitoring.yaml → workflow_connect-monitoring
.muster/workflows/check-cilium-health.yaml → workflow_check-cilium-health
.muster/workflows/login-management-cluster.yaml → workflow_login-management-cluster
```

## Real-World Example: Monitoring Connection Workflow

**Definition** (`.muster/workflows/connect-monitoring.yaml`):
```yaml
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

**Generated Tool Usage**:
```bash
# Single command execution
workflow_connect-monitoring(cluster="my-cluster")

# Automatically handles:
# 1. Teleport authentication to management cluster
# 2. Creates and starts Mimir port-forwarding service
# 3. Teleport authentication to workload cluster
# 4. Tracks execution state throughout
```

## Advanced Features

### **Template Processing**
- **Input Variables**: `{{.input.argName}}` for workflow arguments
- **Step Outputs**: `{{stepId.fieldName}}` for step result chaining
- **Default Values**: Built-in support for argument defaults
- **Type Validation**: Schema validation for all arguments

### **Execution Management**
- **Execution History**: Full audit trail with `core_workflow_execution_list`
- **State Tracking**: Real-time execution progress monitoring
- **Error Recovery**: Configurable retry logic and error handling
- **Resource Cleanup**: Automatic cleanup of temporary resources

### **Tool Integration Patterns**
Workflows seamlessly orchestrate across tool types:

```yaml
steps:
  # Core tool usage
  - id: "create-service"
    tool: "core_service_create"
    
  # External MCP tool usage  
  - id: "kubernetes-action"
    tool: "x_kubernetes_port_forward"
    
  # Conditional execution
  - id: "health-check"
    tool: "core_service_status"
    condition:
      tool: "core_serviceclass_available"
      args:
        name: "monitoring-service"
```

## Integration with Muster Ecosystem

### **Service Management Integration**
Workflows leverage ServiceClasses for complex service orchestration:
- Create service instances with `core_service_create`
- Monitor health with `core_service_status`
- Manage lifecycle with start/stop/restart operations

### **MCP Server Coordination**
Workflows can coordinate multiple external MCP servers:
- Authentication flows (Teleport)
- Infrastructure management (Kubernetes)
- Monitoring setup (Prometheus, Grafana)
- GitOps operations (Flux)

### **Configuration Persistence**
All workflow definitions persist in `.muster/workflows/` enabling:
- Version control of operational procedures
- Team sharing of standardized workflows
- Consistent execution across environments

This architecture transforms complex, error-prone manual procedures into reliable, repeatable, single-command operations that AI agents can execute consistently. 