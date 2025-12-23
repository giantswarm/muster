package reconciler

import (
	"context"
	"fmt"

	"muster/internal/api"
	"muster/pkg/logging"
)

// WorkflowManager is an interface for accessing Workflow definitions.
type WorkflowManager interface {
	GetWorkflows() []api.Workflow
	GetWorkflow(name string) (*api.Workflow, error)
}

// WorkflowReconciler reconciles Workflow resources.
//
// It ensures that Workflow definitions (from CRDs or YAML files) are
// synchronized with the system's understanding of available workflows.
//
// Reconciliation logic:
//   - Create: Validate and register the Workflow definition
//   - Update: Re-validate and update the Workflow configuration
//   - Delete: Remove the Workflow from the system
//
// After each reconciliation, the reconciler syncs validation status
// back to the CRD's Status field. See ADR 007 for details.
type WorkflowReconciler struct {
	BaseStatusConfig

	// workflowManager provides access to Workflow definitions
	workflowManager WorkflowManager
}

// NewWorkflowReconciler creates a new Workflow reconciler.
func NewWorkflowReconciler(
	workflowManager WorkflowManager,
) *WorkflowReconciler {
	return &WorkflowReconciler{
		BaseStatusConfig: BaseStatusConfig{Namespace: DefaultNamespace},
		workflowManager:  workflowManager,
	}
}

// WithStatusUpdater sets the status updater for syncing status back to CRDs.
func (r *WorkflowReconciler) WithStatusUpdater(updater StatusUpdater, namespace string) *WorkflowReconciler {
	r.SetStatusUpdater(updater, namespace)
	return r
}

// GetResourceType returns the resource type this reconciler handles.
func (r *WorkflowReconciler) GetResourceType() ResourceType {
	return ResourceTypeWorkflow
}

// Reconcile processes a single Workflow reconciliation request.
func (r *WorkflowReconciler) Reconcile(ctx context.Context, req ReconcileRequest) ReconcileResult {
	logging.Debug("WorkflowReconciler", "Reconciling Workflow: %s", req.Name)

	// Fetch the desired state from the definition source
	workflow, err := r.workflowManager.GetWorkflow(req.Name)
	if err != nil {
		// If not found, this might be a delete operation
		if isNotFoundError(err) {
			return r.reconcileDelete(ctx, req)
		}
		return ReconcileResult{
			Error:   fmt.Errorf("failed to get Workflow definition: %w", err),
			Requeue: true,
		}
	}

	// Workflow exists, ensure it's valid and properly registered
	result := r.reconcileCreateOrUpdate(ctx, req, workflow)

	// Sync status back to CRD after reconciliation
	r.syncStatus(ctx, req.Name, req.Namespace, workflow, result.Error)

	return result
}

// syncStatus syncs the validation status to the Workflow CRD status.
func (r *WorkflowReconciler) syncStatus(ctx context.Context, name, namespace string, wf *api.Workflow, reconcileErr error) {
	if r.StatusUpdater == nil {
		return
	}

	namespace = r.GetNamespace(namespace)

	// Get the current CRD
	workflow, err := r.StatusUpdater.GetWorkflow(ctx, name, namespace)
	if err != nil {
		logging.Debug("WorkflowReconciler", "Failed to get Workflow for status sync: %v", err)
		return
	}

	// Extract referenced tools from steps
	referencedTools := r.extractReferencedTools(wf)

	// Validate the spec
	validationErrors := []string{}
	if validateErr := r.validateWorkflow(wf); validateErr != nil {
		validationErrors = append(validationErrors, validateErr.Error())
	}

	// Update status
	workflow.Status.Valid = len(validationErrors) == 0
	workflow.Status.ValidationErrors = validationErrors
	workflow.Status.ReferencedTools = referencedTools
	if wf != nil {
		workflow.Status.StepCount = len(wf.Steps)
	}

	// Update the CRD status
	if err := r.StatusUpdater.UpdateWorkflowStatus(ctx, workflow); err != nil {
		logging.Debug("WorkflowReconciler", "Failed to update Workflow status: %v", err)
	} else {
		logging.Debug("WorkflowReconciler", "Synced Workflow %s status: valid=%t, steps=%d, tools=%v",
			name, workflow.Status.Valid, workflow.Status.StepCount, referencedTools)
	}
}

// extractReferencedTools extracts all tool names referenced in the Workflow steps.
func (r *WorkflowReconciler) extractReferencedTools(wf *api.Workflow) []string {
	if wf == nil {
		return []string{}
	}

	toolSet := make(map[string]bool)

	for _, step := range wf.Steps {
		if step.Tool != "" {
			toolSet[step.Tool] = true
		}
		// Also extract tools from conditions
		if step.Condition != nil && step.Condition.Tool != "" {
			toolSet[step.Condition.Tool] = true
		}
	}

	// Convert to slice
	tools := make([]string, 0, len(toolSet))
	for tool := range toolSet {
		tools = append(tools, tool)
	}
	return tools
}

// reconcileCreateOrUpdate handles creating or updating a Workflow.
func (r *WorkflowReconciler) reconcileCreateOrUpdate(ctx context.Context, req ReconcileRequest, wf *api.Workflow) ReconcileResult {
	logging.Info("WorkflowReconciler", "Reconciling Workflow: %s", req.Name)

	// Validate the Workflow definition
	if err := r.validateWorkflow(wf); err != nil {
		logging.Warn("WorkflowReconciler", "Workflow %s validation failed: %v", req.Name, err)
		return ReconcileResult{
			Error:   fmt.Errorf("Workflow validation failed: %w", err),
			Requeue: true,
		}
	}

	// Workflow definitions are primarily static - they define execution templates.
	// The main reconciliation is ensuring the definition is valid and registered.
	// Tool availability is checked dynamically when workflows are executed.

	logging.Info("WorkflowReconciler", "Successfully reconciled Workflow: %s (available=%t)", req.Name, wf.Available)
	return ReconcileResult{}
}

// reconcileDelete handles deleting a Workflow.
func (r *WorkflowReconciler) reconcileDelete(ctx context.Context, req ReconcileRequest) ReconcileResult {
	logging.Info("WorkflowReconciler", "Workflow %s was deleted", req.Name)

	// Workflow deletion is handled by the filesystem/Kubernetes watching.
	// The WorkflowManager will automatically remove deleted definitions.
	// Here we just acknowledge the deletion.

	logging.Debug("WorkflowReconciler", "Workflow %s deletion acknowledged", req.Name)
	return ReconcileResult{}
}

// validateWorkflow performs validation on a Workflow definition.
func (r *WorkflowReconciler) validateWorkflow(wf *api.Workflow) error {
	if wf.Name == "" {
		return fmt.Errorf("workflow name is required")
	}

	if len(wf.Steps) == 0 {
		return fmt.Errorf("workflow must have at least one step")
	}

	// Validate each step has required fields
	stepIDs := make(map[string]bool)
	for i, step := range wf.Steps {
		if step.ID == "" {
			return fmt.Errorf("step %d: ID is required", i)
		}
		if stepIDs[step.ID] {
			return fmt.Errorf("step %d: duplicate step ID '%s'", i, step.ID)
		}
		stepIDs[step.ID] = true

		if step.Tool == "" {
			return fmt.Errorf("step '%s': tool is required", step.ID)
		}
	}

	// Validate argument definitions
	for argName, argDef := range wf.Args {
		if argDef.Type == "" {
			return fmt.Errorf("argument '%s': type is required", argName)
		}
	}

	return nil
}

