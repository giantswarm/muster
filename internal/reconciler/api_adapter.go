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
// Implements api.ReconcileManagerHandler interface.
func (a *Adapter) GetStatus(resourceType, name, namespace string) (*api.ReconcileStatusInfo, bool) {
	rt := ResourceType(resourceType)
	status, ok := a.manager.GetStatus(rt, name, namespace)
	if !ok {
		return nil, false
	}
	return convertToAPIStatus(status), true
}

// GetAllStatuses returns all reconciliation statuses.
// Implements api.ReconcileManagerHandler interface.
func (a *Adapter) GetAllStatuses() []api.ReconcileStatusInfo {
	statuses := a.manager.GetAllStatuses()
	result := make([]api.ReconcileStatusInfo, len(statuses))
	for i, s := range statuses {
		result[i] = *convertToAPIStatus(&s)
	}
	return result
}

// TriggerReconcile manually triggers reconciliation for a resource.
// Implements api.ReconcileManagerHandler interface.
func (a *Adapter) TriggerReconcile(resourceType, name, namespace string) {
	rt := ResourceType(resourceType)
	a.manager.TriggerReconcile(rt, name, namespace)
}

// IsRunning returns whether the reconciliation manager is running.
// Implements api.ReconcileManagerHandler interface.
func (a *Adapter) IsRunning() bool {
	return a.manager.IsRunning()
}

// GetQueueLength returns the current reconciliation queue length.
// Implements api.ReconcileManagerHandler interface.
func (a *Adapter) GetQueueLength() int {
	return a.manager.GetQueueLength()
}

// GetWatchMode returns the current watch mode (kubernetes/filesystem).
// Implements api.ReconcileManagerHandler interface.
func (a *Adapter) GetWatchMode() string {
	return a.manager.GetWatchMode()
}

// GetEnabledResourceTypes returns the list of resource types with reconciliation enabled.
// Implements api.ReconcileManagerHandler interface.
func (a *Adapter) GetEnabledResourceTypes() []string {
	return a.manager.GetEnabledResourceTypes()
}

// IsResourceTypeEnabled checks if reconciliation is enabled for a resource type.
func (a *Adapter) IsResourceTypeEnabled(resourceType string) bool {
	rt := ResourceType(resourceType)
	return a.manager.IsResourceTypeEnabled(rt)
}

// DisableResourceType disables reconciliation for a specific resource type.
func (a *Adapter) DisableResourceType(resourceType string) {
	rt := ResourceType(resourceType)
	a.manager.DisableResourceType(rt)
}

// EnableResourceType enables reconciliation for a specific resource type.
func (a *Adapter) EnableResourceType(resourceType string) {
	rt := ResourceType(resourceType)
	a.manager.EnableResourceType(rt)
}

// GetOverview returns a comprehensive overview of the reconciliation system status.
func (a *Adapter) GetOverview() *api.ReconcileOverview {
	statuses := a.manager.GetAllStatuses()

	// Calculate summary
	summary := api.ReconcileStatusSummary{
		Total: len(statuses),
	}
	for _, s := range statuses {
		switch s.State {
		case StateSynced:
			summary.Synced++
		case StatePending:
			summary.Pending++
		case StateReconciling:
			summary.Reconciling++
		case StateError:
			summary.Error++
		case StateFailed:
			summary.Failed++
		}
	}

	return &api.ReconcileOverview{
		Running:              a.manager.IsRunning(),
		WatchMode:            a.manager.GetWatchMode(),
		QueueLength:          a.manager.GetQueueLength(),
		EnabledResourceTypes: a.manager.GetEnabledResourceTypes(),
		StatusSummary:        summary,
	}
}

// convertToAPIStatus converts internal ReconcileStatus to API format.
func convertToAPIStatus(status *ReconcileStatus) *api.ReconcileStatusInfo {
	var lastReconcileTime *string
	if status.LastReconcileTime != nil {
		t := status.LastReconcileTime.Format("2006-01-02T15:04:05Z")
		lastReconcileTime = &t
	}

	return &api.ReconcileStatusInfo{
		ResourceType:      string(status.ResourceType),
		Name:              status.Name,
		Namespace:         status.Namespace,
		LastReconcileTime: lastReconcileTime,
		LastError:         status.LastError,
		RetryCount:        status.RetryCount,
		State:             string(status.State),
	}
}
