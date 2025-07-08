// Package api provides the central API layer for muster's Service Locator Pattern.
//
// This package serves as the single point of communication between all muster
// packages, preventing direct inter-package dependencies and enabling clean
// architectural separation. All service functionality is accessed through
// handler interfaces registered with this central API layer.
//
// # Service Locator Pattern
//
// The API package implements the core Service Locator Pattern that is
// **mandatory** for all inter-package communication in muster:
//
//  1. **Handler Interfaces** - Define contracts for each service capability
//     (ServiceRegistryHandler, ServiceManagerHandler, AggregatorHandler, etc.)
//
//  2. **API Implementations** - Thin wrappers that delegate to registered handlers
//     (direct handler access through Get* functions)
//
//  3. **Handler Registry** - Central registry for handler implementations
//     with thread-safe registration and access
//
//  4. **Adapter Pattern** - Service packages provide adapters that implement
//     handler interfaces and register with the API layer
//
// This architecture ensures:
// - **Zero circular dependencies** (API doesn't import internal packages)
// - **Clean separation of concerns** between packages
// - **Enhanced testability** through handler mocking
// - **Runtime flexibility** in handler registration
// - **Independent package development** and refactoring
//
// # Handler Interfaces
//
// ## Core Service Handlers
//   - **ServiceRegistryHandler**: Unified service registry access and discovery
//   - **ServiceManagerHandler**: Service lifecycle management for all service types
//   - **AggregatorHandler**: Tool execution and MCP server aggregation
//   - **ConfigHandler**: Configuration management and runtime settings
//
// ## Specialized Handlers
//   - **ServiceClassManagerHandler**: ServiceClass definition and lifecycle management
//   - **MCPServerManagerHandler**: MCP server definition and process management

//   - **WorkflowHandler**: Multi-step workflow execution and orchestration
//
// # Core Operations
//
// The API layer provides unified access to all muster functionality:
//
// ## Service Management
//   - Service registry operations (register, get, list services)
//   - Service lifecycle management (start, stop, restart)
//   - Service state monitoring and health checking
//   - Dynamic service instance creation and management
//   - Both static services (from configuration) and ServiceClass-based instances
//
// ## ServiceClass Operations
//   - List available ServiceClasses with real-time availability status
//   - Get ServiceClass definitions, args, and lifecycle tool configuration
//   - Check tool availability for ServiceClasses and validate configurations
//   - Create and manage ServiceClass-based service instances with arg validation
//   - ServiceClass instance lifecycle, health monitoring, and event streaming
//
// ## MCP Server Management
//   - MCP server definition management (create, update, delete, validate)
//   - MCP server lifecycle management and health monitoring
//   - Tool aggregation, namespace management, and conflict resolution
//   - Dynamic MCP server registration and tool discovery
//
// ## Service Class System
//   - User-defined service class definition management with operation validation
//   - Service class operation execution with arg validation
//   - Dynamic service class availability checking based on underlying tools
//   - Integration with tool provider system for extensible operations
//   - Service class namespace management and conflict resolution
//
// ## Workflow Management
//   - Workflow definition management (create, update, delete, validate)
//   - Multi-step workflow execution with arg templating
//   - Conditional logic and step dependency management
//   - Tool integration for workflow steps with error handling
//   - Workflow input schema validation and documentation generation
//
// ## Tool Provider System
//   - Tool provider registration and discovery
//   - Tool metadata management and arg validation
//   - Tool execution abstraction for different implementation types
//   - Automatic tool aggregation and namespace management
//
// ## Request/Response Handling
//   - Structured request types for all operations (Create, Update, Validate patterns)
//   - Type-safe arg parsing and validation using ParseRequest
//   - Comprehensive error handling with contextual information
//   - Response mapping for ServiceClass tool integration
//
// # Tool Update Events
//
// The API layer provides a centralized event system for tool availability changes:
//
//	// Subscribe to tool updates
//	api.SubscribeToToolUpdates(mySubscriber)
//
//	// Publish tool update events
//	event := api.ToolUpdateEvent{
//	    Type: "server_registered",
//	    ServerName: "kubernetes",
//	    Tools: []string{"kubectl_get_pods", "kubectl_describe"},
//	    Timestamp: time.Now(),
//	}
//	api.PublishToolUpdateEvent(event)
//
// Event types include:
//   - "server_registered": New MCP server registration
//   - "server_deregistered": MCP server removal
//   - "tools_updated": Tool availability changes
//
// This enables managers to automatically refresh availability when the tool
// landscape changes, supporting real-time reactivity throughout the system.
//
// # API Registration Pattern
//
// **Critical**: All packages must follow the registration pattern:
//
//  1. **Implement Handler Interface** in adapter pattern:
//     ```go
//     type ServiceAdapter struct {
//     service *MyService
//     }
//
//     func (a *ServiceAdapter) SomeOperation(ctx context.Context) error {
//     return a.service.performOperation(ctx)
//     }
//     ```
//
//  2. **Register with API Layer**:
//     ```go
//     func (a *ServiceAdapter) Register() {
//     api.RegisterMyServiceHandler(a)
//     }
//     ```
//
//  3. **Access through API Layer** (never direct imports):
//     ```go
//     handler := api.GetMyServiceHandler()
//     if handler != nil {
//     handler.SomeOperation(ctx)
//     }
//     ```
//
// # Example Usage
//
// ## Service Registration (Service Package)
//
//	// Service adapter implements handler interface
//	type RegistryAdapter struct {
//	    registry *ServiceRegistry
//	}
//
//	func (r *RegistryAdapter) GetService(ctx context.Context, name string) (*ServiceInfo, error) {
//	    return r.registry.Get(name)
//	}
//
//	func (r *RegistryAdapter) Register() {
//	    api.RegisterServiceRegistry(r)
//	}
//
// ## ServiceClass Registration
//
//	type ServiceClassAdapter struct {
//	    manager *ServiceClassManager
//	}
//
//	func (s *ServiceClassAdapter) ListServiceClasses(ctx context.Context) ([]*ServiceClassInfo, error) {
//	    return s.manager.ListServiceClasses(ctx)
//	}
//
//	func (s *ServiceClassAdapter) Register() {
//	    api.RegisterServiceClassManager(s)
//	}
//
// ## Tool Provider Registration
//
//	type MyToolProvider struct {
//	    tools []ToolMetadata
//	}
//
//	func (p *MyToolProvider) GetTools() []ToolMetadata {
//	    return p.tools
//	}
//
//	func (p *MyToolProvider) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*CallToolResult, error) {
//	    // Implementation specific logic
//	    return &CallToolResult{Content: []interface{}{result}, IsError: false}, nil
//	}
//
// ## API Usage (Consumer Package)
//
//	// Access through API layer (correct approach)
//	serviceManager := api.GetServiceManager()
//	if serviceManager != nil {
//	    err := serviceManager.StartService(ctx, "my-service")
//	}
//
//	serviceClassMgr := api.GetServiceClassManager()
//	if serviceClassMgr != nil {
//	    classes, err := serviceClassMgr.ListServiceClasses(ctx)
//	}
//
//	// Execute workflows through convenience functions
//	result, err := api.ExecuteWorkflow(ctx, "deploy-app", args)
//
// ## Request Validation and Parsing
//
//	// Parse and validate request args
//	var req ServiceClassCreateRequest
//	if err := api.ParseRequest(args, &req); err != nil {
//	    return fmt.Errorf("invalid request: %w", err)
//	}
//
//	// Validate without creating
//	var validateReq ServiceClassValidateRequest
//	api.ParseRequest(args, &validateReq)
//	// Perform validation logic...
//
// # Workflow Integration
//
// The API provides convenient functions for workflow operations:
//
//	// Get workflow information and input schemas
//	workflows := api.GetWorkflowInfo()
//
// # Health Check System
//
// The API defines standardized health checking for all components:
//
//	// Health status constants
//	const (
//	    HealthUnknown   = "unknown"    // Status cannot be determined
//	    HealthHealthy   = "healthy"    // Operating normally
//	    HealthDegraded  = "degraded"   // Some issues but functional
//	    HealthUnhealthy = "unhealthy"  // Significant issues
//	    HealthChecking  = "checking"   // Health check in progress
//	)
//
//	// Health check configuration
//	config := HealthCheckConfig{
//	    Enabled:          true,
//	    Interval:         30 * time.Second,
//	    FailureThreshold: 3,
//	    SuccessThreshold: 1,
//	}
//
// # Orchestrator Integration
//
// The API provides orchestrator functionality for unified service operations:
//
//	// Create services through orchestrator
//	orchestrator := api.GetOrchestrator()
//	if orchestrator != nil {
//	    instance, err := orchestrator.CreateServiceClassInstance(ctx, req)
//	}
//
//	// List all services with unified status
//	services, err := orchestrator.ListServices(ctx)
//
// # Thread Safety
//
// All API components are fully thread-safe:
//
//   - Handler registry uses mutex protection for registration/access
//   - Concurrent handler registration and access operations
//   - Thread-safe API method execution across all handlers
//   - Safe concurrent service operations and state management
//   - Tool update event broadcasting with goroutine safety
//   - Request parsing and validation thread safety
//
// # Error Handling
//
// The API layer provides structured error handling:
//
//   - Handler availability checking with nil safety
//   - Service and ServiceClass validation with detailed error messages
//   - Tool execution error propagation with context preservation
//   - Workflow execution error handling
//   - Request parsing validation with field-level error reporting
//   - Comprehensive error context and recovery mechanisms
//
// # Performance Characteristics
//
// The Service Locator Pattern provides:
//
//   - **Minimal overhead**: Thin delegation layer to handlers
//   - **Lazy initialization**: Handlers registered as needed during startup
//   - **Concurrent access**: No bottlenecks in handler access patterns
//   - **Memory efficiency**: Single handler instances shared across requests
//   - **Event efficiency**: Asynchronous tool update broadcasting
//   - **Request efficiency**: JSON-based parsing with minimal allocations
//
// # Testing Support
//
// The API package provides testing utilities:
//
//   - **Mock handler registration**: SetServiceClassManagerForTesting and similar
//   - **Handler isolation**: Independent handler registration per test
//   - **Event testing**: Tool update event validation and mocking
//   - **Request validation testing**: ParseRequest error scenario testing
//
// # Design Principles
//
// 1. **Single Point of Truth**: All inter-package communication through API
// 2. **No Direct Dependencies**: Packages never import each other directly
// 3. **Interface Segregation**: Small, focused handler interfaces
// 4. **Dependency Inversion**: Depend on abstractions, not implementations
// 5. **Open/Closed Principle**: Easy to extend with new handlers
// 6. **Event-Driven Architecture**: Reactive updates through tool events
// 7. **Type Safety**: Strong typing through request/response structures
// 8. **Validation by Default**: All requests validated before processing
//
// **Critical Rule**: ALL inter-package communication MUST go through this API layer.
// Direct imports between internal packages are **forbidden** and violate the
// core architectural principle.
package api
