package reconciler

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"k8s.io/client-go/util/retry"

	"muster/internal/api"
	musterv1alpha1 "muster/pkg/apis/muster/v1alpha1"
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
		if IsNotFoundError(err) {
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
//
// This function implements retry-on-conflict logic to handle optimistic locking
// failures that occur when the CRD is modified between read and update operations.
// The retry logic re-fetches the CRD and re-applies the status on each attempt.
//
// Status sync is a best-effort operation - failures are logged with backoff
// to avoid log spam when a resource continuously fails. Failures are tracked
// in metrics for monitoring.
func (r *WorkflowReconciler) syncStatus(ctx context.Context, name, namespace string, wf *api.Workflow, reconcileErr error) {
	if r.StatusUpdater == nil {
		return
	}

	namespace = r.GetNamespace(namespace)

	// Record status sync attempt for metrics
	metrics := GetReconcilerMetrics()
	metrics.RecordStatusSyncAttempt(ResourceTypeWorkflow, name)

	// Get failure tracker for backoff-based logging
	failureTracker := GetStatusSyncFailureTracker()

	// Extract referenced tools from steps (computed once)
	referencedTools := r.extractReferencedTools(wf)

	// Validate the spec (computed once)
	validationErrors := []string{}
	if validateErr := r.validateWorkflow(wf); validateErr != nil {
		validationErrors = append(validationErrors, validateErr.Error())
	}

	// Calculate step count (computed once)
	stepCount := 0
	if wf != nil {
		stepCount = len(wf.Steps)
	}

	// Use retry-on-conflict to handle optimistic locking failures.
	var lastErr error
	err := retry.OnError(StatusSyncRetryBackoff, IsConflictError, func() error {
		// Get the current CRD (re-fetch on each attempt to get latest resource version)
		workflow, err := r.StatusUpdater.GetWorkflow(ctx, name, namespace)
		if err != nil {
			lastErr = err
			return nil // Return nil to exit retry loop
		}

		// Apply status
		r.applyStatus(workflow, referencedTools, validationErrors, stepCount)

		// Update the CRD status
		if err := r.StatusUpdater.UpdateWorkflowStatus(ctx, workflow); err != nil {
			lastErr = err
			return err // Return error to trigger retry if it's a conflict
		}
		lastErr = nil
		return nil
	})

	// Handle the result
	if err != nil || lastErr != nil {
		actualErr := lastErr
		if actualErr == nil {
			actualErr = err
		}

		reason := categorizeWorkflowStatusSyncError(actualErr)
		metrics.RecordStatusSyncFailure(ResourceTypeWorkflow, name, reason)

		if failureTracker.RecordFailure(ResourceTypeWorkflow, name, actualErr) {
			failureCount := failureTracker.GetFailureCount(ResourceTypeWorkflow, name)
			logging.Debug("WorkflowReconciler", "Status sync failed for %s: %s (consecutive failures: %d)",
				name, actualErr.Error(), failureCount)
		}
	} else {
		logging.Debug("WorkflowReconciler", "Synced Workflow %s status: valid=%t, steps=%d, tools=%v",
			name, len(validationErrors) == 0, stepCount, referencedTools)
		metrics.RecordStatusSyncSuccess(ResourceTypeWorkflow, name)
		failureTracker.RecordSuccess(ResourceTypeWorkflow, name)
	}
}

// applyStatus applies the computed status to the Workflow CRD.
func (r *WorkflowReconciler) applyStatus(workflow *musterv1alpha1.Workflow, referencedTools []string, validationErrors []string, stepCount int) {
	workflow.Status.Valid = len(validationErrors) == 0
	workflow.Status.ValidationErrors = validationErrors
	workflow.Status.ReferencedTools = referencedTools
	workflow.Status.StepCount = stepCount
}

// categorizeWorkflowStatusSyncError returns a descriptive reason for a status sync error.
func categorizeWorkflowStatusSyncError(err error) string {
	if err == nil {
		return "unknown"
	}

	errStr := err.Error()

	if IsConflictError(err) {
		return "conflict_after_retries"
	}
	if IsNotFoundError(err) {
		return "crd_not_found"
	}
	if strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no route to host") {
		return "api_server_unreachable"
	}
	if strings.Contains(errStr, "timeout") {
		return "timeout"
	}
	if strings.Contains(errStr, "forbidden") || strings.Contains(errStr, "Forbidden") {
		return "permission_denied"
	}

	return "update_status_failed"
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

	// Convert to sorted slice for deterministic output
	tools := make([]string, 0, len(toolSet))
	for tool := range toolSet {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	return tools
}

// reconcileCreateOrUpdate handles creating or updating a Workflow.
func (r *WorkflowReconciler) reconcileCreateOrUpdate(ctx context.Context, req ReconcileRequest, wf *api.Workflow) ReconcileResult {
	logging.Info("WorkflowReconciler", "Reconciling Workflow: %s", req.Name)

	// Validate the Workflow definition
	if err := r.validateWorkflow(wf); err != nil {
		logging.Warn("WorkflowReconciler", "Workflow %s validation failed: %v", req.Name, err)
		return ReconcileResult{
			Error:   fmt.Errorf("workflow validation failed: %w", err),
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
