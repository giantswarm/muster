package orchestrator

import (
	"context"
	"fmt"
	"muster/internal/api"
	"muster/internal/config"
	"muster/internal/services"
	"muster/internal/services/mcpserver"
	"muster/internal/template"
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
	UpdatedAt        time.Time              `json:"updatedAt"`
	LastChecked      *time.Time             `json:"lastChecked,omitempty"`
	ServiceData      map[string]interface{} `json:"serviceData,omitempty"`
	CreationArgs     map[string]interface{} `json:"creationArgs"`
	Outputs          map[string]interface{} `json:"outputs,omitempty"`
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
	ToolCaller ToolCaller // Optional: for ServiceClass-based services
}

// New creates a new orchestrator.
func New(cfg Config) *Orchestrator {
	registry := services.NewRegistry()

	return &Orchestrator{
		registry:               registry,
		aggregator:             cfg.Aggregator,
		yolo:                   cfg.Yolo,
		toolCaller:             cfg.ToolCaller,
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
	// Note: Using the new unified client approach through the API
	mcpServerMgr := api.GetMCPServerManager()
	if mcpServerMgr == nil {
		logging.Debug("Orchestrator", "MCPServerManager not available through API, skipping MCPServer service creation")
		return nil
	}

	// Get all MCP server definitions from the unified client
	mcpServers := mcpServerMgr.ListMCPServers()
	logging.Info("Orchestrator", "Found %d MCPServer definitions for auto-start processing", len(mcpServers))

	// Process each MCP Server to create services with auto-start enabled
	for _, mcpServerInfo := range mcpServers {
		// Only process auto-start servers
		if !mcpServerInfo.AutoStart {
			logging.Debug("Orchestrator", "Skipping MCPServer %s: AutoStart=false", mcpServerInfo.Name)
			continue
		}

		if err := o.createMCPServerService(ctx, mcpServerInfo); err != nil {
			logging.Error("Orchestrator", err, "Failed to create MCPServer service: %s", mcpServerInfo.Name)
			// Continue processing other servers
		}
	}

	return nil
}

// createMCPServerService creates an MCPServer service from MCPServerInfo and registers it
func (o *Orchestrator) createMCPServerService(ctx context.Context, mcpServerInfo api.MCPServerInfo) error {
	logging.Info("Orchestrator", "Creating MCPServer service: %s", mcpServerInfo.Name)

	// Convert MCPServerInfo to api.MCPServer format expected by the service
	apiDef := &api.MCPServer{
		Name:        mcpServerInfo.Name,
		Type:        api.MCPServerType(mcpServerInfo.Type),
		AutoStart:   mcpServerInfo.AutoStart,
		Command:     mcpServerInfo.Command,
		Env:         mcpServerInfo.Env,
		Description: mcpServerInfo.Description,
	}

	// Create MCPServer service using the service package
	mcpService, err := mcpserver.NewService(apiDef)
	if err != nil {
		return fmt.Errorf("failed to create MCPServer service: %w", err)
	}

	// Set up state change notifications for the service
	mcpService.SetStateChangeCallback(o.createStateChangeCallback())

	// Register the service with the registry so it's managed by the orchestrator
	if err := o.registry.Register(mcpService); err != nil {
		return fmt.Errorf("failed to register MCPServer service: %w", err)
	}

	// Start the service immediately since the orchestrator's Start() method
	// has already started static services and won't start newly registered ones
	go func() {
		if err := mcpService.Start(ctx); err != nil {
			logging.Error("Orchestrator", err, "Failed to start MCPServer service: %s", mcpServerInfo.Name)
		} else {
			logging.Info("Orchestrator", "Started MCPServer service: %s", mcpServerInfo.Name)
		}
	}()

	logging.Info("Orchestrator", "Successfully created and registered MCPServer service: %s", mcpServerInfo.Name)
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
	instance.SetStateChangeCallback(o.createDynamicServiceStateChangeCallback())

	// Store the instance using service name as key
	o.instances[req.Name] = instance
	o.mu.Unlock()

	// Generate service instance created event
	o.generateServiceInstanceEvent(req.Name, req.ServiceClassName, "ServiceInstanceCreated", "", nil, 0, 0)

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

	// Process ServiceClass outputs if defined
	var resolvedOutputs map[string]interface{}
	if serviceClass.ServiceConfig.Outputs != nil && len(serviceClass.ServiceConfig.Outputs) > 0 {
		logging.Debug("Orchestrator", "Processing outputs for ServiceClass %s, service instance %s", req.ServiceClassName, req.Name)

		// Create template context with service args at root level for direct template access
		templateContext := make(map[string]interface{})

		// Add service creation arguments at root level so {{ .text }} works
		for key, value := range req.Args {
			templateContext[key] = value
		}

		// Also add structured context for advanced templates
		templateContext["args"] = req.Args
		templateContext["name"] = req.Name

		// Add runtime data from the service instance if available
		// Get the tool outputs that were extracted during service creation
		serviceData := instance.GetServiceData()
		logging.Debug("Orchestrator", "Tool outputs from service instance %s: %+v", req.Name, serviceData)

		// Add tool outputs under their respective tool names (e.g., "start", "stop")
		// For now, we'll add the start tool outputs under "start" key
		// This assumes the start tool was called - we could track which tools were called
		if serviceData != nil && len(serviceData) > 0 {
			templateContext["start"] = serviceData
			logging.Debug("Orchestrator", "Added start tool outputs to template context: %+v", serviceData)
		}

		logging.Debug("Orchestrator", "Template context for outputs: %+v", templateContext)

		// Create template engine and resolve outputs
		templateEngine := template.New()
		resolvedResult, err := templateEngine.Replace(serviceClass.ServiceConfig.Outputs, templateContext)
		if err != nil {
			logging.Error("Orchestrator", err, "Failed to resolve outputs for ServiceClass %s, service instance %s", req.ServiceClassName, req.Name)
			// Don't fail the creation, just log the error and continue without outputs
			resolvedOutputs = nil
		} else {
			// Ensure the result is a map[string]interface{}
			if outputsMap, ok := resolvedResult.(map[string]interface{}); ok {
				resolvedOutputs = outputsMap
				logging.Debug("Orchestrator", "Successfully resolved outputs for service instance %s: %+v", req.Name, resolvedOutputs)

				// Store the resolved outputs in the service instance for later retrieval
				instance.SetOutputs(resolvedOutputs)
			} else {
				logging.Error("Orchestrator", fmt.Errorf("outputs resolution returned non-map type"), "Invalid outputs format for service instance %s", req.Name)
				resolvedOutputs = nil
			}
		}
	}

	return &ServiceInstanceInfo{
		Name:             req.Name,
		ServiceClassName: req.ServiceClassName,
		ServiceClassType: "serviceclass", // Default since Type field removed from API in Phase 3
		State:            string(instance.GetState()),
		Health:           string(instance.GetHealth()),
		CreatedAt:        instance.GetCreatedAt(),
		UpdatedAt:        time.Now(),
		ServiceData:      instance.GetServiceData(),
		CreationArgs:     req.Args,
		Outputs:          resolvedOutputs,
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

	o.mu.Lock()
	delete(o.instances, name)
	o.mu.Unlock()

	// Generate service instance deleted event
	o.generateServiceInstanceEvent(instance.GetName(), instance.GetServiceClassName(), "ServiceInstanceDeleted", "", nil, 0, 0)

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

	return o.serviceInstanceToInfo(instance), nil
}

// ListServiceClassInstances returns information about all ServiceClass-based service instances
func (o *Orchestrator) ListServiceClassInstances() []ServiceInstanceInfo {
	o.mu.RLock()
	defer o.mu.RUnlock()

	result := make([]ServiceInstanceInfo, 0, len(o.instances))
	for _, instance := range o.instances {
		result = append(result, *o.serviceInstanceToInfo(instance))
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
func (o *Orchestrator) serviceInstanceToInfo(instance *services.GenericServiceInstance) *ServiceInstanceInfo {
	return &ServiceInstanceInfo{
		Name:             instance.GetName(), // Use the instance's actual name
		ServiceClassName: instance.GetServiceClassName(),
		ServiceClassType: string(instance.GetType()),
		State:            string(instance.GetState()),
		Health:           string(instance.GetHealth()),
		LastError:        "",
		CreatedAt:        instance.GetCreatedAt(),
		UpdatedAt:        instance.GetUpdatedAt(),
		ServiceData:      instance.GetServiceData(),
		CreationArgs:     instance.GetCreationArgs(),
		Outputs:          instance.GetOutputs(), // Now available from the instance
	}
}

// createDynamicServiceStateChangeCallback creates a state change callback for ServiceClass-based services
func (o *Orchestrator) createDynamicServiceStateChangeCallback() services.StateChangeCallback {
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

// generateServiceInstanceEvent generates a Kubernetes event for a service instance
func (o *Orchestrator) generateServiceInstanceEvent(serviceName, serviceClassName, reason, operation string, err error, duration time.Duration, stepCount int) {
	eventManager := api.GetEventManager()
	if eventManager == nil {
		logging.Debug("Orchestrator", "Event manager not available, skipping event generation for service instance %s", serviceName)
		return
	}

	// Create event data with service instance context
	eventData := api.ObjectReference{
		Kind:      "ServiceInstance",
		Name:      serviceName,
		Namespace: "default", // Service instances use default namespace
	}

	// Build context-aware message
	message := o.buildServiceInstanceEventMessage(serviceName, serviceClassName, reason, operation, err, duration, stepCount)

	// Determine event type based on reason
	eventType := "Normal"
	if reason == "ServiceInstanceFailed" ||
		reason == "ServiceInstanceUnhealthy" ||
		reason == "ServiceInstanceHealthCheckFailed" ||
		reason == "ServiceInstanceToolExecutionFailed" {
		eventType = "Warning"
	}

	// Generate the event
	ctx := context.Background()
	if err := eventManager.CreateEvent(ctx, eventData, reason, message, eventType); err != nil {
		logging.Error("Orchestrator", err, "Failed to generate event for service instance %s", serviceName)
	}
}

// buildServiceInstanceEventMessage builds the event message for service instance events
func (o *Orchestrator) buildServiceInstanceEventMessage(serviceName, serviceClassName, reason, operation string, err error, duration time.Duration, stepCount int) string {
	switch reason {
	case "ServiceInstanceCreated":
		return fmt.Sprintf("Service instance %s created from ServiceClass %s", serviceName, serviceClassName)
	case "ServiceInstanceDeleted":
		return fmt.Sprintf("Service instance %s deleted successfully", serviceName)
	default:
		return fmt.Sprintf("Service instance %s: %s", serviceName, reason)
	}
}
