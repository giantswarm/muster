package reconciler

import (
	"muster/internal/api"
)

// Adapter wraps the ReconcileManager and provides API registration.
type Adapter struct {
	manager *Manager
}

// NewAdapter creates a new reconciler API adapter.
func NewAdapter(manager *Manager) *Adapter {
	return &Adapter{
		manager: manager,
	}
}

// Register registers the reconciler with the API layer.
// Note: The reconciler doesn't currently expose tools through the aggregator,
// but this provides a consistent pattern for future expansion.
func (a *Adapter) Register() {
	api.RegisterReconcileManager(a)
}

// GetManager returns the underlying reconcile manager.
func (a *Adapter) GetManager() *Manager {
	return a.manager
}

// GetStatus returns the reconciliation status for a resource.
func (a *Adapter) GetStatus(resourceType ResourceType, name, namespace string) (*ReconcileStatus, bool) {
	return a.manager.GetStatus(resourceType, name, namespace)
}

// GetAllStatuses returns all reconciliation statuses.
func (a *Adapter) GetAllStatuses() []ReconcileStatus {
	return a.manager.GetAllStatuses()
}

// TriggerReconcile manually triggers reconciliation for a resource.
func (a *Adapter) TriggerReconcile(resourceType ResourceType, name, namespace string) {
	a.manager.TriggerReconcile(resourceType, name, namespace)
}

// IsRunning returns whether the reconciliation manager is running.
func (a *Adapter) IsRunning() bool {
	return a.manager.IsRunning()
}

// GetQueueLength returns the current reconciliation queue length.
func (a *Adapter) GetQueueLength() int {
	return a.manager.GetQueueLength()
}

