package reconciler

import (
	"context"
	"fmt"
	"sync"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
)

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
// MockStatusUpdater - Mock for CRD status updates
// =============================================================================

// MockStatusUpdater implements StatusUpdater for testing.
type MockStatusUpdater struct {
	mu sync.Mutex

	// Storage for CRDs
	MCPServers map[string]*musterv1alpha1.MCPServer
	Workflows  map[string]*musterv1alpha1.Workflow

	// Tracking for verification
	GetMCPServerCalled          bool
	UpdateMCPServerStatusCalled bool
	GetWorkflowCalled           bool
	UpdateWorkflowStatusCalled  bool

	// Call counts for retry testing
	UpdateMCPServerStatusCallCount int
	UpdateWorkflowStatusCallCount  int

	// Last updated resources for verification
	LastUpdatedMCPServer *musterv1alpha1.MCPServer
	LastUpdatedWorkflow  *musterv1alpha1.Workflow

	// Configurable errors
	GetMCPServerError          error
	UpdateMCPServerStatusError error
	GetWorkflowError           error
	UpdateWorkflowStatusError  error

	// For retry testing: fail N times before succeeding
	UpdateMCPServerStatusFailCount int

	// Mode
	KubernetesMode bool
}

// NewMockStatusUpdater creates a new mock status updater.
func NewMockStatusUpdater() *MockStatusUpdater {
	return &MockStatusUpdater{
		MCPServers:     make(map[string]*musterv1alpha1.MCPServer),
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
	m.GetWorkflowCalled = false
	m.UpdateWorkflowStatusCalled = false
	m.UpdateMCPServerStatusCallCount = 0
	m.UpdateWorkflowStatusCallCount = 0
}
