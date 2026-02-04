package api

import (
	"fmt"
	"muster/pkg/logging"
	"sort"
	"sync"

	"github.com/giantswarm/mcp-oauth/providers/dex"
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
	eventManagerHandler        EventManagerHandler
	reconcileManagerHandler    ReconcileManagerHandler
	teleportClientHandler      TeleportClientHandler

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

// CollectRequiredAudiences collects all unique required audiences from MCPServers
// that have forwardToken: true configured. This is used to determine which
// cross-client audiences to request from Dex during OAuth authentication.
//
// When users authenticate to muster, the OAuth flow requests tokens with these
// audiences, allowing the tokens to be forwarded to downstream MCPServers
// that require specific audience claims (e.g., Kubernetes OIDC authentication).
//
// Returns:
//   - []string: Unique list of required audiences from all SSO-enabled MCPServers
//
// If no MCPServer manager is registered, returns an empty slice.
//
// Thread-safe: Yes, uses registered MCPServerManager which is thread-safe.
//
// Example:
//
//	audiences := api.CollectRequiredAudiences()
//	// Returns: ["dex-k8s-authenticator", "another-audience"]
func CollectRequiredAudiences() []string {
	manager := GetMCPServerManager()
	if manager == nil {
		logging.Debug("API", "MCPServer manager not available, cannot collect required audiences")
		return nil
	}

	servers := manager.ListMCPServers()
	if len(servers) == 0 {
		return nil
	}

	// Use a map to deduplicate audiences
	audienceSet := make(map[string]struct{})

	for _, server := range servers {
		// Only consider servers with forwardToken: true
		if server.Auth == nil || !server.Auth.ForwardToken {
			continue
		}

		// Collect all required audiences from this server.
		// Uses dex.ValidateAudience for security-validated input checking,
		// preventing scope injection attacks from malformed audience strings.
		for _, audience := range server.Auth.RequiredAudiences {
			if dex.ValidateAudience(audience) == nil {
				audienceSet[audience] = struct{}{}
			}
		}
	}

	// Convert set to slice and sort for deterministic ordering
	audiences := make([]string, 0, len(audienceSet))
	for audience := range audienceSet {
		audiences = append(audiences, audience)
	}
	sort.Strings(audiences)

	if len(audiences) > 0 {
		logging.Debug("API", "Collected %d required audiences from MCPServers: %v", len(audiences), audiences)
	}

	return audiences
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

// RegisterEventManager registers the event manager handler implementation.
// This handler provides Kubernetes Event generation functionality for CRD lifecycle operations,
// automatically adapting to both Kubernetes and filesystem modes through the unified client.
//
// The registration is thread-safe and should be called during system initialization.
// Only one event manager handler can be registered at a time; subsequent
// registrations will replace the previous handler.
//
// Args:
//   - h: EventManagerHandler implementation that manages event generation operations
//
// Thread-safe: Yes, protected by handlerMutex.
//
// Example:
//
//	adapter := events.NewAdapter(musterClient)
//	adapter.Register()
func RegisterEventManager(h EventManagerHandler) {
	handlerMutex.Lock()
	defer handlerMutex.Unlock()
	logging.Debug("API", "Registering event manager handler: %v", h != nil)
	eventManagerHandler = h
}

// GetEventManager returns the registered event manager handler.
// This provides access to Kubernetes Event generation functionality for CRD lifecycle operations.
//
// Returns nil if no handler has been registered yet. Callers should always
// check for nil before using the returned handler.
//
// Returns:
//   - EventManagerHandler: The registered handler, or nil if not registered
//
// Thread-safe: Yes, protected by handlerMutex read lock.
//
// Example:
//
//	eventManager := api.GetEventManager()
//	if eventManager == nil {
//	    return fmt.Errorf("event manager not available")
//	}
//	err := eventManager.CreateEvent(ctx, objectRef, "Created", "Object successfully created", "Normal")
func GetEventManager() EventManagerHandler {
	handlerMutex.RLock()
	defer handlerMutex.RUnlock()
	return eventManagerHandler
}

// RegisterReconcileManager registers the reconcile manager handler implementation.
// This handler provides reconciliation status and control functionality,
// enabling automatic synchronization of resource definitions with running services.
//
// The registration is thread-safe and should be called during system initialization.
// Only one reconcile manager handler can be registered at a time; subsequent
// registrations will replace the previous handler.
//
// Args:
//   - h: ReconcileManagerHandler implementation that manages reconciliation operations
//
// Thread-safe: Yes, protected by handlerMutex.
func RegisterReconcileManager(h ReconcileManagerHandler) {
	handlerMutex.Lock()
	defer handlerMutex.Unlock()
	logging.Debug("API", "Registering reconcile manager handler: %v", h != nil)
	reconcileManagerHandler = h
}

// GetReconcileManager returns the registered reconcile manager handler.
// This provides access to reconciliation status and control functionality.
//
// Returns nil if no handler has been registered yet. Callers should always
// check for nil before using the returned handler.
//
// Returns:
//   - ReconcileManagerHandler: The registered handler, or nil if not registered
//
// Thread-safe: Yes, protected by handlerMutex read lock.
func GetReconcileManager() ReconcileManagerHandler {
	handlerMutex.RLock()
	defer handlerMutex.RUnlock()
	return reconcileManagerHandler
}

// UpdateMCPServerState updates the state of an MCPServer service.
// This is used when external events (such as SSO authentication success) need to
// update the service state. The function retrieves the service from the registry,
// checks if it implements StateUpdater, and updates its state.
//
// This function is typically called by the aggregator when:
// - SSO token forwarding succeeds for a session
// - SSO token exchange succeeds for a session
//
// The state update will trigger the reconciler to sync the new state to the CRD status.
//
// Args:
//   - name: The name of the MCPServer service to update
//   - state: The new service state (typically StateConnected for SSO success)
//   - health: The new health status (typically HealthHealthy for SSO success)
//   - err: Optional error to associate with the state (typically nil for success)
//
// Returns:
//   - error: Error if the service doesn't exist, doesn't support state updates, or update fails
//
// Thread-safe: Yes, operations are synchronized appropriately.
//
// Example:
//
//	// Update MCPServer state after SSO token forwarding succeeds
//	if err := api.UpdateMCPServerState("gazelle-mcp-kubernetes", api.StateConnected, api.HealthHealthy, nil); err != nil {
//	    logging.Warn("SSO", "Failed to update MCPServer state: %v", err)
//	}
func UpdateMCPServerState(name string, state ServiceState, health HealthStatus, err error) error {
	registry := GetServiceRegistry()
	if registry == nil {
		return fmt.Errorf("service registry not available")
	}

	service, exists := registry.Get(name)
	if !exists {
		// Service not found - this is expected for servers that failed to start
		// or are configured but not yet registered. Log at debug level.
		logging.Debug("API", "Cannot update state for MCPServer %s: not found in registry", name)
		return nil // Not an error - service may not be running yet
	}

	// Check if the service implements StateUpdater
	updater, ok := service.(StateUpdater)
	if !ok {
		// Service doesn't support external state updates
		logging.Debug("API", "MCPServer %s does not support external state updates", name)
		return nil // Not an error - just can't update
	}

	// Update the state
	updater.UpdateState(state, health, err)
	logging.Info("API", "Updated MCPServer %s state to %s (health: %s)", name, state, health)

	// Trigger reconciliation to sync the state to the CRD status.
	// This ensures that `muster list mcpserver` (which reads from CRD) shows
	// the updated state, not just `muster list services` (which reads from memory).
	if reconcileManager := GetReconcileManager(); reconcileManager != nil {
		reconcileManager.TriggerReconcile("MCPServer", name, "")
	}

	return nil
}

// RegisterTeleportClient registers the Teleport client handler implementation.
// This handler provides HTTP clients configured with Teleport Machine ID certificates
// for accessing MCP servers on private installations via Teleport Application Access.
//
// The registration is thread-safe and should be called during system initialization.
// Only one Teleport client handler can be registered at a time; subsequent
// registrations will replace the previous handler.
//
// Args:
//   - h: TeleportClientHandler implementation that provides Teleport HTTP clients
//
// Thread-safe: Yes, protected by handlerMutex.
//
// Example:
//
//	adapter := teleport.NewAdapter()
//	adapter.Register()
func RegisterTeleportClient(h TeleportClientHandler) {
	handlerMutex.Lock()
	defer handlerMutex.Unlock()
	logging.Debug("API", "Registering Teleport client handler: %v", h != nil)
	teleportClientHandler = h
}

// GetTeleportClient returns the registered Teleport client handler.
// This provides access to HTTP clients configured with Teleport certificates
// for accessing private installations.
//
// Returns nil if no handler has been registered yet. Callers should always
// check for nil before using the returned handler.
//
// Returns:
//   - TeleportClientHandler: The registered handler, or nil if not registered
//
// Thread-safe: Yes, protected by handlerMutex read lock.
//
// Example:
//
//	teleportHandler := api.GetTeleportClient()
//	if teleportHandler == nil {
//	    return fmt.Errorf("Teleport client handler not available")
//	}
//	httpClient, err := teleportHandler.GetHTTPClientForIdentity("/var/run/tbot/identity")
func GetTeleportClient() TeleportClientHandler {
	handlerMutex.RLock()
	defer handlerMutex.RUnlock()
	return teleportClientHandler
}

// metaToolsHandler stores the registered MetaToolsHandler implementation.
var metaToolsHandler MetaToolsHandler

// RegisterMetaTools registers the meta-tools handler implementation.
// This handler provides server-side meta-tool functionality for AI assistants,
// enabling tool discovery, execution, and resource/prompt access.
//
// The registration is thread-safe and should be called during system initialization.
// Only one meta-tools handler can be registered at a time; subsequent
// registrations will replace the previous handler.
//
// Args:
//   - h: MetaToolsHandler implementation that manages meta-tool operations
//
// Thread-safe: Yes, protected by handlerMutex.
//
// Example:
//
//	adapter := metatools.NewAdapter()
//	adapter.Register()
func RegisterMetaTools(h MetaToolsHandler) {
	handlerMutex.Lock()
	defer handlerMutex.Unlock()
	logging.Debug("API", "Registering meta-tools handler: %v", h != nil)
	metaToolsHandler = h
}

// GetMetaTools returns the registered meta-tools handler.
// This provides access to server-side meta-tool functionality for
// tool discovery, execution, and resource/prompt access.
//
// Returns nil if no handler has been registered yet. Callers should always
// check for nil before using the returned handler.
//
// Returns:
//   - MetaToolsHandler: The registered handler, or nil if not registered
//
// Thread-safe: Yes, protected by handlerMutex read lock.
//
// Example:
//
//	metaToolsHandler := api.GetMetaTools()
//	if metaToolsHandler == nil {
//	    return fmt.Errorf("meta-tools handler not available")
//	}
//	tools, err := metaToolsHandler.ListTools(ctx)
func GetMetaTools() MetaToolsHandler {
	handlerMutex.RLock()
	defer handlerMutex.RUnlock()
	return metaToolsHandler
}
