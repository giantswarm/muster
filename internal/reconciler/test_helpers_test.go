package reconciler

import (
	"context"
	"fmt"
	"sync"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
)

// =============================================================================
// MockOrchestratorAPI - Shared orchestrator API mock for all tests
// =============================================================================

// MockOrchestratorAPI implements api.OrchestratorAPI for testing.
// It provides configurable behavior for service lifecycle operations.
type MockOrchestratorAPI struct {
	mu sync.Mutex

	// Tracking maps for verifying operations
	StartedServices   map[string]bool
	StoppedServices   map[string]bool
	RestartedServices map[string]bool

	// Configurable errors for testing error paths
	StartError   error
	StopError    error
	RestartError error

	// Event channel for state change subscription
	EventChan chan api.ServiceStateChangedEvent

	// Service statuses for GetServiceStatus
	ServiceStatuses map[string]*api.ServiceStatus
}

// NewMockOrchestratorAPI creates a new mock orchestrator API.
func NewMockOrchestratorAPI() *MockOrchestratorAPI {
	return &MockOrchestratorAPI{
		StartedServices:   make(map[string]bool),
		StoppedServices:   make(map[string]bool),
		RestartedServices: make(map[string]bool),
		EventChan:         make(chan api.ServiceStateChangedEvent, 100),
		ServiceStatuses:   make(map[string]*api.ServiceStatus),
	}
}

func (m *MockOrchestratorAPI) StartService(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.StartError != nil {
		return m.StartError
	}
	m.StartedServices[name] = true
	return nil
}

func (m *MockOrchestratorAPI) StopService(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.StopError != nil {
		return m.StopError
	}
	m.StoppedServices[name] = true
	return nil
}

func (m *MockOrchestratorAPI) RestartService(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.RestartError != nil {
		return m.RestartError
	}
	m.RestartedServices[name] = true
	return nil
}

func (m *MockOrchestratorAPI) GetServiceStatus(name string) (*api.ServiceStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if status, ok := m.ServiceStatuses[name]; ok {
		return status, nil
	}
	return &api.ServiceStatus{Name: name, State: api.StateRunning}, nil
}

func (m *MockOrchestratorAPI) GetAllServices() []api.ServiceStatus {
	return nil
}

func (m *MockOrchestratorAPI) SubscribeToStateChanges() <-chan api.ServiceStateChangedEvent {
	return m.EventChan
}

// SendEvent sends a state change event (for testing).
func (m *MockOrchestratorAPI) SendEvent(event api.ServiceStateChangedEvent) {
	m.EventChan <- event
}

// Close closes the event channel (for testing shutdown scenarios).
func (m *MockOrchestratorAPI) Close() {
	close(m.EventChan)
}

// =============================================================================
// MockMCPServerManager - Mock for MCPServer management
// =============================================================================

// MockMCPServerManager implements MCPServerManager for testing.
type MockMCPServerManager struct {
	mu         sync.Mutex
	mcpServers map[string]*api.MCPServerInfo
}

// NewMockMCPServerManager creates a new mock MCPServer manager.
func NewMockMCPServerManager() *MockMCPServerManager {
	return &MockMCPServerManager{
		mcpServers: make(map[string]*api.MCPServerInfo),
	}
}

func (m *MockMCPServerManager) ListMCPServers() []api.MCPServerInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]api.MCPServerInfo, 0, len(m.mcpServers))
	for _, server := range m.mcpServers {
		result = append(result, *server)
	}
	return result
}

func (m *MockMCPServerManager) GetMCPServer(name string) (*api.MCPServerInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	server, ok := m.mcpServers[name]
	if !ok {
		return nil, fmt.Errorf("MCPServer %s not found", name)
	}
	return server, nil
}

// AddMCPServer adds an MCPServer to the mock (for test setup).
func (m *MockMCPServerManager) AddMCPServer(server *api.MCPServerInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mcpServers[server.Name] = server
}

// RemoveMCPServer removes an MCPServer from the mock (for test setup).
func (m *MockMCPServerManager) RemoveMCPServer(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.mcpServers, name)
}

// =============================================================================
// MockServiceRegistry - Mock for service registry
// =============================================================================

// MockServiceRegistry implements api.ServiceRegistryHandler for testing.
type MockServiceRegistry struct {
	mu       sync.Mutex
	services map[string]*MockServiceInfo
}

// NewMockServiceRegistry creates a new mock service registry.
func NewMockServiceRegistry() *MockServiceRegistry {
	return &MockServiceRegistry{
		services: make(map[string]*MockServiceInfo),
	}
}

func (m *MockServiceRegistry) Get(name string) (api.ServiceInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	service, ok := m.services[name]
	if !ok {
		return nil, false
	}
	return service, true
}

func (m *MockServiceRegistry) GetAll() []api.ServiceInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]api.ServiceInfo, 0, len(m.services))
	for _, svc := range m.services {
		result = append(result, svc)
	}
	return result
}

func (m *MockServiceRegistry) GetByType(serviceType api.ServiceType) []api.ServiceInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]api.ServiceInfo, 0)
	for _, svc := range m.services {
		if svc.GetType() == serviceType {
			result = append(result, svc)
		}
	}
	return result
}

// AddService adds a service to the mock registry (for test setup).
func (m *MockServiceRegistry) AddService(name string, info *MockServiceInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.services[name] = info
}

// RemoveService removes a service from the mock registry (for test setup).
func (m *MockServiceRegistry) RemoveService(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.services, name)
}

// =============================================================================
// MockServiceInfo - Mock for service information
// =============================================================================

// MockServiceInfo implements api.ServiceInfo and api.ConfigurableService for testing.
type MockServiceInfo struct {
	mu          sync.Mutex
	Name        string
	ServiceType api.ServiceType
	State       api.ServiceState
	Health      api.HealthStatus
	LastError   error
	ServiceData map[string]interface{}

	// Track configuration updates
	ConfigUpdateCalled bool
	LastConfig         *api.MCPServer
}

func (m *MockServiceInfo) GetName() string {
	return m.Name
}

func (m *MockServiceInfo) GetType() api.ServiceType {
	return m.ServiceType
}

func (m *MockServiceInfo) GetState() api.ServiceState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.State
}

func (m *MockServiceInfo) GetHealth() api.HealthStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Health
}

func (m *MockServiceInfo) GetLastError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.LastError
}

func (m *MockServiceInfo) GetServiceData() map[string]interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ServiceData
}

// UpdateConfiguration implements api.ConfigurableService.
func (m *MockServiceInfo) UpdateConfiguration(cfg *api.MCPServer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ConfigUpdateCalled = true
	m.LastConfig = cfg

	// Initialize serviceData if nil to prevent panic
	if m.ServiceData == nil {
		m.ServiceData = make(map[string]interface{})
	}

	// Update service data with new configuration
	m.ServiceData["url"] = cfg.URL
	m.ServiceData["command"] = cfg.Command
	m.ServiceData["type"] = string(cfg.Type)
	m.ServiceData["autoStart"] = cfg.AutoStart
	return nil
}

// =============================================================================
// MockStatusUpdater - Mock for CRD status updates
// =============================================================================

// MockStatusUpdater implements StatusUpdater for testing.
type MockStatusUpdater struct {
	mu sync.Mutex

	// Storage for CRDs
	MCPServers     map[string]*musterv1alpha1.MCPServer
	ServiceClasses map[string]*musterv1alpha1.ServiceClass
	Workflows      map[string]*musterv1alpha1.Workflow

	// Tracking for verification
	GetMCPServerCalled             bool
	UpdateMCPServerStatusCalled    bool
	GetServiceClassCalled          bool
	UpdateServiceClassStatusCalled bool
	GetWorkflowCalled              bool
	UpdateWorkflowStatusCalled     bool

	// Call counts for retry testing
	UpdateMCPServerStatusCallCount    int
	UpdateServiceClassStatusCallCount int
	UpdateWorkflowStatusCallCount     int

	// Last updated resources for verification
	LastUpdatedMCPServer    *musterv1alpha1.MCPServer
	LastUpdatedServiceClass *musterv1alpha1.ServiceClass
	LastUpdatedWorkflow     *musterv1alpha1.Workflow

	// Configurable errors
	GetMCPServerError             error
	UpdateMCPServerStatusError    error
	GetServiceClassError          error
	UpdateServiceClassStatusError error
	GetWorkflowError              error
	UpdateWorkflowStatusError     error

	// For retry testing: fail N times before succeeding
	UpdateMCPServerStatusFailCount int

	// Mode
	KubernetesMode bool
}

// NewMockStatusUpdater creates a new mock status updater.
func NewMockStatusUpdater() *MockStatusUpdater {
	return &MockStatusUpdater{
		MCPServers:     make(map[string]*musterv1alpha1.MCPServer),
		ServiceClasses: make(map[string]*musterv1alpha1.ServiceClass),
		Workflows:      make(map[string]*musterv1alpha1.Workflow),
		KubernetesMode: true,
	}
}

func (m *MockStatusUpdater) GetMCPServer(ctx context.Context, name, namespace string) (*musterv1alpha1.MCPServer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GetMCPServerCalled = true

	if m.GetMCPServerError != nil {
		return nil, m.GetMCPServerError
	}

	key := namespace + "/" + name
	server, ok := m.MCPServers[key]
	if !ok {
		// Return a default server if not found
		defaultServer := &musterv1alpha1.MCPServer{}
		defaultServer.Name = name
		defaultServer.Namespace = namespace
		return defaultServer, nil
	}
	return server, nil
}

func (m *MockStatusUpdater) UpdateMCPServerStatus(ctx context.Context, server *musterv1alpha1.MCPServer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.UpdateMCPServerStatusCalled = true
	m.UpdateMCPServerStatusCallCount++
	m.LastUpdatedMCPServer = server

	// Support for retry testing: fail N times before succeeding
	if m.UpdateMCPServerStatusFailCount > 0 && m.UpdateMCPServerStatusCallCount <= m.UpdateMCPServerStatusFailCount {
		return m.UpdateMCPServerStatusError
	}

	// Regular error (always fail)
	if m.UpdateMCPServerStatusError != nil && m.UpdateMCPServerStatusFailCount == 0 {
		return m.UpdateMCPServerStatusError
	}

	key := server.Namespace + "/" + server.Name
	m.MCPServers[key] = server
	return nil
}

func (m *MockStatusUpdater) GetServiceClass(ctx context.Context, name, namespace string) (*musterv1alpha1.ServiceClass, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GetServiceClassCalled = true

	if m.GetServiceClassError != nil {
		return nil, m.GetServiceClassError
	}

	key := namespace + "/" + name
	sc, ok := m.ServiceClasses[key]
	if !ok {
		defaultSC := &musterv1alpha1.ServiceClass{}
		defaultSC.Name = name
		defaultSC.Namespace = namespace
		return defaultSC, nil
	}
	return sc, nil
}

func (m *MockStatusUpdater) UpdateServiceClassStatus(ctx context.Context, serviceClass *musterv1alpha1.ServiceClass) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.UpdateServiceClassStatusCalled = true
	m.LastUpdatedServiceClass = serviceClass

	if m.UpdateServiceClassStatusError != nil {
		return m.UpdateServiceClassStatusError
	}

	key := serviceClass.Namespace + "/" + serviceClass.Name
	m.ServiceClasses[key] = serviceClass
	return nil
}

func (m *MockStatusUpdater) GetWorkflow(ctx context.Context, name, namespace string) (*musterv1alpha1.Workflow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GetWorkflowCalled = true

	if m.GetWorkflowError != nil {
		return nil, m.GetWorkflowError
	}

	key := namespace + "/" + name
	wf, ok := m.Workflows[key]
	if !ok {
		defaultWF := &musterv1alpha1.Workflow{}
		defaultWF.Name = name
		defaultWF.Namespace = namespace
		return defaultWF, nil
	}
	return wf, nil
}

func (m *MockStatusUpdater) UpdateWorkflowStatus(ctx context.Context, workflow *musterv1alpha1.Workflow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.UpdateWorkflowStatusCalled = true
	m.LastUpdatedWorkflow = workflow

	if m.UpdateWorkflowStatusError != nil {
		return m.UpdateWorkflowStatusError
	}

	key := workflow.Namespace + "/" + workflow.Name
	m.Workflows[key] = workflow
	return nil
}

func (m *MockStatusUpdater) IsKubernetesMode() bool {
	return m.KubernetesMode
}

// AddMCPServer adds an MCPServer to the mock (for test setup).
func (m *MockStatusUpdater) AddMCPServer(server *musterv1alpha1.MCPServer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := server.Namespace + "/" + server.Name
	m.MCPServers[key] = server
}

// =============================================================================
// Thread-safe getters for verification in tests
// =============================================================================

// WasGetMCPServerCalled returns whether GetMCPServer was called (thread-safe).
func (m *MockStatusUpdater) WasGetMCPServerCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.GetMCPServerCalled
}

// WasUpdateMCPServerStatusCalled returns whether UpdateMCPServerStatus was called (thread-safe).
func (m *MockStatusUpdater) WasUpdateMCPServerStatusCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.UpdateMCPServerStatusCalled
}

// WasGetServiceClassCalled returns whether GetServiceClass was called (thread-safe).
func (m *MockStatusUpdater) WasGetServiceClassCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.GetServiceClassCalled
}

// WasUpdateServiceClassStatusCalled returns whether UpdateServiceClassStatus was called (thread-safe).
func (m *MockStatusUpdater) WasUpdateServiceClassStatusCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.UpdateServiceClassStatusCalled
}

// WasGetWorkflowCalled returns whether GetWorkflow was called (thread-safe).
func (m *MockStatusUpdater) WasGetWorkflowCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.GetWorkflowCalled
}

// WasUpdateWorkflowStatusCalled returns whether UpdateWorkflowStatus was called (thread-safe).
func (m *MockStatusUpdater) WasUpdateWorkflowStatusCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.UpdateWorkflowStatusCalled
}

// GetLastUpdatedMCPServer returns the last updated MCPServer (thread-safe copy).
func (m *MockStatusUpdater) GetLastUpdatedMCPServer() *musterv1alpha1.MCPServer {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.LastUpdatedMCPServer
}

// GetLastUpdatedServiceClass returns the last updated ServiceClass (thread-safe copy).
func (m *MockStatusUpdater) GetLastUpdatedServiceClass() *musterv1alpha1.ServiceClass {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.LastUpdatedServiceClass
}

// GetLastUpdatedWorkflow returns the last updated Workflow (thread-safe copy).
func (m *MockStatusUpdater) GetLastUpdatedWorkflow() *musterv1alpha1.Workflow {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.LastUpdatedWorkflow
}

// GetUpdateMCPServerStatusCallCount returns the number of times UpdateMCPServerStatus was called.
func (m *MockStatusUpdater) GetUpdateMCPServerStatusCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.UpdateMCPServerStatusCallCount
}

// ResetCallCounts resets all call tracking state for reuse in tests.
func (m *MockStatusUpdater) ResetCallCounts() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GetMCPServerCalled = false
	m.UpdateMCPServerStatusCalled = false
	m.GetServiceClassCalled = false
	m.UpdateServiceClassStatusCalled = false
	m.GetWorkflowCalled = false
	m.UpdateWorkflowStatusCalled = false
	m.UpdateMCPServerStatusCallCount = 0
	m.UpdateServiceClassStatusCallCount = 0
	m.UpdateWorkflowStatusCallCount = 0
}
