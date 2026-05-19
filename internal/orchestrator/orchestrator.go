package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/internal/services"
	"github.com/giantswarm/muster/pkg/logging"
)

// StopReason tracks why a service was stopped.
type StopReason int

const (
	StopReasonManual StopReason = iota
	StopReasonDependency
)

// Orchestrator manages the lifecycle of static services registered in the
// service registry. MCPServer lifecycle is owned end-to-end by
// internal/reconciler/mcpserver_reconciler.go and agentgateway; the
// orchestrator never spawns MCPServer clients of its own.
type Orchestrator struct {
	registry services.ServiceRegistry

	aggregator config.AggregatorConfig
	yolo       bool

	stopReasons            map[string]StopReason
	stateChangeSubscribers []chan<- ServiceStateChangedEvent

	ctx        context.Context
	cancelFunc context.CancelFunc

	mu sync.RWMutex
}

// Config holds the configuration for the orchestrator.
type Config struct {
	Aggregator config.AggregatorConfig
	Yolo       bool
}

// New creates a new orchestrator.
func New(cfg Config) *Orchestrator {
	return &Orchestrator{
		registry:               services.NewRegistry(),
		aggregator:             cfg.Aggregator,
		yolo:                   cfg.Yolo,
		stopReasons:            make(map[string]StopReason),
		stateChangeSubscribers: make([]chan<- ServiceStateChangedEvent, 0),
	}
}

// Start initializes and starts all registered static services.
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

	logging.Info("Orchestrator", "Started orchestrator with %d static services", len(staticServices))
	return nil
}

// setupStateChangeNotifications wires every registered service to publish
// state-change events through the orchestrator's subscriber list.
func (o *Orchestrator) setupStateChangeNotifications(svcs []services.Service) {
	for _, service := range svcs {
		service.SetStateChangeCallback(o.createStateChangeCallback())
		logging.Debug("Orchestrator", "Set up state change notifications for service: %s", service.GetName())
	}
}

func (o *Orchestrator) createStateChangeCallback() services.StateChangeCallback {
	return func(name string, oldState, newState services.ServiceState, health services.HealthStatus, err error) {
		o.publishStateChangeEvent(name, oldState, newState, health, err)
	}
}

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

// Stop gracefully stops the orchestrator.
func (o *Orchestrator) Stop() error {
	if o.cancelFunc != nil {
		o.cancelFunc()
	}
	return nil
}

// StartService starts a specific static service by name.
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
