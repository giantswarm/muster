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
  # External MCP tool usage
  - id: "kubernetes-action"
    tool: "x_kubernetes_port_forward"

  # Conditional execution
  - id: "health-check"
    tool: "core_service_status"
    args:
      name: "monitoring-service"
```

## Integration with Muster Ecosystem

### **Service Management Integration**
Workflows orchestrate services:
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
