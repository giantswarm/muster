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
//     (OrchestratorHandler, ServiceClassManagerHandler, etc.)
//
//  2. **API Implementations** - Thin wrappers that delegate to registered handlers
//     (OrchestratorAPI, ServiceClassAPI, etc.)
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
// # ServiceClass Integration
//
// The API layer provides comprehensive ServiceClass support through:
//
//   - **ServiceClassManagerHandler**: ServiceClass definition management
//   - **Enhanced OrchestratorHandler**: ServiceClass instance lifecycle
//   - **ServiceClass APIs**: Complete ServiceClass operations
//   - **Event Streaming**: Real-time ServiceClass instance events
//   - **Tool Provider Integration**: ServiceClass tool execution
//
// # Handler Interfaces
//
// ## Core Service Handlers
//   - **ServiceRegistryHandler**: Unified service registry access
//   - **OrchestratorHandler**: Enhanced with ServiceClass instance management
//   - **ServiceClassManagerHandler**: ServiceClass definition and tool management
//   - **AggregatorHandler**: Tool execution and MCP server aggregation
//   - **ConfigHandler**: Configuration management
//
// ## Specialized Handlers
//   - **MCPServiceHandler**: MCP server information and tools
//   - **CapabilityHandler**: Capability operations and workflows
//   - **WorkflowHandler**: Workflow execution and management
//
// # ServiceClass Operations
//
// The API layer provides complete ServiceClass functionality:
//
// ## ServiceClass Management
//   - List available ServiceClasses with availability status
//   - Get ServiceClass definitions and metadata
//   - Check tool availability for ServiceClasses
//   - Register/unregister ServiceClass definitions
//
// ## ServiceClass Instance Lifecycle
//   - Create ServiceClass instances with parameters
//   - Delete ServiceClass instances with cleanup
//   - Get ServiceClass instance status and data
//   - List all ServiceClass instances
//   - Subscribe to ServiceClass instance events
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
//     func (a *ServiceAdapter) SomeOperation() error {
//     return a.service.performOperation()
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
//     handler.SomeOperation()
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
//	func (r *RegistryAdapter) Get(label string) (ServiceInfo, bool) {
//	    return r.registry.Get(label)
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
//	func (s *ServiceClassAdapter) ListServiceClasses() []ServiceClassInfo {
//	    return s.manager.ListServiceClasses()
//	}
//
//	func (s *ServiceClassAdapter) Register() {
//	    api.RegisterServiceClassManager(s)
//	}
//
// ## API Usage (Consumer Package)
//
//	// Access through API layer (correct approach)
//	orchestrator := api.GetOrchestrator()
//	if orchestrator != nil {
//	    instance, err := orchestrator.CreateServiceClassInstance(ctx, req)
//	}
//
//	serviceClassMgr := api.GetServiceClassManager()
//	if serviceClassMgr != nil {
//	    classes := serviceClassMgr.ListServiceClasses()
//	}
//
// # ServiceClass Event Streaming
//
// The API provides real-time event streaming for ServiceClass instances:
//
//	events := orchestrator.SubscribeToServiceInstanceEvents()
//	for event := range events {
//	    fmt.Printf("Instance %s: %s -> %s\n",
//	        event.Label, event.OldState, event.NewState)
//	}
//
// # Tool Provider Integration
//
// Handler interfaces that implement ToolProvider expose tools through the API:
//
//   - ServiceClass management tools
//   - Configuration management tools
//   - Capability operation tools
//   - Workflow execution tools
//
// # Testing Support
//
// The Service Locator Pattern enables comprehensive testing:
//
//	// Mock handler for testing
//	mockServiceClass := &mockServiceClassHandler{
//	    definitions: make(map[string]*ServiceClassDefinition),
//	}
//	api.RegisterServiceClassManager(mockServiceClass)
//	defer api.RegisterServiceClassManager(nil)
//
//	// Test API operations
//	orchestrator := api.NewOrchestratorAPI()
//	instance, err := orchestrator.CreateServiceClassInstance(ctx, req)
//
// # Thread Safety
//
// All API components are fully thread-safe:
//
//   - Handler registry uses mutex protection
//   - Concurrent handler registration/access
//   - Thread-safe API method execution
//   - Safe concurrent ServiceClass operations
//
// # Error Handling
//
// The API layer provides structured error handling:
//
//   - Handler availability checking
//   - ServiceClass validation errors
//   - Tool execution error propagation
//   - Instance management error handling
//
// # Performance Characteristics
//
// The Service Locator Pattern provides:
//
//   - **Minimal overhead**: Thin delegation to handlers
//   - **Lazy initialization**: Handlers registered as needed
//   - **Concurrent access**: No bottlenecks in handler access
//   - **Memory efficiency**: Single handler instances shared
//
// # Design Principles
//
// 1. **Single Point of Truth**: All inter-package communication through API
// 2. **No Direct Dependencies**: Packages never import each other directly
// 3. **Interface Segregation**: Small, focused handler interfaces
// 4. **Dependency Inversion**: Depend on abstractions, not implementations
// 5. **Open/Closed Principle**: Easy to extend with new handlers
//
// **Critical Rule**: ALL inter-package communication MUST go through this API layer.
// Direct imports between internal packages are **forbidden** and violate the
// core architectural principle.
package api
