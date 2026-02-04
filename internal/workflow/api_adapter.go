package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/client"
	"github.com/giantswarm/muster/internal/events"
	"github.com/giantswarm/muster/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Adapter provides the API adapter for workflow management
type Adapter struct {
	client           client.MusterClient
	namespace        string
	executor         *WorkflowExecutor
	executionTracker *ExecutionTracker
	toolChecker      ToolAvailabilityChecker

	// Prevent circular dependency during tool generation
	generatingTools bool
	mu              sync.RWMutex
}

// ToolAvailabilityChecker interface for checking tool availability
type ToolAvailabilityChecker interface {
	IsToolAvailable(toolName string) bool
}

// NewAdapterWithClient creates a new workflow adapter with a pre-configured client
func NewAdapterWithClient(musterClient client.MusterClient, namespace string, toolCaller ToolCaller, toolChecker ToolAvailabilityChecker, configPath string) *Adapter {

	if namespace == "" {
		namespace = "default"
	}

	adapter := &Adapter{
		client:           musterClient,
		namespace:        namespace,
		executionTracker: NewExecutionTracker(NewExecutionStorage(configPath)),
		toolChecker:      toolChecker,
	}

	adapter.executor = NewWorkflowExecutor(toolCaller, adapter)

	return adapter
}

// Register registers this adapter with the API layer
func (a *Adapter) Register() {
	api.RegisterWorkflow(a)
	logging.Debug("WorkflowAdapter", "Registered workflow adapter with API layer")
}

// ExecuteWorkflow executes a workflow and returns MCP result
func (a *Adapter) ExecuteWorkflow(ctx context.Context, workflowName string, args map[string]interface{}) (*api.CallToolResult, error) {
	logging.Debug("WorkflowAdapter", "Executing workflow: %s", workflowName)

	// Get the workflow CRD
	workflowCRD, err := a.client.GetWorkflow(ctx, workflowName, a.namespace)
	if err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("workflow %s not found", workflowName)},
			IsError: true,
		}, nil
	}

	// Convert CRD to internal workflow format
	workflow := a.convertCRDToWorkflow(workflowCRD)

	// Check if workflow is available before execution
	if !a.isWorkflowAvailable(workflow) {
		// Generate workflow unavailable event with missing tools
		missingTools := a.findMissingTools(workflow)
		a.generateCRDEvent(workflowName, events.ReasonWorkflowUnavailable, events.EventData{
			Operation: "execute",
			ToolNames: missingTools,
		})

		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("workflow %s is not available (missing required tools)", workflowName)},
			IsError: true,
		}, nil
	}

	// Generate execution started event
	a.generateCRDEvent(workflowName, events.ReasonWorkflowExecutionStarted, events.EventData{
		Operation: "execute",
		StepCount: len(workflow.Steps),
	})

	// Execute workflow with automatic tracking
	result, execution, err := a.executionTracker.TrackExecution(ctx, workflowName, args, func() (*mcp.CallToolResult, error) {
		return a.executor.ExecuteWorkflow(ctx, workflow, args)
	})

	// Generate execution tracked event
	if execution != nil {
		a.generateCRDEvent(workflowName, events.ReasonWorkflowExecutionTracked, events.EventData{
			Operation:   "execute",
			ExecutionID: execution.ExecutionID,
		})
	}

	// Include execution_id in the response for test scenarios and API consumers
	if execution != nil {
		if result != nil {
			result = a.enhanceResultWithExecutionID(result, execution.ExecutionID)
		} else if err != nil {
			// For failed workflows with no result, create a minimal result with execution_id
			result = &mcp.CallToolResult{
				Content: []mcp.Content{mcp.NewTextContent(fmt.Sprintf(`{"execution_id": "%s", "error": "%s"}`, execution.ExecutionID, err.Error()))},
				IsError: true,
			}
		}
	}

	if err != nil {
		// Generate execution failed event
		eventData := events.EventData{
			Operation: "execute",
			Error:     err.Error(),
		}
		if execution != nil {
			eventData.ExecutionID = execution.ExecutionID
		}
		a.generateCRDEvent(workflowName, events.ReasonWorkflowExecutionFailed, eventData)

		// Convert mcp result to api result
		if result != nil {
			var content []interface{}
			for _, mcpContent := range result.Content {
				if textContent, ok := mcpContent.(mcp.TextContent); ok {
					content = append(content, textContent.Text)
				} else {
					content = append(content, mcpContent)
				}
			}
			return &api.CallToolResult{
				Content: content,
				IsError: true,
			}, nil
		}

		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	// Generate execution completed event
	eventData := events.EventData{
		Operation: "execute",
		StepCount: len(workflow.Steps),
	}
	if execution != nil {
		eventData.ExecutionID = execution.ExecutionID
		if execution.DurationMs > 0 {
			eventData.Duration = time.Duration(execution.DurationMs) * time.Millisecond
		}
	}
	a.generateCRDEvent(workflowName, events.ReasonWorkflowExecutionCompleted, eventData)

	// Convert mcp.CallToolResult to api.CallToolResult
	var content []interface{}
	for _, mcpContent := range result.Content {
		if textContent, ok := mcpContent.(mcp.TextContent); ok {
			content = append(content, textContent.Text)
		} else {
			content = append(content, mcpContent)
		}
	}

	return &api.CallToolResult{
		Content: content,
		IsError: result.IsError,
	}, nil
}

// GetWorkflows returns information about all workflows
func (a *Adapter) GetWorkflows() []api.Workflow {
	ctx := context.Background()
	workflowCRDs, err := a.client.ListWorkflows(ctx, a.namespace)
	if err != nil {
		logging.Error("WorkflowAdapter", err, "Failed to list workflows")
		return []api.Workflow{}
	}

	workflows := make([]api.Workflow, 0, len(workflowCRDs))
	for _, workflowCRD := range workflowCRDs {
		workflow := a.convertCRDToWorkflow(&workflowCRD)
		workflow.Available = a.isWorkflowAvailable(workflow)
		workflows = append(workflows, *workflow)
	}

	return workflows
}

// GetWorkflow returns a specific workflow definition
func (a *Adapter) GetWorkflow(name string) (*api.Workflow, error) {
	ctx := context.Background()
	workflowCRD, err := a.client.GetWorkflow(ctx, name, a.namespace)
	if err != nil {
		return nil, api.NewWorkflowNotFoundError(name)
	}

	workflow := a.convertCRDToWorkflow(workflowCRD)
	workflow.Available = a.isWorkflowAvailable(workflow)
	return workflow, nil
}

// CreateWorkflowFromStructured creates a new workflow from structured arguments
func (a *Adapter) CreateWorkflowFromStructured(args map[string]interface{}) error {
	// Validate the workflow before creating it
	if err := a.ValidateWorkflowFromStructured(args); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	// Convert structured arguments to api.Workflow
	wf, err := convertToWorkflow(args)
	if err != nil {
		return err
	}

	// Create a simplified CRD for filesystem storage that avoids complex conversion
	workflowCRD := &musterv1alpha1.Workflow{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "muster.giantswarm.io/v1alpha1",
			Kind:       "Workflow",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      wf.Name,
			Namespace: a.namespace,
		},
		Spec: musterv1alpha1.WorkflowSpec{
			Description: wf.Description,
			Args:        make(map[string]musterv1alpha1.ArgDefinition),
			Steps:       make([]musterv1alpha1.WorkflowStep, len(wf.Steps)),
		},
	}

	// Convert args safely
	for key, argDef := range wf.Args {
		crdArgDef := musterv1alpha1.ArgDefinition{
			Type:        argDef.Type,
			Required:    argDef.Required,
			Description: argDef.Description,
		}
		// Convert default value if present
		if argDef.Default != nil {
			crdArgDef.Default = a.convertToRawExtension(argDef.Default)
		}
		workflowCRD.Spec.Args[key] = crdArgDef
	}

	// Convert steps safely
	for i, step := range wf.Steps {
		crdStep := musterv1alpha1.WorkflowStep{
			ID:           step.ID,
			Tool:         step.Tool,
			Store:        step.Store,
			AllowFailure: step.AllowFailure,
			Description:  step.Description,
			Args:         make(map[string]*runtime.RawExtension),
		}

		// Convert condition if present
		if step.Condition != nil {
			crdStep.Condition = a.convertWorkflowConditionToCRD(step.Condition)
		}

		// Convert args safely without causing recursion
		for key, value := range step.Args {
			if jsonBytes, err := json.Marshal(value); err == nil {
				crdStep.Args[key] = &runtime.RawExtension{Raw: jsonBytes}
			}
		}

		workflowCRD.Spec.Steps[i] = crdStep
	}

	// Create the CRD
	ctx := context.Background()
	if err := a.client.CreateWorkflow(ctx, workflowCRD); err != nil {
		// Generate failure event
		a.generateCRDEvent(wf.Name, events.ReasonWorkflowValidationFailed, events.EventData{
			Error:     err.Error(),
			Operation: "create",
		})
		return fmt.Errorf("failed to create workflow: %w", err)
	}

	// Generate success event for CRD creation
	a.generateCRDEvent(wf.Name, events.ReasonWorkflowCreated, events.EventData{
		Operation: "create",
		StepCount: len(wf.Steps),
	})

	return nil
}

// UpdateWorkflowFromStructured updates an existing workflow from structured arguments
func (a *Adapter) UpdateWorkflowFromStructured(name string, args map[string]interface{}) error {
	// Validate the workflow before updating it
	if err := a.ValidateWorkflowFromStructured(args); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	// Convert structured arguments to api.Workflow
	wf, err := convertToWorkflow(args)
	if err != nil {
		return err
	}

	// Ensure the name matches
	wf.Name = name

	// Convert to CRD
	workflowCRD := a.convertWorkflowToCRD(&wf)

	// Update the CRD
	ctx := context.Background()
	if err := a.client.UpdateWorkflow(ctx, workflowCRD); err != nil {
		// Generate failure event
		a.generateCRDEvent(wf.Name, events.ReasonWorkflowValidationFailed, events.EventData{
			Error:     err.Error(),
			Operation: "update",
		})
		return fmt.Errorf("failed to update workflow: %w", err)
	}

	// Generate success event for CRD update
	a.generateCRDEvent(wf.Name, events.ReasonWorkflowUpdated, events.EventData{
		Operation: "update",
		StepCount: len(wf.Steps),
	})

	return nil
}

// ValidateWorkflowFromStructured validates a workflow definition from structured arguments
func (a *Adapter) ValidateWorkflowFromStructured(args map[string]interface{}) error {
	// Convert structured args to validate structure
	wf, err := convertToWorkflow(args)
	if err != nil {
		// Generate validation failure event
		a.generateCRDEvent(wf.Name, events.ReasonWorkflowValidationFailed, events.EventData{
			Error:     err.Error(),
			Operation: "validate",
		})
		return err
	}

	// Basic validation
	if wf.Name == "" {
		err := fmt.Errorf("workflow name is required")
		a.generateCRDEvent("", events.ReasonWorkflowValidationFailed, events.EventData{
			Error:     err.Error(),
			Operation: "validate",
		})
		return err
	}
	if len(wf.Steps) == 0 {
		err := fmt.Errorf("workflow must have at least one step")
		a.generateCRDEvent(wf.Name, events.ReasonWorkflowValidationFailed, events.EventData{
			Error:     err.Error(),
			Operation: "validate",
		})
		return err
	}

	// Step validation
	stepIDs := make(map[string]bool)
	for i, step := range wf.Steps {
		// Check for empty step ID
		if step.ID == "" {
			err := fmt.Errorf("step %d: step ID cannot be empty", i)
			a.generateCRDEvent(wf.Name, events.ReasonWorkflowValidationFailed, events.EventData{
				Error:     err.Error(),
				Operation: "validate",
			})
			return err
		}

		// Check for duplicate step IDs
		if stepIDs[step.ID] {
			err := fmt.Errorf("duplicate step ID '%s' found", step.ID)
			a.generateCRDEvent(wf.Name, events.ReasonWorkflowValidationFailed, events.EventData{
				Error:     err.Error(),
				Operation: "validate",
			})
			return err
		}
		stepIDs[step.ID] = true

		// Check for empty tool
		if step.Tool == "" {
			err := fmt.Errorf("step %d (%s): tool cannot be empty", i, step.ID)
			a.generateCRDEvent(wf.Name, events.ReasonWorkflowValidationFailed, events.EventData{
				Error:     err.Error(),
				Operation: "validate",
			})
			return err
		}
	}

	// Generate validation success event
	a.generateCRDEvent(wf.Name, events.ReasonWorkflowValidationSucceeded, events.EventData{
		Operation: "validate",
		StepCount: len(wf.Steps),
	})

	return nil
}

// DeleteWorkflow deletes a workflow
func (a *Adapter) DeleteWorkflow(name string) error {
	ctx := context.Background()
	if err := a.client.DeleteWorkflow(ctx, name, a.namespace); err != nil {
		// Generate failure event
		a.generateCRDEvent(name, events.ReasonWorkflowValidationFailed, events.EventData{
			Error:     err.Error(),
			Operation: "delete",
		})
		return fmt.Errorf("failed to delete workflow: %w", err)
	}

	// Generate success event for CRD deletion
	a.generateCRDEvent(name, events.ReasonWorkflowDeleted, events.EventData{
		Operation: "delete",
	})

	return nil
}

// ListWorkflowExecutions returns paginated list of workflow executions with optional filtering
func (a *Adapter) ListWorkflowExecutions(ctx context.Context, req *api.ListWorkflowExecutionsRequest) (*api.ListWorkflowExecutionsResponse, error) {
	return a.executionTracker.ListExecutions(ctx, req)
}

// GetWorkflowExecution returns detailed information about a specific workflow execution
func (a *Adapter) GetWorkflowExecution(ctx context.Context, req *api.GetWorkflowExecutionRequest) (*api.WorkflowExecution, error) {
	return a.executionTracker.GetExecution(ctx, req)
}

// CallToolInternal calls a tool internally - required by ToolCaller interface
func (a *Adapter) CallToolInternal(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	if a.executor == nil {
		return nil, fmt.Errorf("workflow executor not available")
	}

	// Delegate to the executor's tool caller
	return a.executor.toolCaller.CallToolInternal(ctx, toolName, args)
}

// Stop stops the workflow adapter
func (a *Adapter) Stop() {
	if a.client != nil {
		a.client.Close()
	}
}

// ReloadWorkflows reloads workflow definitions - not needed for CRD-based approach
func (a *Adapter) ReloadWorkflows() error {
	// CRDs are automatically refreshed, no need to reload
	return nil
}

// convertCRDToWorkflow converts a Workflow CRD to internal API format
func (a *Adapter) convertCRDToWorkflow(workflowCRD *musterv1alpha1.Workflow) *api.Workflow {
	workflow := &api.Workflow{
		Name:         workflowCRD.ObjectMeta.Name,
		Description:  workflowCRD.Spec.Description,
		Args:         a.convertArgDefinitions(workflowCRD.Spec.Args),
		Steps:        a.convertWorkflowSteps(workflowCRD.Spec.Steps),
		CreatedAt:    workflowCRD.ObjectMeta.CreationTimestamp.Time,
		LastModified: workflowCRD.ObjectMeta.CreationTimestamp.Time,
	}

	// Use modification time if available
	if workflowCRD.Status.Conditions != nil {
		for _, condition := range workflowCRD.Status.Conditions {
			if condition.LastTransitionTime.After(workflow.LastModified) {
				workflow.LastModified = condition.LastTransitionTime.Time
			}
		}
	}

	return workflow
}

// convertWorkflowToCRD converts an internal API workflow to CRD format
func (a *Adapter) convertWorkflowToCRD(workflow *api.Workflow) *musterv1alpha1.Workflow {
	return &musterv1alpha1.Workflow{
		ObjectMeta: metav1.ObjectMeta{
			Name:      workflow.Name,
			Namespace: a.namespace,
		},
		Spec: musterv1alpha1.WorkflowSpec{
			Description: workflow.Description,
			Args:        a.convertArgDefinitionsToCRD(workflow.Args),
			Steps:       a.convertWorkflowStepsToCRD(workflow.Steps),
		},
	}
}

// convertArgDefinitions converts CRD ArgDefinitions to internal format
func (a *Adapter) convertArgDefinitions(crdArgs map[string]musterv1alpha1.ArgDefinition) map[string]api.ArgDefinition {
	args := make(map[string]api.ArgDefinition)
	for name, crdArg := range crdArgs {
		args[name] = api.ArgDefinition{
			Type:        crdArg.Type,
			Required:    crdArg.Required,
			Description: crdArg.Description,
			Default:     a.convertRawExtension(crdArg.Default),
		}
	}
	return args
}

// convertArgDefinitionsToCRD converts internal ArgDefinitions to CRD format
func (a *Adapter) convertArgDefinitionsToCRD(args map[string]api.ArgDefinition) map[string]musterv1alpha1.ArgDefinition {
	crdArgs := make(map[string]musterv1alpha1.ArgDefinition)
	for name, arg := range args {
		crdArgs[name] = musterv1alpha1.ArgDefinition{
			Type:        arg.Type,
			Required:    arg.Required,
			Description: arg.Description,
			Default:     a.convertToRawExtension(arg.Default),
		}
	}
	return crdArgs
}

// convertWorkflowSteps converts CRD WorkflowSteps to internal format
func (a *Adapter) convertWorkflowSteps(crdSteps []musterv1alpha1.WorkflowStep) []api.WorkflowStep {
	steps := make([]api.WorkflowStep, 0, len(crdSteps))
	for _, crdStep := range crdSteps {
		step := api.WorkflowStep{
			ID:           crdStep.ID,
			Tool:         crdStep.Tool,
			Args:         a.convertRawExtensionMap(crdStep.Args),
			Store:        crdStep.Store,
			AllowFailure: crdStep.AllowFailure,
			Outputs:      a.convertRawExtensionMap(crdStep.Outputs),
			Description:  crdStep.Description,
		}

		if crdStep.Condition != nil {
			step.Condition = a.convertWorkflowCondition(crdStep.Condition)
		}

		steps = append(steps, step)
	}
	return steps
}

// convertWorkflowStepsToCRD converts internal WorkflowSteps to CRD format
func (a *Adapter) convertWorkflowStepsToCRD(steps []api.WorkflowStep) []musterv1alpha1.WorkflowStep {
	crdSteps := make([]musterv1alpha1.WorkflowStep, 0, len(steps))
	for _, step := range steps {
		crdStep := musterv1alpha1.WorkflowStep{
			ID:           step.ID,
			Tool:         step.Tool,
			Args:         a.convertToRawExtensionMap(step.Args),
			Store:        step.Store,
			AllowFailure: step.AllowFailure,
			Outputs:      a.convertToRawExtensionMap(step.Outputs),
			Description:  step.Description,
		}

		if step.Condition != nil {
			crdStep.Condition = a.convertWorkflowConditionToCRD(step.Condition)
		}

		crdSteps = append(crdSteps, crdStep)
	}
	return crdSteps
}

// convertWorkflowCondition converts CRD WorkflowCondition to internal format
func (a *Adapter) convertWorkflowCondition(crdCondition *musterv1alpha1.WorkflowCondition) *api.WorkflowCondition {
	condition := &api.WorkflowCondition{
		Tool:     crdCondition.Tool,
		Args:     a.convertRawExtensionMap(crdCondition.Args),
		FromStep: crdCondition.FromStep,
	}

	if crdCondition.Expect != nil {
		condition.Expect = a.convertWorkflowConditionExpectation(crdCondition.Expect)
	}

	if crdCondition.ExpectNot != nil {
		condition.ExpectNot = a.convertWorkflowConditionExpectation(crdCondition.ExpectNot)
	}

	return condition
}

// convertWorkflowConditionToCRD converts internal WorkflowCondition to CRD format
func (a *Adapter) convertWorkflowConditionToCRD(condition *api.WorkflowCondition) *musterv1alpha1.WorkflowCondition {
	crdCondition := &musterv1alpha1.WorkflowCondition{
		Tool:     condition.Tool,
		Args:     a.convertToRawExtensionMap(condition.Args),
		FromStep: condition.FromStep,
	}

	if condition.Expect.Success || len(condition.Expect.JsonPath) > 0 {
		crdCondition.Expect = a.convertWorkflowConditionExpectationToCRD(condition.Expect)
	}

	if condition.ExpectNot.Success || len(condition.ExpectNot.JsonPath) > 0 {
		crdCondition.ExpectNot = a.convertWorkflowConditionExpectationToCRD(condition.ExpectNot)
	}

	return crdCondition
}

// convertWorkflowConditionExpectation converts CRD WorkflowConditionExpectation to internal format
func (a *Adapter) convertWorkflowConditionExpectation(crdExpectation *musterv1alpha1.WorkflowConditionExpectation) api.WorkflowConditionExpectation {
	expectation := api.WorkflowConditionExpectation{
		JsonPath: a.convertRawExtensionMap(crdExpectation.JsonPath),
	}

	if crdExpectation.Success != nil {
		expectation.Success = *crdExpectation.Success
	}

	return expectation
}

// convertWorkflowConditionExpectationToCRD converts internal WorkflowConditionExpectation to CRD format
func (a *Adapter) convertWorkflowConditionExpectationToCRD(expectation api.WorkflowConditionExpectation) *musterv1alpha1.WorkflowConditionExpectation {
	crdExpectation := &musterv1alpha1.WorkflowConditionExpectation{
		JsonPath: a.convertToRawExtensionMap(expectation.JsonPath),
	}

	if expectation.Success {
		crdExpectation.Success = &expectation.Success
	}

	return crdExpectation
}

// convertRawExtension converts RawExtension to interface{}
func (a *Adapter) convertRawExtension(rawExt *runtime.RawExtension) interface{} {
	if rawExt == nil {
		return nil
	}

	if len(rawExt.Raw) == 0 {
		return nil
	}

	// For test scenarios, try to unmarshal as JSON first
	var value interface{}
	if err := json.Unmarshal(rawExt.Raw, &value); err == nil {
		return value
	}

	// If JSON fails, return as string without any further conversion
	return string(rawExt.Raw)
}

// convertToRawExtension converts interface{} to RawExtension
func (a *Adapter) convertToRawExtension(value interface{}) *runtime.RawExtension {
	if value == nil {
		return &runtime.RawExtension{Raw: []byte("null")}
	}

	// Use simple JSON marshaling for all types to avoid recursion
	raw, err := json.Marshal(value)
	if err != nil {
		// If marshaling fails, return as null
		return &runtime.RawExtension{Raw: []byte("null")}
	}

	return &runtime.RawExtension{Raw: raw}
}

// convertRawExtensionMap converts map[string]*RawExtension to map[string]interface{}
func (a *Adapter) convertRawExtensionMap(rawExtMap map[string]*runtime.RawExtension) map[string]interface{} {
	if rawExtMap == nil {
		return make(map[string]interface{})
	}
	result := make(map[string]interface{})
	for key, rawExt := range rawExtMap {
		if rawExt != nil {
			if converted := a.convertRawExtension(rawExt); converted != nil {
				result[key] = converted
			}
		}
	}
	return result
}

// convertToRawExtensionMap converts map[string]interface{} to map[string]*RawExtension
func (a *Adapter) convertToRawExtensionMap(valueMap map[string]interface{}) map[string]*runtime.RawExtension {
	if valueMap == nil {
		return make(map[string]*runtime.RawExtension)
	}
	result := make(map[string]*runtime.RawExtension)
	for key, value := range valueMap {
		if value != nil {
			if rawExt := a.convertToRawExtension(value); rawExt != nil {
				result[key] = rawExt
			}
		}
	}
	return result
}

// isWorkflowAvailable checks if a workflow has all required tools available
func (a *Adapter) isWorkflowAvailable(workflow *api.Workflow) bool {
	a.mu.RLock()
	if a.generatingTools {
		a.mu.RUnlock()
		// If we're in the middle of generating tools, assume available to avoid circular dependency
		return true
	}
	a.mu.RUnlock()

	if a.toolChecker == nil {
		return true // Assume available if no tool checker
	}

	// Check each step's tool availability
	for _, step := range workflow.Steps {
		if !a.toolChecker.IsToolAvailable(step.Tool) {
			return false
		}
	}

	return true
}

// findMissingTools returns a list of tools that are not available for a workflow
func (a *Adapter) findMissingTools(workflow *api.Workflow) []string {
	a.mu.RLock()
	if a.generatingTools {
		a.mu.RUnlock()
		return []string{} // No missing tools during tool generation
	}
	a.mu.RUnlock()

	if a.toolChecker == nil {
		return []string{} // No missing tools if no tool checker
	}

	var missingTools []string
	for _, step := range workflow.Steps {
		if !a.toolChecker.IsToolAvailable(step.Tool) {
			// Avoid duplicates
			found := false
			for _, tool := range missingTools {
				if tool == step.Tool {
					found = true
					break
				}
			}
			if !found {
				missingTools = append(missingTools, step.Tool)
			}
		}
	}

	return missingTools
}

// enhanceResultWithExecutionID modifies the workflow execution result to include the execution_id
func (a *Adapter) enhanceResultWithExecutionID(result *mcp.CallToolResult, executionID string) *mcp.CallToolResult {
	if result == nil {
		return nil
	}

	// If there's only one text content, try to parse it as JSON and add execution_id
	if len(result.Content) == 1 {
		if textContent, ok := result.Content[0].(mcp.TextContent); ok {
			var jsonData interface{}
			if err := json.Unmarshal([]byte(textContent.Text), &jsonData); err == nil {
				// Successfully parsed as JSON
				if jsonMap, ok := jsonData.(map[string]interface{}); ok {
					jsonMap["execution_id"] = executionID
					if enhancedJSON, err := json.Marshal(jsonMap); err == nil {
						result.Content[0] = mcp.NewTextContent(string(enhancedJSON))
						return result
					}
				}
			}
		}
	}

	// Fallback: append execution_id as separate content
	result.Content = append(result.Content, mcp.NewTextContent(fmt.Sprintf(`{"execution_id": "%s"}`, executionID)))
	return result
}

// GetTools returns all tools this provider offers
func (a *Adapter) GetTools() []api.ToolMetadata {
	// Set flag to prevent circular dependency during tool generation
	a.mu.Lock()
	a.generatingTools = true
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		a.generatingTools = false
		a.mu.Unlock()
	}()

	tools := []api.ToolMetadata{
		// Workflow management tools
		{
			Name:        "workflow_list",
			Description: "List all workflows",
			Args: []api.ArgMetadata{
				{
					Name:        "include_system",
					Type:        "boolean",
					Required:    false,
					Description: "Include system-defined workflows",
					Default:     true,
				},
			},
		},
		{
			Name:        "workflow_get",
			Description: "Get workflow details",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the workflow",
				},
			},
		},
		{
			Name:        "workflow_create",
			Description: "Create a new workflow",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the workflow",
				},
				{
					Name:        "description",
					Type:        "string",
					Required:    false,
					Description: "Description of the workflow",
				},
				{
					Name:        "args",
					Type:        "object",
					Required:    false,
					Description: "Workflow arguments definition",
					Schema:      getWorkflowArgsSchema(),
				},
				{
					Name:        "steps",
					Type:        "array",
					Required:    true,
					Description: "Workflow steps",
					Schema:      getWorkflowStepsSchema(),
				},
			},
		},
		{
			Name:        "workflow_update",
			Description: "Update an existing workflow",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the workflow to update",
				},
				{
					Name:        "description",
					Type:        "string",
					Required:    false,
					Description: "Description of the workflow",
				},
				{
					Name:        "args",
					Type:        "object",
					Required:    false,
					Description: "Workflow arguments definition",
					Schema:      getWorkflowArgsSchema(),
				},
				{
					Name:        "steps",
					Type:        "array",
					Required:    true,
					Description: "Workflow steps",
					Schema:      getWorkflowStepsSchema(),
				},
			},
		},
		{
			Name:        "workflow_delete",
			Description: "Delete a workflow",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the workflow to delete",
				},
			},
		},
		{
			Name:        "workflow_validate",
			Description: "Validate a workflow definition",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the workflow",
				},
				{
					Name:        "description",
					Type:        "string",
					Required:    false,
					Description: "Description of the workflow",
				},
				{
					Name:        "args",
					Type:        "object",
					Required:    false,
					Description: "Workflow arguments definition",
					Schema:      getWorkflowArgsSchema(),
				},
				{
					Name:        "steps",
					Type:        "array",
					Required:    true,
					Description: "Workflow steps",
					Schema:      getWorkflowStepsSchema(),
				},
			},
		},
		{
			Name:        "workflow_available",
			Description: "Check if a workflow is available",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        "string",
					Required:    true,
					Description: "Name of the workflow",
				},
			},
		},
		{
			Name:        "workflow_execution_list",
			Description: "List workflow executions",
			Args: []api.ArgMetadata{
				{
					Name:        "workflow_name",
					Type:        "string",
					Required:    false,
					Description: "Filter by workflow name",
				},
				{
					Name:        "status",
					Type:        "string",
					Required:    false,
					Description: "Filter by execution status",
				},
				{
					Name:        "limit",
					Type:        "number",
					Required:    false,
					Description: "Maximum number of executions to return",
					Default:     50,
				},
				{
					Name:        "offset",
					Type:        "number",
					Required:    false,
					Description: "Number of executions to skip",
					Default:     0,
				},
			},
		},
		{
			Name:        "workflow_execution_get",
			Description: "Get workflow execution details",
			Args: []api.ArgMetadata{
				{
					Name:        "execution_id",
					Type:        "string",
					Required:    true,
					Description: "ID of the execution",
				},
				{
					Name:        "include_steps",
					Type:        "boolean",
					Required:    false,
					Description: "Include step details",
					Default:     true,
				},
				{
					Name:        "step_id",
					Type:        "string",
					Required:    false,
					Description: "Get specific step details",
				},
			},
		},
	}

	// Add workflow execution tools (action_*) dynamically
	workflows := a.GetWorkflows()
	for _, workflow := range workflows {
		tools = append(tools, api.ToolMetadata{
			Name:        "action_" + workflow.Name,
			Description: workflow.Description,
			Args:        a.convertWorkflowArgs(workflow.Name),
		})
	}

	return tools
}

// ExecuteTool executes a tool by name
func (a *Adapter) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	switch {
	case toolName == "workflow_list":
		return a.handleList(args)
	case toolName == "workflow_get":
		return a.handleGet(args)
	case toolName == "workflow_create":
		return a.handleCreate(args)
	case toolName == "workflow_update":
		return a.handleUpdate(args)
	case toolName == "workflow_delete":
		return a.handleDelete(args)
	case toolName == "workflow_validate":
		return a.handleValidate(args)
	case toolName == "workflow_available":
		return a.handleWorkflowAvailable(args)
	case toolName == "workflow_execution_list":
		return a.handleExecutionList(ctx, args)
	case toolName == "workflow_execution_get":
		return a.handleExecutionGet(ctx, args)

	case strings.HasPrefix(toolName, "action_"):
		// Execute workflow
		workflowName := strings.TrimPrefix(toolName, "action_")
		return a.ExecuteWorkflow(ctx, workflowName, args)
	default:
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
}

// convertWorkflowArgs converts workflow args to argument metadata
func (a *Adapter) convertWorkflowArgs(workflowName string) []api.ArgMetadata {
	workflow, err := a.GetWorkflow(workflowName)
	if err != nil {
		return nil
	}

	var params []api.ArgMetadata

	// Extract args from workflow definition
	for name, argDef := range workflow.Args {
		param := api.ArgMetadata{
			Name:        name,
			Type:        argDef.Type,
			Required:    argDef.Required,
			Description: argDef.Description,
			Default:     argDef.Default,
		}

		params = append(params, param)
	}

	return params
}

// Helper methods for handling management operations
func (a *Adapter) handleList(args map[string]interface{}) (*api.CallToolResult, error) {
	workflows := a.GetWorkflows()

	var result []map[string]interface{}
	for _, wf := range workflows {
		workflowInfo := map[string]interface{}{
			"name":      wf.Name,
			"available": wf.Available,
		}

		// Only include description if it's not empty
		if wf.Description != "" {
			workflowInfo["description"] = wf.Description
		}

		result = append(result, workflowInfo)
	}

	// Sort workflows by name for consistent ordering
	sort.Slice(result, func(i, j int) bool {
		return result[i]["name"].(string) < result[j]["name"].(string)
	})

	// Wrap the result in a "workflows" field to match expected format
	response := map[string]interface{}{
		"workflows": result,
	}

	return &api.CallToolResult{
		Content: []interface{}{response},
		IsError: false,
	}, nil
}

func (a *Adapter) handleGet(args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok {
		return &api.CallToolResult{
			Content: []interface{}{"name is required"},
			IsError: true,
		}, nil
	}

	workflow, err := a.GetWorkflow(name)
	if err != nil {
		return api.HandleErrorWithPrefix(err, "Failed to get workflow"), nil
	}

	// Convert to YAML for easier viewing
	yamlData, err := yaml.Marshal(workflow)
	if err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to marshal workflow: %v", err)},
			IsError: true,
		}, nil
	}

	result := map[string]interface{}{
		"workflow": workflow,
		"yaml":     string(yamlData),
	}

	return &api.CallToolResult{
		Content: []interface{}{result},
		IsError: false,
	}, nil
}

func (a *Adapter) handleCreate(args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.WorkflowCreateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	// Convert structured arguments to api.Workflow
	wf, err := convertToWorkflow(args)
	if err != nil {
		return nil, fmt.Errorf("failed to create workflow: %w", err)
	}

	// Create workflow through adapter
	if err := a.CreateWorkflowFromStructured(args); err != nil {
		return nil, fmt.Errorf("failed to create workflow: %w", err)
	}

	// Refresh aggregator capabilities to include the new workflow tool
	if aggregator := api.GetAggregator(); aggregator != nil {
		logging.Info("WorkflowAdapter", "Refreshing aggregator capabilities after creating workflow %s", wf.Name)
		aggregator.UpdateCapabilities()

		// Generate tool registration event
		a.generateCRDEvent(wf.Name, events.ReasonWorkflowToolRegistered, events.EventData{
			Operation: "register",
		})

		// Generate capabilities refresh event
		a.generateCRDEvent(wf.Name, events.ReasonWorkflowCapabilitiesRefreshed, events.EventData{
			Operation: "create",
		})
	}

	return &api.CallToolResult{
		Content: []interface{}{"Workflow created successfully"},
	}, nil
}

func (a *Adapter) handleUpdate(args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.WorkflowUpdateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	if err := a.UpdateWorkflowFromStructured(req.Name, args); err != nil {
		return api.HandleErrorWithPrefix(err, "Failed to update workflow"), nil
	}

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Workflow '%s' updated successfully", req.Name)},
		IsError: false,
	}, nil
}

func (a *Adapter) handleDelete(args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok {
		return &api.CallToolResult{
			Content: []interface{}{"name is required"},
			IsError: true,
		}, nil
	}

	if err := a.DeleteWorkflow(name); err != nil {
		return api.HandleErrorWithPrefix(err, "Failed to delete workflow"), nil
	}

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Workflow '%s' deleted successfully", name)},
		IsError: false,
	}, nil
}

func (a *Adapter) handleValidate(args map[string]interface{}) (*api.CallToolResult, error) {
	var req api.WorkflowValidateRequest
	if err := api.ParseRequest(args, &req); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}

	if err := a.ValidateWorkflowFromStructured(args); err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Validation failed: %v", err)},
			IsError: true,
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{fmt.Sprintf("Validation successful for workflow %s", req.Name)},
		IsError: false,
	}, nil
}

func (a *Adapter) handleWorkflowAvailable(args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok {
		return &api.CallToolResult{
			Content: []interface{}{"name argument is required"},
			IsError: true,
		}, nil
	}

	workflow, err := a.GetWorkflow(name)
	if err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}
	available := a.isWorkflowAvailable(workflow)

	result := map[string]interface{}{
		"name":      name,
		"available": available,
	}

	return &api.CallToolResult{
		Content: []interface{}{result},
		IsError: false,
	}, nil
}

// handleExecutionList handles the workflow_execution_list tool (exposed as core_workflow_execution_list)
func (a *Adapter) handleExecutionList(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	// Parse request arguments
	req := &api.ListWorkflowExecutionsRequest{}

	if workflowName, ok := args["workflow_name"].(string); ok {
		req.WorkflowName = workflowName
	}

	// Validate status arg
	if status, ok := args["status"].(string); ok {
		// Empty status is invalid when explicitly provided
		if status == "" {
			return &api.CallToolResult{
				Content: []interface{}{"status must be one of the enum values: inprogress, completed, failed"},
				IsError: true,
			}, nil
		}
		if status != "inprogress" && status != "completed" && status != "failed" {
			return &api.CallToolResult{
				Content: []interface{}{"status must be one of the enum values: inprogress, completed, failed"},
				IsError: true,
			}, nil
		}
		req.Status = api.WorkflowExecutionStatus(status)
	}

	// Validate limit arg
	if limitVal, ok := args["limit"]; ok {
		// Handle both int and float64 types (JSON may parse as either)
		var limitInt int
		switch v := limitVal.(type) {
		case float64:
			limitInt = int(v)
		case int:
			limitInt = v
		case int64:
			limitInt = int(v)
		default:
			return &api.CallToolResult{
				Content: []interface{}{"limit must be a number"},
				IsError: true,
			}, nil
		}

		if limitInt < 1 {
			return &api.CallToolResult{
				Content: []interface{}{"limit must be at least 1 (minimum value)"},
				IsError: true,
			}, nil
		}
		if limitInt > 1000 {
			return &api.CallToolResult{
				Content: []interface{}{"limit must be at most 1000 (maximum value)"},
				IsError: true,
			}, nil
		}
		req.Limit = limitInt
	}
	if req.Limit <= 0 {
		req.Limit = 50 // Default
	}

	// Validate offset arg
	if offsetVal, ok := args["offset"]; ok {
		// Handle both int and float64 types (JSON may parse as either)
		var offsetInt int
		switch v := offsetVal.(type) {
		case float64:
			offsetInt = int(v)
		case int:
			offsetInt = v
		case int64:
			offsetInt = int(v)
		default:
			return &api.CallToolResult{
				Content: []interface{}{"offset must be a number"},
				IsError: true,
			}, nil
		}

		if offsetInt < 0 {
			return &api.CallToolResult{
				Content: []interface{}{"offset must be at least 0 (minimum value)"},
				IsError: true,
			}, nil
		}
		req.Offset = offsetInt
	}
	if req.Offset < 0 {
		req.Offset = 0 // Default
	}

	// Call the execution tracking functionality
	response, err := a.ListWorkflowExecutions(ctx, req)
	if err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to list executions: %v", err)},
			IsError: true,
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{response},
		IsError: false,
	}, nil
}

// handleExecutionGet handles the workflow_execution_get tool (exposed as core_workflow_execution_get)
func (a *Adapter) handleExecutionGet(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	// Parse request arguments
	req := &api.GetWorkflowExecutionRequest{
		IncludeSteps: true, // Default to true
	}

	executionID, ok := args["execution_id"].(string)
	if !ok || executionID == "" {
		return &api.CallToolResult{
			Content: []interface{}{"execution_id is required"},
			IsError: true,
		}, nil
	}
	req.ExecutionID = executionID

	// Validate include_steps arg
	if includeStepsVal, ok := args["include_steps"]; ok {
		if includeSteps, ok := includeStepsVal.(bool); ok {
			req.IncludeSteps = includeSteps
		} else {
			return &api.CallToolResult{
				Content: []interface{}{"include_steps must be a boolean value"},
				IsError: true,
			}, nil
		}
	}

	// Validate step_id arg - check for empty string and null
	if stepIDVal, exists := args["step_id"]; exists {
		if stepIDVal == nil {
			return &api.CallToolResult{
				Content: []interface{}{"step_id cannot be null"},
				IsError: true,
			}, nil
		}
		if stepIDStr, ok := stepIDVal.(string); ok {
			if stepIDStr == "" {
				// Empty step_id is explicitly invalid per BDD requirements
				return &api.CallToolResult{
					Content: []interface{}{"step_id is invalid: cannot be empty"},
					IsError: true,
				}, nil
			}
			req.StepID = stepIDStr
		}
	}

	// Call the execution tracking functionality
	execution, err := a.GetWorkflowExecution(ctx, req)
	if err != nil {
		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("Failed to get execution: %v", err)},
			IsError: true,
		}, nil
	}

	// For summary mode, create a custom response that completely omits the "steps" field
	if !req.IncludeSteps && execution.Steps == nil {
		summaryResponse := map[string]interface{}{
			"execution_id":  execution.ExecutionID,
			"workflow_name": execution.WorkflowName,
			"status":        execution.Status,
			"started_at":    execution.StartedAt,
			"duration_ms":   execution.DurationMs,
			"input":         execution.Input,
		}

		// Add optional fields only if they exist
		if execution.CompletedAt != nil {
			summaryResponse["completed_at"] = execution.CompletedAt
		}
		if execution.Result != nil {
			summaryResponse["result"] = execution.Result
		}
		if execution.Error != nil {
			summaryResponse["error"] = execution.Error
		}

		return &api.CallToolResult{
			Content: []interface{}{summaryResponse},
			IsError: false,
		}, nil
	}

	return &api.CallToolResult{
		Content: []interface{}{execution},
		IsError: false,
	}, nil
}

// convertToWorkflow converts structured arguments to api.Workflow
func convertToWorkflow(args map[string]interface{}) (api.Workflow, error) {
	var wf api.Workflow

	// Required fields
	name, ok := args["name"].(string)
	if !ok || name == "" {
		return wf, fmt.Errorf("name argument is required")
	}
	wf.Name = name

	// Optional fields
	if desc, ok := args["description"].(string); ok {
		wf.Description = desc
	}

	// Convert args
	if argsParam, ok := args["args"].(map[string]interface{}); ok {
		argsDefinition, err := convertArgsDefinition(argsParam)
		if err != nil {
			return wf, fmt.Errorf("validation failed: args: %v", err)
		}
		wf.Args = argsDefinition
	}
	// Args are optional, so no error if not provided

	// Convert steps
	if stepsParam, ok := args["steps"].([]interface{}); ok {
		steps, err := convertWorkflowSteps(stepsParam)
		if err != nil {
			return wf, fmt.Errorf("validation failed: steps: %v", err)
		}
		wf.Steps = steps
	} else {
		return wf, fmt.Errorf("steps argument is required")
	}

	// Set timestamps
	wf.CreatedAt = time.Now()
	wf.LastModified = time.Now()

	return wf, nil
}

// convertArgsDefinition converts a map[string]interface{} to map[string]api.ArgDefinition
func convertArgsDefinition(argsParam map[string]interface{}) (map[string]api.ArgDefinition, error) {
	argsDefinition := make(map[string]api.ArgDefinition)

	for name, argParam := range argsParam {
		argMap, ok := argParam.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("argument %s is not a valid object", name)
		}

		var argDef api.ArgDefinition

		// Type is required
		if argType, ok := argMap["type"].(string); ok {
			argDef.Type = argType
		} else {
			return nil, fmt.Errorf("argument %s: type is required", name)
		}

		// Required field (default to false)
		if required, ok := argMap["required"].(bool); ok {
			argDef.Required = required
		}

		// Description field
		if desc, ok := argMap["description"].(string); ok {
			argDef.Description = desc
		}

		// Default value
		if def, exists := argMap["default"]; exists {
			argDef.Default = def
		}

		argsDefinition[name] = argDef
	}

	return argsDefinition, nil
}

// convertWorkflowSteps converts []interface{} to []api.WorkflowStep
func convertWorkflowSteps(stepsParam []interface{}) ([]api.WorkflowStep, error) {
	var steps []api.WorkflowStep

	for i, stepParam := range stepsParam {
		stepMap, ok := stepParam.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("step %d is not a valid object", i)
		}

		var step api.WorkflowStep

		// ID is required
		if id, ok := stepMap["id"].(string); ok {
			if id == "" {
				return nil, fmt.Errorf("step %d: id cannot be empty", i)
			}
			step.ID = id
		} else {
			return nil, fmt.Errorf("step %d: id is required", i)
		}

		// Tool is required
		if tool, ok := stepMap["tool"].(string); ok {
			if tool == "" {
				return nil, fmt.Errorf("step %d: tool cannot be empty", i)
			}
			step.Tool = tool
		} else {
			return nil, fmt.Errorf("step %d: tool is required", i)
		}

		// Condition (optional)
		if conditionParam, ok := stepMap["condition"].(map[string]interface{}); ok {
			condition, err := convertWorkflowCondition(conditionParam)
			if err != nil {
				return nil, fmt.Errorf("step %d: invalid condition: %v", i, err)
			}
			step.Condition = &condition
		}

		// Args (optional)
		if args, ok := stepMap["args"].(map[string]interface{}); ok {
			step.Args = args
		}

		// Store (optional)
		if store, ok := stepMap["store"].(bool); ok {
			step.Store = store
		}

		// Description (optional)
		if description, ok := stepMap["description"].(string); ok {
			step.Description = description
		}

		// AllowFailure (optional)
		if allowFailure, ok := stepMap["allow_failure"].(bool); ok {
			step.AllowFailure = allowFailure
		}

		steps = append(steps, step)
	}

	return steps, nil
}

// convertWorkflowCondition converts a condition map to api.WorkflowCondition
func convertWorkflowCondition(conditionParam map[string]interface{}) (api.WorkflowCondition, error) {
	var condition api.WorkflowCondition

	// Tool (optional when from_step is used)
	if tool, ok := conditionParam["tool"].(string); ok {
		condition.Tool = tool
	}

	// FromStep (optional)
	if fromStep, ok := conditionParam["from_step"].(string); ok {
		condition.FromStep = fromStep
	}

	// Either tool or from_step must be provided
	if condition.Tool == "" && condition.FromStep == "" {
		return condition, fmt.Errorf("either tool or from_step is required")
	}

	// Args (optional)
	if args, ok := conditionParam["args"].(map[string]interface{}); ok {
		condition.Args = args
	}

	// Track whether we have any expectations
	hasExpect := false
	hasExpectNot := false

	// Expect (optional)
	if expectParam, ok := conditionParam["expect"].(map[string]interface{}); ok {
		expect, err := convertWorkflowConditionExpectation(expectParam)
		if err != nil {
			return condition, fmt.Errorf("invalid expect: %v", err)
		}
		condition.Expect = expect
		hasExpect = true
	}

	// ExpectNot (optional)
	if expectNotParam, ok := conditionParam["expect_not"].(map[string]interface{}); ok {
		expectNot, err := convertWorkflowConditionExpectation(expectNotParam)
		if err != nil {
			return condition, fmt.Errorf("invalid expect_not: %v", err)
		}
		condition.ExpectNot = expectNot
		hasExpectNot = true
	}

	// At least one of expect or expect_not must be provided
	if !hasExpect && !hasExpectNot {
		return condition, fmt.Errorf("either expect or expect_not is required")
	}

	return condition, nil
}

// convertWorkflowConditionExpectation converts an expect map to api.WorkflowConditionExpectation
func convertWorkflowConditionExpectation(expectParam map[string]interface{}) (api.WorkflowConditionExpectation, error) {
	var expect api.WorkflowConditionExpectation

	// Success (optional, defaults to false)
	if success, ok := expectParam["success"].(bool); ok {
		expect.Success = success
	}

	// JsonPath (optional)
	if jsonPathParam, ok := expectParam["json_path"].(map[string]interface{}); ok {
		expect.JsonPath = jsonPathParam
	}

	// At least one of success or json_path must be provided
	if !expect.Success && len(expect.JsonPath) == 0 {
		// If success was explicitly set to false, that's okay
		if successVal, exists := expectParam["success"]; exists {
			if successBool, ok := successVal.(bool); ok && !successBool {
				// success=false is explicitly set, this is valid
				return expect, nil
			}
		}
		// Neither success nor json_path provided
		return expect, fmt.Errorf("either success field or json_path must be provided")
	}

	return expect, nil
}

// getWorkflowArgsSchema returns the detailed schema definition for workflow arguments
func getWorkflowArgsSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":        "object",
		"description": "Workflow arguments definition with validation rules",
		"additionalProperties": map[string]interface{}{
			"type":                 "object",
			"description":          "Argument definition with validation rules",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"type": map[string]interface{}{
					"type":        "string",
					"description": "Expected data type for validation",
					"enum":        []string{"string", "integer", "boolean", "number", "object", "array"},
				},
				"required": map[string]interface{}{
					"type":        "boolean",
					"description": "Whether this argument is required",
				},
				"default": map[string]interface{}{
					"description": "Default value if argument is not provided",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "Human-readable documentation for this argument",
				},
			},
			"required": []string{"type"},
		},
	}
}

// getWorkflowStepsSchema returns the detailed schema definition for workflow steps
func getWorkflowStepsSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":        "array",
		"description": "Workflow steps defining the sequence of operations",
		"items": map[string]interface{}{
			"type":                 "object",
			"description":          "Individual workflow step configuration",
			"additionalProperties": false,
			"properties": map[string]interface{}{
				"id": map[string]interface{}{
					"type":        "string",
					"description": "Unique identifier for this step within the workflow",
				},
				"tool": map[string]interface{}{
					"type":        "string",
					"description": "Name of the tool to execute for this step",
				},
				"args": map[string]interface{}{
					"type":        "object",
					"description": "Arguments to pass to the tool, supporting templating with {{.argName}} for workflow args and {{stepId.field}} for step outputs",
				},
				"condition": map[string]interface{}{
					"type":                 "object",
					"description":          "Optional condition that determines whether this step should execute",
					"additionalProperties": false,
					"properties": map[string]interface{}{
						"tool": map[string]interface{}{
							"type":        "string",
							"description": "Tool to call for condition evaluation",
						},
						"args": map[string]interface{}{
							"type":        "object",
							"description": "Arguments for the condition tool",
						},
						"expect": map[string]interface{}{
							"type":        "object",
							"description": "Expected results for condition evaluation",
							"properties": map[string]interface{}{
								"success": map[string]interface{}{
									"type":        "boolean",
									"description": "Whether the tool call should succeed",
								},
								"json_path": map[string]interface{}{
									"type":        "object",
									"description": "JSON path expressions to evaluate against tool result",
									"additionalProperties": map[string]interface{}{
										"description": "Expected value for the JSON path",
									},
								},
							},
						},
					},
					"required": []string{"tool"},
				},
				"allow_failure": map[string]interface{}{
					"type":        "boolean",
					"description": "Whether this step is allowed to fail without failing the workflow",
				},
				"outputs": map[string]interface{}{
					"type":        "object",
					"description": "Defines how step results should be stored and made available to subsequent steps",
					"additionalProperties": map[string]interface{}{
						"description": "Output variable assignment, can be a static value or template expression",
					},
				},
				"store": map[string]interface{}{
					"type":        "boolean",
					"description": "Whether the step result should be stored in workflow results",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "Human-readable documentation for this step's purpose",
				},
			},
			"required": []string{"id", "tool"},
		},
		"minItems": 1,
	}
}

// generateCRDEvent creates a Kubernetes event for Workflow CRD operations.
// The message and eventType are determined by the event generator's template engine based on the reason.
func (a *Adapter) generateCRDEvent(name string, reason events.EventReason, data events.EventData) {
	eventManager := api.GetEventManager()
	if eventManager == nil {
		// Event manager not available, skip event generation
		return
	}

	// Create an object reference for the Workflow CRD
	objectRef := api.ObjectReference{
		Kind:      "Workflow",
		Name:      name,
		Namespace: a.namespace,
	}

	// Populate event data
	data.Name = name
	if data.Namespace == "" {
		data.Namespace = a.namespace
	}

	// Note: message and eventType parameters are provided for interface compliance,
	// but the actual values are determined by the event generator's template engine
	// based on the reason code.
	err := eventManager.CreateEvent(context.Background(), objectRef, string(reason), data.Error, "")
	if err != nil {
		// Log error but don't fail the operation
		logging.Debug("WorkflowAdapter", "Failed to generate event %s for Workflow %s: %v", string(reason), name, err)
	} else {
		logging.Debug("WorkflowAdapter", "Generated event %s for Workflow %s", string(reason), name)
	}
}

// GenerateStepEvent implements the EventCallback interface for step-level events
func (a *Adapter) GenerateStepEvent(workflowName string, stepID string, eventType string, data map[string]interface{}) {
	var reason events.EventReason
	var eventData events.EventData

	// Map event types to event reasons
	switch eventType {
	case "condition_evaluated":
		reason = events.ReasonWorkflowStepConditionEvaluated
		eventData = events.EventData{
			StepID:          stepID,
			StepTool:        getStringFromMap(data, "tool"),
			ConditionResult: getStringFromMap(data, "condition_result"),
		}
	case "step_skipped":
		reason = events.ReasonWorkflowStepSkipped
		eventData = events.EventData{
			StepID:          stepID,
			StepTool:        getStringFromMap(data, "tool"),
			ConditionResult: getStringFromMap(data, "condition_result"),
		}
	case "step_started":
		reason = events.ReasonWorkflowStepStarted
		eventData = events.EventData{
			StepID:   stepID,
			StepTool: getStringFromMap(data, "tool"),
		}
	case "step_completed":
		reason = events.ReasonWorkflowStepCompleted
		eventData = events.EventData{
			StepID:   stepID,
			StepTool: getStringFromMap(data, "tool"),
		}
	case "step_failed":
		reason = events.ReasonWorkflowStepFailed
		eventData = events.EventData{
			StepID:       stepID,
			StepTool:     getStringFromMap(data, "tool"),
			Error:        getStringFromMap(data, "error"),
			AllowFailure: getBoolFromMap(data, "allow_failure"),
		}
	default:
		// Unknown event type, skip
		return
	}

	// Generate the event
	a.generateCRDEvent(workflowName, reason, eventData)
}

// Helper functions to extract values from map
func getStringFromMap(data map[string]interface{}, key string) string {
	if val, ok := data[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

func getBoolFromMap(data map[string]interface{}, key string) bool {
	if val, ok := data[key]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return false
}
