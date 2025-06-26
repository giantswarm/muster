// Package orchestrator provides the core service orchestration functionality for muster.
//
// The orchestrator is responsible for managing the lifecycle of all services in muster,
// including both static services (Kubernetes connections, port forwards, MCP servers)
// and dynamic ServiceClass-based service instances. It ensures services are started
// in the correct dependency order and handles automatic recovery when dependencies fail.
//
// # Architecture
//
// The orchestrator has been enhanced with ServiceClass support and now uses:
//
//   - **Service Registry Pattern**: Unified management of static and dynamic services
//   - **Dependency Graph**: Complex dependency relationships across service types
//   - **ServiceClass Integration**: Dynamic service creation and lifecycle management
//   - **Event-Driven Updates**: Real-time state and health monitoring
//   - **API Service Locator**: Clean integration through the central API layer
//   - **Tool Caller Interface**: Integration with aggregator for tool execution
//
// # Service Types
//
// The orchestrator manages both traditional and ServiceClass-based services:
//
// ## Static Services (Traditional)
//   - **K8s Connections**: Establish and maintain connections to Kubernetes clusters
//   - **Port Forwards**: Create kubectl port-forward tunnels to cluster services
//   - **MCP Servers**: Run Model Context Protocol servers for AI assistant integration
//
// ## ServiceClass-Based Services (Dynamic)
//   - **ServiceClass Instances**: Runtime-configurable services created from YAML definitions
//   - **Tool-Driven Lifecycle**: Operations executed through aggregator tool integration
//   - **Template-Based Configuration**: Dynamic parameter substitution and mapping
//   - **Event Streaming**: Real-time instance state and lifecycle events
//   - **Persistence Support**: Optional persistence of service instance definitions
//
// # ServiceClass Integration
//
// The orchestrator provides comprehensive ServiceClass instance management:
//
//   - **CreateServiceClassInstance**: Creates new instances from ServiceClass definitions
//   - **DeleteServiceClassInstance**: Cleanup and lifecycle management
//   - **GetServiceClassInstance**: Instance status and data retrieval
//   - **ListServiceClassInstances**: Full instance inventory
//   - **Event Subscription**: Real-time instance lifecycle events
//
// ## ServiceClass Instance Lifecycle
//
//  1. **ServiceClass Validation**: Verify ServiceClass exists and is available
//  2. **Tool Availability Check**: Ensure required tools are accessible through aggregator
//  3. **Instance Creation**: Create GenericServiceInstance with ServiceClass reference
//  4. **Parameter Validation**: Validate parameters against ServiceClass schema
//  5. **Tool Execution**: Execute ServiceClass create tool with templated parameters
//  6. **Registration**: Register instance with unified service registry
//  7. **Health Monitoring**: Periodic health checks using ServiceClass health tools
//  8. **Event Propagation**: Broadcast instance state changes and lifecycle events
//  9. **Persistence**: Optional persistence of instance definitions to YAML files
//
// # Enhanced Dependency Management
//
// The dependency system now supports complex relationships:
//
// ## Traditional Dependencies
//  1. **K8s connections** (foundation - no dependencies)
//  2. **Port forwards** (depend on K8s connections)
//  3. **MCP servers** (may depend on port forwards)
//
// ## ServiceClass Dependencies
//   - **YAML-Defined Dependencies**: Specified in ServiceClass definitions
//   - **Runtime Dependencies**: Added during instance creation
//   - **Cross-Type Dependencies**: ServiceClass instances can depend on static services
//   - **Tool Dependencies**: ServiceClass availability depends on aggregator tools
//
// ## Dependency Features
//   - **Cascade Operations**: Failure in one service stops all dependents
//   - **Dependency Restoration**: Automatic restart of dependents when dependencies recover
//   - **Circular Dependency Detection**: Prevents invalid dependency configurations
//   - **Dependency Validation**: Ensures all dependencies exist and are valid
//
// # Health Monitoring
//
// Enhanced health monitoring supports both service types:
//
// ## Static Service Health
//   - Periodic health checks for traditional service types
//   - Automatic recovery attempts for failed services
//   - Cascade failure handling for dependent services
//
// ## ServiceClass Health
//   - Tool-driven health checking through ServiceClass health tools
//   - Configurable health check intervals and thresholds
//   - Template-based parameter substitution for health check tools
//   - Response mapping for health status extraction
//   - Failure threshold tracking and recovery detection
//
// # State Management
//
// Comprehensive state management with ServiceClass awareness:
//
//   - **Unified State Tracking**: Both static and ServiceClass services
//   - **Event-Driven Updates**: Real-time state change propagation
//   - **ServiceClass Instance Events**: Detailed lifecycle event streaming
//   - **State Correlation**: Track related operations across service lifecycle
//   - **Health Status Integration**: Combined state and health monitoring
//
// # Service Instance Persistence
//
// ServiceClass instances can be persisted to YAML files:
//
//   - **Automatic Persistence**: Optional persistence during instance creation
//   - **Auto-Start Support**: Instances can be configured to start automatically
//   - **Definition Loading**: Persisted instances loaded on orchestrator startup
//   - **YAML Management**: Create, update, and delete persisted definitions
//
// # API Integration
//
// Following the Service Locator Pattern, the orchestrator provides:
//
//   - **Unified Service Management**: Single interface for all service operations
//   - **ServiceClass APIs**: Complete ServiceClass instance management
//   - **Event Subscription**: Real-time state and instance event streaming
//   - **Tool Provider Integration**: Aggregator tool execution capabilities
//   - **Clean Separation**: No direct inter-package dependencies
//
// # Usage Examples
//
// ## Traditional Orchestrator Usage
//
//	cfg := orchestrator.Config{
//	    Aggregator: aggregatorConfig,
//	    ToolCaller: toolCaller,
//	    Storage:    storage,
//	    Yolo:       false,
//	}
//
//	orch := orchestrator.New(cfg)
//	if err := orch.Start(ctx); err != nil {
//	    log.Fatal(err)
//	}
//	defer orch.Stop()
//
// ## ServiceClass Instance Management
//
//	// Create ServiceClass instance
//	req := orchestrator.CreateServiceRequest{
//	    ServiceClassName: "kubernetes_port_forward",
//	    Name:             "my-app-forward",
//	    Parameters: map[string]interface{}{
//	        "namespace":    "default",
//	        "service_name": "my-app",
//	        "local_port":   "8080",
//	        "remote_port":  "80",
//	    },
//	    Persist:   true,
//	    AutoStart: true,
//	}
//
//	instance, err := orch.CreateServiceClassInstance(ctx, req)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Monitor instance lifecycle
//	events := orch.SubscribeToServiceInstanceEvents()
//	go func() {
//	    for event := range events {
//	        fmt.Printf("Instance %s: %s -> %s\n",
//	            event.Name, event.OldState, event.NewState)
//	    }
//	}()
//
//	// Get instance status
//	status, err := orch.GetServiceClassInstance(instance.Name)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	fmt.Printf("Instance state: %s, health: %s\n", status.State, status.Health)
//
// # Service Names
//
// Services are identified by names following these conventions:
//
// ## Static Services
//   - **K8s connections**: "k8s-mc-{cluster}" or "k8s-wc-{cluster}"
//   - **Port forwards**: "{name}" (from configuration)
//   - **MCP servers**: "mcp-{name}" (from configuration)
//
// ## ServiceClass Instances
//   - **User-defined names** specified during instance creation
//   - Must be unique within the service registry
//   - Can use ServiceClass default name templates with parameter substitution
//
// # Error Handling
//
// Enhanced error handling for ServiceClass operations:
//
//   - **ServiceClass Validation Errors**: Invalid or unavailable ServiceClass definitions
//   - **Tool Execution Errors**: Aggregator tool failures and response mapping errors
//   - **Parameter Template Errors**: Template substitution and validation failures
//   - **Dependency Errors**: Invalid dependencies and circular dependency detection
//   - **Instance Management Errors**: Creation, deletion, and lifecycle management failures
//   - **Persistence Errors**: YAML file operations and storage failures
//
// # Thread Safety
//
// All orchestrator operations are thread-safe:
//
//   - **Concurrent service operations**
//   - **Thread-safe service registry access**
//   - **Atomic state updates and event propagation**
//   - **Safe dependency graph manipulation**
//   - **Concurrent ServiceClass instance management**
//   - **Protected access to persistence layer**
package orchestrator
