// Package services provides the service abstraction layer for muster.
//
// This package defines the core interfaces and types that all services in muster
// must implement. It provides a unified way to manage different types of services
// through a common interface, supporting both static service types and dynamic
// ServiceClass-based service instances.
//
// # Core Concepts
//
// Service: The fundamental unit of work in muster. Each service represents a
// manageable component that can be started, stopped, and monitored. Services
// can be either statically defined or dynamically created from ServiceClass
// definitions.
//
// ServiceRegistry: A thread-safe registry that holds all active services and
// provides methods to query and manage them. Supports both static and dynamic
// service registration.
//
// ServiceState: Represents the current state of a service (unknown, waiting,
// starting, running, stopping, stopped, failed, retrying).
//
// GenericServiceInstance: A runtime-configurable service implementation that
// uses ServiceClass definitions to drive its lifecycle through tool execution.
//
// # Service Architecture
//
// The services package now supports two main service paradigms:
//
// ## Static Services (Traditional)
//   - TypeKubeConnection: Manages Kubernetes cluster connections
//   - TypePortForward: Creates and maintains kubectl port-forward tunnels
//   - TypeMCPServer: Runs Model Context Protocol servers for AI integration
//
// ## ServiceClass-Based Services (Dynamic)
//   - GenericServiceInstance: Runtime-configurable services driven by ServiceClass definitions
//   - Tool-driven lifecycle management through aggregator integration
//   - Template-based parameter handling and response mapping
//   - Configurable health checking and monitoring
//
// # Service Interface
//
// All services must implement the core Service interface:
//
//	type Service interface {
//	    GetLabel() string                    // Unique identifier
//	    GetType() ServiceType                // Service type
//	    GetState() ServiceState              // Current state
//	    GetHealth() HealthStatus             // Health status
//	    GetLastError() error                 // Last error encountered
//	    GetDependencies() []string           // Service dependencies
//	    Start(ctx context.Context) error     // Start the service
//	    Stop(ctx context.Context) error      // Stop the service
//	    Restart(ctx context.Context) error   // Restart the service
//	    SetStateChangeCallback(StateChangeCallback) // State change notifications
//	}
//
// # Extended Interfaces
//
// Services can implement additional interfaces for extended functionality:
//
// HealthChecker: For services that support periodic health checks
//
//	type HealthChecker interface {
//	    CheckHealth(ctx context.Context) (HealthStatus, error)
//	    GetHealthCheckInterval() time.Duration
//	}
//
// ServiceDataProvider: For services that provide runtime data
//
//	type ServiceDataProvider interface {
//	    GetServiceData() map[string]interface{}
//	}
//
// StateUpdater: For external state management
//
//	type StateUpdater interface {
//	    UpdateState(state ServiceState, health HealthStatus, err error)
//	}
//
// # GenericServiceInstance
//
// The GenericServiceInstance provides ServiceClass-driven service management:
//
//   - Loads service behavior from ServiceClass definitions
//   - Executes lifecycle operations through aggregator tools
//   - Supports template-based parameter substitution
//   - Handles response mapping and data extraction
//   - Provides configurable health checking
//   - Manages comprehensive state and event tracking
//
// ## ServiceClass Integration
//
// GenericServiceInstance integrates with the ServiceClass system:
//
//	// Creation through ServiceClass
//	instance := NewGenericServiceInstance(
//	    "service-id",
//	    "service-label",
//	    "serviceclass-name",
//	    toolCaller,
//	    parameters,
//	)
//
//	// Lifecycle managed through ServiceClass tools
//	err := instance.Start(ctx)  // Executes create tool
//	health, err := instance.CheckHealth(ctx)  // Executes health tool
//	err := instance.Stop(ctx)   // Executes delete tool
//
// # Service Lifecycle
//
// ## Traditional Service Lifecycle
// 1. Creation: Service created with static configuration
// 2. Registration: Service registered with ServiceRegistry
// 3. Starting: Service transitions through Stopped → Starting → Running
// 4. Health Monitoring: Optional periodic health checks
// 5. Stopping: Service transitions through Running → Stopping → Stopped
// 6. Failure: Service can transition to Failed state
//
// ## ServiceClass Service Lifecycle
// 1. ServiceClass Loading: ServiceClass definition loaded and validated
// 2. Tool Availability Check: Required tools validated against aggregator
// 3. Instance Creation: GenericServiceInstance created with ServiceClass reference
// 4. Registration: Instance registered with ServiceRegistry
// 5. Tool-Driven Operations: Lifecycle managed through tool execution:
//   - Start: Executes ServiceClass create tool with templated parameters
//   - Health: Periodic execution of ServiceClass health check tool
//   - Stop: Executes ServiceClass delete tool for cleanup
//
// 6. State Management: Comprehensive state tracking with events
// 7. Error Handling: Tool execution errors mapped to service states
//
// # Tool Integration
//
// ServiceClass-based services integrate with the aggregator tool system:
//
//   - Tool Execution: Operations executed through ToolCaller interface
//   - Parameter Templates: Dynamic parameter substitution using template engine
//   - Response Mapping: Structured extraction of data from tool responses
//   - Error Handling: Tool execution errors properly mapped to service states
//
// # State Change Events
//
// Services support state change notifications through callbacks:
//
//	type StateChangeCallback func(
//	    label string,
//	    oldState, newState ServiceState,
//	    health HealthStatus,
//	    err error,
//	)
//
// # Thread Safety
//
// All components in this package are designed for concurrent access:
//
//   - ServiceRegistry is fully thread-safe
//   - GenericServiceInstance uses proper synchronization
//   - Static service implementations maintain thread safety
//   - State updates are atomic and properly synchronized
//
// # API Integration
//
// Following the project's Service Locator Pattern, this package provides
// API adapters for integration with the central API layer:
//
//   - RegistryAdapter: Exposes service registry through API
//   - Proper interface segregation for different access patterns
//   - No direct inter-package dependencies
//
// # Example Usage
//
// ## Static Service Usage
//
//	// Create a registry
//	registry := services.NewRegistry()
//
//	// Create and register a static service
//	service := k8s.NewK8sConnectionService("k8s-mc-prod", "prod-context", kubeMgr)
//	registry.Register(service)
//
//	// Start the service
//	if err := service.Start(ctx); err != nil {
//	    log.Fatal(err)
//	}
//
// ## ServiceClass Service Usage
//
//	// Create a tool caller (typically from aggregator)
//	toolCaller := aggregator.GetToolCaller()
//
//	// Create ServiceClass-based service instance
//	instance := services.NewGenericServiceInstance(
//	    "port-forward-1",
//	    "my-app-port-forward",
//	    "kubernetes_port_forward",  // ServiceClass name
//	    toolCaller,
//	    map[string]interface{}{
//	        "namespace": "default",
//	        "service_name": "my-app",
//	        "local_port": "8080",
//	        "remote_port": "80",
//	    },
//	)
//
//	// Register and start
//	registry.Register(instance)
//	if err := instance.Start(ctx); err != nil {
//	    log.Fatal(err)
//	}
//
//	// Query service state
//	if instance.GetState() == services.StateRunning {
//	    fmt.Println("ServiceClass instance is running")
//	    data := instance.GetServiceData()
//	    fmt.Printf("Service data: %+v\n", data)
//	}
package services
