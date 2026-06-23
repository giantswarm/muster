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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fieldOutput is the argument/field name for both the per-step output flag and
// the workflow-level output template.
const fieldOutput = "output"

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
	// MissingToolsForSession returns the subset of toolNames that is unavailable
	// for the calling session. Availability is resolved against the session's
	// accessible tools, so SSO family tools are considered even without a prior
	// list_tools call (see #764). The session tool set is resolved once, so
	// checking all of a workflow's step tools is a single pass.
	MissingToolsForSession(ctx context.Context, toolNames []string) []string
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
	if missingTools := a.findMissingTools(ctx, workflow); len(missingTools) > 0 {
		// Generate workflow unavailable event with missing tools
		a.generateCRDEvent(workflowName, events.ReasonWorkflowUnavailable, events.EventData{
			Operation: opExecute,
			ToolNames: missingTools,
		})

		return &api.CallToolResult{
			Content: []interface{}{fmt.Sprintf("workflow %s is not available (missing required tools)", workflowName)},
			IsError: true,
		}, nil
	}

	// Generate execution started event
	a.generateCRDEvent(workflowName, events.ReasonWorkflowExecutionStarted, events.EventData{
		Operation: opExecute,
		StepCount: len(workflow.Steps),
	})

	// Execute workflow with automatic tracking
	result, execution, err := a.executionTracker.TrackExecution(ctx, workflowName, args, func() (*mcp.CallToolResult, error) {
		return a.executor.ExecuteWorkflow(ctx, workflow, args)
	})

	// Generate execution tracked event
	if execution != nil {
		a.generateCRDEvent(workflowName, events.ReasonWorkflowExecutionTracked, events.EventData{
			Operation:   opExecute,
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
			Operation: opExecute,
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
		Operation: opExecute,
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

// GetWorkflows returns information about all workflows.
//
// Availability is computed against the process-global tool view (no session
// context). Session-aware availability is computed in the tool-dispatch path
// (handleList) via getWorkflows.
func (a *Adapter) GetWorkflows() []api.Workflow {
	return a.getWorkflows(context.Background())
}

// getWorkflows returns all workflows with availability evaluated for the
// calling session carried by ctx.
func (a *Adapter) getWorkflows(ctx context.Context) []api.Workflow {
	workflowCRDs, err := a.client.ListWorkflows(ctx, a.namespace)
	if err != nil {
		logging.Error("WorkflowAdapter", err, "Failed to list workflows")
		return []api.Workflow{}
	}

	workflows := make([]api.Workflow, 0, len(workflowCRDs))
	for _, workflowCRD := range workflowCRDs {
		workflow := a.convertCRDToWorkflow(&workflowCRD)
		workflow.Available = a.isWorkflowAvailable(ctx, workflow)
		workflows = append(workflows, *workflow)
	}

	return workflows
}

// GetWorkflow returns a specific workflow definition.
//
// Availability is computed against the process-global tool view (no session
// context). Session-aware availability is computed in the tool-dispatch path
// (handleGet / handleWorkflowAvailable) via getWorkflow.
func (a *Adapter) GetWorkflow(name string) (*api.Workflow, error) {
	return a.getWorkflow(context.Background(), name)
}

// getWorkflow returns a specific workflow with availability evaluated for the
// calling session carried by ctx.
func (a *Adapter) getWorkflow(ctx context.Context, name string) (*api.Workflow, error) {
	workflowCRD, err := a.client.GetWorkflow(ctx, name, a.namespace)
	if err != nil {
		return nil, api.NewWorkflowNotFoundError(name)
	}

	workflow := a.convertCRDToWorkflow(workflowCRD)
	workflow.Available = a.isWorkflowAvailable(ctx, workflow)
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

	// Build the CRD from the internal workflow representation.
	workflowCRD := a.convertWorkflowToCRD(&wf)
	workflowCRD.TypeMeta = metav1.TypeMeta{
		APIVersion: "muster.giantswarm.io/v1alpha1",
		Kind:       "Workflow",
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
			Operation: opValidate,
		})
		return err
	}

	// Basic validation
	if wf.Name == "" {
		err := fmt.Errorf("workflow name is required")
		a.generateCRDEvent("", events.ReasonWorkflowValidationFailed, events.EventData{
			Error:     err.Error(),
			Operation: opValidate,
		})
		return err
	}
	if len(wf.Steps) == 0 {
		err := fmt.Errorf("workflow must have at least one step")
		a.generateCRDEvent(wf.Name, events.ReasonWorkflowValidationFailed, events.EventData{
			Error:     err.Error(),
			Operation: opValidate,
		})
		return err
	}

	// fail records a validation failure event and returns the error.
	fail := func(err error) error {
		a.generateCRDEvent(wf.Name, events.ReasonWorkflowValidationFailed, events.EventData{
			Error:     err.Error(),
			Operation: opValidate,
		})
		return err
	}

	// Step validation
	stepIDs := make(map[string]bool)
	for i, step := range wf.Steps {
		if step.ID == "" {
			return fail(fmt.Errorf("step %d: step ID cannot be empty", i))
		}
		if stepIDs[step.ID] {
			return fail(fmt.Errorf("duplicate step ID '%s' found", step.ID))
		}
		stepIDs[step.ID] = true

		// A step must be exactly one of: tool call, forEach loop, or parallel group.
		composite := step.ForEach != nil || len(step.Parallel) > 0
		switch {
		case step.Tool == "" && !composite:
			return fail(fmt.Errorf("step %d (%s): one of tool, forEach, or parallel is required", i, step.ID))
		case step.Tool != "" && composite:
			return fail(fmt.Errorf("step %d (%s): tool is mutually exclusive with forEach/parallel", i, step.ID))
		case step.ForEach != nil && len(step.Parallel) > 0:
			return fail(fmt.Errorf("step %d (%s): forEach and parallel are mutually exclusive", i, step.ID))
		}

		if err := validateWorkflowCondition(step.Condition); err != nil {
			return fail(fmt.Errorf("step %s: %w", step.ID, err))
		}

		if step.ForEach != nil {
			if step.ForEach.Items == "" {
				return fail(fmt.Errorf("step %s: forEach.items is required", step.ID))
			}
			if len(step.ForEach.Steps) == 0 {
				return fail(fmt.Errorf("step %s: forEach.steps must contain at least one sub-step", step.ID))
			}
			if err := validateWorkflowSubSteps(fmt.Sprintf("step %s forEach", step.ID), step.ForEach.Steps); err != nil {
				return fail(err)
			}
		}

		if len(step.Parallel) > 0 {
			if err := validateWorkflowSubSteps(fmt.Sprintf("step %s parallel", step.ID), step.Parallel); err != nil {
				return fail(err)
			}
		}
	}

	if err := validateWorkflowSubSteps("onFailure", wf.OnFailure); err != nil {
		return fail(err)
	}

	logAuthoringWarnings(&wf)

	// Generate validation success event
	a.generateCRDEvent(wf.Name, events.ReasonWorkflowValidationSucceeded, events.EventData{
		Operation: opValidate,
		StepCount: len(wf.Steps),
	})

	return nil
}

// logAuthoringWarnings emits the workflow's non-fatal authoring lint warnings
// (deprecated `store` usage, per-step output flags rendered inert by an output
// output template) at the structured create/validate path. The detection lives in
// the api package so the CRD reconciler emits the same nudges.
func logAuthoringWarnings(wf *api.Workflow) {
	for _, w := range api.AuthoringWarnings(wf) {
		logging.Warn("WorkflowExecutor", "Workflow %q %s", wf.Name, w)
	}
}

// validateWorkflowCondition checks the structural constraint the executor
// relies on: a condition selects its evaluation source with exactly one of
// template, tool, or fromStep. A boolean template gate is mutually exclusive
// with a tool/fromStep condition (when a template is set, Tool/FromStep/Expect
// are ignored), and tool and fromStep cannot be combined.
func validateWorkflowCondition(c *api.WorkflowCondition) error {
	if c == nil {
		return nil
	}
	set := 0
	if c.Template != "" {
		set++
	}
	if c.Tool != "" {
		set++
	}
	if c.FromStep != "" {
		set++
	}
	// Surface the template combination explicitly so the message names the
	// offending fields (and matches the documented/validated behaviour).
	if c.Template != "" && (c.Tool != "" || c.FromStep != "") {
		return fmt.Errorf("condition.template is mutually exclusive with tool/fromStep")
	}
	if set == 0 {
		return fmt.Errorf("condition requires exactly one of template, tool, or fromStep")
	}
	if set > 1 {
		return fmt.Errorf("condition: tool and fromStep are mutually exclusive (set exactly one of template, tool, or fromStep)")
	}
	return nil
}

// validateWorkflowSubSteps validates the sub-steps used inside forEach bodies,
// parallel groups, and onFailure handlers. Sub-step IDs must be present and
// unique within the group, every sub-step must name a tool, and any condition
// must be structurally valid. label identifies the group in error messages.
func validateWorkflowSubSteps(label string, subs []api.WorkflowSubStep) error {
	ids := make(map[string]bool, len(subs))
	for j, sub := range subs {
		if sub.ID == "" {
			return fmt.Errorf("%s sub-step %d: id cannot be empty", label, j)
		}
		if ids[sub.ID] {
			return fmt.Errorf("%s: duplicate sub-step id '%s'", label, sub.ID)
		}
		ids[sub.ID] = true
		if sub.Tool == "" {
			return fmt.Errorf("%s sub-step %s: tool cannot be empty", label, sub.ID)
		}
		if err := validateWorkflowCondition(sub.Condition); err != nil {
			return fmt.Errorf("%s sub-step %s: %w", label, sub.ID, err)
		}
	}
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
		_ = a.client.Close()
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
		Name:         workflowCRD.Name,
		Description:  workflowCRD.Spec.Description,
		Labels:       workflowCRD.Labels,
		Args:         a.convertArgDefinitions(workflowCRD.Spec.Args),
		Steps:        a.convertWorkflowSteps(workflowCRD.Spec.Steps),
		OnFailure:    a.convertSubSteps(workflowCRD.Spec.OnFailure),
		CreatedAt:    workflowCRD.CreationTimestamp.Time,
		LastModified: workflowCRD.CreationTimestamp.Time,
	}

	if len(workflowCRD.Spec.Output) > 0 {
		workflow.Output = a.convertRawExtensionMap(workflowCRD.Spec.Output)
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
			OnFailure:   a.convertSubStepsToCRD(workflow.OnFailure),
			Output:      a.workflowOutputToCRD(workflow.Output),
		},
	}
}

// workflowOutputToCRD converts an internal output template to CRD raw-JSON
// form, returning nil when no output template is declared.
func (a *Adapter) workflowOutputToCRD(output map[string]interface{}) map[string]apiextensionsv1.JSON {
	if len(output) == 0 {
		return nil
	}
	return a.convertToRawExtensionMap(output)
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
			Output:       crdStep.Output,
			Store:        crdStep.Store,
			AllowFailure: crdStep.AllowFailure,
			Parallel:     a.convertSubSteps(crdStep.Parallel),
			Description:  crdStep.Description,
		}

		if crdStep.Condition != nil {
			step.Condition = a.convertWorkflowCondition(crdStep.Condition)
		}

		if crdStep.ForEach != nil {
			step.ForEach = &api.WorkflowForEach{
				Items: crdStep.ForEach.Items,
				As:    crdStep.ForEach.As,
				Steps: a.convertSubSteps(crdStep.ForEach.Steps),
			}
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
			Output:       step.Output,
			Store:        step.Store,
			AllowFailure: step.AllowFailure,
			Parallel:     a.convertSubStepsToCRD(step.Parallel),
			Description:  step.Description,
		}

		if step.Condition != nil {
			crdStep.Condition = a.convertWorkflowConditionToCRD(step.Condition)
		}

		if step.ForEach != nil {
			crdStep.ForEach = &musterv1alpha1.WorkflowForEach{
				Items: step.ForEach.Items,
				As:    step.ForEach.As,
				Steps: a.convertSubStepsToCRD(step.ForEach.Steps),
			}
		}

		crdSteps = append(crdSteps, crdStep)
	}
	return crdSteps
}

// convertSubSteps converts CRD WorkflowSubSteps to internal format
func (a *Adapter) convertSubSteps(crdSubSteps []musterv1alpha1.WorkflowSubStep) []api.WorkflowSubStep {
	if len(crdSubSteps) == 0 {
		return nil
	}
	subSteps := make([]api.WorkflowSubStep, 0, len(crdSubSteps))
	for _, crdSub := range crdSubSteps {
		sub := api.WorkflowSubStep{
			ID:           crdSub.ID,
			Tool:         crdSub.Tool,
			Args:         a.convertRawExtensionMap(crdSub.Args),
			Output:       crdSub.Output,
			Store:        crdSub.Store,
			AllowFailure: crdSub.AllowFailure,
			Description:  crdSub.Description,
		}
		if crdSub.Condition != nil {
			sub.Condition = a.convertWorkflowCondition(crdSub.Condition)
		}
		subSteps = append(subSteps, sub)
	}
	return subSteps
}

// convertSubStepsToCRD converts internal WorkflowSubSteps to CRD format
func (a *Adapter) convertSubStepsToCRD(subSteps []api.WorkflowSubStep) []musterv1alpha1.WorkflowSubStep {
	if len(subSteps) == 0 {
		return nil
	}
	crdSubSteps := make([]musterv1alpha1.WorkflowSubStep, 0, len(subSteps))
	for _, sub := range subSteps {
		crdSub := musterv1alpha1.WorkflowSubStep{
			ID:           sub.ID,
			Tool:         sub.Tool,
			Args:         a.convertToRawExtensionMap(sub.Args),
			Output:       sub.Output,
			Store:        sub.Store,
			AllowFailure: sub.AllowFailure,
			Description:  sub.Description,
		}
		if sub.Condition != nil {
			crdSub.Condition = a.convertWorkflowConditionToCRD(sub.Condition)
		}
		crdSubSteps = append(crdSubSteps, crdSub)
	}
	return crdSubSteps
}

// convertWorkflowCondition converts CRD WorkflowCondition to internal format
func (a *Adapter) convertWorkflowCondition(crdCondition *musterv1alpha1.WorkflowCondition) *api.WorkflowCondition {
	condition := &api.WorkflowCondition{
		Template: crdCondition.Template,
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
		Template: condition.Template,
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

// convertRawExtension converts apiextensionsv1.JSON to interface{}.
// Field name retained from when this wrapped runtime.RawExtension; both types
// just hold a raw JSON []byte and the conversion semantics are identical.
func (a *Adapter) convertRawExtension(rawExt *apiextensionsv1.JSON) interface{} {
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

// convertToRawExtension converts interface{} to apiextensionsv1.JSON.
func (a *Adapter) convertToRawExtension(value interface{}) *apiextensionsv1.JSON {
	if value == nil {
		return &apiextensionsv1.JSON{Raw: []byte("null")}
	}

	// Use simple JSON marshaling for all types to avoid recursion
	raw, err := json.Marshal(value)
	if err != nil {
		// If marshaling fails, return as null
		return &apiextensionsv1.JSON{Raw: []byte("null")}
	}

	return &apiextensionsv1.JSON{Raw: raw}
}

// convertRawExtensionMap converts map[string]apiextensionsv1.JSON to map[string]interface{}.
func (a *Adapter) convertRawExtensionMap(rawExtMap map[string]apiextensionsv1.JSON) map[string]interface{} {
	if rawExtMap == nil {
		return make(map[string]interface{})
	}
	result := make(map[string]interface{})
	for key, rawExt := range rawExtMap {
		v := rawExt
		if converted := a.convertRawExtension(&v); converted != nil {
			result[key] = converted
		}
	}
	return result
}

// convertToRawExtensionMap converts map[string]interface{} to map[string]apiextensionsv1.JSON.
func (a *Adapter) convertToRawExtensionMap(valueMap map[string]interface{}) map[string]apiextensionsv1.JSON {
	if valueMap == nil {
		return make(map[string]apiextensionsv1.JSON)
	}
	result := make(map[string]apiextensionsv1.JSON)
	for key, value := range valueMap {
		if value != nil {
			if rawExt := a.convertToRawExtension(value); rawExt != nil {
				result[key] = *rawExt
			}
		}
	}
	return result
}

// isWorkflowAvailable checks if a workflow has all required tools available.
//
// Availability is evaluated session-aware and transitively via findMissingTools:
// each step tool is resolved against the calling session's accessible tools
// (carried by ctx), so SSO / auth-protected family tools are considered even
// when the session has not called list_tools yet (#764), and nested workflow
// steps require the referenced workflow to exist and be itself available. When
// ctx carries no session, the checker falls back to the process-global view.
func (a *Adapter) isWorkflowAvailable(ctx context.Context, workflow *api.Workflow) bool {
	return len(a.findMissingTools(ctx, workflow)) == 0
}

// workflowManagementTools are the meta-tools exposed under the workflow_
// prefix. They are provided by muster itself and are always available, so they
// must not be treated as nested workflow execution tools.
var workflowManagementTools = map[string]struct{}{
	"workflow_list":           {},
	"workflow_get":            {},
	"workflow_create":         {},
	"workflow_update":         {},
	"workflow_delete":         {},
	"workflow_validate":       {},
	"workflow_available":      {},
	"workflow_execution_list": {},
	"workflow_execution_get":  {},
}

// nestedWorkflowName reports whether toolName is a nested workflow execution
// tool (workflow_<name>) and, if so, returns the referenced workflow name. The
// workflow_ management meta-tools (workflow_list, workflow_get, ...) are not
// nested workflows and resolve as ordinary core tools.
func nestedWorkflowName(toolName string) (string, bool) {
	const prefix = "workflow_"
	if !strings.HasPrefix(toolName, prefix) {
		return "", false
	}
	if _, isManagement := workflowManagementTools[toolName]; isManagement {
		return "", false
	}
	return strings.TrimPrefix(toolName, prefix), true
}

// findMissingTools returns the deduplicated step tools that are not available
// for the workflow in the calling session.
//
// Availability is transitive across nested workflows. Step tools are resolved
// by kind:
//   - Nested workflow execution tools (workflow_<name>) require that the
//     referenced workflow exists AND is itself available: the check descends
//     into it and reports its missing tools. The aggregator's by-prefix
//     core-tool shortcut would otherwise report any workflow_<name> available
//     even when the referenced workflow is missing or transitively broken.
//   - All other step tools are resolved against the calling session's accessible
//     tools. The whole tree's external tools are gathered first and checked in a
//     single MissingToolsForSession call, so the session's (potentially
//     store-backed) tool set is resolved exactly once per check regardless of
//     nesting depth (#764), and SSO / auth-protected family tools are considered
//     even without a prior list_tools call.
//
// Reported names are the actual unavailable tools (a deep backend tool or a
// missing workflow_<name>), so the cause surfaces at the top level.
func (a *Adapter) findMissingTools(ctx context.Context, workflow *api.Workflow) []string {
	a.mu.RLock()
	generating := a.generatingTools
	a.mu.RUnlock()

	// During tool generation, assume everything is available to avoid a
	// circular dependency. Same when no checker is wired (e.g. in tests).
	if generating || a.toolChecker == nil {
		return nil
	}

	// Walk the whole nested-workflow tree once, recording in discovery order
	// every tool whose availability decides the result: external (non-nested)
	// step tools, plus nested workflow_<name> steps whose referenced workflow
	// does not exist (already known unavailable).
	var ordered []string
	knownMissing := make(map[string]struct{})
	a.walkStepTools(ctx, workflow, map[string]struct{}{}, make(map[string]struct{}), &ordered, knownMissing)

	// Resolve every external tool's availability in a single session-scoped
	// check, so the session tool set is built once for the entire tree.
	externalNames := make([]string, 0, len(ordered))
	for _, name := range ordered {
		if _, known := knownMissing[name]; !known {
			externalNames = append(externalNames, name)
		}
	}
	externalMissing := make(map[string]struct{})
	for _, name := range a.toolChecker.MissingToolsForSession(ctx, externalNames) {
		externalMissing[name] = struct{}{}
	}

	// Emit the unavailable tools in discovery order.
	var missing []string
	for _, name := range ordered {
		if _, known := knownMissing[name]; known {
			missing = append(missing, name)
			continue
		}
		if _, ok := externalMissing[name]; ok {
			missing = append(missing, name)
		}
	}
	return missing
}

// walkStepTools appends, in discovery order, the step tools of workflow (and of
// the nested workflows reachable from it) whose availability decides the
// workflow's availability:
//   - every external (non-nested) step tool, and
//   - every nested workflow_<name> whose referenced workflow does not exist,
//     also recorded in knownMissing because a missing nested workflow is itself
//     an unavailable tool.
//
// Nested workflows that exist are descended into. path holds the workflow names
// currently on the recursion stack so cycles (A -> B -> A) stop descending
// instead of looping forever; seen deduplicates the recorded tool names across
// the whole tree.
func (a *Adapter) walkStepTools(ctx context.Context, workflow *api.Workflow, path, seen map[string]struct{}, ordered *[]string, knownMissing map[string]struct{}) {
	// Gather every tool whose availability matters: top-level step tools, the
	// tools of forEach/parallel sub-steps, and onFailure handler tools.
	var tools []string
	for _, step := range workflow.Steps {
		if step.Tool != "" {
			tools = append(tools, step.Tool)
		}
		if step.ForEach != nil {
			for _, sub := range step.ForEach.Steps {
				if sub.Tool != "" {
					tools = append(tools, sub.Tool)
				}
			}
		}
		for _, sub := range step.Parallel {
			if sub.Tool != "" {
				tools = append(tools, sub.Tool)
			}
		}
	}
	for _, sub := range workflow.OnFailure {
		if sub.Tool != "" {
			tools = append(tools, sub.Tool)
		}
	}

	for _, tool := range tools {
		name, isNested := nestedWorkflowName(tool)
		if !isNested {
			if _, dup := seen[tool]; dup {
				continue
			}
			seen[tool] = struct{}{}
			*ordered = append(*ordered, tool)
			continue
		}

		// A referenced workflow that does not exist is itself a missing tool.
		nestedCRD, err := a.client.GetWorkflow(ctx, name, a.namespace)
		if err != nil {
			if _, dup := seen[tool]; dup {
				continue
			}
			seen[tool] = struct{}{}
			knownMissing[tool] = struct{}{}
			*ordered = append(*ordered, tool)
			continue
		}

		// Stop descending on a cycle; the offending edge is left to whichever
		// level first detected a missing tool (if any).
		if _, onPath := path[name]; onPath {
			continue
		}
		path[name] = struct{}{}
		a.walkStepTools(ctx, a.convertCRDToWorkflow(nestedCRD), path, seen, ordered, knownMissing)
		delete(path, name)
	}
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
					jsonMap[api.FieldExecutionID] = executionID
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
					Type:        api.ArgTypeBoolean,
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
					Type:        api.ArgTypeString,
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
					Type:        api.ArgTypeString,
					Required:    true,
					Description: "Name of the workflow",
				},
				{
					Name:        "description",
					Type:        api.ArgTypeString,
					Required:    false,
					Description: "Description of the workflow",
				},
				{
					Name:        "args",
					Type:        api.ArgTypeObject,
					Required:    false,
					Description: "Workflow arguments definition",
					Schema:      getWorkflowArgsSchema(),
				},
				{
					Name:        api.FieldSteps,
					Type:        api.ArgTypeArray,
					Required:    true,
					Description: "Workflow steps",
					Schema:      getWorkflowStepsSchema(),
				},
				{
					Name:        "onFailure",
					Type:        api.ArgTypeArray,
					Required:    false,
					Description: "Cleanup/rollback steps run when the workflow fails",
					Schema:      getWorkflowOnFailureSchema(),
				},
				{
					Name:        fieldOutput,
					Type:        api.ArgTypeObject,
					Required:    false,
					Description: "Optional output template that shapes the returned document",
					Schema:      getWorkflowOutputSchema(),
				},
			},
		},
		{
			Name:        "workflow_update",
			Description: "Update an existing workflow",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        api.ArgTypeString,
					Required:    true,
					Description: "Name of the workflow to update",
				},
				{
					Name:        "description",
					Type:        api.ArgTypeString,
					Required:    false,
					Description: "Description of the workflow",
				},
				{
					Name:        "args",
					Type:        api.ArgTypeObject,
					Required:    false,
					Description: "Workflow arguments definition",
					Schema:      getWorkflowArgsSchema(),
				},
				{
					Name:        api.FieldSteps,
					Type:        api.ArgTypeArray,
					Required:    true,
					Description: "Workflow steps",
					Schema:      getWorkflowStepsSchema(),
				},
				{
					Name:        "onFailure",
					Type:        api.ArgTypeArray,
					Required:    false,
					Description: "Cleanup/rollback steps run when the workflow fails",
					Schema:      getWorkflowOnFailureSchema(),
				},
				{
					Name:        fieldOutput,
					Type:        api.ArgTypeObject,
					Required:    false,
					Description: "Optional output template that shapes the returned document",
					Schema:      getWorkflowOutputSchema(),
				},
			},
		},
		{
			Name:        "workflow_delete",
			Description: "Delete a workflow",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        api.ArgTypeString,
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
					Type:        api.ArgTypeString,
					Required:    true,
					Description: "Name of the workflow",
				},
				{
					Name:        "description",
					Type:        api.ArgTypeString,
					Required:    false,
					Description: "Description of the workflow",
				},
				{
					Name:        "args",
					Type:        api.ArgTypeObject,
					Required:    false,
					Description: "Workflow arguments definition",
					Schema:      getWorkflowArgsSchema(),
				},
				{
					Name:        api.FieldSteps,
					Type:        api.ArgTypeArray,
					Required:    true,
					Description: "Workflow steps",
					Schema:      getWorkflowStepsSchema(),
				},
				{
					Name:        "onFailure",
					Type:        api.ArgTypeArray,
					Required:    false,
					Description: "Cleanup/rollback steps run when the workflow fails",
					Schema:      getWorkflowOnFailureSchema(),
				},
				{
					Name:        fieldOutput,
					Type:        api.ArgTypeObject,
					Required:    false,
					Description: "Optional output template that shapes the returned document",
					Schema:      getWorkflowOutputSchema(),
				},
			},
		},
		{
			Name:        "workflow_available",
			Description: "Check if a workflow is available",
			Args: []api.ArgMetadata{
				{
					Name:        "name",
					Type:        api.ArgTypeString,
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
					Type:        api.ArgTypeString,
					Required:    false,
					Description: "Filter by workflow name",
				},
				{
					Name:        "status",
					Type:        api.ArgTypeString,
					Required:    false,
					Description: "Filter by execution status",
				},
				{
					Name:        "limit",
					Type:        api.ArgTypeNumber,
					Required:    false,
					Description: "Maximum number of executions to return",
					Default:     50,
				},
				{
					Name:        "offset",
					Type:        api.ArgTypeNumber,
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
					Name:        api.FieldExecutionID,
					Type:        api.ArgTypeString,
					Required:    true,
					Description: "ID of the execution",
				},
				{
					Name:        "include_steps",
					Type:        api.ArgTypeBoolean,
					Required:    false,
					Description: "Include step details",
					Default:     true,
				},
				{
					Name:        "step_id",
					Type:        api.ArgTypeString,
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
			Labels:      workflow.Labels,
		})
	}

	return tools
}

// ExecuteTool executes a tool by name
func (a *Adapter) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*api.CallToolResult, error) {
	switch {
	case toolName == "workflow_list":
		return a.handleList(ctx, args)
	case toolName == "workflow_get":
		return a.handleGet(ctx, args)
	case toolName == "workflow_create":
		return a.handleCreate(args)
	case toolName == "workflow_update":
		return a.handleUpdate(args)
	case toolName == "workflow_delete":
		return a.handleDelete(args)
	case toolName == "workflow_validate":
		return a.handleValidate(args)
	case toolName == "workflow_available":
		return a.handleWorkflowAvailable(ctx, args)
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
			Type:        api.ArgType(argDef.Type),
			Required:    argDef.Required,
			Description: argDef.Description,
			Default:     argDef.Default,
		}

		params = append(params, param)
	}

	return params
}

// Helper methods for handling management operations
func (a *Adapter) handleList(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	workflows := a.getWorkflows(ctx)

	var result []map[string]interface{}
	for _, wf := range workflows {
		workflowInfo := map[string]interface{}{
			api.FieldName: wf.Name,
			"available":   wf.Available,
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

func (a *Adapter) handleGet(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok {
		return &api.CallToolResult{
			Content: []interface{}{"name is required"},
			IsError: true,
		}, nil
	}

	workflow, err := a.getWorkflow(ctx, name)
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

func (a *Adapter) handleWorkflowAvailable(ctx context.Context, args map[string]interface{}) (*api.CallToolResult, error) {
	name, ok := args["name"].(string)
	if !ok {
		return &api.CallToolResult{
			Content: []interface{}{"name argument is required"},
			IsError: true,
		}, nil
	}

	workflow, err := a.getWorkflow(ctx, name)
	if err != nil {
		return &api.CallToolResult{
			Content: []interface{}{err.Error()},
			IsError: true,
		}, nil
	}
	available := a.isWorkflowAvailable(ctx, workflow)

	result := map[string]interface{}{
		api.FieldName: name,
		"available":   available,
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
		if status != "inprogress" && status != "completed" && status != "failed" { //nolint:goconst
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

	executionID, ok := args[api.FieldExecutionID].(string)
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
			api.FieldExecutionID: execution.ExecutionID,
			"workflow_name":      execution.WorkflowName,
			api.FieldStatus:      execution.Status,
			"started_at":         execution.StartedAt,
			"duration_ms":        execution.DurationMs,
			api.FieldInput:       execution.Input,
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

// pickString returns the string value of the first present key. It lets the
// structured create/update/validate path accept the canonical camelCase field
// name (matching the CRD and the documentation) while still honouring the
// legacy snake_case alias, e.g. pickString(m, "fromStep", "from_step").
func pickString(m map[string]interface{}, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := m[k].(string); ok {
			return v, true
		}
	}
	return "", false
}

// pickBool mirrors pickString for boolean fields (e.g. "allowFailure" /
// "allow_failure").
func pickBool(m map[string]interface{}, keys ...string) (bool, bool) {
	for _, k := range keys {
		if v, ok := m[k].(bool); ok {
			return v, true
		}
	}
	return false, false
}

// pickMap mirrors pickString for nested object fields (e.g. "expectNot" /
// "expect_not", "jsonPath" / "json_path").
func pickMap(m map[string]interface{}, keys ...string) (map[string]interface{}, bool) {
	for _, k := range keys {
		if v, ok := m[k].(map[string]interface{}); ok {
			return v, true
		}
	}
	return nil, false
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
	if stepsParam, ok := args[api.FieldSteps].([]interface{}); ok {
		steps, err := convertWorkflowSteps(stepsParam)
		if err != nil {
			return wf, fmt.Errorf("validation failed: steps: %v", err)
		}
		wf.Steps = steps
	} else {
		return wf, fmt.Errorf("steps argument is required")
	}

	// Convert onFailure handlers (optional)
	if onFailureParam, ok := args["onFailure"].([]interface{}); ok {
		subSteps, err := convertWorkflowSubSteps(onFailureParam)
		if err != nil {
			return wf, fmt.Errorf("validation failed: onFailure: %v", err)
		}
		wf.OnFailure = subSteps
	}

	// Convert output template (optional)
	if outputParam, ok := args[fieldOutput].(map[string]interface{}); ok {
		wf.Output = outputParam
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

		// forEach (optional, mutually exclusive with tool/parallel)
		if forEachParam, ok := stepMap["forEach"].(map[string]interface{}); ok {
			forEach, err := convertWorkflowForEach(forEachParam)
			if err != nil {
				return nil, fmt.Errorf("step %d (%s): invalid forEach: %v", i, step.ID, err)
			}
			step.ForEach = &forEach
		}

		// parallel (optional, mutually exclusive with tool/forEach)
		if parallelParam, ok := stepMap["parallel"].([]interface{}); ok {
			subSteps, err := convertWorkflowSubSteps(parallelParam)
			if err != nil {
				return nil, fmt.Errorf("step %d (%s): invalid parallel: %v", i, step.ID, err)
			}
			step.Parallel = subSteps
		}

		// Tool (optional when forEach or parallel is provided)
		composite := step.ForEach != nil || len(step.Parallel) > 0
		if tool, ok := stepMap["tool"].(string); ok {
			if tool == "" {
				return nil, fmt.Errorf("step %d (%s): tool cannot be empty", i, step.ID)
			}
			step.Tool = tool
		} else if !composite {
			return nil, fmt.Errorf("step %d (%s): one of tool, forEach, or parallel is required", i, step.ID)
		}
		if step.Tool != "" && composite {
			return nil, fmt.Errorf("step %d (%s): tool is mutually exclusive with forEach/parallel", i, step.ID)
		}
		if step.ForEach != nil && len(step.Parallel) > 0 {
			return nil, fmt.Errorf("step %d (%s): forEach and parallel are mutually exclusive", i, step.ID)
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

		// Output (optional) — include this step's result in the returned document.
		if output, ok := stepMap[fieldOutput].(bool); ok {
			step.Output = &output
		}

		// Store (optional, deprecated alias for output)
		if store, ok := stepMap["store"].(bool); ok {
			step.Store = store
		}

		// Description (optional)
		if description, ok := stepMap["description"].(string); ok {
			step.Description = description
		}

		// AllowFailure (optional). Accept the canonical camelCase name and the
		// legacy snake_case alias.
		if allowFailure, ok := pickBool(stepMap, "allowFailure", "allow_failure"); ok {
			step.AllowFailure = allowFailure
		}

		steps = append(steps, step)
	}

	return steps, nil
}

// convertWorkflowForEach converts a forEach map to api.WorkflowForEach
func convertWorkflowForEach(forEachParam map[string]interface{}) (api.WorkflowForEach, error) {
	var forEach api.WorkflowForEach

	items, ok := forEachParam["items"].(string)
	if !ok || items == "" {
		return forEach, fmt.Errorf("items is required and must be a template string")
	}
	forEach.Items = items

	if as, ok := forEachParam["as"].(string); ok {
		forEach.As = as
	}

	stepsParam, ok := forEachParam[api.FieldSteps].([]interface{})
	if !ok || len(stepsParam) == 0 {
		return forEach, fmt.Errorf("steps is required and must contain at least one sub-step")
	}
	subSteps, err := convertWorkflowSubSteps(stepsParam)
	if err != nil {
		return forEach, err
	}
	forEach.Steps = subSteps

	return forEach, nil
}

// convertWorkflowSubSteps converts []interface{} to []api.WorkflowSubStep
func convertWorkflowSubSteps(subStepsParam []interface{}) ([]api.WorkflowSubStep, error) {
	var subSteps []api.WorkflowSubStep

	for i, subStepParam := range subStepsParam {
		subStepMap, ok := subStepParam.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("sub-step %d is not a valid object", i)
		}

		var sub api.WorkflowSubStep

		if id, ok := subStepMap["id"].(string); ok && id != "" {
			sub.ID = id
		} else {
			return nil, fmt.Errorf("sub-step %d: id is required", i)
		}

		if tool, ok := subStepMap["tool"].(string); ok && tool != "" {
			sub.Tool = tool
		} else {
			return nil, fmt.Errorf("sub-step %d (%s): tool is required", i, sub.ID)
		}

		if conditionParam, ok := subStepMap["condition"].(map[string]interface{}); ok {
			condition, err := convertWorkflowCondition(conditionParam)
			if err != nil {
				return nil, fmt.Errorf("sub-step %d (%s): invalid condition: %v", i, sub.ID, err)
			}
			sub.Condition = &condition
		}

		if args, ok := subStepMap["args"].(map[string]interface{}); ok {
			sub.Args = args
		}
		if output, ok := subStepMap[fieldOutput].(bool); ok {
			sub.Output = &output
		}
		if store, ok := subStepMap["store"].(bool); ok {
			sub.Store = store
		}
		if description, ok := subStepMap["description"].(string); ok {
			sub.Description = description
		}
		if allowFailure, ok := pickBool(subStepMap, "allowFailure", "allow_failure"); ok {
			sub.AllowFailure = allowFailure
		}

		subSteps = append(subSteps, sub)
	}

	return subSteps, nil
}

// convertWorkflowCondition converts a condition map to api.WorkflowCondition
func convertWorkflowCondition(conditionParam map[string]interface{}) (api.WorkflowCondition, error) {
	var condition api.WorkflowCondition

	// Template (optional boolean Go-template gate)
	if template, ok := conditionParam["template"].(string); ok {
		condition.Template = template
	}

	// Tool (optional when fromStep or template is used)
	if tool, ok := conditionParam["tool"].(string); ok {
		condition.Tool = tool
	}

	// FromStep (optional). Accept canonical camelCase and legacy snake_case.
	if fromStep, ok := pickString(conditionParam, "fromStep", "from_step"); ok {
		condition.FromStep = fromStep
	}

	// A condition selects its evaluation source with exactly one of template,
	// tool, or fromStep.
	set := 0
	if condition.Template != "" {
		set++
	}
	if condition.Tool != "" {
		set++
	}
	if condition.FromStep != "" {
		set++
	}
	if condition.Template != "" && (condition.Tool != "" || condition.FromStep != "") {
		return condition, fmt.Errorf("condition.template is mutually exclusive with tool/fromStep")
	}
	if set == 0 {
		return condition, fmt.Errorf("condition requires exactly one of template, tool, or fromStep")
	}
	if set > 1 {
		return condition, fmt.Errorf("condition: tool and fromStep are mutually exclusive (set exactly one of template, tool, or fromStep)")
	}

	// A template gate stands alone: no tool/fromStep or expectations needed.
	if condition.Template != "" {
		return condition, nil
	}

	// Args (optional)
	if args, ok := conditionParam["args"].(map[string]interface{}); ok {
		condition.Args = args
	}

	// Track whether we have any expectations
	hasExpect := false
	hasExpectNot := false

	// Expect (optional)
	if expectParam, ok := pickMap(conditionParam, "expect"); ok {
		expect, err := convertWorkflowConditionExpectation(expectParam)
		if err != nil {
			return condition, fmt.Errorf("invalid expect: %v", err)
		}
		condition.Expect = expect
		hasExpect = true
	}

	// ExpectNot (optional). Accept canonical camelCase and legacy snake_case.
	if expectNotParam, ok := pickMap(conditionParam, "expectNot", "expect_not"); ok {
		expectNot, err := convertWorkflowConditionExpectation(expectNotParam)
		if err != nil {
			return condition, fmt.Errorf("invalid expectNot: %v", err)
		}
		condition.ExpectNot = expectNot
		hasExpectNot = true
	}

	// A tool/fromStep condition needs an explicit expectation: without one the
	// executor falls back to "expect the call to fail", which is rarely what a
	// user means. The CRD enforces the same rule via a CEL validation.
	if !hasExpect && !hasExpectNot {
		return condition, fmt.Errorf("a tool or fromStep condition requires expect or expectNot")
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

	// JsonPath (optional). Accept canonical camelCase and legacy snake_case.
	if jsonPathParam, ok := pickMap(expectParam, "jsonPath", "json_path"); ok {
		expect.JsonPath = jsonPathParam
	}

	// At least one of success or jsonPath must be provided
	if !expect.Success && len(expect.JsonPath) == 0 {
		// If success was explicitly set to false, that's okay
		if successVal, exists := expectParam["success"]; exists {
			if successBool, ok := successVal.(bool); ok && !successBool {
				// success=false is explicitly set, this is valid
				return expect, nil
			}
		}
		// Neither success nor jsonPath provided
		return expect, fmt.Errorf("either success or jsonPath must be provided")
	}

	return expect, nil
}

// getWorkflowArgsSchema returns the detailed schema definition for workflow arguments
func getWorkflowArgsSchema() map[string]interface{} {
	return map[string]interface{}{
		api.SchemaKeyType:        string(api.ArgTypeObject),
		api.SchemaKeyDescription: "Workflow arguments definition with validation rules",
		api.SchemaKeyAdditionalProperties: map[string]interface{}{
			api.SchemaKeyType:                 string(api.ArgTypeObject),
			api.SchemaKeyDescription:          "Argument definition with validation rules",
			api.SchemaKeyAdditionalProperties: false,
			api.SchemaKeyProperties: map[string]interface{}{
				api.SchemaKeyType: map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeString),
					api.SchemaKeyDescription: "Expected data type for validation",
					api.SchemaKeyEnum: []string{
						string(api.ArgTypeString),
						string(api.ArgTypeInteger),
						string(api.ArgTypeBoolean),
						string(api.ArgTypeNumber),
						string(api.ArgTypeObject),
						string(api.ArgTypeArray),
					},
				},
				api.SchemaKeyRequired: map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeBoolean),
					api.SchemaKeyDescription: "Whether this argument is required",
				},
				api.SchemaKeyDefault: map[string]interface{}{
					api.SchemaKeyDescription: "Default value if argument is not provided",
				},
				api.SchemaKeyDescription: map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeString),
					api.SchemaKeyDescription: "Human-readable documentation for this argument",
				},
			},
			api.SchemaKeyRequired: []string{"type"},
		},
	}
}

// getWorkflowConditionSchema returns the schema for a step/sub-step condition.
func getWorkflowConditionSchema() map[string]interface{} {
	return map[string]interface{}{
		api.SchemaKeyType:                 string(api.ArgTypeObject),
		api.SchemaKeyDescription:          "Optional condition that determines whether this step should execute. Set exactly one of template, tool, or fromStep; a tool/fromStep condition requires expect or expectNot.",
		api.SchemaKeyAdditionalProperties: false,
		api.SchemaKeyProperties: map[string]interface{}{
			"template": map[string]interface{}{
				api.SchemaKeyType:        string(api.ArgTypeString),
				api.SchemaKeyDescription: "Boolean Go-template gate, e.g. \"{{ eq .input.env \\\"production\\\" }}\"",
			},
			"tool": map[string]interface{}{
				api.SchemaKeyType:        string(api.ArgTypeString),
				api.SchemaKeyDescription: "Tool to call for condition evaluation",
			},
			"fromStep": map[string]interface{}{
				api.SchemaKeyType:        string(api.ArgTypeString),
				api.SchemaKeyDescription: "Reference a previous step's result for condition evaluation",
			},
			"args": map[string]interface{}{
				api.SchemaKeyType:        string(api.ArgTypeObject),
				api.SchemaKeyDescription: "Arguments for the condition tool",
			},
			"expect": map[string]interface{}{
				api.SchemaKeyType:        string(api.ArgTypeObject),
				api.SchemaKeyDescription: "Expected results for condition evaluation",
				api.SchemaKeyProperties: map[string]interface{}{
					api.FieldSuccess: map[string]interface{}{
						api.SchemaKeyType:        string(api.ArgTypeBoolean),
						api.SchemaKeyDescription: "Whether the tool call should succeed",
					},
					"jsonPath": map[string]interface{}{
						api.SchemaKeyType:        string(api.ArgTypeObject),
						api.SchemaKeyDescription: "JSON path expressions to evaluate against tool result",
						api.SchemaKeyAdditionalProperties: map[string]interface{}{
							api.SchemaKeyDescription: "Expected value for the JSON path",
						},
					},
				},
			},
			"expectNot": map[string]interface{}{
				api.SchemaKeyType:        string(api.ArgTypeObject),
				api.SchemaKeyDescription: "Negated expected results for condition evaluation",
			},
		},
	}
}

// getWorkflowSubStepSchema returns the schema for a sub-step used inside
// forEach bodies, parallel groups, and onFailure handlers. Sub-steps are plain
// tool calls and cannot themselves contain forEach or parallel.
func getWorkflowSubStepSchema() map[string]interface{} {
	return map[string]interface{}{
		api.SchemaKeyType:                 string(api.ArgTypeObject),
		api.SchemaKeyDescription:          "A tool-call sub-step",
		api.SchemaKeyAdditionalProperties: false,
		api.SchemaKeyProperties: map[string]interface{}{
			"id": map[string]interface{}{
				api.SchemaKeyType:        string(api.ArgTypeString),
				api.SchemaKeyDescription: "Unique identifier for this sub-step",
			},
			"tool": map[string]interface{}{
				api.SchemaKeyType:        string(api.ArgTypeString),
				api.SchemaKeyDescription: "Name of the tool to execute",
			},
			"args": map[string]interface{}{
				api.SchemaKeyType:        string(api.ArgTypeObject),
				api.SchemaKeyDescription: "Arguments to pass to the tool (supports templating)",
			},
			"condition":    getWorkflowConditionSchema(),
			"allowFailure": map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeBoolean), api.SchemaKeyDescription: "Whether this sub-step is allowed to fail"},
			"output":       map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeBoolean), api.SchemaKeyDescription: "Whether this sub-step's result is included in the returned document. Results are always referenceable by later steps regardless of this flag."},
			"store":        map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeBoolean), api.SchemaKeyDescription: "Deprecated alias for output; kept for backwards compatibility"},
			api.SchemaKeyDescription: map[string]interface{}{
				api.SchemaKeyType:        string(api.ArgTypeString),
				api.SchemaKeyDescription: "Human-readable documentation for this sub-step",
			},
		},
		api.SchemaKeyRequired: []string{"id", "tool"},
	}
}

// getWorkflowStepsSchema returns the detailed schema definition for workflow steps
func getWorkflowStepsSchema() map[string]interface{} {
	return map[string]interface{}{
		api.SchemaKeyType:        string(api.ArgTypeArray),
		api.SchemaKeyDescription: "Workflow steps defining the sequence of operations. Each step is exactly one of: a tool call, a forEach loop, or a parallel group.",
		api.SchemaKeyItems: map[string]interface{}{
			api.SchemaKeyType:                 string(api.ArgTypeObject),
			api.SchemaKeyDescription:          "Individual workflow step configuration",
			api.SchemaKeyAdditionalProperties: false,
			api.SchemaKeyProperties: map[string]interface{}{
				"id": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeString),
					api.SchemaKeyDescription: "Unique identifier for this step within the workflow",
				},
				"tool": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeString),
					api.SchemaKeyDescription: "Name of the tool to execute for this step (mutually exclusive with forEach/parallel)",
				},
				"args": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeObject),
					api.SchemaKeyDescription: "Arguments to pass to the tool, supporting templating with {{.input.argName}} for workflow args and {{.results.stepId.field}} for stored step results",
				},
				"condition": getWorkflowConditionSchema(),
				"forEach": map[string]interface{}{
					api.SchemaKeyType:                 string(api.ArgTypeObject),
					api.SchemaKeyDescription:          "Run a body of sub-steps once per item of a list",
					api.SchemaKeyAdditionalProperties: false,
					api.SchemaKeyProperties: map[string]interface{}{
						"items": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeString),
							api.SchemaKeyDescription: "Template expression resolving to a list, e.g. \"{{ .input.clusters }}\"",
						},
						"as": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeString),
							api.SchemaKeyDescription: "Loop variable name exposed as {{ .vars.<as> }} (default \"item\")",
						},
						"steps": map[string]interface{}{
							api.SchemaKeyType:        string(api.ArgTypeArray),
							api.SchemaKeyDescription: "Body executed for each item",
							api.SchemaKeyItems:       getWorkflowSubStepSchema(),
							"minItems":               1,
						},
					},
					api.SchemaKeyRequired: []string{"items", "steps"},
				},
				"parallel": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeArray),
					api.SchemaKeyDescription: "Sub-steps to execute concurrently (mutually exclusive with tool/forEach)",
					api.SchemaKeyItems:       getWorkflowSubStepSchema(),
					"minItems":               1,
				},
				"allowFailure": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeBoolean),
					api.SchemaKeyDescription: "Whether this step is allowed to fail without failing the workflow. On a forEach or parallel step this tolerates a failure of the whole group.",
				},
				"output": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeBoolean),
					api.SchemaKeyDescription: "Whether this step's result is included in the workflow's returned document. Every step result is always referenceable by later steps via {{.results.stepId.field}} regardless of this flag.",
				},
				"store": map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeBoolean),
					api.SchemaKeyDescription: "Deprecated alias for output; kept for backwards compatibility",
				},
				api.SchemaKeyDescription: map[string]interface{}{
					api.SchemaKeyType:        string(api.ArgTypeString),
					api.SchemaKeyDescription: "Human-readable documentation for this step's purpose",
				},
			},
			api.SchemaKeyRequired: []string{"id"},
		},
		"minItems": 1,
	}
}

// getWorkflowOnFailureSchema returns the schema for the onFailure handlers list.
func getWorkflowOnFailureSchema() map[string]interface{} {
	return map[string]interface{}{
		api.SchemaKeyType:        string(api.ArgTypeArray),
		api.SchemaKeyDescription: "Best-effort cleanup/rollback sub-steps run when the workflow fails on a non-allowFailure step",
		api.SchemaKeyItems:       getWorkflowSubStepSchema(),
	}
}

// getWorkflowOutputSchema returns the schema for the workflow-level output
// output template: an object whose leaves are templated expressions rendered against
// .input/.results/.vars to shape the returned document.
func getWorkflowOutputSchema() map[string]interface{} {
	return map[string]interface{}{
		api.SchemaKeyType:                 string(api.ArgTypeObject),
		api.SchemaKeyDescription:          "Optional output template rendered once after all steps complete and returned in place of the default envelope. Each leaf is a Go-template/sprig expression evaluated against .input/.results/.vars, e.g. \"{{ .results.pods.items }}\" or \"{{ len .results.events.items }}\". JSON structure is preserved (numbers stay numbers, arrays stay arrays). A leaf's type comes from the value it evaluates to, not from how its text looks: a single-action leaf keeps its real type (\"{{ len .x }}\" is a number) and a computed string keeps its exact string form, so values whose form matters (leading zeros, versions, IDs like \"08\" or \"1.20\") are preserved with no coercion or workaround. Declaring this output template replaces the envelope, so per-step output/store flags no longer affect the returned document; every step result is still referenceable here regardless of those flags.",
		api.SchemaKeyAdditionalProperties: true,
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

	// The message and event type are determined by the generator's template
	// engine from the reason; the structured data is threaded through so the
	// rendered message includes contextual detail (step counts, errors, ...).
	err := eventManager.CreateEventWithData(context.Background(), objectRef, string(reason), data.ToAPI())
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
