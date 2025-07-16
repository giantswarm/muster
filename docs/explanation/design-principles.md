# Muster Design Principles

## Overview

Muster's architecture is built on a foundation of proven design principles that enable scalability, maintainability, and testability. These principles guide all architectural decisions and development practices within the project.

## Core Architectural Principles

### 1. API Service Locator Pattern

#### Principle Statement
**All inter-package communication MUST go through the central API layer.** This is the foundational architectural principle that governs the entire system design.

#### Problem Statement
Modern software systems face the challenge of coordinating multiple complex components while maintaining:
- Loose coupling for independent development
- Easy testing with dependency injection  
- Clear interfaces for component communication
- Prevention of circular dependencies
- Ability to evolve components independently

#### Solution: Central API Registry
The `internal/api` package acts as a service locator that manages all inter-component communication through:
- **Interface-driven design**: All communication through well-defined contracts
- **Service registration**: Components register their capabilities with the API
- **Service discovery**: Components retrieve dependencies through the API
- **Dependency inversion**: High-level policy doesn't depend on low-level details

#### Implementation Pattern

**Step 1: Define Interface in API**
```go
// internal/api/handlers.go
type ServiceHandler interface {
    CreateService(ctx context.Context, req CreateServiceRequest) (*Service, error)
    GetService(ctx context.Context, name string) (*Service, error)
    StartService(ctx context.Context, name string) error
    StopService(ctx context.Context, name string) error
}
```

**Step 2: Implement Adapter in Service Package**
```go
// internal/services/api_adapter.go
type Adapter struct {
    registry *Registry
    logger   *slog.Logger
}

func (a *Adapter) CreateService(ctx context.Context, req CreateServiceRequest) (*Service, error) {
    service, err := a.registry.CreateService(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("failed to create service: %w", err)
    }
    return service, nil
}

func (a *Adapter) GetService(ctx context.Context, name string) (*Service, error) {
    return a.registry.GetService(ctx, name)
}
```

**Step 3: Register with API**
```go
// internal/services/api_adapter.go
func (a *Adapter) Register() {
    api.RegisterServiceHandler(a)
}

// internal/api/service.go
var serviceHandler ServiceHandler

func RegisterServiceHandler(handler ServiceHandler) {
    serviceHandler = handler
}

func GetServiceHandler() ServiceHandler {
    return serviceHandler
}
```

**Step 4: Consume via API**
```go
// internal/workflow/executor.go
func (e *Executor) startService(ctx context.Context, name string) error {
    handler := api.GetServiceHandler()
    if handler == nil {
        return fmt.Errorf("service handler not available")
    }
    return handler.StartService(ctx, name)
}
```

#### Benefits
- **Independent Development**: Teams can develop components without knowledge of other implementations
- **Testing Simplicity**: Easy mocking and dependency injection for unit tests
- **Clear Boundaries**: Well-defined system boundaries and responsibilities
- **Evolutionary Architecture**: Components can be replaced without affecting others
- **Circular Dependency Prevention**: One-way dependency flow prevents architectural rot

#### Trade-offs
- **Additional Indirection**: Extra layer between components adds complexity
- **Learning Curve**: Developers must understand the pattern to contribute effectively
- **Boilerplate Code**: More code required for simple component interactions
- **Discipline Required**: Pattern must be consistently applied across the codebase

### 2. One-Way Dependency Rule

#### Principle Statement
**All packages can depend on `internal/api`, but `internal/api` depends on NO other internal package.**

#### Architectural Constraint
This creates a clean layered architecture:
```
┌─────────────────────────────────────────┐
│ Application Layer (cmd/)                │
│ ├─ Depends on: internal/api, internal/app │
└─────────────────────────────────────────┘
            │
            ▼
┌─────────────────────────────────────────┐
│ Service Layer (internal/*)               │
│ ├─ aggregator/ → depends on api         │
│ ├─ services/   → depends on api         │
│ ├─ workflow/   → depends on api         │
│ ├─ mcpserver/  → depends on api         │
│ └─ app/        → depends on api         │
└─────────────────────────────────────────┘
            │
            ▼
┌─────────────────────────────────────────┐
│ API Layer (internal/api)                │
│ ├─ Depends on: NO internal packages     │
│ ├─ Provides: Interface definitions      │
│ └─ Manages: Service registry            │
└─────────────────────────────────────────┘
```

#### Benefits
- **Prevents Circular Dependencies**: Impossible to create import cycles
- **Clear Layering**: Obvious architectural layers with defined responsibilities  
- **Independent Testing**: API layer can be tested without any service implementations
- **Modular Design**: Services can be added/removed without affecting the API layer

#### Implementation Guidelines
- **API Package Constraints**: Never import from `internal/aggregator`, `internal/services`, etc.
- **Service Package Guidelines**: Always access other services through `internal/api`
- **Dependency Direction**: Dependencies always point toward the API layer

### 3. Progressive Enhancement Architecture

#### Principle Statement
Start with simple, working solutions and add sophistication incrementally while maintaining backward compatibility.

#### Implementation Strategy

**Layer 1: Basic Functionality**
- Simple MCP server aggregation
- Basic service lifecycle management
- Linear workflow execution

**Layer 2: Enhanced Capabilities**
- Intelligent tool discovery and filtering
- Service dependency management
- Parallel workflow execution

**Layer 3: Advanced Features**
- Dynamic service scaling
- Complex workflow orchestration
- Advanced monitoring and observability

#### Benefits
- **Reduced Complexity**: Start simple and add complexity only when needed
- **User Adoption**: Users can adopt incrementally without overwhelming complexity
- **Risk Mitigation**: Each layer can be validated before adding the next
- **Maintainability**: Simpler foundations are easier to maintain and debug

### 4. Configuration as Code

#### Principle Statement
All system configuration should be declarative, version-controlled, and validated.

#### Implementation Approaches

**Kubernetes-Native Configuration (Production)**
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: prometheus-monitoring
spec:
  description: "Prometheus monitoring service"
  startTool: "prometheus_start"
  parameters:
    - name: "port"
      type: "number"
      default: 9090
    - name: "retention"
      type: "string"
      default: "15d"
```

**Filesystem Configuration (Development)**
```yaml
# ~/.config/muster/serviceclass-prometheus.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: prometheus-monitoring
  namespace: default
spec:
  description: "Prometheus monitoring service"
  args:
    port:
      type: integer
      default: 9090
  serviceConfig:
    lifecycleTools:
      start:
        tool: "prometheus_start"
        args:
          port: "{{.port}}"
```

#### Benefits
- **Version Control**: All configuration changes tracked in git
- **Reproducibility**: Identical deployments across environments
- **Validation**: Schema validation prevents configuration errors
- **GitOps Integration**: Native integration with GitOps workflows

### 5. Testing-First Architecture

#### Principle Statement
The architecture must enable comprehensive testing at all levels, from unit tests to end-to-end scenarios.

#### Testing Strategy

**Unit Testing with Mocks**
```go
func TestWorkflowExecution(t *testing.T) {
    // Mock service handler
    mockHandler := &MockServiceHandler{}
    api.RegisterServiceHandler(mockHandler)
    
    // Configure mock expectations
    mockHandler.On("StartService", "prometheus").Return(nil)
    
    // Test workflow execution
    executor := NewExecutor()
    err := executor.ExecuteWorkflow(ctx, workflow)
    
    assert.NoError(t, err)
    mockHandler.AssertExpectations(t)
}
```

**Integration Testing with BDD Scenarios**
```yaml
# internal/testing/scenarios/service-lifecycle.yaml
name: "Service Lifecycle Management"
description: "Test complete service lifecycle from creation to deletion"
steps:
  - name: "Create service"
    tool: "service_create"
    args:
      name: "test-service"
      serviceClass: "basic-web-server"
    expect:
      status: "success"
      
  - name: "Verify service running"
    tool: "service_get"
    args:
      name: "test-service"
    expect:
      status: "success"
      result.state: "running"
```

#### Benefits
- **Confidence**: Comprehensive test coverage ensures system reliability
- **Regression Prevention**: Automated tests catch breaking changes
- **Documentation**: Tests serve as executable documentation
- **Refactoring Safety**: Tests enable safe refactoring and architectural changes

## Design Patterns and Anti-Patterns

### ✅ Recommended Patterns

#### Adapter Pattern for API Integration
Always use adapters to integrate services with the API layer:
```go
type Adapter struct {
    implementation *ServiceImplementation
}

func (a *Adapter) HandleRequest(ctx context.Context, req Request) Response {
    return a.implementation.ProcessRequest(ctx, req)
}
```

#### Factory Pattern for Dynamic Creation
Use factories for creating dynamic components:
```go
type ToolFactory interface {
    CreateTool(definition ToolDefinition) (Tool, error)
}
```

#### Observer Pattern for Events
Use observers for loose coupling of event handling:
```go
type EventObserver interface {
    OnServiceStarted(service *Service)
    OnServiceStopped(service *Service)
}
```

### ❌ Anti-Patterns to Avoid

#### Direct Service Dependencies
**Never import service packages directly:**
```go
// ❌ WRONG - Direct import
import "muster/internal/services"

func (w *Workflow) startService() {
    services.GetRegistry().StartService("prometheus")
}

// ✅ CORRECT - Through API
func (w *Workflow) startService() {
    handler := api.GetServiceHandler()
    handler.StartService(context.Background(), "prometheus")
}
```

#### Circular Dependencies
**Never create circular imports:**
```go
// ❌ WRONG - services importing workflow
// internal/services/manager.go
import "muster/internal/workflow"

// internal/workflow/executor.go  
import "muster/internal/services"
```

#### Synchronous Blocking Operations
**Never use sleep or blocking operations in tests:**
```go
// ❌ WRONG - Non-deterministic timing
func TestAsyncOperation(t *testing.T) {
    triggerAsyncOperation()
    time.Sleep(100 * time.Millisecond) // Flaky!
    assert.True(t, operationComplete)
}

// ✅ CORRECT - Deterministic waiting
func TestAsyncOperation(t *testing.T) {
    done := make(chan bool)
    onComplete := func() { done <- true }
    
    triggerAsyncOperation(onComplete)
    
    select {
    case <-done:
        assert.True(t, operationComplete)
    case <-time.After(5 * time.Second):
        t.Fatal("Operation timed out")
    }
}
```

## Quality Assurance Principles

### Code Quality Standards

#### Minimum Test Coverage
- **80% minimum coverage** for all new code
- **100% coverage** for critical paths (API layer, core business logic)
- **Integration test coverage** for all user-facing features

#### Error Handling
Always wrap errors with context:
```go
func (s *Service) processRequest(ctx context.Context, req Request) error {
    result, err := s.backend.Process(req)
    if err != nil {
        return fmt.Errorf("failed to process request %s: %w", req.ID, err)
    }
    return nil
}
```

#### Logging Standards
Use structured logging with appropriate levels:
```go
func (s *Service) startService(name string) error {
    s.logger.Info("Starting service", "name", name)
    
    err := s.doStart(name)
    if err != nil {
        s.logger.Error("Failed to start service", "name", name, "error", err)
        return err
    }
    
    s.logger.Info("Service started successfully", "name", name)
    return nil
}
```

### Documentation Standards

#### Interface Documentation
Every exported interface must have comprehensive documentation:
```go
// ServiceHandler manages the lifecycle of service instances.
// 
// Services are long-running processes that provide tools to the MCP aggregator.
// The handler is responsible for creating, starting, stopping, and monitoring
// service instances based on ServiceClass templates.
//
// All methods are safe for concurrent use.
type ServiceHandler interface {
    // CreateService creates a new service instance from a ServiceClass template.
    // Returns an error if the ServiceClass doesn't exist or if creation fails.
    CreateService(ctx context.Context, req CreateServiceRequest) (*Service, error)
    
    // GetService retrieves an existing service by name.
    // Returns nil if the service doesn't exist.
    GetService(ctx context.Context, name string) (*Service, error)
}
```

#### Package Documentation
Every package must have a `doc.go` file explaining its purpose:
```go
// Package services provides service instance lifecycle management for Muster.
//
// This package implements the service management capabilities that allow Muster
// to create, monitor, and manage long-running service processes. Services are
// created from ServiceClass templates and can be started, stopped, and queried
// through the ServiceHandler interface.
//
// Key concepts:
//   - ServiceClass: Template defining service capabilities and parameters
//   - Service: Running instance of a ServiceClass
//   - Registry: Central repository for service instances and their state
//
// The package integrates with the central API through the adapter pattern,
// implementing the ServiceHandler interface defined in internal/api.
package services
```

## Evolution and Maintenance

### Architectural Decision Process

When making architectural decisions:

1. **Document the Context**: What problem are we solving?
2. **Consider Alternatives**: What are the different approaches?
3. **Evaluate Trade-offs**: What are the costs and benefits?
4. **Make the Decision**: Choose the approach and document rationale
5. **Record the Decision**: Create an ADR for future reference

### Refactoring Guidelines

#### Safe Refactoring Process
1. **Ensure Test Coverage**: Add tests before refactoring
2. **Small Changes**: Make incremental changes with tests after each step
3. **Maintain Interfaces**: Keep API contracts stable during refactoring
4. **Update Documentation**: Keep documentation synchronized with changes

#### When to Refactor Architecture
- **Performance Issues**: When current architecture doesn't meet performance requirements
- **Scaling Challenges**: When adding new features becomes difficult
- **Technical Debt**: When maintenance costs exceed development velocity
- **New Requirements**: When new requirements don't fit existing patterns

### Continuous Improvement

#### Metrics for Architectural Health
- **Dependency Violations**: Monitor for API pattern violations
- **Test Coverage**: Maintain high test coverage across all components
- **Code Complexity**: Monitor and reduce cyclomatic complexity
- **Documentation Currency**: Ensure documentation stays up-to-date

#### Regular Architecture Reviews
- **Monthly Reviews**: Assess adherence to design principles
- **Quarterly Planning**: Evaluate architectural roadmap and priorities
- **Annual Assessment**: Comprehensive review of architectural decisions and outcomes

These design principles form the foundation for building a maintainable, scalable, and testable system that can evolve with changing requirements while preserving architectural integrity. 