# Service Orchestration and Workflow Management

Deep dive into how Muster orchestrates services and manages complex workflows for platform automation.

## Overview

Muster's orchestration capabilities provide sophisticated automation for complex platform operations. The system combines service lifecycle management with workflow execution to create powerful automation patterns that can handle everything from simple deployments to complex multi-stage platform operations.

## Orchestration Architecture

### Two-Tier Orchestration Model

Muster implements a two-tier orchestration model that separates concerns between service management and workflow execution:

```mermaid
graph TB
    subgraph "Workflow Tier"
        WE[Workflow Engine]
        WD[Workflow Definitions]
        WT[Workflow Templates]
        WS[Workflow Scheduler]
    end

    subgraph "Service Tier"
        SM[Service Manager]
        SI[Service Instances]
        SL[Service Lifecycle]
    end

    subgraph "Execution Layer"
        MCPAgg[MCP Aggregator]
        ExtTools[External Tools]
        CoreTools[Core Tools]
    end

    WE --> SM
    WE --> MCPAgg
    SM --> SI
    WS --> WE
    WD --> WT

    MCPAgg --> ExtTools
    MCPAgg --> CoreTools

    SI --> SL
```

**Workflow Tier Benefits:**
- **Complex Logic**: Handle conditional execution, loops, and error recovery
- **Cross-Service Coordination**: Orchestrate multiple services and external systems
- **Template Reuse**: Create reusable workflow patterns
- **Event-Driven**: React to system events and triggers

**Service Tier Benefits:**
- **Lifecycle Management**: Handle start, stop, restart, and health monitoring
- **Resource Management**: Manage compute, storage, and network resources
- **Dependency Resolution**: Automatically handle service dependencies
- **State Persistence**: Maintain service state across restarts

## Service Orchestration

### Service Instance Management

Service instances are created with specific parameters:

```mermaid
sequenceDiagram
    participant U as User/Workflow
    participant SM as Service Manager
    participant MCPAgg as MCP Aggregator
    participant K8s as Kubernetes MCP

    U->>SM: Create Service Instance
    SM->>SM: Validate Parameters
    SM->>SM: Resolve Dependencies
    SM->>MCPAgg: Execute Start Tool
    MCPAgg->>K8s: Deploy to Kubernetes
    K8s->>MCPAgg: Deployment Status
    MCPAgg->>SM: Tool Result
    SM->>SM: Update Service State
    SM->>U: Service Instance Created
```

### Dependency Resolution

Muster automatically resolves and manages service dependencies:

```go
type DependencyResolver struct {
    graph      *DependencyGraph
    resolver   *ServiceResolver
    healthChecker *HealthChecker
}

func (r *DependencyResolver) ResolveDependencies(service *ServiceInstance) error {
    // Build dependency graph
    deps := r.graph.GetDependencies(service.Name)

    // Topological sort for startup order
    startupOrder := r.graph.TopologicalSort(deps)

    // Start dependencies in order
    for _, dep := range startupOrder {
        if err := r.startDependency(dep); err != nil {
            return fmt.Errorf("failed to start dependency %s: %w", dep.Name, err)
        }

        // Wait for health check
        if err := r.healthChecker.WaitForHealthy(dep, 5*time.Minute); err != nil {
            return fmt.Errorf("dependency %s failed health check: %w", dep.Name, err)
        }
    }

    return nil
}
```

## Workflow Management

### Workflow Execution Engine

The workflow execution engine handles complex orchestration logic:

```go
type WorkflowExecutor struct {
    stepExecutor    *StepExecutor
    dependencyGraph *DependencyGraph
    templateEngine  *TemplateEngine
    errorHandler    *ErrorHandler
    stateManager    *StateManager
}

func (e *WorkflowExecutor) ExecuteWorkflow(ctx context.Context, workflow *Workflow, args map[string]interface{}) (*ExecutionResult, error) {
    // Create execution context
    execCtx := &ExecutionContext{
        WorkflowID: workflow.Name,
        Args:       args,
        State:      make(map[string]interface{}),
        StartTime:  time.Now(),
    }

    // Build step dependency graph
    stepGraph, err := e.dependencyGraph.BuildStepGraph(workflow.Steps)
    if err != nil {
        return nil, fmt.Errorf("failed to build dependency graph: %w", err)
    }

    // Execute steps in topological order with parallelism
    execution := &WorkflowExecution{
        Context:   execCtx,
        Workflow:  workflow,
        StepGraph: stepGraph,
        Results:   make(map[string]*StepResult),
    }

    return e.executeStepsWithDependencies(ctx, execution)
}
```

### Advanced Execution Features

#### Conditional Execution

Steps can be conditionally executed based on parameters or previous step results:

```yaml
# Simple condition
- id: production_only_step
  condition: "{{eq .environment \"production\"}}"
  tool: production_specific_tool

# Complex condition with multiple checks
- id: complex_condition_step
  condition: |
    {{and
      (eq .environment "production")
      (gt .replicas 1)
      (eq .previous_step_result.status "success")
    }}
  tool: conditional_tool
```

#### Error Handling and Recovery

```yaml
# Step-level error handling
- id: risky_operation
  tool: potentially_failing_tool
  retry:
    attempts: 3
    delay: "30s"
    backoff: "exponential"
  on_failure:
    action: "continue"  # or "stop", "rollback"

# Workflow-level error handling
error_handling:
  strategy: "rollback"
  notification:
    on_failure: true
    channels: ["slack", "email"]
```

## Template System

### Parameter Templating

Muster uses a powerful template system for dynamic parameter substitution:

```yaml
# Basic parameter substitution
args:
  app_name: "my-app"
  image: "{{.app_name}}:{{.version}}"

# Conditional logic
database_url: |
  {{if .database_enabled}}
  postgres://{{.app_name}}-db:5432/{{.app_name}}
  {{else}}
  sqlite:///tmp/{{.app_name}}.db
  {{end}}

# Loops and iteration
environments:
  {{range .target_environments}}
  - name: "{{.}}"
    replicas: {{if eq . "production"}}5{{else}}2{{end}}
  {{end}}

# Function helpers
current_time: "{{now.Format \"2006-01-02T15:04:05Z\"}}"
random_suffix: "{{.app_name}}-{{randomString 8}}"
```

### Advanced Template Functions

```go
// Custom template functions available in workflows
var TemplateFuncs = template.FuncMap{
    // String functions
    "upper":    strings.ToUpper,
    "lower":    strings.ToLower,
    "replace":  strings.ReplaceAll,
    "contains": strings.Contains,

    // Math functions
    "add": func(a, b int) int { return a + b },
    "sub": func(a, b int) int { return a - b },
    "mul": func(a, b int) int { return a * b },

    // Time functions
    "now":       time.Now,
    "timeAdd":   func(d time.Duration) time.Time { return time.Now().Add(d) },
    "timeFormat": func(t time.Time, layout string) string { return t.Format(layout) },

    // Random functions
    "randomString": generateRandomString,
    "randomInt":    rand.Intn,

    // Environment functions
    "env":     os.Getenv,
    "envWith": os.LookupEnv,

    // JSON functions
    "toJson":   toJSON,
    "fromJson": fromJSON,

    // Base64 functions
    "base64encode": base64.StdEncoding.EncodeToString,
    "base64decode": base64.StdEncoding.DecodeString,
}
```

## Event-Driven Orchestration

### Event System

Muster supports event-driven orchestration for reactive automation:

```yaml
# Event-triggered workflow
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: auto-scale-response
spec:
  triggers:
    - type: metric_threshold
      source: prometheus
      query: "avg(cpu_usage) > 80"
      duration: "5m"

    - type: service_event
      source: kubernetes
      event_type: "pod_oom_killed"

  steps:
    - id: analyze_load
      tool: x_monitoring_analyze_load

    - id: scale_service
      condition: "{{gt .load_analysis.recommended_replicas .current_replicas}}"
      tool: core_service_scale
      args:
        name: "{{.triggered_service}}"
        replicas: "{{.load_analysis.recommended_replicas}}"
```

### Service Lifecycle Events

Services emit lifecycle events that can trigger workflows:

```go
type ServiceEvent struct {
    Type        EventType `json:"type"`
    ServiceName string    `json:"serviceName"`
    Timestamp   time.Time `json:"timestamp"`
    Details     EventDetails `json:"details"`
}

type EventType string

const (
    ServiceCreated    EventType = "service.created"
    ServiceStarted    EventType = "service.started"
    ServiceStopped    EventType = "service.stopped"
    ServiceFailed     EventType = "service.failed"
    ServiceHealthy    EventType = "service.healthy"
    ServiceUnhealthy  EventType = "service.unhealthy"
    ServiceScaled     EventType = "service.scaled"
    ServiceUpdated    EventType = "service.updated"
)
```

## Monitoring and Observability

### Orchestration Metrics

Comprehensive metrics for monitoring orchestration performance:

```prometheus
# Workflow execution metrics
muster_workflow_executions_total{workflow_name, status}
muster_workflow_duration_seconds{workflow_name}
muster_workflow_step_duration_seconds{workflow_name, step_id}
muster_workflow_active_executions{workflow_name}

# Service orchestration metrics
muster_service_lifecycle_events_total{service_name, event_type}
muster_service_dependency_resolution_duration_seconds
muster_service_health_check_duration_seconds{service_name}
muster_service_startup_duration_seconds{service_name}

# Error and retry metrics
muster_workflow_step_retries_total{workflow_name, step_id}
muster_workflow_failures_total{workflow_name, failure_type}
muster_service_startup_failures_total{service_class}
```

### Execution Tracing

Distributed tracing for complex workflow executions:

```go
type ExecutionTrace struct {
    TraceID      string                 `json:"traceId"`
    WorkflowName string                 `json:"workflowName"`
    StartTime    time.Time              `json:"startTime"`
    Duration     time.Duration          `json:"duration"`
    Steps        []StepTrace            `json:"steps"`
    Services     []ServiceTrace         `json:"services"`
    Events       []Event                `json:"events"`
}

type StepTrace struct {
    StepID       string               `json:"stepId"`
    StartTime    time.Time            `json:"startTime"`
    Duration     time.Duration        `json:"duration"`
    Tool         string               `json:"tool"`
    Status       ExecutionStatus      `json:"status"`
    Dependencies []string             `json:"dependencies"`
    Retries      int                  `json:"retries"`
    Error        string               `json:"error,omitempty"`
}
```

## Best Practices

### Service Design

1. **Single Responsibility**: Each service should have a clear, focused purpose
2. **Stateless Design**: Prefer stateless services with external state storage
3. **Health Checks**: Implement comprehensive health checks
4. **Graceful Shutdown**: Handle shutdown signals properly
5. **Resource Limits**: Define appropriate resource requests and limits

### Workflow Design

1. **Idempotent Steps**: Design steps to be safely retryable
2. **Clear Dependencies**: Explicitly define step dependencies
3. **Error Handling**: Plan for failure scenarios and recovery
4. **Parameterization**: Use parameters for reusability
5. **Documentation**: Document workflow purpose and usage

## Related Documentation

- [System Architecture](architecture.md) - Overall system design
- [MCP Aggregation](mcp-aggregation.md) - Tool aggregation details
- [Workflow Creation](../how-to/workflow-creation.md) - Practical workflow creation
- [Monitoring](../operations/monitoring.md) - Observability setup
