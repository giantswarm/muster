package api

import (
	"context"
	"fmt"
	"muster/pkg/logging"
	"sync"
)

// Handler registry variables store the registered implementations.
// These variables are protected by handlerMutex for thread-safe access.
var (
	registryHandler            ServiceRegistryHandler
	serviceManagerHandler      ServiceManagerHandler
	serviceClassManagerHandler ServiceClassManagerHandler
	mcpServerManagerHandler    MCPServerManagerHandler
	aggregatorHandler          AggregatorHandler
	configHandler              ConfigHandler
	workflowHandler            WorkflowHandler

	// toolUpdateSubscribers stores the list of components subscribed to tool update events.
	// Access is protected by toolUpdateMutex.
	toolUpdateSubscribers []ToolUpdateSubscriber
	toolUpdateMutex       sync.Mutex

	// handlerMutex protects all handler registry operations for thread-safe registration and access.
	handlerMutex sync.RWMutex
)

// RegisterServiceRegistry registers the service registry handler implementation.
// This handler provides access to all registered services in the system,
// including both static services (defined in configuration) and dynamic
// ServiceClass-based service instances.
//
// The registration is thread-safe and should be called during system initialization.
// Only one service registry handler can be registered at a time; subsequent
// registrations will replace the previous handler.
//
// Args:
//   - h: ServiceRegistryHandler implementation that manages service discovery and information
//
// Thread-safe: Yes, protected by handlerMutex.
//
// Example:
//
//	adapter := &myservice.RegistryAdapter{registry: myRegistry}
//	api.RegisterServiceRegistry(adapter)
func RegisterServiceRegistry(h ServiceRegistryHandler) {
	handlerMutex.Lock()
	defer handlerMutex.Unlock()
	registryHandler = h
}

// RegisterServiceManager registers the service manager handler implementation.
// This handler provides unified service lifecycle management for both static services
// and ServiceClass-based service instances.
//
// The registration is thread-safe and should be called during system initialization.
// Only one service manager handler can be registered at a time; subsequent
// registrations will replace the previous handler.
//
// Args:
//   - h: ServiceManagerHandler implementation that manages service lifecycle operations
//
// Thread-safe: Yes, protected by handlerMutex.
//
// Example:
//
//	adapter := &services.ManagerAdapter{manager: myManager}
//	api.RegisterServiceManager(adapter)
func RegisterServiceManager(h ServiceManagerHandler) {
	handlerMutex.Lock()
	defer handlerMutex.Unlock()
	logging.Debug("API", "Registering service manager handler: %v", h != nil)
	serviceManagerHandler = h
}

// RegisterAggregator registers the aggregator handler implementation.
// This handler provides tool execution and MCP server aggregation functionality,
// serving as the central component for unified tool access across multiple MCP servers.
//
// The registration is thread-safe and should be called during system initialization.
// Only one aggregator handler can be registered at a time; subsequent
// registrations will replace the previous handler.
//
// Args:
//   - h: AggregatorHandler implementation that manages tool execution and MCP server coordination
//
// Thread-safe: Yes, protected by handlerMutex.
//
// Example:
//
//	adapter := &aggregator.Adapter{aggregator: myAggregator}
//	api.RegisterAggregator(adapter)
func RegisterAggregator(h AggregatorHandler) {
	handlerMutex.Lock()
	defer handlerMutex.Unlock()
	aggregatorHandler = h
}

// RegisterConfigHandler registers the configuration handler implementation.
// This handler provides runtime configuration management functionality,
// including configuration retrieval, updates, and persistence operations.
//
// The registration is thread-safe and should be called during system initialization.
// Only one configuration handler can be registered at a time; subsequent
// registrations will replace the previous handler.
//
// Args:
//   - h: ConfigHandler implementation that manages configuration operations
//
// Thread-safe: Yes, protected by handlerMutex.
//
// Example:
//
//	adapter := &config.Adapter{manager: myConfigManager}
//	api.RegisterConfigHandler(adapter)
func RegisterConfigHandler(h ConfigHandler) {
	handlerMutex.Lock()
	defer handlerMutex.Unlock()
	configHandler = h
}

// RegisterConfig is an alias for RegisterConfigHandler for backward compatibility.
// New code should prefer using RegisterConfigHandler for clarity.
//
// Args:
//   - h: ConfigHandler implementation that manages configuration operations
//
// Thread-safe: Yes, delegates to RegisterConfigHandler.
//
// Deprecated: Use RegisterConfigHandler directly for better clarity.
func RegisterConfig(h ConfigHandler) {
	RegisterConfigHandler(h)
}

// GetServiceRegistry returns the registered service registry handler.
// This provides access to the service discovery and information interface.
//
// Returns nil if no handler has been registered yet. Callers should always
// check for nil before using the returned handler.
//
// Returns:
//   - ServiceRegistryHandler: The registered handler, or nil if not registered
//
// Thread-safe: Yes, protected by handlerMutex read lock.
//
// Example:
//
//	registry := api.GetServiceRegistry()
//	if registry == nil {
//	    return fmt.Errorf("service registry not available")
//	}
//	services := registry.GetAll()
func GetServiceRegistry() ServiceRegistryHandler {
	handlerMutex.RLock()
	defer handlerMutex.RUnlock()
	return registryHandler
}

// GetServiceManager returns the registered service manager handler.
// This provides access to unified service lifecycle management for both
// static services and ServiceClass-based service instances.
//
// Returns nil if no handler has been registered yet. Callers should always
// check for nil before using the returned handler.
//
// Returns:
//   - ServiceManagerHandler: The registered handler, or nil if not registered
//
// Thread-safe: Yes, protected by handlerMutex read lock.
//
// Example:
//
//	manager := api.GetServiceManager()
//	if manager == nil {
//	    return fmt.Errorf("service manager not available")
//	}
//	err := manager.StartService("my-service")
func GetServiceManager() ServiceManagerHandler {
	handlerMutex.RLock()
	defer handlerMutex.RUnlock()
	return serviceManagerHandler
}

// GetAggregator returns the registered aggregator handler.
// This provides access to tool execution and MCP server aggregation functionality.
//
// Returns nil if no handler has been registered yet. Callers should always
// check for nil before using the returned handler.
//
// Returns:
//   - AggregatorHandler: The registered handler, or nil if not registered
//
// Thread-safe: Yes, protected by handlerMutex read lock.
//
// Example:
//
//	aggregator := api.GetAggregator()
//	if aggregator == nil {
//	    return fmt.Errorf("aggregator not available")
//	}
//	result, err := aggregator.CallTool(ctx, "my-tool", args)
func GetAggregator() AggregatorHandler {
	handlerMutex.RLock()
	defer handlerMutex.RUnlock()
	return aggregatorHandler
}

// GetConfigHandler returns the registered configuration handler.
// This provides access to runtime configuration management functionality.
//
// Returns nil if no handler has been registered yet. Callers should always
// check for nil before using the returned handler.
//
// Returns:
//   - ConfigHandler: The registered handler, or nil if not registered
//
// Thread-safe: Yes, protected by handlerMutex read lock.
//
// Example:
//
//	configHandler := api.GetConfigHandler()
//	if configHandler == nil {
//	    return fmt.Errorf("config handler not available")
//	}
//	config, err := configHandler.GetConfig(ctx)
func GetConfigHandler() ConfigHandler {
	handlerMutex.RLock()
	defer handlerMutex.RUnlock()
	return configHandler
}

// GetConfig is an alias for GetConfigHandler for backward compatibility.
// New code should prefer using GetConfigHandler for clarity.
//
// Returns:
//   - ConfigHandler: The registered handler, or nil if not registered
//
// Thread-safe: Yes, delegates to GetConfigHandler.
//
// Deprecated: Use GetConfigHandler directly for better clarity.
func GetConfig() ConfigHandler {
	return GetConfigHandler()
}

// RegisterWorkflow registers the workflow handler implementation.
// This handler provides workflow definition management and execution functionality,
// allowing components to define and execute multi-step processes through the system.
//
// The registration is thread-safe and should be called during system initialization.
// Only one workflow handler can be registered at a time; subsequent
// registrations will replace the previous handler.
//
// Args:
//   - h: WorkflowHandler implementation that manages workflow operations
//
// Thread-safe: Yes, protected by handlerMutex.
//
// Example:
//
//	adapter := workflow.NewAdapter(toolCaller, toolChecker)
//	adapter.Register()
func RegisterWorkflow(h WorkflowHandler) {
	handlerMutex.Lock()
	defer handlerMutex.Unlock()
	logging.Debug("API", "Registering workflow handler: %v", h != nil)
	workflowHandler = h
}

// GetWorkflow returns the registered workflow handler.
// This provides access to workflow definition management and execution functionality.
//
// Returns nil if no handler has been registered yet. Callers should always
// check for nil before using the returned handler.
//
// Returns:
//   - WorkflowHandler: The registered handler, or nil if not registered
//
// Thread-safe: Yes, protected by handlerMutex read lock.
//
// Example:
//
//	workflow := api.GetWorkflow()
//	if workflow == nil {
//	    return fmt.Errorf("workflow handler not available")
//	}
//	result, err := workflow.ExecuteWorkflow(ctx, "deploy-app", args)
func GetWorkflow() WorkflowHandler {
	handlerMutex.RLock()
	defer handlerMutex.RUnlock()
	return workflowHandler
}

// RegisterServiceClassManager registers the service class manager handler implementation.
// This handler provides ServiceClass definition management and lifecycle tool access,
// enabling the creation of service instances from predefined templates.
//
// The registration is thread-safe and should be called during system initialization.
// Only one service class manager handler can be registered at a time; subsequent
// registrations will replace the previous handler.
//
// Args:
//   - h: ServiceClassManagerHandler implementation that manages ServiceClass operations
//
// Thread-safe: Yes, protected by handlerMutex.
//
// Example:
//
//	adapter := serviceclass.NewAdapter(configPath)
//	adapter.Register()
func RegisterServiceClassManager(h ServiceClassManagerHandler) {
	handlerMutex.Lock()
	defer handlerMutex.Unlock()
	logging.Debug("API", "Registering service class manager handler: %v", h != nil)
	serviceClassManagerHandler = h
}

// GetServiceClassManager returns the registered service class manager handler.
// This provides access to ServiceClass definition management and lifecycle tool access.
//
// Returns nil if no handler has been registered yet. Callers should always
// check for nil before using the returned handler.
//
// Returns:
//   - ServiceClassManagerHandler: The registered handler, or nil if not registered
//
// Thread-safe: Yes, protected by handlerMutex read lock.
//
// Example:
//
//	manager := api.GetServiceClassManager()
//	if manager == nil {
//	    return fmt.Errorf("service class manager not available")
//	}
//	classes := manager.ListServiceClasses()
func GetServiceClassManager() ServiceClassManagerHandler {
	handlerMutex.RLock()
	defer handlerMutex.RUnlock()
	return serviceClassManagerHandler
}

// SetServiceClassManagerForTesting sets the service class manager handler for testing purposes.
// This function bypasses normal registration and should only be used in test code
// to provide mock implementations for unit testing.
//
// Args:
//   - h: ServiceClassManagerHandler implementation for testing
//
// Thread-safe: Yes, protected by handlerMutex.
//
// Testing: This function is intended for test use only and should not be called in production code.
//
// Example:
//
//	mockManager := &testutils.MockServiceClassManager{}
//	api.SetServiceClassManagerForTesting(mockManager)
//	defer api.SetServiceClassManagerForTesting(nil) // cleanup
func SetServiceClassManagerForTesting(h ServiceClassManagerHandler) {
	handlerMutex.Lock()
	defer handlerMutex.Unlock()
	serviceClassManagerHandler = h
}

// ExecuteWorkflow is a convenience function for executing workflows
func ExecuteWorkflow(ctx context.Context, workflowName string, args map[string]interface{}) (*CallToolResult, error) {
	handler := GetWorkflow()
	if handler == nil {
		return nil, fmt.Errorf("workflow handler not registered")
	}
	return handler.ExecuteWorkflow(ctx, workflowName, args)
}

// GetWorkflowInfo returns information about all workflows
func GetWorkflowInfo() []Workflow {
	handler := GetWorkflow()
	if handler == nil {
		return nil
	}
	return handler.GetWorkflows()
}

// RegisterMCPServerManager registers the MCP server manager handler implementation.
// This handler provides MCP server definition management and lifecycle operations,
// enabling the management of MCP servers that provide tools to the aggregator.
//
// The registration is thread-safe and should be called during system initialization.
// Only one MCP server manager handler can be registered at a time; subsequent
// registrations will replace the previous handler.
//
// Args:
//   - h: MCPServerManagerHandler implementation that manages MCP server operations
//
// Thread-safe: Yes, protected by handlerMutex.
//
// Example:
//
//	adapter := mcpserver.NewAdapter(configPath)
//	adapter.Register()
func RegisterMCPServerManager(h MCPServerManagerHandler) {
	handlerMutex.Lock()
	defer handlerMutex.Unlock()
	logging.Debug("API", "Registering MCP server manager handler: %v", h != nil)
	mcpServerManagerHandler = h
}

// GetMCPServerManager returns the registered MCP server manager handler.
// This provides access to MCP server definition management and lifecycle operations.
//
// Returns nil if no handler has been registered yet. Callers should always
// check for nil before using the returned handler.
//
// Returns:
//   - MCPServerManagerHandler: The registered handler, or nil if not registered
//
// Thread-safe: Yes, protected by handlerMutex read lock.
//
// Example:
//
//	manager := api.GetMCPServerManager()
//	if manager == nil {
//	    return fmt.Errorf("MCP server manager not available")
//	}
//	servers := manager.ListMCPServers()
func GetMCPServerManager() MCPServerManagerHandler {
	handlerMutex.RLock()
	defer handlerMutex.RUnlock()
	return mcpServerManagerHandler
}

// SubscribeToToolUpdates allows components to subscribe to tool availability change events.
// Subscribers will receive notifications when tools are added, removed, or updated across MCP servers.
// This enables components to react to changes in the tool landscape in real-time.
//
// The subscription is thread-safe and can be called from any goroutine.
// Subscriber callbacks are executed in separate goroutines to prevent blocking
// the event publishing mechanism.
//
// Args:
//   - subscriber: ToolUpdateSubscriber that will receive tool update notifications
//
// Thread-safe: Yes, protected by toolUpdateMutex.
//
// Note: Subscriber callbacks are executed asynchronously and should not block.
// Panics in subscriber callbacks are recovered and logged as errors.
//
// Example:
//
//	type MySubscriber struct{}
//	func (s *MySubscriber) OnToolsUpdated(event api.ToolUpdateEvent) {
//	    fmt.Printf("Tools updated: %s\n", event.Type)
//	}
//
//	subscriber := &MySubscriber{}
//	api.SubscribeToToolUpdates(subscriber)
func SubscribeToToolUpdates(subscriber ToolUpdateSubscriber) {
	toolUpdateMutex.Lock()
	defer toolUpdateMutex.Unlock()
	toolUpdateSubscribers = append(toolUpdateSubscribers, subscriber)
	logging.Debug("API", "Added tool update subscriber, total subscribers: %d", len(toolUpdateSubscribers))
}

// PublishToolUpdateEvent publishes a tool update event to all registered subscribers.
// This function is used to notify components about changes in tool availability,
// such as when MCP servers are registered/deregistered or their tools change.
//
// The event is delivered asynchronously to all subscribers. Each subscriber
// receives the event in a separate goroutine to prevent blocking, ensuring
// that slow or failing subscribers don't affect other subscribers or the publisher.
//
// Args:
//   - event: ToolUpdateEvent containing details about the tool update
//
// Thread-safe: Yes, subscriber list is safely copied before notification.
//
// Note: Each subscriber is notified in a separate goroutine to prevent blocking.
// Panics in subscriber callbacks are recovered and logged as errors.
//
// Example:
//
//	event := api.ToolUpdateEvent{
//	    Type:       "server_registered",
//	    ServerName: "kubernetes",
//	    Tools:      []string{"kubectl_get_pods", "kubectl_describe"},
//	    Timestamp:  time.Now(),
//	}
//	api.PublishToolUpdateEvent(event)
func PublishToolUpdateEvent(event ToolUpdateEvent) {
	toolUpdateMutex.Lock()
	subscribers := make([]ToolUpdateSubscriber, len(toolUpdateSubscribers))
	copy(subscribers, toolUpdateSubscribers)
	toolUpdateMutex.Unlock()

	logging.Debug("API", "Publishing tool update event: type=%s, server=%s, tools=%d, subscribers=%d",
		event.Type, event.ServerName, len(event.Tools), len(subscribers))

	for _, subscriber := range subscribers {
		// Call subscriber in goroutine to avoid blocking
		go func(s ToolUpdateSubscriber) {
			defer func() {
				if r := recover(); r != nil {
					logging.Error("API", fmt.Errorf("panic in tool update subscriber: %v", r), "Tool update subscriber panicked")
				}
			}()
			s.OnToolsUpdated(event)
		}(subscriber)
	}
}
