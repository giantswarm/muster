package reconciler

import (
	"context"
	"fmt"
	"testing"

	"muster/internal/api"
)

func TestMCPServerReconciler_GetResourceType(t *testing.T) {
	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()
	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	if reconciler.GetResourceType() != ResourceTypeMCPServer {
		t.Errorf("expected ResourceTypeMCPServer, got %s", reconciler.GetResourceType())
	}
}

func TestMCPServerReconciler_ReconcileCreate(t *testing.T) {
	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()
	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	// Add a valid MCPServer with AutoStart enabled
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "test-command",
		AutoStart: true,
	})

	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	// Verify service was started
	if !orchAPI.StartedServices["test-server"] {
		t.Error("expected service to be started")
	}
}

func TestMCPServerReconciler_ReconcileCreateNoAutoStart(t *testing.T) {
	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()
	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	// Add a valid MCPServer with AutoStart disabled
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "test-command",
		AutoStart: false,
	})

	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	// Verify service was NOT started (AutoStart is false)
	if orchAPI.StartedServices["test-server"] {
		t.Error("service should not be started when AutoStart is false")
	}
}

func TestMCPServerReconciler_ReconcileDelete(t *testing.T) {
	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()

	// Add service to registry to simulate it exists
	registry.AddService("deleted-server", &MockServiceInfo{
		Name:        "deleted-server",
		ServiceType: api.TypeMCPServer,
		State:       api.StateRunning,
		Health:      api.HealthHealthy,
	})

	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	// Do not add the MCPServer to manager - simulate a delete scenario
	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "deleted-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error for delete: %v", result.Error)
	}

	// Verify service was stopped
	if !orchAPI.StoppedServices["deleted-server"] {
		t.Error("expected service to be stopped on delete")
	}
}

func TestMCPServerReconciler_ReconcileDeleteNotFound(t *testing.T) {
	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()
	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	// Do not add the MCPServer to manager or registry - nothing to delete
	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "nonexistent-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error for nonexistent delete: %v", result.Error)
	}
	if result.Requeue {
		t.Error("expected no requeue for nonexistent resource")
	}
}

func TestMCPServerReconciler_ReconcileUpdate(t *testing.T) {
	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()

	// Add existing service with old configuration
	registry.AddService("test-server", &MockServiceInfo{
		Name:        "test-server",
		ServiceType: api.TypeMCPServer,
		State:       api.StateRunning,
		Health:      api.HealthHealthy,
		ServiceData: map[string]interface{}{
			"url":       "http://old-url",
			"command":   "old-command",
			"type":      "stdio",
			"autoStart": true,
		},
	})

	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	// Add MCPServer with new configuration
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "new-command", // Changed
		AutoStart: true,
	})

	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	// Verify service was restarted due to config change
	if !orchAPI.RestartedServices["test-server"] {
		t.Error("expected service to be restarted on config change")
	}
}

func TestMCPServerReconciler_ReconcileUpdateNoChange(t *testing.T) {
	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()

	// Add existing service with same configuration
	registry.AddService("test-server", &MockServiceInfo{
		Name:        "test-server",
		ServiceType: api.TypeMCPServer,
		State:       api.StateRunning,
		Health:      api.HealthHealthy,
		ServiceData: map[string]interface{}{
			"url":       "",
			"command":   "test-command",
			"type":      "stdio",
			"autoStart": true,
		},
	})

	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	// Add MCPServer with same configuration
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "test-command",
		AutoStart: true,
	})

	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	// Verify service was NOT restarted (no config change)
	if orchAPI.RestartedServices["test-server"] {
		t.Error("service should not be restarted when config is unchanged")
	}
}

func TestMCPServerReconciler_ReconcileStartError(t *testing.T) {
	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()

	// Simulate start error
	orchAPI.StartError = fmt.Errorf("service not found in orchestrator")

	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "test-command",
		AutoStart: true,
	})

	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error == nil {
		t.Error("expected error when start fails")
	}
	if !result.Requeue {
		t.Error("expected requeue on start error")
	}
}

func TestMCPServerReconciler_ReconcileStopError(t *testing.T) {
	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()

	// Add service to registry to simulate it exists
	registry.AddService("deleted-server", &MockServiceInfo{
		Name:        "deleted-server",
		ServiceType: api.TypeMCPServer,
		State:       api.StateRunning,
		Health:      api.HealthHealthy,
	})

	// Simulate stop error
	orchAPI.StopError = fmt.Errorf("failed to stop service")

	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	// Do not add the MCPServer to manager - simulate a delete scenario
	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "deleted-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error == nil {
		t.Error("expected error when stop fails")
	}
	if !result.Requeue {
		t.Error("expected requeue on stop error")
	}
}

func TestMCPServerReconciler_ReconcileRestartError(t *testing.T) {
	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()

	// Add existing service with old configuration
	registry.AddService("test-server", &MockServiceInfo{
		Name:        "test-server",
		ServiceType: api.TypeMCPServer,
		State:       api.StateRunning,
		Health:      api.HealthHealthy,
		ServiceData: map[string]interface{}{
			"url":       "",
			"command":   "old-command",
			"type":      "stdio",
			"autoStart": true,
		},
	})

	// Simulate restart error
	orchAPI.RestartError = fmt.Errorf("failed to restart service")

	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	// Add MCPServer with new configuration to trigger restart
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "new-command", // Changed
		AutoStart: true,
	})

	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error == nil {
		t.Error("expected error when restart fails")
	}
	if !result.Requeue {
		t.Error("expected requeue on restart error")
	}
}

func TestMCPServerReconciler_NeedsRestart(t *testing.T) {
	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()
	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	tests := []struct {
		name         string
		desired      *api.MCPServerInfo
		serviceData  map[string]interface{}
		expectChange bool
	}{
		{
			name: "no change",
			desired: &api.MCPServerInfo{
				Name:      "test",
				Type:      "stdio",
				Command:   "cmd",
				AutoStart: true,
			},
			serviceData: map[string]interface{}{
				"url":       "",
				"command":   "cmd",
				"type":      "stdio",
				"autoStart": true,
			},
			expectChange: false,
		},
		{
			name: "command changed",
			desired: &api.MCPServerInfo{
				Name:      "test",
				Type:      "stdio",
				Command:   "new-cmd",
				AutoStart: true,
			},
			serviceData: map[string]interface{}{
				"url":       "",
				"command":   "old-cmd",
				"type":      "stdio",
				"autoStart": true,
			},
			expectChange: true,
		},
		{
			name: "url changed",
			desired: &api.MCPServerInfo{
				Name:      "test",
				Type:      "streamable-http",
				URL:       "http://new-url",
				AutoStart: true,
			},
			serviceData: map[string]interface{}{
				"url":       "http://old-url",
				"command":   "",
				"type":      "streamable-http",
				"autoStart": true,
			},
			expectChange: true,
		},
		{
			name: "type changed",
			desired: &api.MCPServerInfo{
				Name:      "test",
				Type:      "streamable-http",
				AutoStart: true,
			},
			serviceData: map[string]interface{}{
				"url":       "",
				"command":   "",
				"type":      "stdio",
				"autoStart": true,
			},
			expectChange: true,
		},
		{
			name: "autostart enabled",
			desired: &api.MCPServerInfo{
				Name:      "test",
				Type:      "stdio",
				AutoStart: true,
			},
			serviceData: map[string]interface{}{
				"url":       "",
				"command":   "",
				"type":      "stdio",
				"autoStart": false,
			},
			expectChange: true,
		},
		{
			name: "nil service data",
			desired: &api.MCPServerInfo{
				Name:      "test",
				Type:      "stdio",
				AutoStart: true,
			},
			serviceData:  nil,
			expectChange: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svcInfo := &MockServiceInfo{
				Name:        "test",
				ServiceType: api.TypeMCPServer,
				State:       api.StateRunning,
				ServiceData: tt.serviceData,
			}

			needsRestart := reconciler.needsRestart(tt.desired, svcInfo)

			if needsRestart != tt.expectChange {
				t.Errorf("needsRestart() = %v, expected %v", needsRestart, tt.expectChange)
			}
		})
	}
}

func TestMCPServerReconciler_PeriodicRequeue(t *testing.T) {
	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()

	// Add existing service (no config change)
	registry.AddService("test-server", &MockServiceInfo{
		Name:        "test-server",
		ServiceType: api.TypeMCPServer,
		State:       api.StateRunning,
		Health:      api.HealthHealthy,
		ServiceData: map[string]interface{}{
			"url":       "",
			"command":   "test-command",
			"type":      "stdio",
			"autoStart": true,
		},
	})

	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "test-command",
		AutoStart: true,
	})

	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	// Verify RequeueAfter is set for periodic status sync
	if result.RequeueAfter == 0 {
		t.Error("expected RequeueAfter to be set for periodic status sync")
	}

	if result.RequeueAfter != DefaultStatusSyncInterval {
		t.Errorf("expected RequeueAfter = %v, got %v", DefaultStatusSyncInterval, result.RequeueAfter)
	}
}

func TestMCPServerReconciler_ArgsChange(t *testing.T) {
	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()

	// Add existing service with args
	registry.AddService("test-server", &MockServiceInfo{
		Name:        "test-server",
		ServiceType: api.TypeMCPServer,
		State:       api.StateRunning,
		Health:      api.HealthHealthy,
		ServiceData: map[string]interface{}{
			"url":       "",
			"command":   "test-command",
			"type":      "stdio",
			"autoStart": true,
			"args":      []string{"old-arg1", "old-arg2"},
		},
	})

	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	// Add MCPServer with different args
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "test-command",
		Args:      []string{"new-arg1", "new-arg2"},
		AutoStart: true,
	})

	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	// Verify service was restarted due to args change
	if !orchAPI.RestartedServices["test-server"] {
		t.Error("expected service to be restarted when args change")
	}
}

// =============================================================================
// Status Sync Tests - Verify syncStatus functionality
// =============================================================================

func TestMCPServerReconciler_SyncStatus_RunningService(t *testing.T) {
	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()
	statusUpdater := NewMockStatusUpdater()

	// Add existing running service
	registry.AddService("test-server", &MockServiceInfo{
		Name:        "test-server",
		ServiceType: api.TypeMCPServer,
		State:       api.StateRunning,
		Health:      api.HealthHealthy,
	})

	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry).
		WithStatusUpdater(statusUpdater, "default")

	// Add MCPServer with same config (no restart needed)
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "test-command",
		AutoStart: true,
	})

	req := ReconcileRequest{
		Type:      ResourceTypeMCPServer,
		Name:      "test-server",
		Namespace: "default",
		Attempt:   1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	// Verify status was synced
	if !statusUpdater.GetMCPServerCalled {
		t.Error("expected GetMCPServer to be called for status sync")
	}
	if !statusUpdater.UpdateMCPServerStatusCalled {
		t.Error("expected UpdateMCPServerStatus to be called")
	}

	// Verify status values
	if statusUpdater.LastUpdatedMCPServer == nil {
		t.Fatal("expected LastUpdatedMCPServer to be set")
	}
	if statusUpdater.LastUpdatedMCPServer.Status.State != "running" {
		t.Errorf("expected state 'running', got '%s'", statusUpdater.LastUpdatedMCPServer.Status.State)
	}
	if statusUpdater.LastUpdatedMCPServer.Status.Health != "healthy" {
		t.Errorf("expected health 'healthy', got '%s'", statusUpdater.LastUpdatedMCPServer.Status.Health)
	}
	if statusUpdater.LastUpdatedMCPServer.Status.LastConnected == nil {
		t.Error("expected LastConnected to be set for running service")
	}
}

func TestMCPServerReconciler_SyncStatus_ServiceNotFound(t *testing.T) {
	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()
	statusUpdater := NewMockStatusUpdater()

	// No service in registry - simulate deleted service

	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry).
		WithStatusUpdater(statusUpdater, "default")

	// MCPServer exists but service doesn't - will be created
	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "test-command",
		AutoStart: true,
	})

	req := ReconcileRequest{
		Type:      ResourceTypeMCPServer,
		Name:      "test-server",
		Namespace: "default",
		Attempt:   1,
	}

	ctx := context.Background()
	_ = reconciler.Reconcile(ctx, req)

	// Verify status was synced (even if service is not yet running)
	if !statusUpdater.UpdateMCPServerStatusCalled {
		t.Error("expected UpdateMCPServerStatus to be called")
	}
}

func TestMCPServerReconciler_SyncStatus_WithError(t *testing.T) {
	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()
	statusUpdater := NewMockStatusUpdater()

	// Add service with error
	registry.AddService("test-server", &MockServiceInfo{
		Name:        "test-server",
		ServiceType: api.TypeMCPServer,
		State:       api.StateError,
		Health:      api.HealthUnhealthy,
		LastError:   fmt.Errorf("connection failed"),
	})

	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry).
		WithStatusUpdater(statusUpdater, "default")

	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "test-command",
		AutoStart: true,
	})

	req := ReconcileRequest{
		Type:      ResourceTypeMCPServer,
		Name:      "test-server",
		Namespace: "default",
		Attempt:   1,
	}

	ctx := context.Background()
	_ = reconciler.Reconcile(ctx, req)

	// Verify status was synced with error
	if !statusUpdater.UpdateMCPServerStatusCalled {
		t.Error("expected UpdateMCPServerStatus to be called")
	}

	if statusUpdater.LastUpdatedMCPServer == nil {
		t.Fatal("expected LastUpdatedMCPServer to be set")
	}
	if statusUpdater.LastUpdatedMCPServer.Status.State != "error" {
		t.Errorf("expected state 'error', got '%s'", statusUpdater.LastUpdatedMCPServer.Status.State)
	}
	if statusUpdater.LastUpdatedMCPServer.Status.LastError == "" {
		t.Error("expected LastError to be set")
	}
}

func TestMCPServerReconciler_SyncStatus_NoUpdaterConfigured(t *testing.T) {
	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()

	// No status updater configured - should not panic
	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry)

	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "test-command",
		AutoStart: true,
	})

	req := ReconcileRequest{
		Type:    ResourceTypeMCPServer,
		Name:    "test-server",
		Attempt: 1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	// Should complete without error even without status updater
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}
}

func TestMCPServerReconciler_SyncStatus_GetMCPServerError(t *testing.T) {
	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()
	statusUpdater := NewMockStatusUpdater()

	// Simulate error when getting MCPServer CRD
	statusUpdater.GetMCPServerError = fmt.Errorf("CRD not found")

	registry.AddService("test-server", &MockServiceInfo{
		Name:        "test-server",
		ServiceType: api.TypeMCPServer,
		State:       api.StateRunning,
		Health:      api.HealthHealthy,
	})

	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry).
		WithStatusUpdater(statusUpdater, "default")

	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "test-command",
		AutoStart: true,
	})

	req := ReconcileRequest{
		Type:      ResourceTypeMCPServer,
		Name:      "test-server",
		Namespace: "default",
		Attempt:   1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	// Reconciliation should still succeed even if status sync fails
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	// GetMCPServer was called but UpdateMCPServerStatus should not be called
	if !statusUpdater.GetMCPServerCalled {
		t.Error("expected GetMCPServer to be called")
	}
	if statusUpdater.UpdateMCPServerStatusCalled {
		t.Error("expected UpdateMCPServerStatus NOT to be called when GetMCPServer fails")
	}
}

func TestMCPServerReconciler_SyncStatus_UpdateError(t *testing.T) {
	mgr := NewMockMCPServerManager()
	orchAPI := NewMockOrchestratorAPI()
	registry := NewMockServiceRegistry()
	statusUpdater := NewMockStatusUpdater()

	// Simulate error when updating status
	statusUpdater.UpdateMCPServerStatusError = fmt.Errorf("update failed")

	registry.AddService("test-server", &MockServiceInfo{
		Name:        "test-server",
		ServiceType: api.TypeMCPServer,
		State:       api.StateRunning,
		Health:      api.HealthHealthy,
	})

	reconciler := NewMCPServerReconciler(orchAPI, mgr, registry).
		WithStatusUpdater(statusUpdater, "default")

	mgr.AddMCPServer(&api.MCPServerInfo{
		Name:      "test-server",
		Type:      "stdio",
		Command:   "test-command",
		AutoStart: true,
	})

	req := ReconcileRequest{
		Type:      ResourceTypeMCPServer,
		Name:      "test-server",
		Namespace: "default",
		Attempt:   1,
	}

	ctx := context.Background()
	result := reconciler.Reconcile(ctx, req)

	// Reconciliation should still succeed even if status update fails
	// (status sync is best-effort)
	if result.Error != nil {
		t.Errorf("unexpected error: %v", result.Error)
	}

	// Both methods should be called
	if !statusUpdater.GetMCPServerCalled {
		t.Error("expected GetMCPServer to be called")
	}
	if !statusUpdater.UpdateMCPServerStatusCalled {
		t.Error("expected UpdateMCPServerStatus to be called")
	}
}
