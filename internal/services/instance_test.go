package services

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"muster/internal/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockToolCaller implements ToolCaller for testing
type mockToolCaller struct {
	responses map[string]map[string]interface{}
	errors    map[string]error
	calls     []toolCall
}

type toolCall struct {
	toolName string
	args     map[string]interface{}
}

func newMockToolCaller() *mockToolCaller {
	return &mockToolCaller{
		responses: make(map[string]map[string]interface{}),
		errors:    make(map[string]error),
		calls:     []toolCall{},
	}
}

func (m *mockToolCaller) CallTool(ctx context.Context, toolName string, args map[string]interface{}) (map[string]interface{}, error) {
	m.calls = append(m.calls, toolCall{toolName: toolName, args: args})

	if err, exists := m.errors[toolName]; exists {
		return nil, err
	}

	if response, exists := m.responses[toolName]; exists {
		return response, nil
	}

	// Default successful response
	return map[string]interface{}{
		"success": true,
		"name":    "test-service-id-123",
		"status":  "running",
		"healthy": true,
	}, nil
}

func (m *mockToolCaller) SetResponse(toolName string, response map[string]interface{}) {
	m.responses[toolName] = response
}

func (m *mockToolCaller) SetError(toolName string, err error) {
	m.errors[toolName] = err
}

func (m *mockToolCaller) GetCalls() []toolCall {
	return m.calls
}

func (m *mockToolCaller) Reset() {
	m.calls = []toolCall{}
}

// mockServiceClassManager implements ServiceClassManagerHandler for testing
type mockServiceClassManager struct {
	serviceClasses     map[string]*api.ServiceClass
	createTools        map[string]createToolInfo
	deleteTools        map[string]deleteToolInfo
	healthCheckTools   map[string]healthCheckToolInfo
	healthCheckConfigs map[string]healthCheckConfig
	dependencies       map[string][]string
	availability       map[string]bool
}

type createToolInfo struct {
	toolName  string
	arguments map[string]interface{}
	outputs   map[string]string
}

type deleteToolInfo struct {
	toolName  string
	arguments map[string]interface{}
	outputs   map[string]string
}

type healthCheckToolInfo struct {
	toolName    string
	arguments   map[string]interface{}
	expectation *api.HealthCheckExpectation
}

type healthCheckConfig struct {
	enabled          bool
	interval         time.Duration
	failureThreshold int
	successThreshold int
}

func newMockServiceClassManager() *mockServiceClassManager {
	return &mockServiceClassManager{
		serviceClasses:     make(map[string]*api.ServiceClass),
		createTools:        make(map[string]createToolInfo),
		deleteTools:        make(map[string]deleteToolInfo),
		healthCheckTools:   make(map[string]healthCheckToolInfo),
		healthCheckConfigs: make(map[string]healthCheckConfig),
		dependencies:       make(map[string][]string),
		availability:       make(map[string]bool),
	}
}

func (m *mockServiceClassManager) GetServiceClass(name string) (*api.ServiceClass, error) {
	if def, exists := m.serviceClasses[name]; exists {
		return def, nil
	}
	return nil, fmt.Errorf("service class %s not found", name)
}

func (m *mockServiceClassManager) GetStartTool(name string) (toolName string, args map[string]interface{}, outputs map[string]string, err error) {
	if info, exists := m.createTools[name]; exists {
		return info.toolName, info.arguments, info.outputs, nil
	}
	return "", nil, nil, fmt.Errorf("start tool for service class %s not found", name)
}

func (m *mockServiceClassManager) GetStopTool(name string) (toolName string, args map[string]interface{}, outputs map[string]string, err error) {
	if info, exists := m.deleteTools[name]; exists {
		return info.toolName, info.arguments, info.outputs, nil
	}
	return "", nil, nil, fmt.Errorf("stop tool for service class %s not found", name)
}

func (m *mockServiceClassManager) GetRestartTool(name string) (toolName string, args map[string]interface{}, outputs map[string]string, err error) {
	return "", nil, nil, nil // Optional tool
}

func (m *mockServiceClassManager) GetHealthCheckTool(name string) (toolName string, args map[string]interface{}, expectation *api.HealthCheckExpectation, err error) {
	if info, exists := m.healthCheckTools[name]; exists {
		return info.toolName, info.arguments, info.expectation, nil
	}
	return "", nil, nil, fmt.Errorf("health check tool for service class %s not found", name)
}

func (m *mockServiceClassManager) GetHealthCheckConfig(name string) (enabled bool, interval time.Duration, failureThreshold, successThreshold int, err error) {
	if config, exists := m.healthCheckConfigs[name]; exists {
		return config.enabled, config.interval, config.failureThreshold, config.successThreshold, nil
	}
	return false, 0, 0, 0, fmt.Errorf("health check config for service class %s not found", name)
}

func (m *mockServiceClassManager) GetServiceDependencies(name string) ([]string, error) {
	if deps, exists := m.dependencies[name]; exists {
		return deps, nil
	}
	return nil, nil
}

func (m *mockServiceClassManager) IsServiceClassAvailable(name string) bool {
	if available, exists := m.availability[name]; exists {
		return available
	}
	return false
}

func (m *mockServiceClassManager) ListServiceClasses() []api.ServiceClass { return nil }
func (m *mockServiceClassManager) GetTools() []api.ToolMetadata           { return nil }
func (m *mockServiceClassManager) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	return nil, nil
}
func (m *mockServiceClassManager) RegisterDefinition(*api.ServiceClass) error {
	return nil
}

// Helper methods for setting up test data
func (m *mockServiceClassManager) SetAvailable(name string, available bool) {
	m.availability[name] = available
}

// SetStartTool configures the start tool for a service class
func (m *mockServiceClassManager) SetStartTool(name, toolName string, args map[string]interface{}, outputs map[string]string) {
	m.createTools[name] = createToolInfo{
		toolName:  toolName,
		arguments: args,
		outputs:   outputs,
	}
}

// SetStopTool configures the stop tool for a service class
func (m *mockServiceClassManager) SetStopTool(name, toolName string, args map[string]interface{}, outputs map[string]string) {
	m.deleteTools[name] = deleteToolInfo{
		toolName:  toolName,
		arguments: args,
		outputs:   outputs,
	}
}

// SetHealthCheckTool configures the health check tool for a service class
func (m *mockServiceClassManager) SetHealthCheckTool(name, toolName string, args map[string]interface{}, expectation *api.HealthCheckExpectation) {
	m.healthCheckTools[name] = healthCheckToolInfo{
		toolName:    toolName,
		arguments:   args,
		expectation: expectation,
	}
}

// SetHealthCheckConfig configures health check behavior for a service class
func (m *mockServiceClassManager) SetHealthCheckConfig(name string, enabled bool, interval time.Duration, failureThreshold, successThreshold int) {
	m.healthCheckConfigs[name] = healthCheckConfig{
		enabled:          enabled,
		interval:         interval,
		failureThreshold: failureThreshold,
		successThreshold: successThreshold,
	}
}

func (m *mockServiceClassManager) SetServiceClass(name string, def *api.ServiceClass) {
	m.serviceClasses[name] = def
}

// SetDependencies configures dependencies for a service class
func (m *mockServiceClassManager) SetDependencies(name string, deps []string) {
	m.dependencies[name] = deps
}

// Test helper functions
func setupTestEnvironment() (*mockServiceClassManager, func()) {
	// Create mock service class manager
	mockMgr := newMockServiceClassManager()

	// Register with API
	originalMgr := api.GetServiceClassManager()
	api.RegisterServiceClassManager(mockMgr)

	// Return cleanup function
	cleanup := func() {
		if originalMgr != nil {
			api.RegisterServiceClassManager(originalMgr)
		} else {
			api.RegisterServiceClassManager(nil)
		}
	}

	return mockMgr, cleanup
}

func TestNewGenericServiceInstance(t *testing.T) {
	mockMgr, cleanup := setupTestEnvironment()
	defer cleanup()

	// Setup test service class
	mockMgr.SetServiceClass("test-service", &api.ServiceClass{
		Name:        "test-service",
		Version:     "1.0.0",
		Description: "Test service class",
	})
	mockMgr.SetDependencies("test-service", []string{"dependency1", "dependency2"})

	// Create tool caller
	toolCaller := newMockToolCaller()

	// Test successful creation
	args := map[string]interface{}{
		"param1": "value1",
		"param2": "value2",
	}

	instance := NewGenericServiceInstance(
		"test-name",
		"test-service",
		toolCaller,
		args,
	)

	require.NotNil(t, instance)
	assert.Equal(t, "test-name", instance.name)
	assert.Equal(t, "test-service", instance.serviceClassName)
	assert.Equal(t, StateUnknown, instance.state)
	assert.Equal(t, HealthUnknown, instance.health)
	assert.Equal(t, []string{"dependency1", "dependency2"}, instance.dependencies)
	assert.Equal(t, args, instance.creationArgs)
}

func TestNewGenericServiceInstance_ServiceClassNotFound(t *testing.T) {
	_, cleanup := setupTestEnvironment()
	defer cleanup()

	// Don't set up the service class - it should fail

	toolCaller := newMockToolCaller()
	args := map[string]interface{}{}

	instance := NewGenericServiceInstance(
		"test-name",
		"non-existent-service",
		toolCaller,
		args,
	)

	assert.Nil(t, instance)
}

func TestGenericServiceInstance_Start_Success(t *testing.T) {
	mockMgr, cleanup := setupTestEnvironment()
	defer cleanup()

	// Setup test service class
	mockMgr.SetServiceClass("test-service", &api.ServiceClass{
		Name: "test-service",
	})
	mockMgr.SetStartTool("test-service", "test_create_tool",
		map[string]interface{}{
			"name": "{{ .param1 }}",
			"type": "{{ .param2 }}",
		},
		map[string]string{
			"name":   "name",   // Changed from "$.name" to "name" to match ProcessToolOutputs expectation
			"status": "status", // Changed from "$.status" to "status"
		})

	// Create tool caller with successful response
	toolCaller := newMockToolCaller()
	toolCaller.SetResponse("test_create_tool", map[string]interface{}{
		"success": true,
		"name":    "created-service-123",
		"status":  "running",
	})

	// Create instance
	instance := NewGenericServiceInstance(
		"test-name",
		"test-service",
		toolCaller,
		map[string]interface{}{
			"param1": "test-name",
			"param2": "test-type",
		},
	)
	require.NotNil(t, instance)

	// Test start
	ctx := context.Background()
	err := instance.Start(ctx)

	assert.NoError(t, err)
	assert.Equal(t, StateRunning, instance.GetState())
	assert.Equal(t, HealthHealthy, instance.GetHealth())

	// Verify tool was called with correct arguments
	calls := toolCaller.GetCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "test_create_tool", calls[0].toolName)
	assert.Equal(t, "test-name", calls[0].args["name"])
	assert.Equal(t, "test-type", calls[0].args["type"])

	// NEW: Verify that outputs were extracted and stored in serviceData
	serviceData := instance.GetServiceData()
	assert.Equal(t, "created-service-123", serviceData["name"])
	assert.Equal(t, "running", serviceData["status"])
}

func TestGenericServiceInstance_Start_ToolCallError(t *testing.T) {
	mockMgr, cleanup := setupTestEnvironment()
	defer cleanup()

	// Setup test service class
	mockMgr.SetServiceClass("test-service", &api.ServiceClass{
		Name: "test-service",
	})
	// Use SetStartTool since GetStartTool delegates to GetCreateTool in the mock
	mockMgr.SetStartTool("test-service", "test_create_tool",
		map[string]interface{}{},
		map[string]string{})

	// Create tool caller with error
	toolCaller := newMockToolCaller()
	toolCaller.SetError("test_create_tool", errors.New("tool call failed"))

	// Create instance
	instance := NewGenericServiceInstance(
		"test-name",
		"test-service",
		toolCaller,
		map[string]interface{}{},
	)
	require.NotNil(t, instance)

	// Test start
	ctx := context.Background()
	err := instance.Start(ctx)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "start tool failed") // Updated to expect "start tool failed"
	assert.Equal(t, StateFailed, instance.GetState())
	assert.Equal(t, HealthUnhealthy, instance.GetHealth())
	assert.NotNil(t, instance.GetLastError())
}

func TestGenericServiceInstance_Start_ToolIndicatesFailure(t *testing.T) {
	mockMgr, cleanup := setupTestEnvironment()
	defer cleanup()

	// Setup test service class
	mockMgr.SetServiceClass("test-service", &api.ServiceClass{
		Name: "test-service",
	})
	// Use SetStartTool since GetStartTool delegates to GetCreateTool in the mock
	mockMgr.SetStartTool("test-service", "test_create_tool",
		map[string]interface{}{},
		map[string]string{})

	// Create tool caller with failure response
	toolCaller := newMockToolCaller()
	toolCaller.SetResponse("test_create_tool", map[string]interface{}{
		"success": false,
		"text":    "Service creation failed due to insufficient resources",
	})

	// Create instance
	instance := NewGenericServiceInstance(
		"test-name",
		"test-service",
		toolCaller,
		map[string]interface{}{},
	)
	require.NotNil(t, instance)

	// Test start
	ctx := context.Background()
	err := instance.Start(ctx)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "start tool failed") // Updated to expect "start tool failed"
	assert.Contains(t, err.Error(), "insufficient resources")
	assert.Equal(t, StateFailed, instance.GetState())
	assert.Equal(t, HealthUnhealthy, instance.GetHealth())
}

func TestGenericServiceInstance_Stop_Success(t *testing.T) {
	mockMgr, cleanup := setupTestEnvironment()
	defer cleanup()

	// Setup test service class
	mockMgr.SetServiceClass("test-service", &api.ServiceClass{
		Name: "test-service",
	})
	mockMgr.SetStopTool("test-service", "test_delete_tool",
		map[string]interface{}{
			"name": "{{ .name }}",
		},
		map[string]string{
			"status": "$.status",
		})

	// Create tool caller with successful response
	toolCaller := newMockToolCaller()
	toolCaller.SetResponse("test_delete_tool", map[string]interface{}{
		"success": true,
		"status":  "stopped",
	})

	// Create instance
	instance := NewGenericServiceInstance(
		"test-name",
		"test-service",
		toolCaller,
		map[string]interface{}{},
	)
	require.NotNil(t, instance)

	// Set instance to running state first
	instance.UpdateState(StateRunning, HealthHealthy, nil)

	// Test stop
	ctx := context.Background()
	err := instance.Stop(ctx)

	assert.NoError(t, err)
	assert.Equal(t, StateStopped, instance.GetState())
	assert.Equal(t, HealthUnknown, instance.GetHealth())

	// Verify tool was called with correct arguments
	calls := toolCaller.GetCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "test_delete_tool", calls[0].toolName)
	assert.Equal(t, "test-name", calls[0].args["name"])
}

func TestGenericServiceInstance_Restart(t *testing.T) {
	mockMgr, cleanup := setupTestEnvironment()
	defer cleanup()

	// Setup test service class
	mockMgr.SetServiceClass("test-service", &api.ServiceClass{
		Name: "test-service",
	})
	// Setup both create and delete tools since restart uses stop/start fallback
	mockMgr.SetStartTool("test-service", "test_create_tool",
		map[string]interface{}{},
		map[string]string{})
	mockMgr.SetStopTool("test-service", "test_delete_tool",
		map[string]interface{}{},
		map[string]string{})

	// Create tool caller with successful responses
	toolCaller := newMockToolCaller()
	toolCaller.SetResponse("test_create_tool", map[string]interface{}{
		"success": true,
		"status":  "running",
	})
	toolCaller.SetResponse("test_delete_tool", map[string]interface{}{
		"success": true,
		"status":  "stopped",
	})

	// Create instance
	instance := NewGenericServiceInstance(
		"test-name",
		"test-service",
		toolCaller,
		map[string]interface{}{},
	)
	require.NotNil(t, instance)

	// Set instance to running state first
	instance.UpdateState(StateRunning, HealthHealthy, nil)

	// Test restart - Note: Since GetRestartTool returns error (no restart tool configured),
	// it will fall back to Stop + Start sequence
	ctx := context.Background()
	err := instance.Restart(ctx)

	assert.NoError(t, err)
	assert.Equal(t, StateRunning, instance.GetState())
	assert.Equal(t, HealthHealthy, instance.GetHealth())

	// Verify both tools were called (stop then start)
	calls := toolCaller.GetCalls()
	require.Len(t, calls, 2)
	assert.Equal(t, "test_delete_tool", calls[0].toolName)
	assert.Equal(t, "test_create_tool", calls[1].toolName)
}

func TestGenericServiceInstance_CheckHealth_Success(t *testing.T) {
	mockMgr, cleanup := setupTestEnvironment()
	defer cleanup()

	// Setup test service class with health checking
	mockMgr.SetServiceClass("test-service", &api.ServiceClass{
		Name: "test-service",
	})
	expectation := &api.HealthCheckExpectation{
		Success: func() *bool { b := true; return &b }(),
		JsonPath: map[string]interface{}{
			"healthy": true,
			"status":  "healthy",
		},
	}
	mockMgr.SetHealthCheckTool("test-service", "test_health_tool",
		map[string]interface{}{
			"name": "{{ .name }}",
		},
		expectation)
	mockMgr.SetHealthCheckConfig("test-service", true, 10*time.Second, 3, 1)

	// Create tool caller with healthy response
	toolCaller := newMockToolCaller()
	toolCaller.SetResponse("test_health_tool", map[string]interface{}{
		"success": true,
		"healthy": true,
		"status":  "healthy",
	})

	// Create instance
	instance := NewGenericServiceInstance(
		"test-name",
		"test-service",
		toolCaller,
		map[string]interface{}{},
	)
	require.NotNil(t, instance)

	// Test health check
	ctx := context.Background()
	health, err := instance.CheckHealth(ctx)

	assert.NoError(t, err)
	assert.Equal(t, HealthHealthy, health)
	assert.Equal(t, HealthHealthy, instance.GetHealth())

	// Verify tool was called
	calls := toolCaller.GetCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "test_health_tool", calls[0].toolName)
	assert.Equal(t, "test-name", calls[0].args["name"])
}

func TestGenericServiceInstance_CheckHealth_Disabled(t *testing.T) {
	mockMgr, cleanup := setupTestEnvironment()
	defer cleanup()

	// Setup test service class with health checking disabled
	mockMgr.SetServiceClass("test-service", &api.ServiceClass{
		Name: "test-service",
	})
	mockMgr.SetHealthCheckConfig("test-service", false, 10*time.Second, 3, 1)

	// Create tool caller
	toolCaller := newMockToolCaller()

	// Create instance with initial health
	instance := NewGenericServiceInstance(
		"test-name",
		"test-service",
		toolCaller,
		map[string]interface{}{},
	)
	require.NotNil(t, instance)
	instance.UpdateState(StateRunning, HealthHealthy, nil)

	// Test health check
	ctx := context.Background()
	health, err := instance.CheckHealth(ctx)

	assert.NoError(t, err)
	assert.Equal(t, HealthHealthy, health) // Should return current health

	// Verify no tools were called
	calls := toolCaller.GetCalls()
	assert.Len(t, calls, 0)
}

func TestGenericServiceInstance_Getters(t *testing.T) {
	mockMgr, cleanup := setupTestEnvironment()
	defer cleanup()

	// Setup test service class
	mockMgr.SetServiceClass("test-service", &api.ServiceClass{
		Name:        "test-service",
		ServiceType: "kubernetes-service",
	})
	mockMgr.SetDependencies("test-service", []string{"dep1", "dep2"})

	// Create tool caller
	toolCaller := newMockToolCaller()

	// Create instance
	instance := NewGenericServiceInstance(
		"test-name",
		"test-service",
		toolCaller,
		map[string]interface{}{
			"key1": "value1",
		},
	)
	require.NotNil(t, instance)

	// Test initial state
	assert.Equal(t, "test-name", instance.GetName())
	assert.Equal(t, ServiceType("kubernetes-service"), instance.GetType())
	assert.Equal(t, StateUnknown, instance.GetState())
	assert.Equal(t, HealthUnknown, instance.GetHealth())
	assert.Nil(t, instance.GetLastError())
	assert.Equal(t, []string{"dep1", "dep2"}, instance.GetDependencies())

	// Test service data
	data := instance.GetServiceData()
	assert.NotNil(t, data)

	// Update state and test
	testErr := errors.New("test error")
	instance.UpdateState(StateRunning, HealthHealthy, testErr)

	assert.Equal(t, StateRunning, instance.GetState())
	assert.Equal(t, HealthHealthy, instance.GetHealth())
	assert.Equal(t, testErr, instance.GetLastError())
}

func TestGenericServiceInstance_StateChangeCallback(t *testing.T) {
	mockMgr, cleanup := setupTestEnvironment()
	defer cleanup()

	// Setup test service class
	mockMgr.SetServiceClass("test-service", &api.ServiceClass{
		Name: "test-service",
	})

	// Create tool caller
	toolCaller := newMockToolCaller()

	// Create instance
	instance := NewGenericServiceInstance(
		"test-name",
		"test-service",
		toolCaller,
		map[string]interface{}{},
	)
	require.NotNil(t, instance)

	// Set up callback to track state changes with proper synchronization
	var callbackCalls []StateChangeEvent
	var mu sync.Mutex
	callbackDone := make(chan struct{}, 1)
	callback := func(name string, oldState, newState ServiceState, health HealthStatus, err error) {
		mu.Lock()
		callbackCalls = append(callbackCalls, StateChangeEvent{
			Name:     name,
			OldState: oldState,
			NewState: newState,
			Health:   health,
			Error:    err,
		})
		mu.Unlock()
		select {
		case callbackDone <- struct{}{}:
		default:
		}
	}

	instance.SetStateChangeCallback(callback)

	// Update state to trigger callback
	testErr := errors.New("test error")
	instance.UpdateState(StateRunning, HealthHealthy, testErr)

	// Wait for callback to be called using channel (with timeout)
	select {
	case <-callbackDone:
		// Callback was called
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Callback was not called within timeout")
	}

	// Verify callback was called (protected by mutex)
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, callbackCalls, 1)
	assert.Equal(t, "test-name", callbackCalls[0].Name)
	assert.Equal(t, StateUnknown, callbackCalls[0].OldState)
	assert.Equal(t, StateRunning, callbackCalls[0].NewState)
	assert.Equal(t, HealthHealthy, callbackCalls[0].Health)
	assert.Equal(t, testErr, callbackCalls[0].Error)
}

// StateChangeEvent for testing callback
type StateChangeEvent struct {
	Name     string
	OldState ServiceState
	NewState ServiceState
	Health   HealthStatus
	Error    error
}
