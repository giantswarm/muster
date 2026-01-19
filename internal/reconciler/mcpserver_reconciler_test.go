package reconciler

import (
	"context"
	"fmt"
	"testing"

	"muster/internal/api"
)

// mcprMockMCPServerManager implements MCPServerManager for testing.
// The mcpr prefix distinguishes it from other mock types in the package.
type mcprMockMCPServerManager struct {
	mcpServers map[string]*api.MCPServerInfo
}

func newMCPRMockMCPServerManager() *mcprMockMCPServerManager {
	return &mcprMockMCPServerManager{
		mcpServers: make(map[string]*api.MCPServerInfo),
	}
}

func (m *mcprMockMCPServerManager) ListMCPServers() []api.MCPServerInfo {
	result := make([]api.MCPServerInfo, 0, len(m.mcpServers))
	for _, server := range m.mcpServers {
		result = append(result, *server)
	}
	return result
}

func (m *mcprMockMCPServerManager) GetMCPServer(name string) (*api.MCPServerInfo, error) {
	server, ok := m.mcpServers[name]
	if !ok {
		return nil, fmt.Errorf("MCPServer %s not found", name)
	}
	return server, nil
}

func (m *mcprMockMCPServerManager) AddMCPServer(server *api.MCPServerInfo) {
	m.mcpServers[server.Name] = server
}

func (m *mcprMockMCPServerManager) RemoveMCPServer(name string) {
	delete(m.mcpServers, name)
}

// mcprMockOrchestratorAPI implements api.OrchestratorAPI for testing.
// The mcpr prefix distinguishes it from the mockOrchestratorAPI in state_change_bridge_test.go.
type mcprMockOrchestratorAPI struct {
	startedServices   map[string]bool
	stoppedServices   map[string]bool
	restartedServices map[string]bool
	startError        error
	stopError         error
	restartError      error
	stateChangeChan   chan api.ServiceStateChangedEvent
}

func newMCPRMockOrchestratorAPI() *mcprMockOrchestratorAPI {
	return &mcprMockOrchestratorAPI{
		startedServices:   make(map[string]bool),
		stoppedServices:   make(map[string]bool),
		restartedServices: make(map[string]bool),
		stateChangeChan:   make(chan api.ServiceStateChangedEvent, 10),
	}
}

func (m *mcprMockOrchestratorAPI) StartService(name string) error {
	if m.startError != nil {
		return m.startError
	}
	m.startedServices[name] = true
	return nil
}

func (m *mcprMockOrchestratorAPI) StopService(name string) error {
	if m.stopError != nil {
		return m.stopError
	}
	m.stoppedServices[name] = true
	return nil
}

func (m *mcprMockOrchestratorAPI) RestartService(name string) error {
	if m.restartError != nil {
		return m.restartError
	}
	m.restartedServices[name] = true
	return nil
}

func (m *mcprMockOrchestratorAPI) GetServiceStatus(name string) (*api.ServiceStatus, error) {
	return &api.ServiceStatus{Name: name, State: api.StateRunning}, nil
}

func (m *mcprMockOrchestratorAPI) GetAllServices() []api.ServiceStatus {
	return nil
}

func (m *mcprMockOrchestratorAPI) SubscribeToStateChanges() <-chan api.ServiceStateChangedEvent {
	return m.stateChangeChan
}

// mcprMockServiceRegistry implements api.ServiceRegistryHandler for testing.
type mcprMockServiceRegistry struct {
	services map[string]*mcprMockServiceInfo
}

func newMCPRMockServiceRegistry() *mcprMockServiceRegistry {
	return &mcprMockServiceRegistry{
		services: make(map[string]*mcprMockServiceInfo),
	}
}

func (m *mcprMockServiceRegistry) Get(name string) (api.ServiceInfo, bool) {
	service, ok := m.services[name]
	if !ok {
		return nil, false
	}
	return service, true
}

func (m *mcprMockServiceRegistry) GetAll() []api.ServiceInfo {
	result := make([]api.ServiceInfo, 0, len(m.services))
	for _, svc := range m.services {
		result = append(result, svc)
	}
	return result
}

func (m *mcprMockServiceRegistry) GetByType(serviceType api.ServiceType) []api.ServiceInfo {
	result := make([]api.ServiceInfo, 0)
	for _, svc := range m.services {
		if svc.GetType() == serviceType {
			result = append(result, svc)
		}
	}
	return result
}

func (m *mcprMockServiceRegistry) AddService(name string, info *mcprMockServiceInfo) {
	m.services[name] = info
}

// mcprMockServiceInfo implements api.ServiceInfo for testing.
type mcprMockServiceInfo struct {
	name        string
	serviceType api.ServiceType
	state       api.ServiceState
	health      api.HealthStatus
	lastError   error
	serviceData map[string]interface{}
}

func (m *mcprMockServiceInfo) GetName() string {
	return m.name
}

func (m *mcprMockServiceInfo) GetType() api.ServiceType {
	return m.serviceType
}

func (m *mcprMockServiceInfo) GetState() api.ServiceState {
	return m.state
}

func (m *mcprMockServiceInfo) GetHealth() api.HealthStatus {
	return m.health
}

func (m *mcprMockServiceInfo) GetLastError() error {
	return m.lastError
}

func (m *mcprMockServiceInfo) GetServiceData() map[string]interface{} {
	return m.serviceData
}

func (m *mcprMockServiceInfo) UpdateConfiguration(cfg *api.MCPServer) error {
	// Update service data with new configuration
	m.serviceData["url"] = cfg.URL
	m.serviceData["command"] = cfg.Command
	m.serviceData["type"] = string(cfg.Type)
	m.serviceData["autoStart"] = cfg.AutoStart
	return nil
}

func TestMCPServerReconciler_GetResourceType(t *testing.T) {
	mgr := newMCPRMockMCPServerManager()
	orchAPI := newMCPRMockOrchestratorAPI()
	registry := newMCPRMockServiceRegistry()
	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	if reconciler.GetResourceType() != ResourceTypeMCPServer {
		t.Errorf("expected ResourceTypeMCPServer, got %s", reconciler.GetResourceType())
	}
}

func TestMCPServerReconciler_ReconcileCreate(t *testing.T) {
	mgr := newMCPRMockMCPServerManager()
	orchAPI := newMCPRMockOrchestratorAPI()
	registry := newMCPRMockServiceRegistry()
	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	// Add a valid MCPServer with AutoStart enabled
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "test-command",
		AutoStart: true,
	})

	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	// Verify service was started
	if !orchAPI.startedServices["test-server"] {
		t.Error("expected service to be started")
	}
}

func TestMCPServerReconciler_ReconcileCreateNoAutoStart(t *testing.T) {
	mgr := newMCPRMockMCPServerManager()
	orchAPI := newMCPRMockOrchestratorAPI()
	registry := newMCPRMockServiceRegistry()
	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	// Add a valid MCPServer with AutoStart disabled
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "test-command",
		AutoStart: false,
	})

	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	// Verify service was NOT started (AutoStart is false)
	if orchAPI.startedServices["test-server"] {
		t.Error("service should not be started when AutoStart is false")
	}
}

func TestMCPServerReconciler_ReconcileDelete(t *testing.T) {
	mgr := newMCPRMockMCPServerManager()
	orchAPI := newMCPRMockOrchestratorAPI()
	registry := newMCPRMockServiceRegistry()

	// Add service to registry to simulate it exists
	registry.AddService("deleted-server", &mcprMockServiceInfo{
		name:        "deleted-server",
		serviceType: api.TypeMCPServer,
		state:       api.StateRunning,
		health:      api.HealthHealthy,
	})

	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	// Do not add the MCPServer to manager - simulate a delete scenario
	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "deleted-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error for delete: %v", result.Error)
	}

	// Verify service was stopped
	if !orchAPI.stoppedServices["deleted-server"] {
		t.Error("expected service to be stopped on delete")
	}
}

func TestMCPServerReconciler_ReconcileDeleteNotFound(t *testing.T) {
	mgr := newMCPRMockMCPServerManager()
	orchAPI := newMCPRMockOrchestratorAPI()
	registry := newMCPRMockServiceRegistry()
	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	// Do not add the MCPServer to manager or registry - nothing to delete
	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "nonexistent-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error for nonexistent delete: %v", result.Error)
	}
	if result.Requeue {
		t.Error("expected no requeue for nonexistent resource")
	}
}

func TestMCPServerReconciler_ReconcileUpdate(t *testing.T) {
	mgr := newMCPRMockMCPServerManager()
	orchAPI := newMCPRMockOrchestratorAPI()
	registry := newMCPRMockServiceRegistry()

	// Add existing service with old configuration
	registry.AddService("test-server", &mcprMockServiceInfo{
		name:        "test-server",
		serviceType: api.TypeMCPServer,
		state:       api.StateRunning,
		health:      api.HealthHealthy,
		serviceData: map[string]interface{}{
			"url":       "http://old-url",
			"command":   "old-command",
			"type":      "stdio",
			"autoStart": true,
		},
	})

	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	// Add MCPServer with new configuration
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "new-command", // Changed
		AutoStart: true,
	})

	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	// Verify service was restarted due to config change
	if !orchAPI.restartedServices["test-server"] {
		t.Error("expected service to be restarted on config change")
	}
}

func TestMCPServerReconciler_ReconcileUpdateNoChange(t *testing.T) {
	mgr := newMCPRMockMCPServerManager()
	orchAPI := newMCPRMockOrchestratorAPI()
	registry := newMCPRMockServiceRegistry()

	// Add existing service with same configuration
	registry.AddService("test-server", &mcprMockServiceInfo{
		name:        "test-server",
		serviceType: api.TypeMCPServer,
		state:       api.StateRunning,
		health:      api.HealthHealthy,
		serviceData: map[string]interface{}{
			"url":       "",
			"command":   "test-command",
			"type":      "stdio",
			"autoStart": true,
		},
	})

	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	// Add MCPServer with same configuration
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "test-command",
		AutoStart: true,
	})

	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	// Verify service was NOT restarted (no config change)
	if orchAPI.restartedServices["test-server"] {
		t.Error("service should not be restarted when config is unchanged")
	}
}

func TestMCPServerReconciler_ReconcileStartError(t *testing.T) {
	mgr := newMCPRMockMCPServerManager()
	orchAPI := newMCPRMockOrchestratorAPI()
	registry := newMCPRMockServiceRegistry()

	// Simulate start error
	orchAPI.startError = fmt.Errorf("service not found in orchestrator")

	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "test-command",
		AutoStart: true,
	})

	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error == nil {
		t.Error("expected error when start fails")
	}
	if !result.Requeue {
		t.Error("expected requeue on start error")
	}
}

func TestMCPServerReconciler_NeedsRestart(t *testing.T) {
	mgr := newMCPRMockMCPServerManager()
	orchAPI := newMCPRMockOrchestratorAPI()
	registry := newMCPRMockServiceRegistry()
	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	tests := []struct {
		name         string
		desired      *api.MCPServerInfo
		serviceData  map[string]interface{}
		expectChange bool
	}{
		{
			name: "no change",
			desired: &api.MCPServerInfo{
				Name:      "test",
				Type:      "stdio",
				Command:   "cmd",
				AutoStart: true,
			},
			serviceData: map[string]interface{}{
				"url":       "",
				"command":   "cmd",
				"type":      "stdio",
				"autoStart": true,
			},
			expectChange: false,
		},
		{
			name: "command changed",
			desired: &api.MCPServerInfo{
				Name:      "test",
				Type:      "stdio",
				Command:   "new-cmd",
				AutoStart: true,
			},
			serviceData: map[string]interface{}{
				"url":       "",
				"command":   "old-cmd",
				"type":      "stdio",
				"autoStart": true,
			},
			expectChange: true,
		},
		{
			name: "url changed",
			desired: &api.MCPServerInfo{
				Name:      "test",
				Type:      "streamable-http",
				URL:       "http://new-url",
				AutoStart: true,
			},
			serviceData: map[string]interface{}{
				"url":       "http://old-url",
				"command":   "",
				"type":      "streamable-http",
				"autoStart": true,
			},
			expectChange: true,
		},
		{
			name: "type changed",
			desired: &api.MCPServerInfo{
				Name:      "test",
				Type:      "streamable-http",
				AutoStart: true,
			},
			serviceData: map[string]interface{}{
				"url":       "",
				"command":   "",
				"type":      "stdio",
				"autoStart": true,
			},
			expectChange: true,
		},
		{
			name: "autostart enabled",
			desired: &api.MCPServerInfo{
				Name:      "test",
				Type:      "stdio",
				AutoStart: true,
			},
			serviceData: map[string]interface{}{
				"url":       "",
				"command":   "",
				"type":      "stdio",
				"autoStart": false,
			},
			expectChange: true,
		},
		{
			name: "nil service data",
			desired: &api.MCPServerInfo{
				Name:      "test",
				Type:      "stdio",
				AutoStart: true,
			},
			serviceData:  nil,
			expectChange: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svcInfo := &mcprMockServiceInfo{
				name:        "test",
				serviceType: api.TypeMCPServer,
				state:       api.StateRunning,
				serviceData: tt.serviceData,
			}

			needsRestart := reconciler.needsRestart(tt.desired, svcInfo)

			if needsRestart != tt.expectChange {
				t.Errorf("needsRestart() = %v, expected %v", needsRestart, tt.expectChange)
			}
		})
	}
}

func TestMCPServerReconciler_PeriodicRequeue(t *testing.T) {
	mgr := newMCPRMockMCPServerManager()
	orchAPI := newMCPRMockOrchestratorAPI()
	registry := newMCPRMockServiceRegistry()

	// Add existing service (no config change)
	registry.AddService("test-server", &mcprMockServiceInfo{
		name:        "test-server",
		serviceType: api.TypeMCPServer,
		state:       api.StateRunning,
		health:      api.HealthHealthy,
		serviceData: map[string]interface{}{
			"url":       "",
			"command":   "test-command",
			"type":      "stdio",
			"autoStart": true,
		},
	})

	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "test-command",
		AutoStart: true,
	})

	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	// Verify RequeueAfter is set for periodic status sync
	if result.RequeueAfter == 0 {
		t.Error("expected RequeueAfter to be set for periodic status sync")
	}

	if result.RequeueAfter != DefaultStatusSyncInterval {
		t.Errorf("expected RequeueAfter = %v, got %v", DefaultStatusSyncInterval, result.RequeueAfter)
	}
}

func TestMCPServerReconciler_ArgsChange(t *testing.T) {
	mgr := newMCPRMockMCPServerManager()
	orchAPI := newMCPRMockOrchestratorAPI()
	registry := newMCPRMockServiceRegistry()

	// Add existing service with args
	registry.AddService("test-server", &mcprMockServiceInfo{
		name:        "test-server",
		serviceType: api.TypeMCPServer,
		state:       api.StateRunning,
		health:      api.HealthHealthy,
		serviceData: map[string]interface{}{
			"url":       "",
			"command":   "test-command",
			"type":      "stdio",
			"autoStart": true,
			"args":      []string{"old-arg1", "old-arg2"},
		},
	})

	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	// Add MCPServer with different args
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "test-command",
		Args:      []string{"new-arg1", "new-arg2"},
		AutoStart: true,
	})

	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	// Verify service was restarted due to args change
	if !orchAPI.restartedServices["test-server"] {
		t.Error("expected service to be restarted when args change")
	}
}
