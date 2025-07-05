package serviceclass

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"muster/internal/api"
	"muster/internal/config"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockToolChecker implements ToolChecker for testing
type mockToolChecker struct {
	availableTools map[string]bool
}

func (m *mockToolChecker) IsToolAvailable(toolName string) bool {
	if m.availableTools == nil {
		return false
	}
	return m.availableTools[toolName]
}

func (m *mockToolChecker) GetAvailableTools() []string {
	var tools []string
	for tool, available := range m.availableTools {
		if available {
			tools = append(tools, tool)
		}
	}
	return tools
}

// mockToolCaller implements api.ToolCaller for testing
type mockToolCaller struct {
	calls []toolCall
}

type toolCall struct {
	toolName string
	args     map[string]interface{}
}

func (m *mockToolCaller) CallToolInternal(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	m.calls = append(m.calls, toolCall{toolName: toolName, args: args})

	// Return different responses based on tool name
	switch toolName {
	case "api_kubernetes_connect":
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent(`{
					"connectionId": "k8s-test-connection-123",
					"status": "connected",
					"connected": true,
					"clusterName": "test-cluster",
					"context": "test-context"
				}`),
			},
			IsError: false,
		}, nil
	default:
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent(`{"status": "success", "tool": "` + toolName + `"}`),
			},
			IsError: false,
		}, nil
	}
}

// mockStorage implements a test-only Storage that doesn't load from system directories
type mockStorage struct {
	data map[string]map[string][]byte // entityType -> name -> data
	mu   sync.RWMutex
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		data: make(map[string]map[string][]byte),
	}
}

func (m *mockStorage) Save(entityType string, name string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data[entityType] == nil {
		m.data[entityType] = make(map[string][]byte)
	}
	m.data[entityType][name] = data
	return nil
}

func (m *mockStorage) Load(entityType string, name string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.data[entityType] == nil {
		return nil, fmt.Errorf("entity %s/%s not found", entityType, name)
	}
	data, exists := m.data[entityType][name]
	if !exists {
		return nil, fmt.Errorf("entity %s/%s not found", entityType, name)
	}
	return data, nil
}

func (m *mockStorage) Delete(entityType string, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data[entityType] == nil {
		return fmt.Errorf("entity %s/%s not found", entityType, name)
	}
	if _, exists := m.data[entityType][name]; !exists {
		return fmt.Errorf("entity %s/%s not found", entityType, name)
	}
	delete(m.data[entityType], name)
	return nil
}

func (m *mockStorage) List(entityType string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.data[entityType] == nil {
		return []string{}, nil
	}
	var names []string
	for name := range m.data[entityType] {
		names = append(names, name)
	}
	return names, nil
}

// setupTestDirectory creates a test directory structure and overrides the config paths
func setupTestDirectory(t *testing.T) (string, func()) {
	// Create a temporary directory for test
	tmpDir := t.TempDir()

	// Create the .muster/serviceclasses directory structure in tmpDir
	serviceclassDir := filepath.Join(tmpDir, ".muster", "serviceclasses")
	require.NoError(t, os.MkdirAll(serviceclassDir, 0755))

	// Store original functions
	originalOsGetwd := config.GetOsGetwd()

	// Override osGetwd to return our test directory
	config.SetOsGetwd(func() (string, error) {
		return tmpDir, nil
	})

	// Return cleanup function
	cleanup := func() {
		config.SetOsGetwd(originalOsGetwd)
	}

	return serviceclassDir, cleanup
}

// setupTestManager creates a ServiceClassManager and a mockToolChecker for testing
func setupTestManager(t *testing.T) (*ServiceClassManager, *mockToolChecker) {
	t.Helper()
	toolChecker := &mockToolChecker{
		availableTools: make(map[string]bool),
	}
	storage := config.NewStorage()
	manager, err := NewServiceClassManager(toolChecker, storage)
	require.NoError(t, err)
	return manager, toolChecker
}

// TestServiceClassManagerIntegration tests the complete integration of ServiceClass loading
func TestServiceClassManagerIntegration(t *testing.T) {
	serviceclassDir, cleanup := setupTestDirectory(t)
	defer cleanup()

	// Write a test ServiceClass definition
	testYAML := `name: test_k8s_connection
type: service_k8s_connection
version: "1.0.0"
description: "Test Kubernetes connection service class"

serviceConfig:
  serviceType: "DynamicK8sConnection"
  defaultName: "k8s-{{ .cluster_name }}"
  dependencies: []
  
  lifecycleTools:
    start:
      tool: "api_kubernetes_connect"
      arguments:
        clusterName: "{{ .cluster_name }}"
        context: "{{ .context | default .cluster_name }}"
      outputs:
        name: "name"
        status: "status"
        health: "connected"
    stop:
      tool: "api_kubernetes_disconnect"
      arguments:
        name: "{{ .name }}"
      outputs:
        status: "status"
  
  createArgs:
    cluster_name:
      toolArg: "clusterName"
      required: true
    context:
      toolArg: "context"
      required: false

operations:
  create_connection:
    description: "Create Kubernetes connection"
    args:
      cluster_name:
        type: string
        required: true
    requires:
      - api_kubernetes_connect

metadata:
  provider: "kubernetes"
  category: "connection"
`

	require.NoError(t, os.WriteFile(filepath.Join(serviceclassDir, "service_k8s_connection.yaml"), []byte(testYAML), 0644))

	// Create mock tool checker with required tools available
	mockChecker := &mockToolChecker{
		availableTools: map[string]bool{
			"api_kubernetes_connect":    true,
			"api_kubernetes_disconnect": true,
		},
	}

	// Create ServiceClass manager
	storage := config.NewStorage()
	manager, err := NewServiceClassManager(mockChecker, storage)
	require.NoError(t, err)

	// Load definitions
	require.NoError(t, manager.LoadDefinitions())

	// Test that ServiceClass is loaded and available
	assert.True(t, manager.IsServiceClassAvailable("test_k8s_connection"))

	// Test retrieving ServiceClass definition
	def, exists := manager.GetServiceClassDefinition("test_k8s_connection")
	require.True(t, exists)
	assert.Equal(t, "test_k8s_connection", def.Name)
	// Note: Type field structure has changed in consolidated API
	assert.Equal(t, "Test Kubernetes connection service class", def.Description)

	// Test listing ServiceClass definitions
	definitions := manager.ListServiceClasses()

	// Find our test ServiceClass among the definitions (may include system ServiceClasses)
	var testServiceClass *api.ServiceClass
	for i := range definitions {
		if definitions[i].Name == "test_k8s_connection" {
			testServiceClass = &definitions[i]
			break
		}
	}

	require.NotNil(t, testServiceClass, "test_k8s_connection ServiceClass should be found")
	assert.Equal(t, "test_k8s_connection", testServiceClass.Name)
	assert.True(t, testServiceClass.Available)
	assert.True(t, testServiceClass.CreateToolAvailable)
	assert.True(t, testServiceClass.DeleteToolAvailable)
}

// TestServiceClassMissingTools tests behavior when required tools are not available
func TestServiceClassMissingTools(t *testing.T) {
	serviceclassDir, cleanup := setupTestDirectory(t)
	defer cleanup()

	// Write a ServiceClass definition that requires missing tools
	portForwardYAML := `name: test_portforward
type: service_portforward
version: "1.0.0"
description: "Test port forward service class"

serviceConfig:
  serviceType: "DynamicPortForward"
  defaultName: "pf-{{ .service_name }}"
  
  lifecycleTools:
    start:
      tool: "api_k8s_port_forward"
      arguments:
        namespace: "{{ .namespace }}"
        service: "{{ .service_name }}"
      outputs:
        serviceId: "forwardId"
        status: "status"
    stop:
      tool: "api_k8s_port_forward_stop"
      arguments:
        forwardId: "{{ .service_id }}"
      outputs:
        status: "status"

operations:
  create_portforward:
    description: "Create port forward"
    args:
      namespace:
        type: string
        required: true
    requires:
      - api_k8s_port_forward

metadata:
  provider: "kubectl"
  category: "networking"
`

	require.NoError(t, os.WriteFile(filepath.Join(serviceclassDir, "service_portforward.yaml"), []byte(portForwardYAML), 0644))

	// Create mock tool checker with no tools available
	mockChecker := &mockToolChecker{
		availableTools: map[string]bool{},
	}

	// Create ServiceClass manager
	storage := config.NewStorage()
	manager, err := NewServiceClassManager(mockChecker, storage)
	require.NoError(t, err)

	// Load definitions
	require.NoError(t, manager.LoadDefinitions())

	// Test that ServiceClass is loaded but not available
	assert.False(t, manager.IsServiceClassAvailable("test_portforward"))

	// Test listing should show unavailable service class
	definitions := manager.ListServiceClasses()

	// Find our test ServiceClass among the definitions (may include system ServiceClasses)
	var testServiceClass *api.ServiceClass
	for i := range definitions {
		if definitions[i].Name == "test_portforward" {
			testServiceClass = &definitions[i]
			break
		}
	}

	require.NotNil(t, testServiceClass, "test_portforward ServiceClass should be found")
	assert.Equal(t, "test_portforward", testServiceClass.Name)
	assert.False(t, testServiceClass.Available)
	assert.False(t, testServiceClass.CreateToolAvailable)
	assert.False(t, testServiceClass.DeleteToolAvailable)
}

// TestServiceClassAPIAdapter tests the API adapter integration
func TestServiceClassAPIAdapter(t *testing.T) {
	serviceclassDir, cleanup := setupTestDirectory(t)
	defer cleanup()

	// Write a simple ServiceClass definition
	simpleYAML := `name: test_simple
type: simple_service
version: "1.0.0"
description: "Simple test service class"

serviceConfig:
  serviceType: "TestService"
  defaultName: "test-{{ .name }}"
  
  lifecycleTools:
    start:
      tool: "test_create_tool"
      arguments:
        name: "{{ .name }}"
      outputs:
        serviceId: "id"
        status: "status"
    stop:
      tool: "test_delete_tool"
      arguments:
        serviceId: "{{ .service_id }}"
      outputs:
        status: "status"

metadata:
  provider: "test"
`

	require.NoError(t, os.WriteFile(filepath.Join(serviceclassDir, "service_simple.yaml"), []byte(simpleYAML), 0644))

	// Create mock tool checker
	mockChecker := &mockToolChecker{
		availableTools: map[string]bool{
			"test_create_tool": true,
			"test_delete_tool": true,
		},
	}

	// Create ServiceClass manager and adapter
	storage := config.NewStorage()
	manager, err := NewServiceClassManager(mockChecker, storage)
	require.NoError(t, err)

	// Load definitions explicitly (internal method still needed for startup)
	require.NoError(t, manager.LoadDefinitions())

	adapter := NewAdapter(manager)

	// Test API methods
	serviceClass, err := adapter.GetServiceClass("test_simple")
	require.NoError(t, err)
	assert.Equal(t, "test_simple", serviceClass.Name)

	// Test availability check
	assert.True(t, adapter.IsServiceClassAvailable("test_simple"))

	// Test listing
	classes := adapter.ListServiceClasses()

	// Find our test ServiceClass among the classes (may include system ServiceClasses)
	var testServiceClass *api.ServiceClass
	for i := range classes {
		if classes[i].Name == "test_simple" {
			testServiceClass = &classes[i]
			break
		}
	}

	require.NotNil(t, testServiceClass, "test_simple ServiceClass should be found")
	assert.Equal(t, "test_simple", testServiceClass.Name)
	assert.True(t, testServiceClass.Available)

	// Note: GetCreateTool and GetDeleteTool methods are no longer available
	// The functionality has been moved to the unified manager pattern
}

// TestServiceClassErrorHandling tests various error scenarios
func TestServiceClassErrorHandling(t *testing.T) {
	serviceclassDir, cleanup := setupTestDirectory(t)
	defer cleanup()

	// Write an invalid ServiceClass definition (missing required fields)
	invalidYAML := `name: test_invalid
# Missing type and other required fields
description: "Invalid service class for testing"
`

	require.NoError(t, os.WriteFile(filepath.Join(serviceclassDir, "service_invalid.yaml"), []byte(invalidYAML), 0644))

	// Create mock tool checker
	mockChecker := &mockToolChecker{
		availableTools: map[string]bool{},
	}

	// Create ServiceClass manager
	storage := config.NewStorage()
	manager, err := NewServiceClassManager(mockChecker, storage)
	require.NoError(t, err)

	// Loading should succeed but invalid definitions should be skipped
	require.NoError(t, manager.LoadDefinitions())

	// Invalid ServiceClass should not be available
	assert.False(t, manager.IsServiceClassAvailable("test_invalid"))

	// Test getting non-existent ServiceClass
	_, exists := manager.GetServiceClassDefinition("non_existent")
	assert.False(t, exists)

	// Test API adapter error handling
	adapter := NewAdapter(manager)

	// Test getting non-existent ServiceClass through API
	_, err = adapter.GetServiceClass("non_existent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	// Note: GetCreateTool method is no longer available in the new architecture
}
