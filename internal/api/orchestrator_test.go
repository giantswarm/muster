package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// mockOrchestratorService implements Service for testing
type mockOrchestratorService struct {
	name        string
	serviceType ServiceType
	state       ServiceState
	health      HealthStatus
	err         error
	deps        []string
}

func (m *mockOrchestratorService) GetName() string                   { return m.name }
func (m *mockOrchestratorService) GetType() ServiceType              { return m.serviceType }
func (m *mockOrchestratorService) GetState() ServiceState            { return m.state }
func (m *mockOrchestratorService) GetHealth() HealthStatus           { return m.health }
func (m *mockOrchestratorService) GetError() error                   { return m.err }
func (m *mockOrchestratorService) GetDependencies() []string         { return m.deps }
func (m *mockOrchestratorService) Start(ctx context.Context) error   { return nil }
func (m *mockOrchestratorService) Stop(ctx context.Context) error    { return nil }
func (m *mockOrchestratorService) Restart(ctx context.Context) error { return nil }
func (m *mockOrchestratorService) GetLastError() error               { return m.err }
func (m *mockOrchestratorService) SetStateChangeCallback(fn func(old, new ServiceState)) {
}

// orchestratorMockService implements Service for testing
type orchestratorMockService struct {
	name                string
	serviceType         ServiceType
	state               ServiceState
	health              HealthStatus
	dependencies        []string
	startErr            error
	stopErr             error
	restartErr          error
	lastErr             error
	mu                  sync.Mutex
	stateChangeCallback func(name string, oldState, newState ServiceState, health HealthStatus, err error)
}

func (m *orchestratorMockService) GetName() string           { return m.name }
func (m *orchestratorMockService) GetType() ServiceType      { return m.serviceType }
func (m *orchestratorMockService) GetState() ServiceState    { return m.state }
func (m *orchestratorMockService) GetHealth() HealthStatus   { return m.health }
func (m *orchestratorMockService) GetError() error           { return m.lastErr }
func (m *orchestratorMockService) GetDependencies() []string { return m.dependencies }
func (m *orchestratorMockService) GetLastError() error       { return m.lastErr }
func (m *orchestratorMockService) SetStateChangeCallback(cb func(name string, oldState, newState ServiceState, health HealthStatus, err error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stateChangeCallback = cb
}

func (m *orchestratorMockService) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	oldState := m.state

	if m.startErr != nil {
		m.lastErr = m.startErr
		m.state = StateFailed
		if m.stateChangeCallback != nil {
			m.stateChangeCallback(m.name, oldState, m.state, m.health, m.startErr)
		}
		return m.startErr
	}

	// Simulate state transition
	m.state = StateStarting
	if m.stateChangeCallback != nil {
		m.stateChangeCallback(m.name, oldState, m.state, m.health, nil)
	}

	// Simulate successful start
	m.state = StateRunning
	m.health = HealthHealthy
	if m.stateChangeCallback != nil {
		m.stateChangeCallback(m.name, StateStarting, m.state, m.health, nil)
	}

	return nil
}

func (m *orchestratorMockService) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	oldState := m.state

	if m.stopErr != nil {
		m.lastErr = m.stopErr
		return m.stopErr
	}

	m.state = StateStopped
	m.health = HealthUnknown

	if m.stateChangeCallback != nil {
		m.stateChangeCallback(m.name, oldState, m.state, m.health, nil)
	}

	return nil
}

func (m *orchestratorMockService) Restart(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	oldState := m.state

	if m.restartErr != nil {
		m.lastErr = m.restartErr
		return m.restartErr
	}

	// Simulate restart - set to running after restart
	m.state = StateRunning

	if m.stateChangeCallback != nil {
		m.stateChangeCallback(m.name, oldState, m.state, m.health, nil)
	}

	return nil
}

// mockServiceInfo implements ServiceInfo for testing
type mockServiceInfo struct {
	name    string
	svcType ServiceType
	state   ServiceState
	health  HealthStatus
	lastErr error
	data    map[string]interface{}
}

func (m *mockServiceInfo) GetName() string                        { return m.name }
func (m *mockServiceInfo) GetType() ServiceType                   { return m.svcType }
func (m *mockServiceInfo) GetState() ServiceState                 { return m.state }
func (m *mockServiceInfo) GetHealth() HealthStatus                { return m.health }
func (m *mockServiceInfo) GetLastError() error                    { return m.lastErr }
func (m *mockServiceInfo) GetServiceData() map[string]interface{} { return m.data }

// mockServiceRegistryHandler implements ServiceRegistryHandler for testing
type mockServiceRegistryHandler struct {
	services map[string]ServiceInfo
}

func newMockServiceRegistryHandler() *mockServiceRegistryHandler {
	return &mockServiceRegistryHandler{
		services: make(map[string]ServiceInfo),
	}
}

func (m *mockServiceRegistryHandler) Get(name string) (ServiceInfo, bool) {
	svc, ok := m.services[name]
	return svc, ok
}

func (m *mockServiceRegistryHandler) GetAll() []ServiceInfo {
	var result []ServiceInfo
	for _, svc := range m.services {
		result = append(result, svc)
	}
	return result
}

func (m *mockServiceRegistryHandler) GetByType(serviceType ServiceType) []ServiceInfo {
	var result []ServiceInfo
	for _, svc := range m.services {
		if svc.GetType() == serviceType {
			result = append(result, svc)
		}
	}
	return result
}

func (m *mockServiceRegistryHandler) addService(svc ServiceInfo) {
	m.services[svc.GetName()] = svc
}

// mockOrchestratorHandler implements OrchestratorHandler for testing
type mockOrchestratorHandler struct {
	startErr   error
	stopErr    error
	restartErr error
	eventChan  chan ServiceStateChangedEvent
	services   []ServiceStatus
}

func newMockOrchestratorHandler() *mockOrchestratorHandler {
	return &mockOrchestratorHandler{
		eventChan: make(chan ServiceStateChangedEvent, 100),
		services:  []ServiceStatus{},
	}
}

// ToolProvider methods
func (m *mockOrchestratorHandler) GetTools() []ToolMetadata {
	return []ToolMetadata{
		{
			Name:        "test_orchestrator_tool",
			Description: "Test orchestrator tool for mock",
			Parameters:  []ParameterMetadata{},
		},
	}
}

func (m *mockOrchestratorHandler) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*CallToolResult, error) {
	return &CallToolResult{
		Content: []interface{}{"test orchestrator result"},
	}, nil
}

func (m *mockOrchestratorHandler) StartService(name string) error {
	if m.startErr != nil {
		return m.startErr
	}
	// Simulate state change event
	m.eventChan <- ServiceStateChangedEvent{
		Name:        name,
		ServiceType: "test",
		OldState:    string(StateStopped),
		NewState:    string(StateRunning),
		Health:      string(HealthHealthy),
		Timestamp:   time.Now(),
	}
	return nil
}

func (m *mockOrchestratorHandler) StopService(name string) error {
	return m.stopErr
}

func (m *mockOrchestratorHandler) RestartService(name string) error {
	return m.restartErr
}

func (m *mockOrchestratorHandler) SubscribeToStateChanges() <-chan ServiceStateChangedEvent {
	return m.eventChan
}

func (m *mockOrchestratorHandler) GetServiceStatus(name string) (*ServiceStatus, error) {
	for _, s := range m.services {
		if s.Name == name {
			return &s, nil
		}
	}
	return nil, fmt.Errorf("service not found: %s", name)
}

func (m *mockOrchestratorHandler) GetAllServices() []ServiceStatus {
	return m.services
}

// ServiceClass-based dynamic service instance management methods (for test compatibility)
func (m *mockOrchestratorHandler) CreateServiceClassInstance(ctx context.Context, req CreateServiceInstanceRequest) (*ServiceInstance, error) {
	return &ServiceInstance{
		ID:               "test-service-id",
		Name:             req.Name,
		ServiceClassName: req.ServiceClassName,
		ServiceClassType: "test",
		State:            StateStopped,
		Health:           HealthUnknown,
		CreatedAt:        time.Now(),
		ServiceData:      make(map[string]interface{}),
	}, nil
}

func (m *mockOrchestratorHandler) DeleteServiceClassInstance(ctx context.Context, serviceID string) error {
	return nil
}

func (m *mockOrchestratorHandler) GetServiceClassInstance(serviceID string) (*ServiceInstance, error) {
	return &ServiceInstance{
		ID:               serviceID,
		Name:             "test-name",
		ServiceClassName: "test-class",
		ServiceClassType: "test",
		State:            StateRunning,
		Health:           HealthHealthy,
		CreatedAt:        time.Now(),
		ServiceData:      make(map[string]interface{}),
	}, nil
}

func (m *mockOrchestratorHandler) GetServiceClassInstanceByName(name string) (*ServiceInstance, error) {
	return &ServiceInstance{
		ID:               "test-service-id",
		Name:             name,
		ServiceClassName: "test-class",
		ServiceClassType: "test",
		State:            StateRunning,
		Health:           HealthHealthy,
		CreatedAt:        time.Now(),
		ServiceData:      make(map[string]interface{}),
	}, nil
}

func (m *mockOrchestratorHandler) ListServiceClassInstances() []ServiceInstance {
	return []ServiceInstance{
		{
			ID:               "test-service-id-1",
			Name:             "test-name-1",
			ServiceClassName: "test-class",
			ServiceClassType: "test",
			State:            StateRunning,
			Health:           HealthHealthy,
			CreatedAt:        time.Now(),
			ServiceData:      make(map[string]interface{}),
		},
	}
}

func (m *mockOrchestratorHandler) SubscribeToServiceInstanceEvents() <-chan ServiceInstanceEvent {
	eventChan := make(chan ServiceInstanceEvent, 100)
	return eventChan
}

// Add missing ServiceManagerHandler methods
func (m *mockOrchestratorHandler) GetService(name string) (*ServiceInstance, error) {
	return &ServiceInstance{
		ID:               "test-service-id",
		Name:             name,
		ServiceClassName: "test-class",
		ServiceClassType: "test",
		State:            StateRunning,
		Health:           HealthHealthy,
		CreatedAt:        time.Now(),
		ServiceData:      make(map[string]interface{}),
	}, nil
}

func (m *mockOrchestratorHandler) CreateService(ctx context.Context, req CreateServiceInstanceRequest) (*ServiceInstance, error) {
	return &ServiceInstance{
		ID:               "test-service-id",
		Name:             req.Name,
		ServiceClassName: req.ServiceClassName,
		ServiceClassType: "test",
		State:            StateRunning,
		Health:           HealthHealthy,
		CreatedAt:        time.Now(),
		ServiceData:      make(map[string]interface{}),
	}, nil
}

func (m *mockOrchestratorHandler) DeleteService(ctx context.Context, name string) error {
	return nil
}

func TestNewOrchestratorAPI(t *testing.T) {
	// Setup mock handlers
	registry := newMockServiceRegistryHandler()
	RegisterServiceRegistry(registry)
	defer func() {
		RegisterServiceRegistry(nil)
	}()

	api := NewOrchestratorAPI()

	if api == nil {
		t.Error("Expected NewOrchestratorAPI to return non-nil API")
	}

	// Test that it's the correct type
	if _, ok := api.(*orchestratorAPI); !ok {
		t.Error("Expected NewOrchestratorAPI to return *orchestratorAPI type")
	}
}

func TestOrchestratorAPI_StartService(t *testing.T) {
	tests := []struct {
		name        string
		startError  error
		expectError bool
	}{
		{
			name:        "successful start",
			startError:  nil,
			expectError: false,
		},
		{
			name:        "service start error",
			startError:  errors.New("start failed"),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mock handlers
			mockOrch := newMockOrchestratorHandler()
			mockOrch.startErr = tt.startError
			RegisterServiceManager(mockOrch)
			defer func() {
				RegisterServiceManager(nil)
			}()

			api := NewOrchestratorAPI()

			err := api.StartService(tt.name)

			if (err != nil) != tt.expectError {
				t.Errorf("StartService() error = %v, expectError %v", err, tt.expectError)
			}
		})
	}
}

func TestOrchestratorAPI_StopService(t *testing.T) {
	tests := []struct {
		name        string
		stopError   error
		expectError bool
	}{
		{
			name:        "successful stop",
			stopError:   nil,
			expectError: false,
		},
		{
			name:        "service stop error",
			stopError:   errors.New("stop failed"),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mock handlers
			mockOrch := newMockOrchestratorHandler()
			mockOrch.stopErr = tt.stopError
			RegisterServiceManager(mockOrch)
			defer func() {
				RegisterServiceManager(nil)
			}()

			api := NewOrchestratorAPI()

			err := api.StopService(tt.name)

			if (err != nil) != tt.expectError {
				t.Errorf("StopService() error = %v, expectError %v", err, tt.expectError)
			}
		})
	}
}

func TestOrchestratorAPI_RestartService(t *testing.T) {
	tests := []struct {
		name        string
		restartErr  error
		expectError bool
	}{
		{
			name:        "successful restart",
			restartErr:  nil,
			expectError: false,
		},
		{
			name:        "service restart error",
			restartErr:  errors.New("restart failed"),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mock handlers
			mockOrch := newMockOrchestratorHandler()
			mockOrch.restartErr = tt.restartErr
			RegisterServiceManager(mockOrch)
			defer func() {
				RegisterServiceManager(nil)
			}()

			api := NewOrchestratorAPI()

			err := api.RestartService(tt.name)

			if (err != nil) != tt.expectError {
				t.Errorf("RestartService() error = %v, expectError %v", err, tt.expectError)
			}
		})
	}
}

func TestOrchestratorAPI_GetServiceStatus(t *testing.T) {
	// Setup mock handlers
	registry := newMockServiceRegistryHandler()
	RegisterServiceRegistry(registry)
	defer func() {
		RegisterServiceRegistry(nil)
	}()

	// Add a test service
	svc := &mockServiceInfo{
		name:    "test-service",
		svcType: TypeMCPServer,
		state:   StateRunning,
		health:  HealthHealthy,
		lastErr: nil,
		data:    map[string]interface{}{"test": "data"},
	}
	registry.addService(svc)

	// Create API
	api := NewOrchestratorAPI()

	// Test existing service
	status, err := api.GetServiceStatus("test-service")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Check status fields
	if status.Name != "test-service" {
		t.Errorf("Expected name %s, got %s", "test-service", status.Name)
	}
	if status.ServiceType != "MCPServer" {
		t.Errorf("Expected type %s, got %s", "MCPServer", status.ServiceType)
	}
	if status.State != StateRunning {
		t.Errorf("Expected state %s, got %s", StateRunning, status.State)
	}
	if status.Health != HealthHealthy {
		t.Errorf("Expected health %s, got %s", HealthHealthy, status.Health)
	}

	// Test non-existing service
	_, err = api.GetServiceStatus("non-existent")
	if err == nil {
		t.Error("Expected error for non-existent service")
	}
}

func TestOrchestratorAPI_SubscribeToStateChanges(t *testing.T) {
	// Setup mock handlers
	mockOrch := newMockOrchestratorHandler()
	RegisterServiceManager(mockOrch)
	defer func() {
		RegisterServiceManager(nil)
	}()

	api := NewOrchestratorAPI()

	// Subscribe to state changes
	ch := api.SubscribeToStateChanges()

	if ch == nil {
		t.Error("Expected non-nil channel")
	}

	// Trigger a state change by starting a service
	go func() {
		time.Sleep(100 * time.Millisecond)
		mockOrch.StartService("test-service")
	}()

	// Wait for event
	select {
	case event := <-ch:
		t.Logf("Received event: Name=%s, OldState=%s, NewState=%s", event.Name, event.OldState, event.NewState)
		if event.Name != "test-service" {
			t.Errorf("Expected name 'test-service', got %s", event.Name)
		}
		if event.NewState != string(StateRunning) {
			t.Errorf("Expected new state to be Running, got %s", event.NewState)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected to receive state change event")
	}
}

func TestServiceStateChangedEvent_Structure(t *testing.T) {
	// Test that the event structure has all expected fields
	event := ServiceStateChangedEvent{
		Name:        "test",
		ServiceType: "MCPServer",
		OldState:    "stopped",
		NewState:    "running",
		Health:      "healthy",
		Error:       errors.New("test error"),
		Timestamp:   time.Now(),
	}

	if event.Name != "test" {
		t.Errorf("Expected Name 'test', got %s", event.Name)
	}

	if event.ServiceType != "MCPServer" {
		t.Errorf("Expected ServiceType 'MCPServer', got %s", event.ServiceType)
	}

	if event.OldState != "stopped" {
		t.Errorf("Expected OldState 'stopped', got %s", event.OldState)
	}

	if event.NewState != "running" {
		t.Errorf("Expected NewState 'running', got %s", event.NewState)
	}

	if event.Health != "healthy" {
		t.Errorf("Expected Health 'healthy', got %s", event.Health)
	}

	if event.Error == nil || event.Error.Error() != "test error" {
		t.Errorf("Expected Error 'test error', got %v", event.Error)
	}

	if event.Timestamp.IsZero() {
		t.Error("Expected Timestamp to be set")
	}
}

func TestOrchestratorAPI_GetAllServices(t *testing.T) {
	// Setup mock handlers
	registry := newMockServiceRegistryHandler()
	RegisterServiceRegistry(registry)
	defer func() {
		RegisterServiceRegistry(nil)
	}()

	// Add test services
	services := []ServiceInfo{
		&mockServiceInfo{
			name:    "service1",
			svcType: TypeMCPServer,
			state:   StateRunning,
			health:  HealthHealthy,
		},
		&mockServiceInfo{
			name:    "service2",
			svcType: TypeMCPServer,
			state:   StateStopped,
			health:  HealthUnknown,
		},
		&mockServiceInfo{
			name:    "service3",
			svcType: TypeMCPServer,
			state:   StateError,
			health:  HealthUnhealthy,
			lastErr: errors.New("connection failed"),
		},
	}

	for _, svc := range services {
		registry.addService(svc)
	}

	// Create API
	api := NewOrchestratorAPI()

	// Get all services
	statuses := api.GetAllServices()

	if len(statuses) != 3 {
		t.Errorf("Expected 3 services, got %d", len(statuses))
	}

	// Check that all services are included
	foundNames := make(map[string]bool)
	for _, status := range statuses {
		foundNames[status.Name] = true

		// Check error is properly converted
		if status.Name == "service3" && status.Error == "" {
			t.Error("Expected service3 to have error string")
		}
	}

	for _, svc := range services {
		if !foundNames[svc.GetName()] {
			t.Errorf("Service %s not found in results", svc.GetName())
		}
	}
}
