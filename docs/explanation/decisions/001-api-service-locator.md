# ADR-001: API Service Locator Pattern

## Status
Accepted

## Context

### Problem Statement
Muster coordinates multiple complex components (aggregator, services, workflows, MCP servers) that need to interact with each other. Without a clear architectural pattern, this leads to:

- **Tight Coupling**: Components directly importing and depending on each other
- **Circular Dependencies**: Import cycles between packages making the code unmaintainable
- **Testing Difficulty**: Hard to mock dependencies and test components in isolation
- **Architectural Drift**: No clear boundaries between components, leading to spaghetti code
- **Scaling Challenges**: Adding new components requires changes throughout the codebase

### Requirements
- Loose coupling for independent development and testing
- Clear interfaces for component communication
- Prevention of circular dependencies
- Easy dependency injection for testing
- Ability to evolve components independently
- Clear system boundaries and responsibilities

### Constraints
- Must work with Go's package system and import model
- Should not significantly impact performance
- Must be easy for developers to understand and follow
- Should integrate cleanly with testing frameworks

## Decision

We will implement a **Central API Service Locator Pattern** where:

1. **All interfaces are defined in `internal/api/handlers.go`**
2. **All inter-component communication goes through the API package**
3. **Components register themselves with the API during initialization**
4. **No component imports any other internal component directly**
5. **The API package depends on NO other internal package**

### Architecture Overview

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

### Implementation Pattern

**Step 1: Define Interface in API Package**
```go
// internal/api/handlers.go
type ServiceHandler interface {
    CreateService(ctx context.Context, req CreateServiceRequest) (*Service, error)
    GetService(ctx context.Context, name string) (*Service, error)
    StartService(ctx context.Context, name string) error
    StopService(ctx context.Context, name string) error
    ListServices(ctx context.Context, filter *ServiceFilter) ([]*Service, error)
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

// Implement all other interface methods...
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

## Consequences

### Positive

#### Development Benefits
- **Independent Development**: Teams can develop components without knowledge of other implementations
- **Clear Boundaries**: Well-defined system boundaries and responsibilities
- **Modular Design**: Components can be added/removed without affecting the API layer
- **Interface-First Design**: Forces thinking about contracts before implementation

#### Testing Benefits
- **Easy Mocking**: Simple dependency injection for unit tests
- **Component Isolation**: Each component can be tested independently
- **Integration Testing**: Clear interfaces for integration test setup
- **Test Doubles**: Easy to create test implementations

#### Maintenance Benefits
- **Circular Dependency Prevention**: Impossible to create import cycles
- **Evolutionary Architecture**: Components can be replaced without affecting others
- **Refactoring Safety**: Clear contracts enable safe refactoring
- **Documentation**: Interfaces serve as API documentation

### Negative

#### Complexity Costs
- **Additional Indirection**: Extra layer between components adds complexity
- **Boilerplate Code**: More code required for simple component interactions
- **Learning Curve**: Developers must understand the pattern to contribute effectively

#### Performance Considerations
- **Runtime Dispatch**: Interface calls have slight overhead vs direct calls
- **Memory Usage**: Additional objects and indirection use more memory
- **Debugging**: More layers to step through during debugging

#### Development Overhead
- **Discipline Required**: Pattern must be consistently applied across the codebase
- **Interface Design**: Requires careful design of component interfaces
- **Registration Order**: Service registration order must be managed

### Risk Mitigation

#### For Complexity
- **Documentation**: Comprehensive documentation of the pattern and its usage
- **Examples**: Clear examples for each type of integration
- **Tooling**: Linting rules to enforce pattern compliance

#### For Performance
- **Benchmarking**: Regular performance testing to ensure acceptable overhead
- **Optimization**: Profile-guided optimization where needed
- **Caching**: Cache interface lookups where appropriate

#### For Development
- **Templates**: Code generation templates for new components
- **Guidelines**: Clear development guidelines and best practices
- **Reviews**: Code review process to ensure pattern compliance

## Implementation Guidelines

### For Component Authors

#### Define Clear Interfaces
```go
// Good: Clear, focused interface
type ServiceHandler interface {
    CreateService(ctx context.Context, req CreateServiceRequest) (*Service, error)
    GetService(ctx context.Context, name string) (*Service, error)
}

// Bad: Overly broad interface
type ServiceHandler interface {
    CreateService(ctx context.Context, req CreateServiceRequest) (*Service, error)
    GetService(ctx context.Context, name string) (*Service, error)
    ProcessMetrics(metrics []Metric) error  // Unrelated responsibility
    SendNotification(msg string) error     // Unrelated responsibility
}
```

#### Implement Adapter Pattern
```go
// internal/mycomponent/api_adapter.go
type Adapter struct {
    implementation *MyComponentImplementation
    logger        *slog.Logger
}

func (a *Adapter) HandleRequest(ctx context.Context, req Request) (Response, error) {
    // Validate request
    if err := req.Validate(); err != nil {
        return Response{}, fmt.Errorf("invalid request: %w", err)
    }
    
    // Call implementation
    result, err := a.implementation.ProcessRequest(ctx, req)
    if err != nil {
        a.logger.Error("Request processing failed", "error", err, "request_id", req.ID)
        return Response{}, fmt.Errorf("processing failed: %w", err)
    }
    
    return result, nil
}
```

### For Interface Designers

#### Single Responsibility
Each interface should have a single, clear responsibility:
```go
// Good: Single responsibility
type ServiceHandler interface {
    CreateService(ctx context.Context, req CreateServiceRequest) (*Service, error)
    GetService(ctx context.Context, name string) (*Service, error)
}

type WorkflowHandler interface {
    ExecuteWorkflow(ctx context.Context, req WorkflowRequest) (*WorkflowResult, error)
    GetWorkflowStatus(ctx context.Context, id string) (*WorkflowStatus, error)
}
```

#### Context and Error Handling
Always include context and proper error handling:
```go
type MyHandler interface {
    // Always include context as first parameter
    ProcessRequest(ctx context.Context, req Request) (Response, error)
    
    // Return meaningful errors
    GetItem(ctx context.Context, id string) (*Item, error) // Returns nil, ErrNotFound if not found
}
```

## Testing Strategy

### Unit Testing with Mocks
```go
func TestWorkflowExecution(t *testing.T) {
    // Create mock service handler
    mockServiceHandler := &MockServiceHandler{}
    api.RegisterServiceHandler(mockServiceHandler)
    
    // Set up expectations
    mockServiceHandler.On("StartService", mock.Anything, "prometheus").Return(nil)
    
    // Test the workflow
    executor := NewWorkflowExecutor()
    err := executor.ExecuteWorkflow(ctx, workflowDef)
    
    assert.NoError(t, err)
    mockServiceHandler.AssertExpectations(t)
}
```

### Integration Testing
```go
func TestServiceIntegration(t *testing.T) {
    // Use real implementations for integration tests
    serviceRegistry := services.NewRegistry()
    serviceAdapter := &services.Adapter{Registry: serviceRegistry}
    api.RegisterServiceHandler(serviceAdapter)
    
    workflowExecutor := workflow.NewExecutor()
    
    // Test actual integration
    result, err := workflowExecutor.ExecuteWorkflow(ctx, workflowDef)
    assert.NoError(t, err)
    assert.NotNil(t, result)
}
```

## Evolution Strategy

### Adding New Components
1. **Define Interface**: Add interface to `internal/api/handlers.go`
2. **Implement Registration**: Add registration functions to `internal/api/`
3. **Create Adapter**: Implement adapter in new component package
4. **Update Bootstrap**: Register component in application startup

### Modifying Existing Interfaces
1. **Backward Compatibility**: Add new methods without changing existing ones
2. **Deprecation Process**: Mark old methods as deprecated with clear migration path
3. **Version Interfaces**: Create versioned interfaces if breaking changes are necessary
4. **Migration Tools**: Provide tools or scripts for large-scale migrations

### Performance Optimization
1. **Benchmark First**: Establish baseline performance metrics
2. **Profile Regularly**: Use Go's profiling tools to identify bottlenecks
3. **Cache Judiciously**: Cache interface lookups where appropriate
4. **Optimize Gradually**: Make incremental improvements based on profiling data

## Monitoring and Compliance

### Architectural Compliance
- **Linting Rules**: Go linting rules to prevent direct component imports
- **Import Analysis**: Tools to analyze and visualize package dependencies
- **CI Checks**: Automated checks for pattern compliance in CI/CD pipeline

### Performance Monitoring
- **Interface Call Metrics**: Track frequency and latency of interface calls
- **Memory Usage**: Monitor memory usage patterns for interface overhead
- **Performance Regression**: Alert on performance degradation

### Code Quality
- **Interface Documentation**: Ensure all interfaces have comprehensive documentation
- **Example Code**: Maintain up-to-date examples for each integration pattern
- **Best Practices**: Regular updates to development guidelines

## Related Decisions
- [ADR-002: CRD Migration Strategy](002-crd-migration.md) - Builds on this API pattern
- [ADR-003: Testing Framework Architecture](003-testing-framework.md) - Leverages this pattern for testing
- [ADR-004: Configuration Management](004-configuration-management.md) - Uses this pattern for config distribution

## References
- [Dependency Inversion Principle](https://en.wikipedia.org/wiki/Dependency_inversion_principle)
- [Service Locator Pattern](https://martinfowler.com/articles/injection.html#UsingAServiceLocator)
- [Clean Architecture](https://blog.cleancoder.com/uncle-bob/2012/08/13/the-clean-architecture.html)
- [Go Interfaces](https://go.dev/doc/effective_go#interfaces)

This ADR establishes the foundational architectural pattern that enables all other design decisions in the Muster project, providing a scalable and maintainable approach to component interaction. 