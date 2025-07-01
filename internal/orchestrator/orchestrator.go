package orchestrator

import (
	"context"
	"fmt"
	"muster/internal/api"
	"muster/internal/config"
	"muster/internal/services"
	"muster/pkg/logging"
	"sync"
	"time"
)

// StopReason tracks why a service was stopped.
type StopReason int

const (
	StopReasonManual StopReason = iota
	StopReasonDependency
)

// ToolCaller represents the interface for calling aggregator tools
// This interface is implemented by the aggregator integration
type ToolCaller interface {
	CallTool(ctx context.Context, toolName string, args map[string]interface{}) (map[string]interface{}, error)
}

// CreateServiceRequest represents a request to create a new ServiceClass-based service instance
type CreateServiceRequest struct {
	// ServiceClass to use
	ServiceClassName string `json:"serviceClassName"`

	// Name for the service instance (must be unique)
	Name string `json:"name"`

	// Arguments for service creation
	Args map[string]interface{} `json:"args"`

	// Whether to persist this service instance definition to YAML files
	Persist bool `json:"persist,omitempty"`

	// Optional: Whether this instance should be started automatically on system startup
	AutoStart bool `json:"autoStart,omitempty"`

	// Override default timeouts (future use)
	CreateTimeout *time.Duration `json:"createTimeout,omitempty"`
	DeleteTimeout *time.Duration `json:"deleteTimeout,omitempty"`
}

// ServiceInstanceInfo provides information about a ServiceClass-based service instance
type ServiceInstanceInfo struct {
	Name             string                 `json:"name"`
	ServiceClassName string                 `json:"serviceClassName"`
	ServiceClassType string                 `json:"serviceClassType"`
	State            string                 `json:"state"`
	Health           string                 `json:"health"`
	LastError        string                 `json:"lastError,omitempty"`
	CreatedAt        time.Time              `json:"createdAt"`
	LastChecked      *time.Time             `json:"lastChecked,omitempty"`
	ServiceData      map[string]interface{} `json:"serviceData,omitempty"`
	CreationArgs     map[string]interface{} `json:"creationArgs"`
}

// ServiceInstanceEvent represents a service instance state change event
type ServiceInstanceEvent struct {
	Name        string                 `json:"name"`
	ServiceType string                 `json:"serviceType"`
	OldState    string                 `json:"oldState"`
	NewState    string                 `json:"newState"`
	OldHealth   string                 `json:"oldHealth"`
	NewHealth   string                 `json:"newHealth"`
	Error       string                 `json:"error,omitempty"`
	Timestamp   time.Time              `json:"timestamp"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// Orchestrator manages services using the unified service registry architecture.
// It serves as the single source of truth for all active services, both static and dynamic.
type Orchestrator struct {
	registry services.ServiceRegistry

	// Configuration
	aggregator config.AggregatorConfig
	yolo       bool

	// ServiceClass-based dynamic service management
	toolCaller     ToolCaller
	instances      map[string]*services.GenericServiceInstance // name -> instance
	instanceEvents []chan<- ServiceInstanceEvent

	// Service instance persistence
	persistence *services.ServiceInstancePersistence

	// Service tracking
	stopReasons map[string]StopReason

	// State change event subscribers
	stateChangeSubscribers []chan<- ServiceStateChangedEvent

	// Context for cancellation
	ctx        context.Context
	cancelFunc context.CancelFunc

	mu sync.RWMutex
}

// Config holds the configuration for the orchestrator.
type Config struct {
	Aggregator config.AggregatorConfig
	Yolo       bool
	ToolCaller ToolCaller      // Optional: for ServiceClass-based services
	Storage    *config.Storage // Required: for configuration and persistence
}

// New creates a new orchestrator.
func New(cfg Config) *Orchestrator {
	registry := services.NewRegistry()

	// Initialize persistence helper
	var persistence *services.ServiceInstancePersistence
	if cfg.Storage != nil {
		persistence = services.NewServiceInstancePersistence(cfg.Storage)
	}

	return &Orchestrator{
		registry:               registry,
		aggregator:             cfg.Aggregator,
		yolo:                   cfg.Yolo,
		toolCaller:             cfg.ToolCaller,
		persistence:            persistence,
		instances:              make(map[string]*services.GenericServiceInstance),
		instanceEvents:         make([]chan<- ServiceInstanceEvent, 0),
		stopReasons:            make(map[string]StopReason),
		stateChangeSubscribers: make([]chan<- ServiceStateChangedEvent, 0),
	}
}

// Start initializes and starts all services (both static and ServiceClass-based).
func (o *Orchestrator) Start(ctx context.Context) error {
	o.ctx, o.cancelFunc = context.WithCancel(ctx)

	// Start static services that are already registered
	staticServices := o.registry.GetAll()

	// Set up state change callbacks on static services
	o.setupStateChangeNotifications(staticServices)

	// Start static services asynchronously
	for _, service := range staticServices {
		go func(svc services.Service) {
			if err := svc.Start(o.ctx); err != nil {
				logging.Error("Orchestrator", err, "Failed to start static service: %s", svc.GetName())
			} else {
				logging.Info("Orchestrator", "Started static service: %s", svc.GetName())
			}
		}(service)
	}

	// Process ServiceClass-based services from MCP Server configurations
	if err := o.processServiceClassRequirements(ctx); err != nil {
		logging.Error("Orchestrator", err, "Failed to process ServiceClass requirements")
		// Don't fail the orchestrator start if ServiceClass processing fails
	}

	// Load and start persisted service instances
	if err := o.loadPersistedServiceInstances(ctx); err != nil {
		logging.Error("Orchestrator", err, "Failed to load persisted service instances")
		// Don't fail the orchestrator start if persistence loading fails
	}

	logging.Info("Orchestrator", "Started orchestrator with unified service management (static: %d, dynamic: %d)",
		len(staticServices), len(o.instances))
	return nil
}

// processServiceClassRequirements identifies and instantiates ServiceClass-based services
// based on MCP Server configuration requirements
func (o *Orchestrator) processServiceClassRequirements(ctx context.Context) error {
	if o.toolCaller == nil {
		logging.Debug("Orchestrator", "No ToolCaller available, skipping ServiceClass-based services")
		return nil
	}

	// Get ServiceClassManager through API
	serviceClassMgr := api.GetServiceClassManager()
	if serviceClassMgr == nil {
		logging.Warn("Orchestrator", "ServiceClassManager not available through API")
		return nil
	}

	// Get MCPServerManager through API to access MCP server definitions
	mcpServerMgr := api.GetMCPServerManager()
	if mcpServerMgr == nil {
		logging.Debug("Orchestrator", "MCPServerManager not available through API, skipping ServiceClass processing")
		return nil
	}

	// Get all MCP server definitions from the manager
	mcpServers := mcpServerMgr.ListMCPServers()

	// Process each MCP Server to identify required ServiceClasses
	for _, mcpServerInfo := range mcpServers {
		// Only process auto-start servers
		if !mcpServerInfo.AutoStart {
			continue
		}

		if err := o.processMCPServerServiceClasses(ctx, mcpServerInfo, serviceClassMgr); err != nil {
			logging.Error("Orchestrator", err, "Failed to process ServiceClasses for MCP Server: %s", mcpServerInfo.Name)
			// Continue processing other servers
		}
	}

	return nil
}

// processMCPServerServiceClasses processes ServiceClass requirements for a single MCP Server
func (o *Orchestrator) processMCPServerServiceClasses(ctx context.Context, mcpServerInfo api.MCPServerInfo, serviceClassMgr api.ServiceClassManagerHandler) error {
	// Extract ServiceClass requirements from MCP server configuration
	// This logic will depend on how ServiceClasses are specified in the config
	serviceClassNames := o.extractServiceClassNames(mcpServerInfo)

	for _, serviceClassName := range serviceClassNames {
		// Check if we already have an instance for this service class + server combination
		name := fmt.Sprintf("%s-%s", mcpServerInfo.Name, serviceClassName)

		o.mu.RLock()
		_, exists := o.instances[name]
		o.mu.RUnlock()

		if exists {
			logging.Debug("Orchestrator", "ServiceClass instance already exists: %s", name)
			continue
		}

		// Verify ServiceClass is available
		if !serviceClassMgr.IsServiceClassAvailable(serviceClassName) {
			logging.Warn("Orchestrator", "ServiceClass %s not available for MCP Server %s", serviceClassName, mcpServerInfo.Name)
			continue
		}

		// Create service instance
		req := CreateServiceRequest{
			ServiceClassName: serviceClassName,
			Name:             name,
			Args:             o.buildServiceArgs(mcpServerInfo, serviceClassName),
		}

		if _, err := o.CreateServiceClassInstance(ctx, req); err != nil {
			logging.Error("Orchestrator", err, "Failed to create ServiceClass instance %s for MCP Server %s", serviceClassName, mcpServerInfo.Name)
			// Continue with other service classes
		}
	}

	return nil
}

// extractServiceClassNames extracts ServiceClass names from MCP Server configuration
// This is a placeholder - the actual implementation will depend on how ServiceClasses
// are specified in the MCP server configuration
func (o *Orchestrator) extractServiceClassNames(mcpServerInfo api.MCPServerInfo) []string {
	// For now, return empty slice - this will be implemented when we know
	// how ServiceClasses are specified in the configuration
	return []string{}
}

// buildServiceArgs builds args for ServiceClass instantiation based on MCP Server config
func (o *Orchestrator) buildServiceArgs(mcpServerInfo api.MCPServerInfo, serviceClassName string) map[string]interface{} {
	return map[string]interface{}{
		"mcpServerName": mcpServerInfo.Name,
		"mcpServerType": mcpServerInfo.Type,
		"serviceClass":  serviceClassName,
		// Add other relevant args from MCP server config
	}
}

// loadPersistedServiceInstances loads and starts persisted service instances from YAML files
func (o *Orchestrator) loadPersistedServiceInstances(ctx context.Context) error {
	if o.persistence == nil {
		logging.Debug("Orchestrator", "No persistence configured, skipping persisted service instance loading")
		return nil
	}

	if o.toolCaller == nil {
		logging.Debug("Orchestrator", "No ToolCaller available, skipping persisted service instance loading")
		return nil
	}

	// Load persisted definitions
	definitions, err := o.persistence.LoadPersistedDefinitions()
	if err != nil {
		return fmt.Errorf("failed to load persisted service instance definitions: %w", err)
	}

	if len(definitions) == 0 {
		logging.Debug("Orchestrator", "No persisted service instances found")
		return nil
	}

	logging.Info("Orchestrator", "Loading %d persisted service instances", len(definitions))

	// Create and start instances for enabled definitions
	for _, def := range definitions {
		if !def.Enabled {
			logging.Debug("Orchestrator", "Skipping disabled persisted service instance: %s", def.Name)
			continue
		}

		// Create the service instance
		req := CreateServiceRequest{
			ServiceClassName: def.ServiceClassName,
			Name:             def.Name,
			Args:             def.Args,
			Persist:          false, // Already persisted, don't save again
			AutoStart:        def.AutoStart,
		}

		instance, err := o.CreateServiceClassInstance(ctx, req)
		if err != nil {
			logging.Error("Orchestrator", err, "Failed to create persisted service instance: %s", def.Name)
			continue
		}

		logging.Info("Orchestrator", "Successfully restored persisted service instance: %s", instance.Name)

		// Start the instance if AutoStart is enabled
		if def.AutoStart {
			// The instance is already started by CreateServiceClassInstance, so we just log it
			logging.Info("Orchestrator", "Auto-started persisted service instance: %s", instance.Name)
		}
	}

	return nil
}

// CreateServiceClassInstance creates a new ServiceClass-based service instance
func (o *Orchestrator) CreateServiceClassInstance(ctx context.Context, req CreateServiceRequest) (*ServiceInstanceInfo, error) {
	// Validate the request
	if err := o.validateCreateRequest(req); err != nil {
		return nil, fmt.Errorf("invalid create request: %w", err)
	}

	// Check if ToolCaller is available
	if o.toolCaller == nil {
		return nil, fmt.Errorf("ToolCaller not available for ServiceClass-based services")
	}

	// Get ServiceClassManager through API
	serviceClassMgr := api.GetServiceClassManager()
	if serviceClassMgr == nil {
		return nil, fmt.Errorf("ServiceClassManager not available through API")
	}

	// Verify ServiceClass exists and is available
	serviceClass, err := serviceClassMgr.GetServiceClass(req.ServiceClassName)
	if err != nil {
		return nil, fmt.Errorf("ServiceClass %s not found: %w", req.ServiceClassName, err)
	}

	if !serviceClassMgr.IsServiceClassAvailable(req.ServiceClassName) {
		return nil, fmt.Errorf("ServiceClass %s is not available (missing required tools)", req.ServiceClassName)
	}

	// Validate service creation args against ServiceClass arg definitions
	if err := serviceClass.ValidateServiceArgs(req.Args); err != nil {
		return nil, fmt.Errorf("arg validation failed: %w", err)
	}

	// Check if name is already in use
	o.mu.Lock()
	if _, exists := o.instances[req.Name]; exists {
		o.mu.Unlock()
		return nil, fmt.Errorf("service with name %s already exists", req.Name)
	}

	// Also check static services
	if _, exists := o.registry.Get(req.Name); exists {
		o.mu.Unlock()
		return nil, fmt.Errorf("static service with name %s already exists", req.Name)
	}

	// Create GenericServiceInstance
	instance := services.NewGenericServiceInstance(
		req.Name,
		req.ServiceClassName,
		o.toolCaller,
		req.Args,
	)

	if instance == nil {
		o.mu.Unlock()
		return nil, fmt.Errorf("failed to create GenericServiceInstance for ServiceClass %s", req.ServiceClassName)
	}

	// Set up state change callback
	instance.SetStateChangeCallback(o.createDynamicServiceStateChangeCallback(req.Name))

	// Store the instance using service name as key
	o.instances[req.Name] = instance
	o.mu.Unlock()

	logging.Info("Orchestrator", "Creating ServiceClass-based service instance: %s (ServiceClass: %s)", req.Name, req.ServiceClassName)

	// Start the service instance
	if err := instance.Start(ctx); err != nil {
		logging.Error("Orchestrator", err, "Failed to start ServiceClass instance %s", instance.GetName())

		// Remove from tracking on failure
		o.mu.Lock()
		delete(o.instances, req.Name)
		o.mu.Unlock()

		return nil, fmt.Errorf("failed to start ServiceClass instance: %w", err)
	}

	// Register with the main service registry for unified management
	if err := o.registry.Register(instance); err != nil {
		// Remove from tracking on registration failure
		o.mu.Lock()
		delete(o.instances, req.Name)
		o.mu.Unlock()

		return nil, fmt.Errorf("failed to register ServiceClass instance in registry: %w", err)
	}

	// Persist the instance definition if requested
	if req.Persist && o.persistence != nil {
		definition := services.CreateDefinitionFromInstance(
			req.Name,
			req.ServiceClassName,
			"serviceclass", // Default since Type field removed from API in Phase 3
			req.Args,
			req.AutoStart,
		)

		if err := o.persistence.SaveDefinition(definition); err != nil {
			logging.Error("Orchestrator", err, "Failed to persist service instance definition: %s", req.Name)
			// Don't fail the creation, just log the error
		} else {
			logging.Info("Orchestrator", "Persisted service instance definition: %s", req.Name)
		}
	}

	return &ServiceInstanceInfo{
		Name:             req.Name,
		ServiceClassName: req.ServiceClassName,
		ServiceClassType: "serviceclass", // Default since Type field removed from API in Phase 3
		State:            string(instance.GetState()),
		Health:           string(instance.GetHealth()),
		CreatedAt:        time.Now(),
		ServiceData:      instance.GetServiceData(),
		CreationArgs:     req.Args,
	}, nil
}

// DeleteServiceClassInstance deletes a ServiceClass-based service instance
func (o *Orchestrator) DeleteServiceClassInstance(ctx context.Context, name string) error {
	o.mu.Lock()
	instance, exists := o.instances[name]
	if !exists {
		o.mu.Unlock()
		return fmt.Errorf("ServiceClass instance %s not found", name)
	}
	o.mu.Unlock()

	logging.Info("Orchestrator", "Deleting ServiceClass-based service instance: %s", instance.GetName())

	// Stop the service instance
	if err := instance.Stop(ctx); err != nil {
		logging.Error("Orchestrator", err, "Failed to stop ServiceClass instance %s during deletion", instance.GetName())
		// Continue with deletion even if stop fails
	}

	// Remove from registry and tracking
	o.registry.Unregister(instance.GetName())

	// Check if this instance was persisted and remove from persistence
	if o.persistence != nil {
		// Try to remove from persistence - if it wasn't persisted, this will fail silently
		if err := o.persistence.DeleteDefinition(instance.GetName()); err != nil {
			logging.Debug("Orchestrator", "Service instance %s was not persisted (or failed to remove): %v", instance.GetName(), err)
		} else {
			logging.Info("Orchestrator", "Removed persisted definition for service instance: %s", instance.GetName())
		}
	}

	o.mu.Lock()
	delete(o.instances, name)
	o.mu.Unlock()

	logging.Info("Orchestrator", "Successfully deleted ServiceClass-based service instance: %s", instance.GetName())
	return nil
}

// GetServiceClassInstance returns information about a ServiceClass-based service instance
func (o *Orchestrator) GetServiceClassInstance(name string) (*ServiceInstanceInfo, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	instance, exists := o.instances[name]
	if !exists {
		return nil, fmt.Errorf("ServiceClass instance %s not found", name)
	}

	return o.serviceInstanceToInfo(name, instance), nil
}

// ListServiceClassInstances returns information about all ServiceClass-based service instances
func (o *Orchestrator) ListServiceClassInstances() []ServiceInstanceInfo {
	o.mu.RLock()
	defer o.mu.RUnlock()

	result := make([]ServiceInstanceInfo, 0, len(o.instances))
	for serviceName, instance := range o.instances {
		result = append(result, *o.serviceInstanceToInfo(serviceName, instance))
	}

	return result
}

// SubscribeToServiceInstanceEvents returns a channel for receiving ServiceClass-based service instance events
func (o *Orchestrator) SubscribeToServiceInstanceEvents() <-chan ServiceInstanceEvent {
	o.mu.Lock()
	defer o.mu.Unlock()

	eventChan := make(chan ServiceInstanceEvent, 100)
	o.instanceEvents = append(o.instanceEvents, eventChan)
	return eventChan
}

// validateCreateRequest validates a ServiceClass service creation request
func (o *Orchestrator) validateCreateRequest(req CreateServiceRequest) error {
	if req.ServiceClassName == "" {
		return fmt.Errorf("ServiceClass name is required")
	}
	if req.Name == "" {
		return fmt.Errorf("service name is required")
	}
	if req.Args == nil {
		req.Args = make(map[string]interface{})
	}
	return nil
}

// serviceInstanceToInfo converts a GenericServiceInstance to ServiceInstanceInfo
func (o *Orchestrator) serviceInstanceToInfo(serviceName string, instance *services.GenericServiceInstance) *ServiceInstanceInfo {
	return &ServiceInstanceInfo{
		Name:             instance.GetName(), // Use the instance's actual name
		ServiceClassName: instance.GetServiceClassName(),
		ServiceClassType: string(instance.GetType()),
		State:            string(instance.GetState()),
		Health:           string(instance.GetHealth()),
		LastError:        "",
		CreatedAt:        instance.GetCreatedAt(),
		ServiceData:      instance.GetServiceData(),
		CreationArgs:     instance.GetCreationArgs(),
	}
}

// createDynamicServiceStateChangeCallback creates a state change callback for ServiceClass-based services
func (o *Orchestrator) createDynamicServiceStateChangeCallback(name string) services.StateChangeCallback {
	return func(name string, oldState, newState services.ServiceState, health services.HealthStatus, err error) {
		// Publish to both static service subscribers and dynamic service subscribers
		o.publishStateChangeEvent(name, oldState, newState, health, err)
		o.publishServiceInstanceEvent(name, oldState, newState, health, err)
	}
}

// publishServiceInstanceEvent publishes a ServiceClass-based service instance event
func (o *Orchestrator) publishServiceInstanceEvent(name string, oldState, newState services.ServiceState, health services.HealthStatus, err error) {
	o.mu.RLock()
	instance, exists := o.instances[name]
	if !exists {
		o.mu.RUnlock()
		return
	}

	// Create the event
	event := ServiceInstanceEvent{
		Name:        name,
		ServiceType: string(instance.GetType()),
		OldState:    string(oldState),
		NewState:    string(newState),
		OldHealth:   string(health), // Previous health would need tracking
		NewHealth:   string(health),
		Error:       "",
		Timestamp:   time.Now(),
		Metadata:    instance.GetServiceData(),
	}

	if err != nil {
		event.Error = err.Error()
	}

	// Publish to all subscribers
	subscribers := make([]chan<- ServiceInstanceEvent, len(o.instanceEvents))
	copy(subscribers, o.instanceEvents)
	o.mu.RUnlock()

	for _, subscriber := range subscribers {
		select {
		case subscriber <- event:
		default:
			// Don't block if subscriber can't receive immediately
			logging.Debug("Orchestrator", "ServiceInstance event subscriber blocked, skipping event for service %s", name)
		}
	}
}

// setupStateChangeNotifications configures services to notify the orchestrator of state changes
func (o *Orchestrator) setupStateChangeNotifications(services []services.Service) {
	for _, service := range services {
		service.SetStateChangeCallback(o.createStateChangeCallback())
		logging.Debug("Orchestrator", "Set up state change notifications for service: %s", service.GetName())
	}
}

// createStateChangeCallback creates a state change callback that publishes events
func (o *Orchestrator) createStateChangeCallback() services.StateChangeCallback {
	return func(name string, oldState, newState services.ServiceState, health services.HealthStatus, err error) {
		o.publishStateChangeEvent(name, oldState, newState, health, err)
	}
}

// publishStateChangeEvent publishes a state change event to all subscribers
func (o *Orchestrator) publishStateChangeEvent(name string, oldState, newState services.ServiceState, health services.HealthStatus, err error) {
	// Get service to determine its type (try both static and dynamic)
	service, exists := o.registry.Get(name)
	if !exists {
		return
	}

	logging.Debug("Orchestrator", "Service %s state changed: %s -> %s (health: %s)", name, oldState, newState, health)

	// Create the event
	event := ServiceStateChangedEvent{
		Name:        name,
		ServiceType: string(service.GetType()),
		OldState:    string(oldState),
		NewState:    string(newState),
		Health:      string(health),
		Error:       err,
		Timestamp:   time.Now().Unix(),
	}

	// Publish to all subscribers
	o.mu.RLock()
	subscribers := make([]chan<- ServiceStateChangedEvent, len(o.stateChangeSubscribers))
	copy(subscribers, o.stateChangeSubscribers)
	o.mu.RUnlock()

	for _, subscriber := range subscribers {
		select {
		case subscriber <- event:
		default:
			// Don't block if subscriber can't receive immediately
			logging.Debug("Orchestrator", "Subscriber blocked, skipping event for service %s", name)
		}
	}
}

// Stop gracefully stops all services (both static and ServiceClass-based).
func (o *Orchestrator) Stop() error {
	if o.cancelFunc != nil {
		o.cancelFunc()
	}

	// Stop all ServiceClass-based services
	o.mu.RLock()
	var instances []*services.GenericServiceInstance
	for _, instance := range o.instances {
		instances = append(instances, instance)
	}
	o.mu.RUnlock()

	// Stop dynamic services concurrently
	var wg sync.WaitGroup
	for _, instance := range instances {
		wg.Add(1)
		go func(inst *services.GenericServiceInstance) {
			defer wg.Done()
			if err := inst.Stop(o.ctx); err != nil {
				logging.Error("Orchestrator", err, "Failed to stop ServiceClass instance %s during shutdown", inst.GetServiceClassName())
			}
		}(instance)
	}

	// Wait for dynamic services to stop
	wg.Wait()

	return nil
}

// StartService starts a specific service by name
func (o *Orchestrator) StartService(name string) error {
	service, exists := o.registry.Get(name)
	if !exists {
		return fmt.Errorf("service %s not found", name)
	}

	if err := service.Start(o.ctx); err != nil {
		return fmt.Errorf("failed to start service %s: %w", name, err)
	}

	logging.Info("Orchestrator", "Started service: %s", name)
	return nil
}

// StopService stops a specific service by name
func (o *Orchestrator) StopService(name string) error {
	service, exists := o.registry.Get(name)
	if !exists {
		return fmt.Errorf("service %s not found", name)
	}

	if err := service.Stop(o.ctx); err != nil {
		return fmt.Errorf("failed to stop service %s: %w", name, err)
	}

	logging.Info("Orchestrator", "Stopped service: %s", name)
	return nil
}

// RestartService restarts a specific service by name
func (o *Orchestrator) RestartService(name string) error {
	service, exists := o.registry.Get(name)
	if !exists {
		return fmt.Errorf("service %s not found", name)
	}

	if err := service.Restart(o.ctx); err != nil {
		return fmt.Errorf("failed to restart service %s: %w", name, err)
	}

	logging.Info("Orchestrator", "Restarted service: %s", name)
	return nil
}

// GetServiceRegistry returns the service registry.
func (o *Orchestrator) GetServiceRegistry() services.ServiceRegistry {
	return o.registry
}

// SubscribeToStateChanges returns a channel for state change events.
func (o *Orchestrator) SubscribeToStateChanges() <-chan ServiceStateChangedEvent {
	eventChan := make(chan ServiceStateChangedEvent, 100)
	o.mu.Lock()
	o.stateChangeSubscribers = append(o.stateChangeSubscribers, eventChan)
	o.mu.Unlock()
	return eventChan
}

// ServiceStateChangedEvent represents a service state change event.
type ServiceStateChangedEvent struct {
	Name        string
	ServiceType string
	OldState    string
	NewState    string
	Health      string
	Error       error
	Timestamp   int64
}

// GetServiceStatus returns the status of a specific service.
func (o *Orchestrator) GetServiceStatus(name string) (*ServiceStatus, error) {
	service, exists := o.registry.Get(name)
	if !exists {
		return nil, fmt.Errorf("service %s not found", name)
	}

	return &ServiceStatus{
		Name:   name,
		Type:   string(service.GetType()),
		State:  string(service.GetState()),
		Health: string(service.GetHealth()),
		Error:  service.GetLastError(),
	}, nil
}

// GetAllServices returns status for all services (both static and ServiceClass-based).
func (o *Orchestrator) GetAllServices() []ServiceStatus {
	services := o.registry.GetAll()
	statuses := make([]ServiceStatus, len(services))

	for i, service := range services {
		statuses[i] = ServiceStatus{
			Name:   service.GetName(),
			Type:   string(service.GetType()),
			State:  string(service.GetState()),
			Health: string(service.GetHealth()),
			Error:  service.GetLastError(),
		}
	}

	return statuses
}

// ServiceStatus represents the status of a service.
type ServiceStatus struct {
	Name   string
	Type   string
	State  string
	Health string
	Error  error
}

// SetToolCaller sets the ToolCaller for ServiceClass-based services
// This is called after the aggregator is available
func (o *Orchestrator) SetToolCaller(toolCaller ToolCaller) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.toolCaller = toolCaller
}

// GetToolCaller returns the current ToolCaller
func (o *Orchestrator) GetToolCaller() ToolCaller {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.toolCaller
}
