package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	mcpserverPkg "github.com/giantswarm/muster/internal/mcpserver"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/internal/services"
	"github.com/giantswarm/muster/internal/services/mcpserver"
	"github.com/giantswarm/muster/pkg/logging"
)

// StopReason tracks why a service was stopped.
type StopReason int

const (
	StopReasonManual StopReason = iota
	StopReasonDependency
)

// RetryInterval is the interval at which the orchestrator checks for failed servers to retry.
const RetryInterval = 30 * time.Second

// MaxConcurrentRetries limits the number of MCPServers that can be retried simultaneously.
// This prevents a "thundering herd" scenario where many failed servers retry at once,
// potentially overwhelming the system or upstream services.
const MaxConcurrentRetries = 5

// Orchestrator manages the lifecycle of static services registered in the
// service registry (MCPServer services and the aggregator service).
type Orchestrator struct {
	registry services.ServiceRegistry

	// Configuration
	aggregator config.AggregatorConfig
	yolo       bool

	// Service tracking
	stopReasons map[string]StopReason

	// State change event subscribers
	stateChangeSubscribers []chan<- ServiceStateChangedEvent

	// Context for cancellation
	ctx        context.Context
	cancelFunc context.CancelFunc

	// WaitGroup for tracking in-flight retry goroutines
	retryWg sync.WaitGroup

	mu sync.RWMutex
}

// Config holds the configuration for the orchestrator.
type Config struct {
	Aggregator config.AggregatorConfig
	Yolo       bool
}

// New creates a new orchestrator.
func New(cfg Config) *Orchestrator {
	registry := services.NewRegistry()

	return &Orchestrator{
		registry:               registry,
		aggregator:             cfg.Aggregator,
		yolo:                   cfg.Yolo,
		stopReasons:            make(map[string]StopReason),
		stateChangeSubscribers: make([]chan<- ServiceStateChangedEvent, 0),
	}
}

// Start initializes and starts all registered static services and creates
// auto-start MCPServer services from MCPServer definitions.
func (o *Orchestrator) Start(ctx context.Context) error {
	o.ctx, o.cancelFunc = context.WithCancel(ctx)

	staticServices := o.registry.GetAll()

	o.setupStateChangeNotifications(staticServices)

	for _, service := range staticServices {
		go func(svc services.Service) {
			if err := svc.Start(o.ctx); err != nil {
				logging.Error("Orchestrator", err, "Failed to start static service: %s", svc.GetName())
			} else {
				logging.Info("Orchestrator", "Started static service: %s", svc.GetName())
			}
		}(service)
	}

	if err := o.processAutoStartMCPServers(ctx); err != nil {
		logging.Error("Orchestrator", err, "Failed to process auto-start MCPServers")
	}

	go o.retryFailedMCPServers()

	logging.Info("Orchestrator", "Started orchestrator with %d static services", len(staticServices))
	return nil
}

// processAutoStartMCPServers creates and registers MCPServer services for every
// MCPServer definition that has AutoStart=true.
func (o *Orchestrator) processAutoStartMCPServers(ctx context.Context) error {
	mcpServerMgr := api.GetMCPServerManager()
	if mcpServerMgr == nil {
		logging.Debug("Orchestrator", "MCPServerManager not available through API, skipping MCPServer service creation")
		return nil
	}

	mcpServers := mcpServerMgr.ListMCPServers()
	logging.Info("Orchestrator", "Found %d MCPServer definitions for auto-start processing", len(mcpServers))

	for _, mcpServerInfo := range mcpServers {
		if !mcpServerInfo.AutoStart {
			logging.Debug("Orchestrator", "Skipping MCPServer %s: AutoStart=false", mcpServerInfo.Name)
			continue
		}

		if err := o.createMCPServerService(ctx, mcpServerInfo); err != nil {
			logging.Error("Orchestrator", err, "Failed to create MCPServer service: %s", mcpServerInfo.Name)
		}
	}

	return nil
}

// createMCPServerService creates an MCPServer service from MCPServerInfo and registers it.
func (o *Orchestrator) createMCPServerService(ctx context.Context, mcpServerInfo api.MCPServerInfo) error {
	logging.Info("Orchestrator", "Creating MCPServer service: %s", mcpServerInfo.Name)

	apiDef := &api.MCPServer{
		Name:        mcpServerInfo.Name,
		Type:        api.MCPServerType(mcpServerInfo.Type),
		Description: mcpServerInfo.Description,
		ToolPrefix:  mcpServerInfo.ToolPrefix,
		AutoStart:   mcpServerInfo.AutoStart,
		Command:     mcpServerInfo.Command,
		Args:        mcpServerInfo.Args,
		URL:         mcpServerInfo.URL,
		Env:         mcpServerInfo.Env,
		Headers:     mcpServerInfo.Headers,
		Timeout:     mcpServerInfo.Timeout,
		Auth:        mcpServerInfo.Auth,
	}

	mcpService, err := mcpserver.NewService(apiDef)
	if err != nil {
		return fmt.Errorf("failed to create MCPServer service: %w", err)
	}

	mcpService.SetStateChangeCallback(o.createStateChangeCallback())

	if err := o.registry.Register(mcpService); err != nil {
		return fmt.Errorf("failed to register MCPServer service: %w", err)
	}

	// Start the service immediately since the orchestrator's Start() method
	// has already started static services and won't start newly registered ones
	go func() {
		if err := mcpService.Start(ctx); err != nil {
			var authErr *mcpserverPkg.AuthRequiredError
			if errors.As(err, &authErr) {
				logging.Info("Orchestrator", "MCPServer %s requires authentication, registering pending auth", mcpServerInfo.Name)
				o.handleAuthRequiredServer(mcpServerInfo, authErr)
				return
			}
			logging.Error("Orchestrator", err, "Failed to start MCPServer service: %s", mcpServerInfo.Name)
		} else {
			logging.Info("Orchestrator", "Started MCPServer service: %s", mcpServerInfo.Name)
		}
	}()

	logging.Info("Orchestrator", "Successfully created and registered MCPServer service: %s", mcpServerInfo.Name)
	return nil
}

// handleAuthRequiredServer registers a server that requires OAuth authentication
// with the aggregator in pending auth state.
func (o *Orchestrator) handleAuthRequiredServer(mcpServerInfo api.MCPServerInfo, authErr *mcpserverPkg.AuthRequiredError) {
	aggregator := api.GetAggregator()
	if aggregator == nil {
		logging.Error("Orchestrator", nil, "Aggregator not available to register pending auth server: %s", mcpServerInfo.Name)
		return
	}

	authInfo := &api.AuthInfo{
		Issuer:              authErr.AuthInfo.Issuer,
		Scope:               authErr.AuthInfo.Scope,
		ResourceMetadataURL: authErr.AuthInfo.ResourceMetadataURL,
	}

	if err := aggregator.RegisterServerPendingAuth(api.PendingAuthRegistration{
		Name:       mcpServerInfo.Name,
		URL:        mcpServerInfo.URL,
		ToolPrefix: mcpServerInfo.ToolPrefix,
		Family:     mcpServerInfo.Family,
		AuthInfo:   authInfo,
		AuthConfig: mcpServerInfo.Auth,
	}); err != nil {
		logging.Error("Orchestrator", err, "Failed to register pending auth server: %s", mcpServerInfo.Name)
		return
	}

	if mcpServerInfo.Auth != nil && mcpServerInfo.Auth.ForwardToken {
		logging.Info("Orchestrator", "Registered MCPServer %s in pending auth state (SSO token forwarding enabled)", mcpServerInfo.Name)
	} else {
		logging.Info("Orchestrator", "Registered MCPServer %s in pending auth state with synthetic auth tool", mcpServerInfo.Name)
	}
}

// setupStateChangeNotifications configures services to notify the orchestrator of state changes.
func (o *Orchestrator) setupStateChangeNotifications(svcs []services.Service) {
	for _, service := range svcs {
		service.SetStateChangeCallback(o.createStateChangeCallback())
		logging.Debug("Orchestrator", "Set up state change notifications for service: %s", service.GetName())
	}
}

// createStateChangeCallback creates a state change callback that publishes events.
func (o *Orchestrator) createStateChangeCallback() services.StateChangeCallback {
	return func(name string, oldState, newState services.ServiceState, health services.HealthStatus, err error) {
		o.publishStateChangeEvent(name, oldState, newState, health, err)
	}
}

// publishStateChangeEvent publishes a state change event to all subscribers.
func (o *Orchestrator) publishStateChangeEvent(name string, oldState, newState services.ServiceState, health services.HealthStatus, err error) {
	service, exists := o.registry.Get(name)
	if !exists {
		return
	}

	logging.Debug("Orchestrator", "Service %s state changed: %s -> %s (health: %s)", name, oldState, newState, health)

	event := ServiceStateChangedEvent{
		Name:        name,
		ServiceType: string(service.GetType()),
		OldState:    string(oldState),
		NewState:    string(newState),
		Health:      string(health),
		Error:       err,
		Timestamp:   time.Now().Unix(),
	}

	o.mu.RLock()
	subscribers := make([]chan<- ServiceStateChangedEvent, len(o.stateChangeSubscribers))
	copy(subscribers, o.stateChangeSubscribers)
	o.mu.RUnlock()

	for _, subscriber := range subscribers {
		select {
		case subscriber <- event:
		default:
			logging.Debug("Orchestrator", "Subscriber blocked, skipping event for service %s", name)
		}
	}
}

// Stop gracefully stops all services.
func (o *Orchestrator) Stop() error {
	if o.cancelFunc != nil {
		o.cancelFunc()
	}
	return nil
}

// retryFailedMCPServers runs a periodic background task that attempts to reconnect
// MCPServers that have failed due to transient connectivity issues.
// It respects the exponential backoff calculated by the service.
func (o *Orchestrator) retryFailedMCPServers() {
	ticker := time.NewTicker(RetryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-o.ctx.Done():
			logging.Debug("Orchestrator", "Stopping failed MCPServer retry loop, waiting for in-flight retries")
			o.retryWg.Wait()
			logging.Debug("Orchestrator", "All in-flight retries completed")
			return
		case <-ticker.C:
			o.attemptReconnectFailedServers()
		}
	}
}

// attemptReconnectFailedServers checks all MCPServer services for failed/unreachable
// ones and attempts to reconnect them if their backoff period has expired.
// It limits concurrent retries to MaxConcurrentRetries to prevent thundering herd.
func (o *Orchestrator) attemptReconnectFailedServers() {
	mcpServers := o.registry.GetByType(services.TypeMCPServer)

	var eligibleServices []services.Service
	for _, svc := range mcpServers {
		if o.shouldAttemptRetry(svc) {
			eligibleServices = append(eligibleServices, svc)
		}
	}

	retryCount := len(eligibleServices)
	if retryCount > MaxConcurrentRetries {
		logging.Info("Orchestrator", "Limiting retry batch from %d to %d services (MaxConcurrentRetries)", retryCount, MaxConcurrentRetries)
		eligibleServices = eligibleServices[:MaxConcurrentRetries]
	}

	for _, svc := range eligibleServices {
		logging.Info("Orchestrator", "Attempting to reconnect failed MCPServer: %s (backoff expired)", svc.GetName())

		o.retryWg.Add(1)
		go func(service services.Service) {
			defer o.retryWg.Done()

			if o.ctx.Err() != nil {
				logging.Debug("Orchestrator", "Context cancelled, skipping retry for %s", service.GetName())
				return
			}

			if err := service.Restart(o.ctx); err != nil {
				logging.Warn("Orchestrator", "Failed to reconnect MCPServer %s: %v (will retry after backoff)", service.GetName(), err)
			} else {
				logging.Info("Orchestrator", "Successfully reconnected MCPServer: %s", service.GetName())
			}
		}(svc)
	}
}

// shouldAttemptRetry checks if a service should be retried based on its state and backoff timing.
// Returns true if the service is in a failed/unreachable state and its backoff period has expired.
func (o *Orchestrator) shouldAttemptRetry(svc services.Service) bool {
	state := svc.GetState()

	if state != services.StateFailed && state != services.StateUnreachable {
		return false
	}

	dataProvider, ok := svc.(services.ServiceDataProvider)
	if !ok {
		return false
	}

	serviceData := dataProvider.GetServiceData()
	if serviceData == nil {
		return false
	}

	nextRetryRaw, hasRetry := serviceData["nextRetryAfter"]
	if !hasRetry {
		logging.Debug("Orchestrator", "No retry backoff set for %s, skipping automatic retry", svc.GetName())
		return false
	}

	nextRetry, ok := nextRetryRaw.(time.Time)
	if !ok {
		logging.Debug("Orchestrator", "Invalid nextRetryAfter type for %s, skipping", svc.GetName())
		return false
	}

	if time.Now().Before(nextRetry) {
		logging.Debug("Orchestrator", "Backoff not expired for %s (retry after %v)", svc.GetName(), nextRetry)
		return false
	}

	return true
}

// StartService starts a specific service by name.
// For MCP servers, this method waits for the server to be fully registered
// with the aggregator before returning, ensuring that tools are available.
func (o *Orchestrator) StartService(name string) error {
	service, exists := o.registry.Get(name)
	if !exists {
		return fmt.Errorf("service %s not found", name)
	}

	if err := service.Start(o.ctx); err != nil {
		return fmt.Errorf("failed to start service %s: %w", name, err)
	}

	if service.GetType() == services.TypeMCPServer {
		if err := o.waitForMCPServerRegistration(name); err != nil {
			logging.Warn("Orchestrator", "MCP server %s started but registration wait failed: %v", name, err)
		}
	}

	logging.Info("Orchestrator", "Started service: %s", name)
	return nil
}

// waitForMCPServerRegistration waits for an MCP server to be registered with the aggregator.
func (o *Orchestrator) waitForMCPServerRegistration(serverName string) error {
	aggregator := api.GetAggregator()
	if aggregator == nil {
		return fmt.Errorf("aggregator not available")
	}

	timeout := 5 * time.Second
	interval := 50 * time.Millisecond
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		availableTools := aggregator.GetAvailableTools()
		prefix := "x_" + serverName + "_"

		for _, tool := range availableTools {
			if len(tool) > len(prefix) && tool[:len(prefix)] == prefix {
				logging.Debug("Orchestrator", "MCP server %s registered with aggregator (found tool %s)", serverName, tool)
				return nil
			}
		}

		time.Sleep(interval)
	}

	return fmt.Errorf("timeout waiting for MCP server %s to register with aggregator", serverName)
}

// StopService stops a specific service by name.
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

// RestartService restarts a specific service by name.
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

// GetAllServices returns status for all services.
func (o *Orchestrator) GetAllServices() []ServiceStatus {
	svcs := o.registry.GetAll()
	statuses := make([]ServiceStatus, len(svcs))

	for i, service := range svcs {
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
