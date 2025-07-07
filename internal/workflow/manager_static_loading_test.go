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
	// Create a workflow manager with storage
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

	// Should start with cleaned up test workflows (may have system workflows)
	definitions := manager.ListDefinitions()
	// Just verify that LoadDefinitions completed without error
	// System may have pre-existing workflows, so don't assert exact count
	assert.NotNil(t, definitions)
}

func TestWorkflowManager_ValidationFunction(t *testing.T) {
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
		Args:        map[string]api.ArgDefinition{},
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

	// Should have the dynamic workflow (may also have system workflows)
	definitions := manager.ListDefinitions()
	require.GreaterOrEqual(t, len(definitions), 1)

	// Find our test workflow
	var loadedWorkflow *api.Workflow
	for _, wf := range definitions {
		if wf.Name == "dynamic-test-workflow" {
			loadedWorkflow = &wf
			break
		}
	}
	require.NotNil(t, loadedWorkflow, "Should find the dynamic-test-workflow")

	assert.Equal(t, "dynamic-test-workflow", loadedWorkflow.Name)
	assert.Equal(t, "Test workflow for storage", loadedWorkflow.Description)
	assert.Len(t, loadedWorkflow.Steps, 1)
	assert.Equal(t, "step1", loadedWorkflow.Steps[0].ID)
	assert.Equal(t, "test-tool", loadedWorkflow.Steps[0].Tool)

	// Clean up
	storage.Delete("workflows", "dynamic-test-workflow")
}

func TestWorkflowManager_InvalidDynamicWorkflow(t *testing.T) {
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

	// Invalid workflow should be skipped (may have system workflows)
	definitions := manager.ListDefinitions()
	// Verify the invalid workflow was not loaded
	for _, wf := range definitions {
		assert.NotEqual(t, "invalid-workflow-test", wf.Name, "Invalid workflow should not be loaded")
	}

	// Clean up
	storage.Delete("workflows", "invalid-workflow-test")
}

func TestWorkflowManager_MalformedYAML(t *testing.T) {
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

	// Malformed workflow should be skipped (may have system workflows)
	definitions := manager.ListDefinitions()
	// Verify the malformed workflow was not loaded
	for _, wf := range definitions {
		assert.NotEqual(t, "malformed-workflow-test", wf.Name, "Malformed workflow should not be loaded")
	}

	// Clean up
	storage.Delete("workflows", "malformed-workflow-test")
}

func TestWorkflowManager_WorkflowAvailability(t *testing.T) {
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

	// Should have both test workflows (may also have system workflows)
	definitions := manager.ListDefinitions()
	require.GreaterOrEqual(t, len(definitions), 2)

	// Verify our test workflows are present
	testWorkflows := make(map[string]bool)
	for _, wf := range definitions {
		if wf.Name == "available-workflow-test" || wf.Name == "unavailable-workflow-test" {
			testWorkflows[wf.Name] = true
		}
	}
	assert.True(t, testWorkflows["available-workflow-test"], "Should have available-workflow-test")
	assert.True(t, testWorkflows["unavailable-workflow-test"], "Should have unavailable-workflow-test")

	// Test availability
	assert.True(t, manager.IsAvailable("available-workflow-test"))
	assert.False(t, manager.IsAvailable("unavailable-workflow-test"))

	// Test available definitions filter - should include available test workflow
	availableDefinitions := manager.ListAvailableDefinitions()
	var foundAvailableTest bool
	for _, wf := range availableDefinitions {
		if wf.Name == "available-workflow-test" {
			foundAvailableTest = true
			break
		}
	}
	assert.True(t, foundAvailableTest, "Should find available-workflow-test in available definitions")

	// Clean up test files
	storage.Delete("workflows", "available-workflow-test")
	storage.Delete("workflows", "unavailable-workflow-test")
}
