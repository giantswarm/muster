package reconciler

import (
	"testing"
)

func TestReconcilerMetrics_NewInstance(t *testing.T) {
	metrics := NewReconcilerMetrics()
	if metrics == nil {
		t.Fatal("expected non-nil metrics instance")
	}
	if metrics.resourceMetrics == nil {
		t.Error("expected resourceMetrics map to be initialized")
	}
}

func TestReconcilerMetrics_RecordReconcileAttempt(t *testing.T) {
	metrics := NewReconcilerMetrics()

	metrics.RecordReconcileAttempt(ResourceTypeMCPServer, "test-server")

	summary := metrics.GetSummary()
	if summary.TotalReconcileAttempts != 1 {
		t.Errorf("expected TotalReconcileAttempts=1, got %d", summary.TotalReconcileAttempts)
	}

	// Verify per-resource-type metrics
	rtMetrics, ok := metrics.GetResourceTypeMetrics(ResourceTypeMCPServer)
	if !ok {
		t.Fatal("expected MCPServer metrics to exist")
	}
	if rtMetrics.ReconcileAttempts != 1 {
		t.Errorf("expected ReconcileAttempts=1, got %d", rtMetrics.ReconcileAttempts)
	}
}

func TestReconcilerMetrics_RecordReconcileSuccess(t *testing.T) {
	metrics := NewReconcilerMetrics()

	metrics.RecordReconcileSuccess(ResourceTypeServiceClass, "test-class")

	summary := metrics.GetSummary()
	if summary.TotalReconcileSuccesses != 1 {
		t.Errorf("expected TotalReconcileSuccesses=1, got %d", summary.TotalReconcileSuccesses)
	}
}

func TestReconcilerMetrics_RecordReconcileFailure(t *testing.T) {
	metrics := NewReconcilerMetrics()

	metrics.RecordReconcileFailure(ResourceTypeWorkflow, "test-workflow", "validation failed")

	summary := metrics.GetSummary()
	if summary.TotalReconcileFailures != 1 {
		t.Errorf("expected TotalReconcileFailures=1, got %d", summary.TotalReconcileFailures)
	}

	rtMetrics, ok := metrics.GetResourceTypeMetrics(ResourceTypeWorkflow)
	if !ok {
		t.Fatal("expected Workflow metrics to exist")
	}
	if rtMetrics.ReconcileFailures != 1 {
		t.Errorf("expected ReconcileFailures=1, got %d", rtMetrics.ReconcileFailures)
	}
	if rtMetrics.LastFailureAt.IsZero() {
		t.Error("expected LastFailureAt to be set")
	}
}

func TestReconcilerMetrics_StatusSyncMetrics(t *testing.T) {
	metrics := NewReconcilerMetrics()

	// Record status sync attempt, success, and failure
	metrics.RecordStatusSyncAttempt(ResourceTypeMCPServer, "server1")
	metrics.RecordStatusSyncSuccess(ResourceTypeMCPServer, "server1")

	metrics.RecordStatusSyncAttempt(ResourceTypeMCPServer, "server2")
	metrics.RecordStatusSyncFailure(ResourceTypeMCPServer, "server2", "CRD not found")

	summary := metrics.GetSummary()
	if summary.TotalStatusSyncAttempts != 2 {
		t.Errorf("expected TotalStatusSyncAttempts=2, got %d", summary.TotalStatusSyncAttempts)
	}
	if summary.TotalStatusSyncSuccesses != 1 {
		t.Errorf("expected TotalStatusSyncSuccesses=1, got %d", summary.TotalStatusSyncSuccesses)
	}
	if summary.TotalStatusSyncFailures != 1 {
		t.Errorf("expected TotalStatusSyncFailures=1, got %d", summary.TotalStatusSyncFailures)
	}

	// Check failure rate
	expectedRate := 0.5 // 1 failure / 2 attempts
	if summary.StatusSyncFailureRate != expectedRate {
		t.Errorf("expected StatusSyncFailureRate=%f, got %f", expectedRate, summary.StatusSyncFailureRate)
	}
}

func TestReconcilerMetrics_MultipleResourceTypes(t *testing.T) {
	metrics := NewReconcilerMetrics()

	// Record metrics for different resource types
	metrics.RecordReconcileAttempt(ResourceTypeMCPServer, "server1")
	metrics.RecordReconcileSuccess(ResourceTypeMCPServer, "server1")

	metrics.RecordReconcileAttempt(ResourceTypeServiceClass, "class1")
	metrics.RecordReconcileFailure(ResourceTypeServiceClass, "class1", "validation failed")

	metrics.RecordReconcileAttempt(ResourceTypeWorkflow, "workflow1")
	metrics.RecordReconcileSuccess(ResourceTypeWorkflow, "workflow1")

	summary := metrics.GetSummary()
	if summary.TotalReconcileAttempts != 3 {
		t.Errorf("expected TotalReconcileAttempts=3, got %d", summary.TotalReconcileAttempts)
	}
	if summary.TotalReconcileSuccesses != 2 {
		t.Errorf("expected TotalReconcileSuccesses=2, got %d", summary.TotalReconcileSuccesses)
	}
	if summary.TotalReconcileFailures != 1 {
		t.Errorf("expected TotalReconcileFailures=1, got %d", summary.TotalReconcileFailures)
	}

	// Verify per-resource-type metrics
	if len(summary.PerResourceTypeMetrics) != 3 {
		t.Errorf("expected 3 resource type metrics, got %d", len(summary.PerResourceTypeMetrics))
	}
}

func TestReconcilerMetrics_GetResourceTypeMetrics_NotFound(t *testing.T) {
	metrics := NewReconcilerMetrics()

	_, ok := metrics.GetResourceTypeMetrics(ResourceTypeMCPServer)
	if ok {
		t.Error("expected MCPServer metrics to not exist")
	}
}

func TestReconcilerMetrics_FailureRateZeroAttempts(t *testing.T) {
	metrics := NewReconcilerMetrics()

	summary := metrics.GetSummary()

	// With zero attempts, failure rates should be zero
	if summary.ReconcileFailureRate != 0 {
		t.Errorf("expected ReconcileFailureRate=0 with no attempts, got %f", summary.ReconcileFailureRate)
	}
	if summary.StatusSyncFailureRate != 0 {
		t.Errorf("expected StatusSyncFailureRate=0 with no attempts, got %f", summary.StatusSyncFailureRate)
	}
}

func TestGetReconcilerMetrics_Singleton(t *testing.T) {
	// Reset to ensure clean state for this test
	ResetReconcilerMetrics()

	// GetReconcilerMetrics should return the same instance
	metrics1 := GetReconcilerMetrics()
	metrics2 := GetReconcilerMetrics()

	if metrics1 != metrics2 {
		t.Error("expected GetReconcilerMetrics to return the same instance")
	}
}

func TestResetReconcilerMetrics_ClearsState(t *testing.T) {
	// Get initial metrics and record some data
	metrics := GetReconcilerMetrics()
	metrics.RecordReconcileAttempt(ResourceTypeMCPServer, "test-server")

	summary := metrics.GetSummary()
	if summary.TotalReconcileAttempts != 1 {
		t.Fatalf("expected 1 attempt before reset, got %d", summary.TotalReconcileAttempts)
	}

	// Reset metrics
	ResetReconcilerMetrics()

	// Get new metrics instance and verify it's fresh
	newMetrics := GetReconcilerMetrics()
	newSummary := newMetrics.GetSummary()

	if newSummary.TotalReconcileAttempts != 0 {
		t.Errorf("expected 0 attempts after reset, got %d", newSummary.TotalReconcileAttempts)
	}

	// Verify it's a different instance
	if metrics == newMetrics {
		t.Error("expected different instance after reset")
	}
}

func TestGetReconcilerMetrics_ConcurrentAccess(t *testing.T) {
	// Reset to ensure clean state
	ResetReconcilerMetrics()

	// Test concurrent access to GetReconcilerMetrics
	const goroutines = 10
	done := make(chan *ReconcilerMetrics, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			done <- GetReconcilerMetrics()
		}()
	}

	// Collect all instances
	var instances []*ReconcilerMetrics
	for i := 0; i < goroutines; i++ {
		instances = append(instances, <-done)
	}

	// All instances should be the same
	first := instances[0]
	for i, inst := range instances {
		if inst != first {
			t.Errorf("instance %d differs from first instance", i)
		}
	}
}
