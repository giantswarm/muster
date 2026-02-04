package reconciler

import (
	"sync"
	"time"

	"github.com/giantswarm/muster/pkg/logging"
)

// ReconcilerMetrics tracks reconciliation-related metrics for monitoring and alerting.
//
// This provides visibility into reconciliation patterns, status sync failures,
// and overall reconciler health. Metrics are tracked per-resource-type to enable
// targeted alerting and debugging.
type ReconcilerMetrics struct {
	mu sync.RWMutex

	// Per-resource-type metrics
	resourceMetrics map[ResourceType]*resourceTypeMetrics

	// Global counters for summary metrics
	totalReconcileAttempts   int64
	totalReconcileSuccesses  int64
	totalReconcileFailures   int64
	totalStatusSyncAttempts  int64
	totalStatusSyncSuccesses int64
	totalStatusSyncFailures  int64
}

// resourceTypeMetrics holds reconciliation metrics for a specific resource type.
type resourceTypeMetrics struct {
	ResourceType        ResourceType
	ReconcileAttempts   int64
	ReconcileSuccesses  int64
	ReconcileFailures   int64
	StatusSyncAttempts  int64
	StatusSyncSuccesses int64
	StatusSyncFailures  int64
	LastReconcileAt     time.Time
	LastSuccessAt       time.Time
	LastFailureAt       time.Time
	LastStatusSyncAt    time.Time
}

// NewReconcilerMetrics creates a new ReconcilerMetrics instance.
func NewReconcilerMetrics() *ReconcilerMetrics {
	return &ReconcilerMetrics{
		resourceMetrics: make(map[ResourceType]*resourceTypeMetrics),
	}
}

// getOrCreateResourceMetrics returns existing metrics for a resource type or creates new ones.
func (m *ReconcilerMetrics) getOrCreateResourceMetrics(resourceType ResourceType) *resourceTypeMetrics {
	if metrics, exists := m.resourceMetrics[resourceType]; exists {
		return metrics
	}

	metrics := &resourceTypeMetrics{
		ResourceType: resourceType,
	}
	m.resourceMetrics[resourceType] = metrics
	return metrics
}

// RecordStatusSyncAttempt records a status sync attempt.
func (m *ReconcilerMetrics) RecordStatusSyncAttempt(resourceType ResourceType, resourceName string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getOrCreateResourceMetrics(resourceType)
	metrics.StatusSyncAttempts++
	metrics.LastStatusSyncAt = time.Now()
	m.totalStatusSyncAttempts++

	logging.Debug("ReconcilerMetrics", "Status sync attempt for %s/%s", resourceType, resourceName)
}

// RecordStatusSyncSuccess records a successful status sync.
func (m *ReconcilerMetrics) RecordStatusSyncSuccess(resourceType ResourceType, resourceName string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getOrCreateResourceMetrics(resourceType)
	metrics.StatusSyncSuccesses++
	m.totalStatusSyncSuccesses++

	logging.Debug("ReconcilerMetrics", "Status sync success for %s/%s", resourceType, resourceName)
}

// RecordStatusSyncFailure records a failed status sync attempt.
//
// This metric is important for monitoring the health of CRD status updates.
// High failure rates may indicate:
//   - Kubernetes API server issues
//   - RBAC permission problems
//   - Network connectivity issues
//   - CRD schema mismatches
func (m *ReconcilerMetrics) RecordStatusSyncFailure(resourceType ResourceType, resourceName string, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getOrCreateResourceMetrics(resourceType)
	metrics.StatusSyncFailures++
	m.totalStatusSyncFailures++

	logging.Warn("ReconcilerMetrics", "Status sync failure for %s/%s: %s (failures: %d)",
		resourceType, resourceName, reason, metrics.StatusSyncFailures)
}

// ReconcilerMetricsSummary provides a summary of reconciliation metrics.
type ReconcilerMetricsSummary struct {
	TotalReconcileAttempts   int64                    `json:"total_reconcile_attempts"`
	TotalReconcileSuccesses  int64                    `json:"total_reconcile_successes"`
	TotalReconcileFailures   int64                    `json:"total_reconcile_failures"`
	TotalStatusSyncAttempts  int64                    `json:"total_status_sync_attempts"`
	TotalStatusSyncSuccesses int64                    `json:"total_status_sync_successes"`
	TotalStatusSyncFailures  int64                    `json:"total_status_sync_failures"`
	PerResourceTypeMetrics   []ResourceTypeMetricView `json:"per_resource_type_metrics"`
	StatusSyncFailureRate    float64                  `json:"status_sync_failure_rate"`
	ReconcileFailureRate     float64                  `json:"reconcile_failure_rate"`
}

// ResourceTypeMetricView is a read-only view of resource-type-specific metrics.
type ResourceTypeMetricView struct {
	ResourceType        ResourceType `json:"resource_type"`
	ReconcileAttempts   int64        `json:"reconcile_attempts"`
	ReconcileSuccesses  int64        `json:"reconcile_successes"`
	ReconcileFailures   int64        `json:"reconcile_failures"`
	StatusSyncAttempts  int64        `json:"status_sync_attempts"`
	StatusSyncSuccesses int64        `json:"status_sync_successes"`
	StatusSyncFailures  int64        `json:"status_sync_failures"`
	LastReconcileAt     time.Time    `json:"last_reconcile_at,omitempty"`
	LastSuccessAt       time.Time    `json:"last_success_at,omitempty"`
	LastFailureAt       time.Time    `json:"last_failure_at,omitempty"`
	LastStatusSyncAt    time.Time    `json:"last_status_sync_at,omitempty"`
}

// Global metrics instance for use by reconcilers.
// This is initialized lazily and should be accessed via GetReconcilerMetrics().
var (
	globalReconcilerMetrics     *ReconcilerMetrics
	globalReconcilerMetricsMu   sync.RWMutex
	globalReconcilerMetricsOnce sync.Once
)

// GetReconcilerMetrics returns the global reconciler metrics instance.
// It creates the instance on first access (lazy initialization).
func GetReconcilerMetrics() *ReconcilerMetrics {
	globalReconcilerMetricsMu.RLock()
	if globalReconcilerMetrics != nil {
		defer globalReconcilerMetricsMu.RUnlock()
		return globalReconcilerMetrics
	}
	globalReconcilerMetricsMu.RUnlock()

	globalReconcilerMetricsMu.Lock()
	defer globalReconcilerMetricsMu.Unlock()

	// Double-check after acquiring write lock
	if globalReconcilerMetrics == nil {
		globalReconcilerMetrics = NewReconcilerMetrics()
	}
	return globalReconcilerMetrics
}
