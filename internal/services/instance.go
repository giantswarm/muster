package services

import (
	"context"
	"fmt"
	"sync"
	"time"

	"muster/internal/api"
	"muster/internal/template"
	"muster/pkg/logging"
)

// ToolCaller represents the interface for calling aggregator tools
// This interface is implemented by the aggregator integration
type ToolCaller interface {
	CallTool(ctx context.Context, toolName string, args map[string]interface{}) (map[string]interface{}, error)
}

// GenericServiceInstance is a runtime-configurable service instance
// that implements the services.Service interface using API-accessed ServiceClass definitions
type GenericServiceInstance struct {
	// Synchronization
	mu sync.RWMutex

	// Configuration (accessed via API)
	serviceClassName string
	toolCaller       ToolCaller

	// Identity
	name string

	// Service interface state - this is the source of truth
	state        ServiceState
	health       HealthStatus
	lastError    error
	dependencies []string

	// Service data and tracking
	creationArgs         map[string]interface{}
	serviceData          map[string]interface{}
	outputs              map[string]interface{} // ServiceClass-level outputs resolved during creation
	createdAt            time.Time
	updatedAt            time.Time
	lastChecked          *time.Time
	healthCheckFailures  int
	healthCheckSuccesses int

	// Callback for state changes
	stateCallback StateChangeCallback

	// Templating engine (using existing template package)
	templater *template.Engine
}

// NewGenericServiceInstance creates a new generic service instance
// configured with a service class name and ToolCaller
func NewGenericServiceInstance(
	name string,
	serviceClassName string,
	toolCaller ToolCaller,
	args map[string]interface{},
) *GenericServiceInstance {
	// Get service class info through API
	serviceClassMgr := api.GetServiceClassManager()
	if serviceClassMgr == nil {
		logging.Error("GenericServiceInstance", fmt.Errorf("service class manager not available"), "ServiceClassManager not available through API")
		return nil
	}

	// Verify service class exists
	_, err := serviceClassMgr.GetServiceClass(serviceClassName)
	if err != nil {
		logging.Error("GenericServiceInstance", err, "Failed to get service class %s", serviceClassName)
		return nil
	}

	// Get dependencies through API
	dependencies, err := serviceClassMgr.GetServiceDependencies(serviceClassName)
	if err != nil {
		logging.Warn("GenericServiceInstance", "Failed to get dependencies for service class %s: %v", serviceClassName, err)
		dependencies = []string{} // Default to no dependencies
	}

	// Convert dependencies to local format
	localDependencies := make([]string, len(dependencies))
	copy(localDependencies, dependencies)

	return &GenericServiceInstance{
		serviceClassName:     serviceClassName,
		toolCaller:           toolCaller,
		name:                 name,
		state:                StateUnknown,
		health:               HealthUnknown,
		dependencies:         localDependencies,
		creationArgs:         args,
		serviceData:          make(map[string]interface{}),
		outputs:              make(map[string]interface{}),
		createdAt:            time.Now(),
		updatedAt:            time.Now(),
		healthCheckFailures:  0,
		healthCheckSuccesses: 0,
		templater:            template.New(),
	}
}

// Start implements the Service interface - starts the service using the start tool
func (gsi *GenericServiceInstance) Start(ctx context.Context) error {
	gsi.mu.Lock()
	if gsi.state == StateRunning || gsi.state == StateStarting {
		gsi.mu.Unlock()
		return nil // Already running or starting
	}
	gsi.mu.Unlock()

	gsi.updateStateInternal(StateStarting, HealthChecking, nil)

	// Get start tool info through API
	serviceClassMgr := api.GetServiceClassManager()
	if serviceClassMgr == nil {
		err := fmt.Errorf("service class manager not available")
		gsi.updateStateInternal(StateFailed, HealthUnknown, err)
		return err
	}

	toolName, args, outputs, err := serviceClassMgr.GetStartTool(gsi.serviceClassName)
	if err != nil {
		err = fmt.Errorf("failed to get start tool: %w", err)
		gsi.updateStateInternal(StateFailed, HealthUnknown, err)
		return err
	}

	// Generate starting event
	gsi.generateEvent("ServiceInstanceStarting", "start", toolName, nil, 0, 0)

	// Execute the start tool
	err = gsi.executeLifecycleTool(ctx, "start", toolName, args, outputs)
	if err != nil {
		return err
	}

	// Generate started event on success
	gsi.generateEvent("ServiceInstanceStarted", "start", toolName, nil, 0, 0)
	return nil
}

// Stop implements the Service interface - stops the service using the stop tool
func (gsi *GenericServiceInstance) Stop(ctx context.Context) error {
	gsi.mu.Lock()
	if gsi.state == StateStopped || gsi.state == StateStopping {
		gsi.mu.Unlock()
		return nil // Already stopped or stopping
	}
	gsi.mu.Unlock()

	gsi.updateStateInternal(StateStopping, HealthUnknown, nil)

	// Get stop tool info through API
	serviceClassMgr := api.GetServiceClassManager()
	if serviceClassMgr == nil {
		err := fmt.Errorf("service class manager not available")
		gsi.updateStateInternal(StateFailed, HealthUnknown, err)
		return err
	}

	toolName, args, outputs, err := serviceClassMgr.GetStopTool(gsi.serviceClassName)
	if err != nil {
		err = fmt.Errorf("failed to get stop tool: %w", err)
		gsi.updateStateInternal(StateFailed, HealthUnknown, err)
		return err
	}

	// Generate stopping event
	gsi.generateEvent("ServiceInstanceStopping", "stop", toolName, nil, 0, 0)

	// Execute the stop tool
	err = gsi.executeLifecycleTool(ctx, "stop", toolName, args, outputs)
	if err != nil {
		return err
	}

	// Final state after successful stop tool execution
	gsi.updateStateInternal(StateStopped, HealthUnknown, nil)

	// Generate stopped event on success
	gsi.generateEvent("ServiceInstanceStopped", "stop", toolName, nil, 0, 0)
	return nil
}

// Restart implements the Service interface
func (gsi *GenericServiceInstance) Restart(ctx context.Context) error {
	gsi.mu.Lock()
	if gsi.state == StateStarting || gsi.state == StateStopping {
		gsi.mu.Unlock()
		return fmt.Errorf("cannot restart while starting or stopping")
	}
	gsi.mu.Unlock()

	startTime := time.Now()
	logging.Info("GenericServiceInstance", "Restarting service %s", gsi.name)

	// Get restart tool info through API
	serviceClassMgr := api.GetServiceClassManager()
	if serviceClassMgr == nil {
		err := fmt.Errorf("service class manager not available")
		gsi.updateStateInternal(StateFailed, HealthUnknown, err)
		return err
	}

	toolName, args, outputs, err := serviceClassMgr.GetRestartTool(gsi.serviceClassName)
	// If a restart tool is defined and available, use it
	if err == nil && toolName != "" {
		// Generate restarting event
		gsi.generateEvent("ServiceInstanceRestarting", "restart", toolName, nil, 0, 0)

		gsi.updateStateInternal(StateStarting, HealthChecking, nil) // A restart is a form of starting
		err = gsi.executeLifecycleTool(ctx, "restart", toolName, args, outputs)
		if err != nil {
			return err
		}

		// Generate restarted event on success
		duration := time.Since(startTime)
		gsi.generateEvent("ServiceInstanceRestarted", "restart", toolName, nil, duration, 0)
		return nil
	}

	// Otherwise, fallback to Stop() then Start()
	logging.Info("GenericServiceInstance", "No custom restart tool for %s, using Stop/Start", gsi.name)

	// Generate restarting event for fallback method
	gsi.generateEvent("ServiceInstanceRestarting", "restart", "", nil, 0, 0)

	if err := gsi.Stop(ctx); err != nil {
		return fmt.Errorf("failed to stop service during restart: %w", err)
	}

	// Wait a moment for the service to fully stop
	// In a real scenario, we might poll for StateStopped, but a short sleep is simpler here
	time.Sleep(100 * time.Millisecond)

	if err := gsi.Start(ctx); err != nil {
		return fmt.Errorf("failed to start service during restart: %w", err)
	}

	// Generate restarted event on success
	duration := time.Since(startTime)
	gsi.generateEvent("ServiceInstanceRestarted", "restart", "", nil, duration, 0)
	return nil
}

// GetName implements the Service interface
func (gsi *GenericServiceInstance) GetName() string {
	gsi.mu.RLock()
	defer gsi.mu.RUnlock()
	return gsi.name
}

// GetState implements the Service interface
func (gsi *GenericServiceInstance) GetState() ServiceState {
	gsi.mu.RLock()
	defer gsi.mu.RUnlock()
	return gsi.state
}

// GetHealth implements the Service interface
func (gsi *GenericServiceInstance) GetHealth() HealthStatus {
	gsi.mu.RLock()
	defer gsi.mu.RUnlock()
	return gsi.health
}

// GetLastError implements the Service interface
func (gsi *GenericServiceInstance) GetLastError() error {
	gsi.mu.RLock()
	defer gsi.mu.RUnlock()
	return gsi.lastError
}

// GetType implements the Service interface
func (gsi *GenericServiceInstance) GetType() ServiceType {
	gsi.mu.RLock()
	defer gsi.mu.RUnlock()

	// Get service class definition through API to get type
	serviceClassMgr := api.GetServiceClassManager()
	if serviceClassMgr == nil {
		return ServiceType("unknown")
	}

	// Get the service class definition
	serviceClass, err := serviceClassMgr.GetServiceClass(gsi.serviceClassName)
	if err != nil {
		return ServiceType("unknown")
	}

	// Return the service type from the service class definition
	if serviceClass.ServiceType != "" {
		return ServiceType(serviceClass.ServiceType)
	}

	// Fallback to generic if no service type is specified
	return ServiceType("generic")
}

// GetDependencies implements the Service interface
func (gsi *GenericServiceInstance) GetDependencies() []string {
	gsi.mu.RLock()
	defer gsi.mu.RUnlock()
	// Return a copy to prevent external modification
	deps := make([]string, len(gsi.dependencies))
	copy(deps, gsi.dependencies)
	return deps
}

// SetStateChangeCallback implements the Service interface
func (gsi *GenericServiceInstance) SetStateChangeCallback(callback StateChangeCallback) {
	gsi.mu.Lock()
	defer gsi.mu.Unlock()
	gsi.stateCallback = callback
}

// CheckHealth implements the HealthChecker interface
func (gsi *GenericServiceInstance) CheckHealth(ctx context.Context) (HealthStatus, error) {
	gsi.mu.Lock()
	defer gsi.mu.Unlock()

	// Capture service name early for use in event generation (avoids lock re-acquisition)
	serviceName := gsi.name

	// Get service class manager through API
	serviceClassMgr := api.GetServiceClassManager()
	if serviceClassMgr == nil {
		return HealthUnhealthy, fmt.Errorf("service class manager not available through API")
	}

	// Check if health checking is enabled
	enabled, _, failureThreshold, successThreshold, err := serviceClassMgr.GetHealthCheckConfig(gsi.serviceClassName)
	if err != nil {
		return gsi.health, nil // If we can't get config, return current health
	}

	if !enabled {
		return gsi.health, nil
	}

	// Get the health check tool configuration through API
	toolName, toolArgs, expectation, err := serviceClassMgr.GetHealthCheckTool(gsi.serviceClassName)
	if err != nil {
		// No health check tool configured, return current health
		return gsi.health, nil
	}

	// Check if tool caller is available
	if gsi.toolCaller == nil {
		return HealthUnhealthy, fmt.Errorf("tool caller not available")
	}

	// Prepare the context for template substitution
	templateContext := gsi.buildTemplateContext()

	// Apply template substitution to tool arguments
	processedArgs, err := gsi.templater.Replace(toolArgs, templateContext)
	if err != nil {
		gsi.updateHealthTracking(false, failureThreshold, successThreshold)
		return HealthUnhealthy, fmt.Errorf("failed to process health check tool arguments: %w", err)
	}

	// Ensure tool arguments is a map
	toolArgsMap, ok := processedArgs.(map[string]interface{})
	if !ok {
		gsi.updateHealthTracking(false, failureThreshold, successThreshold)
		return HealthUnhealthy, fmt.Errorf("health check tool arguments must be a map, got %T", processedArgs)
	}

	// Call the health check tool
	logging.Debug("GenericServiceInstance", "Calling health check tool %s for service %s", toolName, gsi.name)
	response, err := gsi.toolCaller.CallTool(ctx, toolName, toolArgsMap)
	if err != nil {
		gsi.updateHealthTracking(false, failureThreshold, successThreshold)
		newHealth := gsi.determineHealthFromTracking(failureThreshold, successThreshold)
		gsi.updateStateInternal(gsi.state, newHealth, err)
		return newHealth, fmt.Errorf("health check tool failed: %w", err)
	}

	// Process the response using expectation matching
	isHealthy, err := EvaluateHealthCheckExpectation(response, expectation)
	if err != nil {
		gsi.updateHealthTracking(false, failureThreshold, successThreshold)
		newHealth := gsi.determineHealthFromTracking(failureThreshold, successThreshold)

		// Generate health check failed event (use WithName to avoid lock re-acquisition)
		gsi.generateEventWithName(serviceName, "ServiceInstanceHealthCheckFailed", "health_check", toolName, err, 0, 0)

		gsi.updateStateInternal(gsi.state, newHealth, err)
		return newHealth, fmt.Errorf("failed to evaluate health check expectation: %w", err)
	}

	// Store previous health for comparison
	previousHealth := gsi.health
	previousFailures := gsi.healthCheckFailures

	// Update health tracking
	gsi.updateHealthTracking(isHealthy, failureThreshold, successThreshold)
	newHealth := gsi.determineHealthFromTracking(failureThreshold, successThreshold)

	// Update last checked time
	now := time.Now()
	gsi.lastChecked = &now
	gsi.updatedAt = now

	// Generate health-related events (use WithName to avoid lock re-acquisition since we hold the mutex)
	if !isHealthy {
		// Generate health check failed event for individual failures
		gsi.generateEventWithName(serviceName, "ServiceInstanceHealthCheckFailed", "health_check", toolName, nil, 0, gsi.healthCheckFailures)
	}

	// Check for health state transitions and generate appropriate events
	if newHealth != previousHealth {
		switch newHealth {
		case HealthHealthy:
			if previousHealth == HealthUnhealthy {
				// Recovery from unhealthy state
				gsi.generateEventWithName(serviceName, "ServiceInstanceHealthCheckRecovered", "health_recovery", toolName, nil, 0, previousFailures)
			}
			gsi.generateEventWithName(serviceName, "ServiceInstanceHealthy", "health_status", toolName, nil, 0, gsi.healthCheckSuccesses)
		case HealthUnhealthy:
			gsi.generateEventWithName(serviceName, "ServiceInstanceUnhealthy", "health_status", toolName, nil, 0, gsi.healthCheckFailures)
		}
	}

	// Update state if health changed
	if newHealth != gsi.health {
		gsi.updateStateInternal(gsi.state, newHealth, nil)
	}

	return newHealth, nil
}

// GetHealthCheckInterval implements the HealthChecker interface
func (gsi *GenericServiceInstance) GetHealthCheckInterval() time.Duration {
	gsi.mu.RLock()
	defer gsi.mu.RUnlock()

	// Get service class manager through API
	serviceClassMgr := api.GetServiceClassManager()
	if serviceClassMgr == nil {
		return 30 * time.Second // Default interval
	}

	_, interval, _, _, err := serviceClassMgr.GetHealthCheckConfig(gsi.serviceClassName)
	if err != nil {
		return 30 * time.Second // Default interval
	}

	return interval
}

// GetServiceData implements the ServiceDataProvider interface
func (gsi *GenericServiceInstance) GetServiceData() map[string]interface{} {
	gsi.mu.RLock()
	defer gsi.mu.RUnlock()

	// Return a copy to prevent external modification
	data := make(map[string]interface{})
	for k, v := range gsi.serviceData {
		data[k] = v
	}
	return data
}

// SetOutputs sets the ServiceClass-level outputs for this service instance
func (gsi *GenericServiceInstance) SetOutputs(outputs map[string]interface{}) {
	gsi.mu.Lock()
	defer gsi.mu.Unlock()

	if outputs == nil {
		gsi.outputs = make(map[string]interface{})
	} else {
		gsi.outputs = make(map[string]interface{})
		for k, v := range outputs {
			gsi.outputs[k] = v
		}
	}
}

// GetOutputs returns the ServiceClass-level outputs for this service instance
func (gsi *GenericServiceInstance) GetOutputs() map[string]interface{} {
	gsi.mu.RLock()
	defer gsi.mu.RUnlock()

	// Return a copy to prevent external modification
	outputs := make(map[string]interface{})
	for k, v := range gsi.outputs {
		outputs[k] = v
	}
	return outputs
}

// GetServiceClassName returns the service class name for this instance
func (gsi *GenericServiceInstance) GetServiceClassName() string {
	gsi.mu.RLock()
	defer gsi.mu.RUnlock()
	return gsi.serviceClassName
}

// GetCreationArgs returns the creation args for this instance
func (gsi *GenericServiceInstance) GetCreationArgs() map[string]interface{} {
	gsi.mu.RLock()
	defer gsi.mu.RUnlock()

	// Return a copy to prevent external modification
	params := make(map[string]interface{})
	for k, v := range gsi.creationArgs {
		params[k] = v
	}
	return params
}

// GetCreatedAt returns the creation time for this instance
func (gsi *GenericServiceInstance) GetCreatedAt() time.Time {
	gsi.mu.RLock()
	defer gsi.mu.RUnlock()
	return gsi.createdAt
}

// GetUpdatedAt returns the last update time for this instance
func (gsi *GenericServiceInstance) GetUpdatedAt() time.Time {
	gsi.mu.RLock()
	defer gsi.mu.RUnlock()
	return gsi.updatedAt
}

// UpdateState implements the StateUpdater interface
func (gsi *GenericServiceInstance) UpdateState(state ServiceState, health HealthStatus, err error) {
	gsi.mu.Lock()
	defer gsi.mu.Unlock()
	gsi.updateStateInternal(state, health, err)
}

// Helper methods

// generateEventWithName generates a Kubernetes event for this service instance using the provided name.
// This variant is used when the caller already has the name to avoid acquiring locks.
func (gsi *GenericServiceInstance) generateEventWithName(serviceName string, reason string, operation string, toolName string, err error, duration time.Duration, stepCount int) {
	eventManager := api.GetEventManager()
	if eventManager == nil {
		logging.Debug("GenericServiceInstance", "Event manager not available, skipping event generation for service %s", serviceName)
		return
	}

	// Create event data with service instance context
	eventData := api.ObjectReference{
		Kind:      "ServiceInstance",
		Name:      serviceName,
		Namespace: "default", // Service instances use default namespace
	}

	// Prepare additional context for event message templating
	message := gsi.buildEventMessageWithName(serviceName, reason, operation, toolName, err, duration, stepCount)

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
		logging.Error("GenericServiceInstance", err, "Failed to generate event for service instance %s", serviceName)
	}
}

// generateEvent generates a Kubernetes event for this service instance.
// Thread-safe: acquires read lock to get the service name.
func (gsi *GenericServiceInstance) generateEvent(reason string, operation string, toolName string, err error, duration time.Duration, stepCount int) {
	gsi.mu.RLock()
	serviceName := gsi.name
	gsi.mu.RUnlock()

	gsi.generateEventWithName(serviceName, reason, operation, toolName, err, duration, stepCount)
}

// buildEventMessageWithName builds the event message without acquiring locks.
// The caller provides the service name to avoid lock contention.
func (gsi *GenericServiceInstance) buildEventMessageWithName(serviceName string, reason string, operation string, toolName string, err error, duration time.Duration, stepCount int) string {
	// Get serviceClassName under lock for messages that need it
	gsi.mu.RLock()
	serviceClassName := gsi.serviceClassName
	gsi.mu.RUnlock()

	// Build context-aware messages based on the reason
	switch reason {
	case "ServiceInstanceCreated":
		return fmt.Sprintf("Service instance %s created from ServiceClass %s", serviceName, serviceClassName)
	case "ServiceInstanceStarting":
		if toolName != "" {
			return fmt.Sprintf("Service instance %s starting with tool %s", serviceName, toolName)
		}
		return fmt.Sprintf("Service instance %s starting", serviceName)
	case "ServiceInstanceStarted":
		return fmt.Sprintf("Service instance %s started successfully and is running", serviceName)
	case "ServiceInstanceStopping":
		if toolName != "" {
			return fmt.Sprintf("Service instance %s stopping with tool %s", serviceName, toolName)
		}
		return fmt.Sprintf("Service instance %s stopping", serviceName)
	case "ServiceInstanceStopped":
		return fmt.Sprintf("Service instance %s stopped successfully", serviceName)
	case "ServiceInstanceRestarting":
		if toolName != "" {
			return fmt.Sprintf("Service instance %s restarting with tool %s", serviceName, toolName)
		}
		return fmt.Sprintf("Service instance %s restarting", serviceName)
	case "ServiceInstanceRestarted":
		if duration > 0 {
			return fmt.Sprintf("Service instance %s restarted successfully after %s", serviceName, duration.String())
		}
		return fmt.Sprintf("Service instance %s restarted successfully", serviceName)
	case "ServiceInstanceDeleted":
		return fmt.Sprintf("Service instance %s deleted successfully", serviceName)
	case "ServiceInstanceFailed":
		if err != nil {
			return fmt.Sprintf("Service instance %s operation failed: %s", serviceName, err.Error())
		}
		return fmt.Sprintf("Service instance %s operation failed", serviceName)
	case "ServiceInstanceHealthy":
		if stepCount > 0 {
			return fmt.Sprintf("Service instance %s health checks passing (%d consecutive successes)", serviceName, stepCount)
		}
		return fmt.Sprintf("Service instance %s health checks passing", serviceName)
	case "ServiceInstanceUnhealthy":
		if stepCount > 0 {
			return fmt.Sprintf("Service instance %s health checks failing (%d consecutive failures)", serviceName, stepCount)
		}
		return fmt.Sprintf("Service instance %s health checks failing", serviceName)
	case "ServiceInstanceHealthCheckFailed":
		if err != nil {
			return fmt.Sprintf("Service instance %s health check failed: %s", serviceName, err.Error())
		}
		return fmt.Sprintf("Service instance %s health check failed", serviceName)
	case "ServiceInstanceHealthCheckRecovered":
		return fmt.Sprintf("Service instance %s health check recovered after %d failures", serviceName, stepCount)
	case "ServiceInstanceStateChanged":
		return fmt.Sprintf("Service instance %s state changed: %s", serviceName, operation)
	case "ServiceInstanceToolExecutionStarted":
		return fmt.Sprintf("Service instance %s %s tool %s execution started", serviceName, operation, toolName)
	case "ServiceInstanceToolExecutionCompleted":
		return fmt.Sprintf("Service instance %s %s tool %s execution completed successfully", serviceName, operation, toolName)
	case "ServiceInstanceToolExecutionFailed":
		if err != nil {
			return fmt.Sprintf("Service instance %s %s tool %s execution failed: %s", serviceName, operation, toolName, err.Error())
		}
		return fmt.Sprintf("Service instance %s %s tool %s execution failed", serviceName, operation, toolName)
	default:
		return fmt.Sprintf("Service instance %s: %s", serviceName, reason)
	}
}

// buildEventMessage builds the event message for a given reason and context.
// Thread-safe: acquires read lock to get the service name.
func (gsi *GenericServiceInstance) buildEventMessage(reason string, operation string, toolName string, err error, duration time.Duration, stepCount int) string {
	gsi.mu.RLock()
	serviceName := gsi.name
	gsi.mu.RUnlock()

	return gsi.buildEventMessageWithName(serviceName, reason, operation, toolName, err, duration, stepCount)
}

// buildTemplateContext creates the template context for tool argument substitution
func (gsi *GenericServiceInstance) buildTemplateContext() map[string]interface{} {
	// Build context with args nested under "args" key for template usage
	serviceContext := map[string]interface{}{
		"name":             gsi.name,
		"serviceClassName": gsi.serviceClassName,
		"args":             gsi.creationArgs, // Service class templates use {{ .args.repository_url }}
		"service": map[string]interface{}{
			"id":       gsi.name,        // Service ID for templates like {{ .service.id }}
			"name":     gsi.name,        // Service name
			"metadata": gsi.serviceData, // Service metadata for templates like {{ .service.metadata.database_id }}
		},
		// Add tool outputs for template access like {{ .start.sessionID }}
		"start":   make(map[string]interface{}),
		"stop":    make(map[string]interface{}),
		"restart": make(map[string]interface{}),
	}

	// Populate tool outputs from service data
	// This allows templates to reference outputs like {{ .start.sessionID }}
	for key, value := range gsi.serviceData {
		// For now, assume all outputs come from start tool
		// TODO: Track which tool produced which outputs
		if startOutputs, ok := serviceContext["start"].(map[string]interface{}); ok {
			startOutputs[key] = value
		}
	}

	// Merge with creation args at root level for direct template access
	return template.MergeContexts(
		gsi.creationArgs, // Args at root level for direct template access
		serviceContext,   // Structured context with args nested under "args"
	)
}

// updateStateInternal updates the service state and triggers callbacks
// Must be called with mutex held
func (gsi *GenericServiceInstance) updateStateInternal(newState ServiceState, newHealth HealthStatus, err error) {
	oldState := gsi.state
	oldHealth := gsi.health

	// Update state
	gsi.state = newState
	gsi.health = newHealth
	gsi.lastError = err
	gsi.updatedAt = time.Now()

	// Collect event information while holding the mutex
	shouldGenerateStateChangeEvent := oldState != newState || oldHealth != newHealth
	shouldGenerateFailedEvent := newState == StateFailed && oldState != StateFailed
	stateChangeMsg := ""
	serviceName := gsi.name

	if shouldGenerateStateChangeEvent {
		stateChangeMsg = fmt.Sprintf("%s → %s (health: %s → %s)", oldState, newState, oldHealth, newHealth)
	}

	// Trigger callback if state or health changed
	if gsi.stateCallback != nil && shouldGenerateStateChangeEvent {
		// Call callback without holding lock to prevent deadlocks
		go gsi.stateCallback(serviceName, oldState, newState, newHealth, err)
	}

	logging.Debug("GenericServiceInstance", "Service %s state changed: %s -> %s (health: %s -> %s)",
		serviceName, oldState, newState, oldHealth, newHealth)

	// Generate events in a goroutine to avoid holding the lock while making external calls.
	// We use generateEventWithName to pass the already-captured serviceName, avoiding
	// the need to acquire the lock again in the goroutine.
	if shouldGenerateStateChangeEvent {
		go func() {
			gsi.generateEventWithName(serviceName, "ServiceInstanceStateChanged", stateChangeMsg, "", err, 0, 0)

			// Also generate specific state events for major transitions
			if shouldGenerateFailedEvent {
				gsi.generateEventWithName(serviceName, "ServiceInstanceFailed", "state_transition", "", err, 0, 0)
			}
		}()
	}
}

// updateHealthTracking updates the health check tracking counters
// Must be called with mutex held
func (gsi *GenericServiceInstance) updateHealthTracking(success bool, failureThreshold, successThreshold int) {
	if success {
		gsi.healthCheckSuccesses++
		gsi.healthCheckFailures = 0 // Reset failure count on success
	} else {
		gsi.healthCheckFailures++
		gsi.healthCheckSuccesses = 0 // Reset success count on failure
	}
}

// determineHealthFromTracking determines health status based on success/failure tracking
// Must be called with mutex held
func (gsi *GenericServiceInstance) determineHealthFromTracking(failureThreshold, successThreshold int) HealthStatus {
	// If we have enough failures, mark as unhealthy
	if gsi.healthCheckFailures >= failureThreshold {
		return HealthUnhealthy
	}

	// If we have enough successes, mark as healthy
	if gsi.healthCheckSuccesses >= successThreshold {
		return HealthHealthy
	}

	// Otherwise, checking/unknown
	return HealthChecking
}

// executeLifecycleTool executes a generic lifecycle tool (start, stop, etc.)
func (gsi *GenericServiceInstance) executeLifecycleTool(
	ctx context.Context,
	toolType string,
	toolName string,
	args map[string]interface{},
	outputs map[string]string,
) error {
	// Create a shorter timeout context for tool execution to prevent deadlocks
	// when called from workflow execution context
	toolCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Prepare the context for template substitution
	templateContext := gsi.buildTemplateContext()

	// Debug logging
	logging.Debug("GenericServiceInstance", "Template context for %s tool %s: %+v", toolType, toolName, templateContext)
	logging.Debug("GenericServiceInstance", "Raw arguments for %s tool %s: %+v", toolType, toolName, args)

	// Apply template substitution to tool arguments
	processedArgs, err := gsi.templater.Replace(args, templateContext)
	if err != nil {
		err = fmt.Errorf("failed to process %s tool arguments: %w", toolType, err)
		gsi.updateStateInternal(StateFailed, HealthUnhealthy, err)
		return err
	}

	// Debug logging for processed arguments
	logging.Debug("GenericServiceInstance", "Processed arguments for %s tool %s: %+v", toolType, toolName, processedArgs)

	// Ensure tool arguments is a map
	toolArgsMap, ok := processedArgs.(map[string]interface{})
	if !ok {
		err = fmt.Errorf("tool arguments must be a map, got %T", processedArgs)
		gsi.updateStateInternal(StateFailed, HealthUnhealthy, err)
		return err
	}

	// Generate tool execution started event
	gsi.generateEvent("ServiceInstanceToolExecutionStarted", toolType, toolName, nil, 0, 0)

	// Call the lifecycle tool with shorter timeout context
	logging.Debug("GenericServiceInstance", "Calling %s tool %s for service %s", toolType, toolName, gsi.name)
	response, err := gsi.toolCaller.CallTool(toolCtx, toolName, toolArgsMap)
	if err != nil {
		// Generate tool execution failed event
		gsi.generateEvent("ServiceInstanceToolExecutionFailed", toolType, toolName, err, 0, 0)
		gsi.updateStateInternal(StateFailed, HealthUnhealthy, err)
		return fmt.Errorf("%s tool failed: %w", toolType, err)
	}

	logging.Debug("GenericServiceInstance", "Tool %s response: %+v", toolName, response)
	logging.Debug("GenericServiceInstance", "Tool %s outputs config: %+v", toolName, outputs)

	// Process the response and extract outputs
	extractedOutputs := ProcessToolOutputs(response, outputs)
	logging.Debug("GenericServiceInstance", "Extracted outputs from %s tool: %+v", toolName, extractedOutputs)

	// Store outputs in service data for later use in templates
	if extractedOutputs != nil {
		for outputName, value := range extractedOutputs {
			gsi.serviceData[outputName] = value
			logging.Debug("GenericServiceInstance", "Stored output %s=%v in serviceData", outputName, value)
		}
	}

	// Check if tool call was successful
	if success, ok := response["success"].(bool); ok && !success {
		errorMsg := "tool indicated failure"
		if text, exists := response["text"].(string); exists {
			errorMsg = text
		}
		err = fmt.Errorf("%s tool failed: %s", toolType, errorMsg)
		gsi.updateStateInternal(StateFailed, HealthUnhealthy, err)
		return err
	}

	// Mark as running and healthy
	gsi.updateStateInternal(StateRunning, HealthHealthy, nil)

	// Generate tool execution completed event
	gsi.generateEvent("ServiceInstanceToolExecutionCompleted", toolType, toolName, nil, 0, 0)

	logging.Info("GenericServiceInstance", "Successfully %sed service instance: %s", toolType, gsi.name)
	return nil
}
