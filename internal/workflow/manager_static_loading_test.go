package workflow

import (
	"testing"

	"muster/internal/api"
	"muster/internal/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// MockToolChecker for testing
type MockToolChecker struct {
	availableTools map[string]bool
}

func NewMockToolChecker() *MockToolChecker {
	return &MockToolChecker{
		availableTools: make(map[string]bool),
	}
}

func (mtc *MockToolChecker) IsToolAvailable(toolName string) bool {
	return mtc.availableTools[toolName]
}

func (mtc *MockToolChecker) GetAvailableTools() []string {
	tools := make([]string, 0, len(mtc.availableTools))
	for tool, available := range mtc.availableTools {
		if available {
			tools = append(tools, tool)
		}
	}
	return tools
}

func (mtc *MockToolChecker) SetToolAvailable(toolName string, available bool) {
	mtc.availableTools[toolName] = available
}

func TestWorkflowManager_LoadDefinitions_Integration(t *testing.T) {
	// Save original functions for proper cleanup
	originalUserHomeDir := config.GetOsUserHomeDir()
	originalGetwd := config.GetOsGetwd()
	defer func() {
		config.SetOsUserHomeDir(originalUserHomeDir)
		config.SetOsGetwd(originalGetwd)
	}()

	// Create temporary directories for testing
	tempDir := t.TempDir()
	userDir := tempDir + "/user"
	projectDir := tempDir + "/project"

	// Mock functions to use temp directories
	config.SetOsUserHomeDir(func() (string, error) {
		return userDir, nil
	})
	config.SetOsGetwd(func() (string, error) {
		return projectDir, nil
	})

	// Create a workflow manager with storage using mocked filesystem
	storage := config.NewStorage()
	mockToolChecker := NewMockToolChecker()

	// Clean up any existing workflows first
	existingWorkflows, _ := storage.List("workflows")
	for _, name := range existingWorkflows {
		storage.Delete("workflows", name)
	}

	manager, err := NewWorkflowManager(storage, nil, mockToolChecker)
	require.NoError(t, err)

	// Test that LoadDefinitions doesn't crash and can handle missing directories
	err = manager.LoadDefinitions()
	require.NoError(t, err)

	// Should start with no workflows
	definitions := manager.ListDefinitions()
	assert.Len(t, definitions, 0)
}

func TestWorkflowManager_ValidationFunction(t *testing.T) {
	// Save original functions for proper cleanup
	originalUserHomeDir := config.GetOsUserHomeDir()
	originalGetwd := config.GetOsGetwd()
	defer func() {
		config.SetOsUserHomeDir(originalUserHomeDir)
		config.SetOsGetwd(originalGetwd)
	}()

	// Create temporary directories for testing
	tempDir := t.TempDir()
	userDir := tempDir + "/user"
	projectDir := tempDir + "/project"

	// Mock functions to use temp directories
	config.SetOsUserHomeDir(func() (string, error) {
		return userDir, nil
	})
	config.SetOsGetwd(func() (string, error) {
		return projectDir, nil
	})

	storage := config.NewStorage()
	mockToolChecker := NewMockToolChecker()

	manager, err := NewWorkflowManager(storage, nil, mockToolChecker)
	require.NoError(t, err)

	testCases := []struct {
		name        string
		workflow    api.Workflow
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid workflow",
			workflow: api.Workflow{
				Name:        "test-workflow",
				Description: "Test workflow",
				Steps: []api.WorkflowStep{
					{ID: "step1", Tool: "test-tool"},
				},
			},
			expectError: false,
		},
		{
			name: "missing name",
			workflow: api.Workflow{
				Description: "Test workflow",
				Steps: []api.WorkflowStep{
					{ID: "step1", Tool: "test-tool"},
				},
			},
			expectError: true,
			errorMsg:    "field 'name': is required for workflow",
		},
		{
			name: "no steps",
			workflow: api.Workflow{
				Name:        "test-workflow",
				Description: "Test workflow",
				Steps:       []api.WorkflowStep{},
			},
			expectError: true,
			errorMsg:    "field 'steps': must have at least one step for workflow",
		},
		{
			name: "empty step ID",
			workflow: api.Workflow{
				Name:        "test-workflow",
				Description: "Test workflow",
				Steps: []api.WorkflowStep{
					{ID: "", Tool: "test-tool"},
				},
			},
			expectError: true,
			errorMsg:    "step ID cannot be empty",
		},
		{
			name: "empty tool name",
			workflow: api.Workflow{
				Name:        "test-workflow",
				Description: "Test workflow",
				Steps: []api.WorkflowStep{
					{ID: "step1", Tool: ""},
				},
			},
			expectError: true,
			errorMsg:    "tool name cannot be empty",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := manager.validateWorkflowDefinition(&tc.workflow)
			if tc.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.errorMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestWorkflowManager_Storage_Integration(t *testing.T) {
	// Save original functions for proper cleanup
	originalUserHomeDir := config.GetOsUserHomeDir()
	originalGetwd := config.GetOsGetwd()
	defer func() {
		config.SetOsUserHomeDir(originalUserHomeDir)
		config.SetOsGetwd(originalGetwd)
	}()

	// Create temporary directories for testing
	tempDir := t.TempDir()
	userDir := tempDir + "/user"
	projectDir := tempDir + "/project"

	// Mock functions to use temp directories
	config.SetOsUserHomeDir(func() (string, error) {
		return userDir, nil
	})
	config.SetOsGetwd(func() (string, error) {
		return projectDir, nil
	})

	storage := config.NewStorage()
	mockToolChecker := NewMockToolChecker()

	// Clean up any existing workflows first
	existingWorkflows, _ := storage.List("workflows")
	for _, name := range existingWorkflows {
		storage.Delete("workflows", name)
	}

	manager, err := NewWorkflowManager(storage, nil, mockToolChecker)
	require.NoError(t, err)

	// Create a test workflow
	testWorkflow := api.Workflow{
		Name:        "dynamic-test-workflow",
		Description: "Test workflow for storage",
		InputSchema: api.WorkflowInputSchema{
			Type: "object",
			Args: map[string]api.SchemaProperty{},
		},
		Steps: []api.WorkflowStep{
			{ID: "step1", Tool: "test-tool", Args: map[string]interface{}{"param": "value"}},
		},
	}

	// Save to storage
	data, err := yaml.Marshal(testWorkflow)
	require.NoError(t, err)
	err = storage.Save("workflows", "dynamic-test-workflow", data)
	require.NoError(t, err)

	// Load definitions
	err = manager.LoadDefinitions()
	require.NoError(t, err)

	// Should have the dynamic workflow
	definitions := manager.ListDefinitions()
	require.Len(t, definitions, 1)

	loadedWorkflow := definitions[0]
	assert.Equal(t, "dynamic-test-workflow", loadedWorkflow.Name)
	assert.Equal(t, "Test workflow for storage", loadedWorkflow.Description)
	assert.Len(t, loadedWorkflow.Steps, 1)
	assert.Equal(t, "step1", loadedWorkflow.Steps[0].ID)
	assert.Equal(t, "test-tool", loadedWorkflow.Steps[0].Tool)

	// Clean up
	storage.Delete("workflows", "dynamic-test-workflow")
}

func TestWorkflowManager_InvalidDynamicWorkflow(t *testing.T) {
	// Save original functions for proper cleanup
	originalUserHomeDir := config.GetOsUserHomeDir()
	originalGetwd := config.GetOsGetwd()
	defer func() {
		config.SetOsUserHomeDir(originalUserHomeDir)
		config.SetOsGetwd(originalGetwd)
	}()

	// Create temporary directories for testing
	tempDir := t.TempDir()
	userDir := tempDir + "/user"
	projectDir := tempDir + "/project"

	// Mock functions to use temp directories
	config.SetOsUserHomeDir(func() (string, error) {
		return userDir, nil
	})
	config.SetOsGetwd(func() (string, error) {
		return projectDir, nil
	})

	storage := config.NewStorage()
	mockToolChecker := NewMockToolChecker()

	manager, err := NewWorkflowManager(storage, nil, mockToolChecker)
	require.NoError(t, err)

	// Create an invalid workflow (no steps) with unique name
	invalidWorkflow := api.Workflow{
		Name:        "invalid-workflow-test",
		Description: "Invalid workflow with no steps",
		Steps:       []api.WorkflowStep{}, // Empty steps - should fail validation
	}

	// Save to storage
	data, err := yaml.Marshal(invalidWorkflow)
	require.NoError(t, err)
	err = storage.Save("workflows", "invalid-workflow-test", data)
	require.NoError(t, err)

	// Clean up any existing workflows first to ensure clean test
	existingWorkflows, _ := storage.List("workflows")
	for _, name := range existingWorkflows {
		if name != "invalid-workflow-test" {
			storage.Delete("workflows", name)
		}
	}

	// Load definitions - should skip invalid workflow
	err = manager.LoadDefinitions()
	require.NoError(t, err)

	// Should have no workflows (invalid one was skipped)
	definitions := manager.ListDefinitions()
	assert.Len(t, definitions, 0)

	// Clean up
	storage.Delete("workflows", "invalid-workflow-test")
}

func TestWorkflowManager_MalformedYAML(t *testing.T) {
	// Save original functions for proper cleanup
	originalUserHomeDir := config.GetOsUserHomeDir()
	originalGetwd := config.GetOsGetwd()
	defer func() {
		config.SetOsUserHomeDir(originalUserHomeDir)
		config.SetOsGetwd(originalGetwd)
	}()

	// Create temporary directories for testing
	tempDir := t.TempDir()
	userDir := tempDir + "/user"
	projectDir := tempDir + "/project"

	// Mock functions to use temp directories
	config.SetOsUserHomeDir(func() (string, error) {
		return userDir, nil
	})
	config.SetOsGetwd(func() (string, error) {
		return projectDir, nil
	})

	storage := config.NewStorage()
	mockToolChecker := NewMockToolChecker()

	manager, err := NewWorkflowManager(storage, nil, mockToolChecker)
	require.NoError(t, err)

	// Clean up any existing workflows first
	existingWorkflows, _ := storage.List("workflows")
	for _, name := range existingWorkflows {
		storage.Delete("workflows", name)
	}

	// Save malformed YAML to storage with unique name
	malformedYAML := []byte(`
name: malformed-workflow-test
description: This workflow has malformed YAML
steps:
  - id: step1
    tool: test-tool
    args: {invalid: yaml: structure}
`)

	err = storage.Save("workflows", "malformed-workflow-test", malformedYAML)
	require.NoError(t, err)

	// Load definitions - should skip malformed workflow
	err = manager.LoadDefinitions()
	require.NoError(t, err)

	// Should have no workflows (malformed one was skipped)
	definitions := manager.ListDefinitions()
	assert.Len(t, definitions, 0)

	// Clean up
	storage.Delete("workflows", "malformed-workflow-test")
}

func TestWorkflowManager_WorkflowAvailability(t *testing.T) {
	// Save original functions for proper cleanup
	originalUserHomeDir := config.GetOsUserHomeDir()
	originalGetwd := config.GetOsGetwd()
	defer func() {
		config.SetOsUserHomeDir(originalUserHomeDir)
		config.SetOsGetwd(originalGetwd)
	}()

	// Create temporary directories for testing
	tempDir := t.TempDir()
	userDir := tempDir + "/user"
	projectDir := tempDir + "/project"

	// Mock functions to use temp directories
	config.SetOsUserHomeDir(func() (string, error) {
		return userDir, nil
	})
	config.SetOsGetwd(func() (string, error) {
		return projectDir, nil
	})

	storage := config.NewStorage()
	mockToolChecker := NewMockToolChecker()

	// Clean up any existing workflows first
	existingWorkflows, _ := storage.List("workflows")
	for _, name := range existingWorkflows {
		storage.Delete("workflows", name)
	}

	// Set some tools as available
	mockToolChecker.SetToolAvailable("available-tool", true)
	mockToolChecker.SetToolAvailable("unavailable-tool", false)

	manager, err := NewWorkflowManager(storage, nil, mockToolChecker)
	require.NoError(t, err)

	// Create workflows with different tool availability using unique names
	availableWorkflow := api.Workflow{
		Name:        "available-workflow-test",
		Description: "Workflow with available tools",
		Steps: []api.WorkflowStep{
			{ID: "step1", Tool: "available-tool"},
		},
	}

	unavailableWorkflow := api.Workflow{
		Name:        "unavailable-workflow-test",
		Description: "Workflow with unavailable tools",
		Steps: []api.WorkflowStep{
			{ID: "step1", Tool: "unavailable-tool"},
		},
	}

	// Save both workflows
	for _, wf := range []api.Workflow{availableWorkflow, unavailableWorkflow} {
		data, err := yaml.Marshal(wf)
		require.NoError(t, err)
		err = storage.Save("workflows", wf.Name, data)
		require.NoError(t, err)
	}

	// Load definitions
	err = manager.LoadDefinitions()
	require.NoError(t, err)

	// Should have both workflows
	definitions := manager.ListDefinitions()
	require.Len(t, definitions, 2)

	// Test availability
	assert.True(t, manager.IsAvailable("available-workflow-test"))
	assert.False(t, manager.IsAvailable("unavailable-workflow-test"))

	// Test available definitions filter
	availableDefinitions := manager.ListAvailableDefinitions()
	require.Len(t, availableDefinitions, 1)
	assert.Equal(t, "available-workflow-test", availableDefinitions[0].Name)

	// Clean up test files
	storage.Delete("workflows", "available-workflow-test")
	storage.Delete("workflows", "unavailable-workflow-test")
}
