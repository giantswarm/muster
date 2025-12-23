package reconciler

import (
	"context"
	"fmt"

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
type ServiceClassReconciler struct {
	// serviceClassManager provides access to ServiceClass definitions
	serviceClassManager ServiceClassManager
}

// NewServiceClassReconciler creates a new ServiceClass reconciler.
func NewServiceClassReconciler(
	serviceClassManager ServiceClassManager,
) *ServiceClassReconciler {
	return &ServiceClassReconciler{
		serviceClassManager: serviceClassManager,
	}
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
		if isNotFoundError(err) {
			return r.reconcileDelete(ctx, req)
		}
		return ReconcileResult{
			Error:   fmt.Errorf("failed to get ServiceClass definition: %w", err),
			Requeue: true,
		}
	}

	// ServiceClass exists, ensure it's valid and properly registered
	return r.reconcileCreateOrUpdate(ctx, req, serviceClass)
}

// reconcileCreateOrUpdate handles creating or updating a ServiceClass.
func (r *ServiceClassReconciler) reconcileCreateOrUpdate(ctx context.Context, req ReconcileRequest, sc *api.ServiceClass) ReconcileResult {
	logging.Info("ServiceClassReconciler", "Reconciling ServiceClass: %s", req.Name)

	// Validate the ServiceClass definition
	if err := r.validateServiceClass(sc); err != nil {
		logging.Warn("ServiceClassReconciler", "ServiceClass %s validation failed: %v", req.Name, err)
		return ReconcileResult{
			Error:   fmt.Errorf("ServiceClass validation failed: %w", err),
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
		return fmt.Errorf("ServiceClass name is required")
	}

	if sc.ServiceConfig.ServiceType == "" {
		return fmt.Errorf("ServiceClass serviceType is required")
	}

	// Validate that lifecycle tools are defined
	if sc.ServiceConfig.LifecycleTools.Start.Tool == "" {
		return fmt.Errorf("ServiceClass start lifecycle tool is required")
	}

	if sc.ServiceConfig.LifecycleTools.Stop.Tool == "" {
		return fmt.Errorf("ServiceClass stop lifecycle tool is required")
	}

	return nil
}

