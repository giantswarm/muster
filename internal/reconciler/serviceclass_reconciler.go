package reconciler

import (
	"context"
	"fmt"
	"sort"

	"muster/internal/api"
	"muster/pkg/logging"
)

// ServiceClassManager is an interface for accessing ServiceClass definitions.
type ServiceClassManager interface {
	ListServiceClasses() []api.ServiceClass
	GetServiceClass(name string) (*api.ServiceClass, error)
}

// ServiceClassReconciler reconciles ServiceClass resources.
//
// It ensures that ServiceClass definitions (from CRDs or YAML files) are
// synchronized with the system's understanding of available service templates.
//
// Reconciliation logic:
//   - Create: Validate and register the ServiceClass definition
//   - Update: Re-validate and update the ServiceClass configuration
//   - Delete: Remove the ServiceClass from the system
//
// After each reconciliation, the reconciler syncs validation status
// back to the CRD's Status field. See ADR 007 for details.
type ServiceClassReconciler struct {
	BaseStatusConfig

	// serviceClassManager provides access to ServiceClass definitions
	serviceClassManager ServiceClassManager
}

// NewServiceClassReconciler creates a new ServiceClass reconciler.
func NewServiceClassReconciler(
	serviceClassManager ServiceClassManager,
) *ServiceClassReconciler {
	return &ServiceClassReconciler{
		BaseStatusConfig:    BaseStatusConfig{Namespace: DefaultNamespace},
		serviceClassManager: serviceClassManager,
	}
}

// WithStatusUpdater sets the status updater for syncing status back to CRDs.
func (r *ServiceClassReconciler) WithStatusUpdater(updater StatusUpdater, namespace string) *ServiceClassReconciler {
	r.SetStatusUpdater(updater, namespace)
	return r
}

// GetResourceType returns the resource type this reconciler handles.
func (r *ServiceClassReconciler) GetResourceType() ResourceType {
	return ResourceTypeServiceClass
}

// Reconcile processes a single ServiceClass reconciliation request.
func (r *ServiceClassReconciler) Reconcile(ctx context.Context, req ReconcileRequest) ReconcileResult {
	logging.Debug("ServiceClassReconciler", "Reconciling ServiceClass: %s", req.Name)

	// Fetch the desired state from the definition source
	serviceClass, err := r.serviceClassManager.GetServiceClass(req.Name)
	if err != nil {
		// If not found, this might be a delete operation
		if IsNotFoundError(err) {
			return r.reconcileDelete(ctx, req)
		}
		return ReconcileResult{
			Error:   fmt.Errorf("failed to get ServiceClass definition: %w", err),
			Requeue: true,
		}
	}

	// ServiceClass exists, ensure it's valid and properly registered
	result := r.reconcileCreateOrUpdate(ctx, req, serviceClass)

	// Sync status back to CRD after reconciliation
	r.syncStatus(ctx, req.Name, req.Namespace, serviceClass, result.Error)

	return result
}

// syncStatus syncs the validation status to the ServiceClass CRD status.
//
// Status sync is a best-effort operation - failures are logged at Debug level
// rather than Warn/Error to avoid log spam. Status sync may fail frequently in
// legitimate scenarios (e.g., filesystem mode, CRD not yet created, temporary
// API server unavailability). Failures are tracked in metrics for monitoring.
func (r *ServiceClassReconciler) syncStatus(ctx context.Context, name, namespace string, sc *api.ServiceClass, reconcileErr error) {
	if r.StatusUpdater == nil {
		return
	}

	namespace = r.GetNamespace(namespace)

	// Record status sync attempt for metrics
	metrics := GetReconcilerMetrics()
	metrics.RecordStatusSyncAttempt(ResourceTypeServiceClass, name)

	// Get the current CRD
	serviceClass, err := r.StatusUpdater.GetServiceClass(ctx, name, namespace)
	if err != nil {
		logging.Debug("ServiceClassReconciler", "Failed to get ServiceClass for status sync: %v", err)
		metrics.RecordStatusSyncFailure(ResourceTypeServiceClass, name, "get_crd_failed")
		return
	}

	// Extract referenced tools from lifecycle definitions
	referencedTools := r.extractReferencedTools(sc)

	// Validate the spec
	validationErrors := []string{}
	if validateErr := r.validateServiceClass(sc); validateErr != nil {
		validationErrors = append(validationErrors, validateErr.Error())
	}

	// Update status
	serviceClass.Status.Valid = len(validationErrors) == 0
	serviceClass.Status.ValidationErrors = validationErrors
	serviceClass.Status.ReferencedTools = referencedTools

	// Update the CRD status
	if err := r.StatusUpdater.UpdateServiceClassStatus(ctx, serviceClass); err != nil {
		logging.Debug("ServiceClassReconciler", "Failed to update ServiceClass status: %v", err)
		metrics.RecordStatusSyncFailure(ResourceTypeServiceClass, name, "update_status_failed")
	} else {
		logging.Debug("ServiceClassReconciler", "Synced ServiceClass %s status: valid=%t, tools=%v",
			name, serviceClass.Status.Valid, referencedTools)
		metrics.RecordStatusSyncSuccess(ResourceTypeServiceClass, name)
	}
}

// extractReferencedTools extracts all tool names referenced in the ServiceClass.
func (r *ServiceClassReconciler) extractReferencedTools(sc *api.ServiceClass) []string {
	toolSet := make(map[string]bool)

	if sc == nil {
		return []string{}
	}

	// Extract from lifecycle tools
	if sc.ServiceConfig.LifecycleTools.Start.Tool != "" {
		toolSet[sc.ServiceConfig.LifecycleTools.Start.Tool] = true
	}
	if sc.ServiceConfig.LifecycleTools.Stop.Tool != "" {
		toolSet[sc.ServiceConfig.LifecycleTools.Stop.Tool] = true
	}
	if sc.ServiceConfig.LifecycleTools.Restart != nil && sc.ServiceConfig.LifecycleTools.Restart.Tool != "" {
		toolSet[sc.ServiceConfig.LifecycleTools.Restart.Tool] = true
	}
	if sc.ServiceConfig.LifecycleTools.HealthCheck != nil && sc.ServiceConfig.LifecycleTools.HealthCheck.Tool != "" {
		toolSet[sc.ServiceConfig.LifecycleTools.HealthCheck.Tool] = true
	}
	if sc.ServiceConfig.LifecycleTools.Status != nil && sc.ServiceConfig.LifecycleTools.Status.Tool != "" {
		toolSet[sc.ServiceConfig.LifecycleTools.Status.Tool] = true
	}

	// Convert to sorted slice for deterministic output
	tools := make([]string, 0, len(toolSet))
	for tool := range toolSet {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	return tools
}

// reconcileCreateOrUpdate handles creating or updating a ServiceClass.
func (r *ServiceClassReconciler) reconcileCreateOrUpdate(ctx context.Context, req ReconcileRequest, sc *api.ServiceClass) ReconcileResult {
	logging.Info("ServiceClassReconciler", "Reconciling ServiceClass: %s", req.Name)

	// Validate the ServiceClass definition
	if err := r.validateServiceClass(sc); err != nil {
		logging.Warn("ServiceClassReconciler", "ServiceClass %s validation failed: %v", req.Name, err)
		return ReconcileResult{
			Error:   fmt.Errorf("serviceClass validation failed: %w", err),
			Requeue: true,
		}
	}

	// ServiceClass definitions are primarily static - they define templates.
	// The main reconciliation is ensuring the definition is valid and registered.
	// Tool availability is checked dynamically when services are created.

	logging.Info("ServiceClassReconciler", "Successfully reconciled ServiceClass: %s (available=%t)", req.Name, sc.Available)
	return ReconcileResult{}
}

// reconcileDelete handles deleting a ServiceClass.
func (r *ServiceClassReconciler) reconcileDelete(ctx context.Context, req ReconcileRequest) ReconcileResult {
	logging.Info("ServiceClassReconciler", "ServiceClass %s was deleted", req.Name)

	// ServiceClass deletion is handled by the filesystem/Kubernetes watching.
	// The ServiceClassManager will automatically remove deleted definitions.
	// Here we just acknowledge the deletion.

	logging.Debug("ServiceClassReconciler", "ServiceClass %s deletion acknowledged", req.Name)
	return ReconcileResult{}
}

// validateServiceClass performs validation on a ServiceClass definition.
func (r *ServiceClassReconciler) validateServiceClass(sc *api.ServiceClass) error {
	if sc.Name == "" {
		return fmt.Errorf("serviceClass name is required")
	}

	if sc.ServiceConfig.ServiceType == "" {
		return fmt.Errorf("serviceClass serviceType is required")
	}

	// Validate that lifecycle tools are defined
	if sc.ServiceConfig.LifecycleTools.Start.Tool == "" {
		return fmt.Errorf("serviceClass start lifecycle tool is required")
	}

	if sc.ServiceConfig.LifecycleTools.Stop.Tool == "" {
		return fmt.Errorf("serviceClass stop lifecycle tool is required")
	}

	return nil
}
