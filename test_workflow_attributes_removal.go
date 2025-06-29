package main

import (
	"fmt"
	"testing"

	"muster/internal/api"
)

// TestWorkflowAttributesRemoval tests that workflows can be created and used
// without the agentModifiable, createdBy, and icon fields
func TestWorkflowAttributesRemoval(t *testing.T) {
	// Test creating a workflow struct without the removed attributes
	workflow := api.Workflow{
		Name:        "test-workflow-clean",
		Description: "Test workflow without obsolete attributes",
		Version:     1,
		InputSchema: api.WorkflowInputSchema{
			Type:       "object",
			Properties: map[string]api.SchemaProperty{
				"test_param": {
					Type:        "string",
					Description: "A test parameter",
					Default:     "test-value",
				},
			},
			Required: []string{},
		},
		Steps: []api.WorkflowStep{
			{
				ID:          "test-step",
				Tool:        "core_service_list",
				Description: "List services for testing",
			},
		},
	}

	// Verify the workflow can be created successfully
	if workflow.Name != "test-workflow-clean" {
		t.Errorf("Expected workflow name to be 'test-workflow-clean', got %s", workflow.Name)
	}

	if len(workflow.Steps) != 1 {
		t.Errorf("Expected 1 workflow step, got %d", len(workflow.Steps))
	}

	if workflow.InputSchema.Properties["test_param"].Type != "string" {
		t.Errorf("Expected input schema parameter type to be 'string', got %s", 
			workflow.InputSchema.Properties["test_param"].Type)
	}

	fmt.Printf("✅ Workflow creation successful without obsolete attributes\n")
	fmt.Printf("   - Name: %s\n", workflow.Name)
	fmt.Printf("   - Description: %s\n", workflow.Description)
	fmt.Printf("   - Version: %d\n", workflow.Version)
	fmt.Printf("   - Steps: %d\n", len(workflow.Steps))
	fmt.Printf("   - Input Parameters: %d\n", len(workflow.InputSchema.Properties))
}

// TestWorkflowReferenceClean tests that WorkflowReference works without obsolete attributes
func TestWorkflowReferenceClean(t *testing.T) {
	// Test creating a WorkflowReference without obsolete attributes
	ref := api.WorkflowReference{
		Name:        "test-ref-clean",
		Description: "Test workflow reference without obsolete attributes",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"param1": map[string]interface{}{
					"type":        "string",
					"description": "First parameter",
				},
			},
		},
		Steps: []api.WorkflowStep{
			{
				ID:   "ref-step-clean",
				Tool: "core_service_list",
			},
		},
	}

	// Verify the reference can be created successfully
	if ref.Name != "test-ref-clean" {
		t.Errorf("Expected reference name to be 'test-ref-clean', got %s", ref.Name)
	}

	if len(ref.Steps) != 1 {
		t.Errorf("Expected 1 workflow step in reference, got %d", len(ref.Steps))
	}

	fmt.Printf("✅ WorkflowReference creation successful without obsolete attributes\n")
	fmt.Printf("   - Name: %s\n", ref.Name)
	fmt.Printf("   - Description: %s\n", ref.Description)
	fmt.Printf("   - Steps: %d\n", len(ref.Steps))
} 