package reconciler

import (
	"context"
	"fmt"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"muster/internal/api"
	"muster/pkg/logging"
)

// MCPServerManager is an interface for accessing MCPServer definitions.
// This is an alias for the api.MCPServerManagerHandler interface.
type MCPServerManager interface {
	ListMCPServers() []api.MCPServerInfo
	GetMCPServer(name string) (*api.MCPServerInfo, error)
}

// MCPServerReconciler reconciles MCPServer resources.
//
// It ensures that MCPServer definitions (from CRDs or YAML files) are
// synchronized with the running services managed by the orchestrator.
//
// Reconciliation logic:
//   - Create: Register and start a new MCPServer service
//   - Update: Update the service configuration and restart if needed
//   - Delete: Stop and unregister the MCPServer service
//
// After each reconciliation, the reconciler syncs the service state
// back to the CRD's Status field. See ADR 007 for details.
type MCPServerReconciler struct {
	BaseStatusConfig

	// orchestratorAPI provides access to service lifecycle management
	orchestratorAPI api.OrchestratorAPI

	// mcpServerManager provides access to MCPServer definitions
	mcpServerManager MCPServerManager

	// serviceRegistry provides access to running services
	serviceRegistry api.ServiceRegistryHandler
}

// NewMCPServerReconciler creates a new MCPServer reconciler.
func NewMCPServerReconciler(
	orchestratorAPI api.OrchestratorAPI,
	mcpServerManager MCPServerManager,
	serviceRegistry api.ServiceRegistryHandler,
) *MCPServerReconciler {
	return &MCPServerReconciler{
		BaseStatusConfig: BaseStatusConfig{Namespace: DefaultNamespace},
		orchestratorAPI:  orchestratorAPI,
		mcpServerManager: mcpServerManager,
		serviceRegistry:  serviceRegistry,
	}
}

// WithStatusUpdater sets the status updater for syncing status back to CRDs.
func (r *MCPServerReconciler) WithStatusUpdater(updater StatusUpdater, namespace string) *MCPServerReconciler {
	r.SetStatusUpdater(updater, namespace)
	return r
}

// GetResourceType returns the resource type this reconciler handles.
func (r *MCPServerReconciler) GetResourceType() ResourceType {
	return ResourceTypeMCPServer
}

// Reconcile processes a single MCPServer reconciliation request.
//
// After successful reconciliation, this returns RequeueAfter to enable periodic
// status sync. This ensures that runtime state changes (service crashes, health
// check failures, etc.) are eventually reflected in the CRD status even if
// state change events are missed.
func (r *MCPServerReconciler) Reconcile(ctx context.Context, req ReconcileRequest) ReconcileResult {
	logging.Debug("MCPServerReconciler", "Reconciling MCPServer: %s", req.Name)

	// Fetch the desired state from the definition source
	mcpServerInfo, err := r.mcpServerManager.GetMCPServer(req.Name)
	if err != nil {
		// If not found, this might be a delete operation
		if IsNotFoundError(err) {
			return r.reconcileDelete(ctx, req)
		}
		return ReconcileResult{
			Error:   fmt.Errorf("failed to get MCPServer definition: %w", err),
			Requeue: true,
		}
	}

	// Check if service exists
	existingService, exists := r.serviceRegistry.Get(req.Name)

	var result ReconcileResult
	if !exists {
		// Service doesn't exist, create it
		result = r.reconcileCreate(ctx, req, mcpServerInfo)
	} else {
		// Service exists, check if update is needed
		result = r.reconcileUpdate(ctx, req, mcpServerInfo, existingService)
	}

	// Sync status back to CRD after reconciliation
	r.syncStatus(ctx, req.Name, req.Namespace, result.Error)

	// If reconciliation succeeded, schedule periodic requeue for status sync.
	// This implements the idiomatic Kubernetes controller pattern where status
	// is periodically refreshed to ensure eventual consistency.
	if result.Error == nil && !result.Requeue {
		result.RequeueAfter = DefaultStatusSyncInterval
	}

	return result
}

// syncStatus syncs the current service state to the MCPServer CRD status.
//
// Status sync is a best-effort operation - failures are logged at Debug level
// rather than Warn/Error to avoid log spam. Status sync may fail frequently in
// legitimate scenarios (e.g., filesystem mode, CRD not yet created, temporary
// API server unavailability). Failures are tracked in metrics for monitoring.
func (r *MCPServerReconciler) syncStatus(ctx context.Context, name, namespace string, reconcileErr error) {
	if r.StatusUpdater == nil {
		return
	}

	namespace = r.GetNamespace(namespace)

	// Record status sync attempt for metrics
	metrics := GetReconcilerMetrics()
	metrics.RecordStatusSyncAttempt(ResourceTypeMCPServer, name)

	// Get the current CRD
	server, err := r.StatusUpdater.GetMCPServer(ctx, name, namespace)
	if err != nil {
		logging.Debug("MCPServerReconciler", "Failed to get MCPServer for status sync: %v", err)
		metrics.RecordStatusSyncFailure(ResourceTypeMCPServer, name, "get_crd_failed")
		return
	}

	// Get the current service state
	service, exists := r.serviceRegistry.Get(name)

	if exists {
		// Update status from service state
		server.Status.State = string(service.GetState())
		server.Status.Health = string(service.GetHealth())
		if service.GetLastError() != nil {
			// Sanitize error message to remove sensitive data before CRD exposure
			server.Status.LastError = SanitizeErrorMessage(service.GetLastError().Error())
		} else {
			server.Status.LastError = ""
		}
		// Update LastConnected if service is running
		if service.GetState() == api.StateRunning {
			now := metav1.NewTime(time.Now())
			server.Status.LastConnected = &now
		}
	} else {
		// Service doesn't exist - use typed constants
		server.Status.State = ServiceStateStopped
		server.Status.Health = ServiceHealthUnknown
		if reconcileErr != nil {
			// Sanitize error message to remove sensitive data before CRD exposure
			server.Status.LastError = SanitizeErrorMessage(reconcileErr.Error())
		}
	}

	// Update the CRD status
	if err := r.StatusUpdater.UpdateMCPServerStatus(ctx, server); err != nil {
		logging.Debug("MCPServerReconciler", "Failed to update MCPServer status: %v", err)
		metrics.RecordStatusSyncFailure(ResourceTypeMCPServer, name, "update_status_failed")
	} else {
		logging.Debug("MCPServerReconciler", "Synced MCPServer %s status: state=%s, health=%s",
			name, server.Status.State, server.Status.Health)
		metrics.RecordStatusSyncSuccess(ResourceTypeMCPServer, name)
	}
}

// reconcileCreate handles creating a new MCPServer service.
func (r *MCPServerReconciler) reconcileCreate(ctx context.Context, req ReconcileRequest, info *api.MCPServerInfo) ReconcileResult {
	logging.Info("MCPServerReconciler", "Creating MCPServer service: %s", req.Name)

	// Only create if AutoStart is enabled
	if !info.AutoStart {
		logging.Debug("MCPServerReconciler", "Skipping MCPServer %s: AutoStart=false", req.Name)
		return ReconcileResult{}
	}

	// Start the service via orchestrator
	if err := r.orchestratorAPI.StartService(req.Name); err != nil {
		// If service doesn't exist in orchestrator, we need to create it first
		// The orchestrator should handle this via processServiceClassRequirements
		// For now, we trigger a manual refresh
		logging.Debug("MCPServerReconciler", "Service %s not found in orchestrator, may need creation", req.Name)
		return ReconcileResult{
			Error:   fmt.Errorf("service not found in orchestrator: %w", err),
			Requeue: true,
		}
	}

	logging.Info("MCPServerReconciler", "Successfully created MCPServer service: %s", req.Name)
	return ReconcileResult{}
}

// reconcileUpdate handles updating an existing MCPServer service.
func (r *MCPServerReconciler) reconcileUpdate(ctx context.Context, req ReconcileRequest, info *api.MCPServerInfo, existingService api.ServiceInfo) ReconcileResult {
	logging.Debug("MCPServerReconciler", "Checking MCPServer service for updates: %s", req.Name)

	// Compare current state with desired state
	needsRestart := r.needsRestart(info, existingService)

	if !needsRestart {
		logging.Debug("MCPServerReconciler", "MCPServer %s is up to date", req.Name)
		return ReconcileResult{}
	}

	logging.Info("MCPServerReconciler", "MCPServer %s configuration changed, updating and restarting", req.Name)

	// Update the service configuration before restarting
	// This ensures the service uses the new configuration when it restarts
	if configurableService, ok := existingService.(api.ConfigurableService); ok {
		// Convert MCPServerInfo to api.MCPServer for the configuration update
		newConfig := &api.MCPServer{
			Name:        info.Name,
			Type:        api.MCPServerType(info.Type),
			Description: info.Description,
			ToolPrefix:  info.ToolPrefix,
			AutoStart:   info.AutoStart,
			Command:     info.Command,
			Args:        info.Args,
			URL:         info.URL,
			Env:         info.Env,
			Headers:     info.Headers,
			Timeout:     info.Timeout,
		}
		if err := configurableService.UpdateConfiguration(newConfig); err != nil {
			return ReconcileResult{
				Error:   fmt.Errorf("failed to update service configuration: %w", err),
				Requeue: true,
			}
		}
		logging.Debug("MCPServerReconciler", "Updated configuration for MCPServer %s", req.Name)
	} else {
		logging.Warn("MCPServerReconciler", "Service %s does not implement ConfigurableService, restart may use old config", req.Name)
	}

	// Restart the service to apply changes
	if err := r.orchestratorAPI.RestartService(req.Name); err != nil {
		return ReconcileResult{
			Error:   fmt.Errorf("failed to restart service: %w", err),
			Requeue: true,
		}
	}

	logging.Info("MCPServerReconciler", "Successfully updated MCPServer service: %s", req.Name)
	return ReconcileResult{}
}

// reconcileDelete handles deleting an MCPServer service.
func (r *MCPServerReconciler) reconcileDelete(ctx context.Context, req ReconcileRequest) ReconcileResult {
	logging.Info("MCPServerReconciler", "Deleting MCPServer service: %s", req.Name)

	// Check if service exists
	_, exists := r.serviceRegistry.Get(req.Name)
	if !exists {
		logging.Debug("MCPServerReconciler", "MCPServer service %s already deleted", req.Name)
		return ReconcileResult{}
	}

	// Stop the service
	if err := r.orchestratorAPI.StopService(req.Name); err != nil {
		// If service not found, it's already stopped
		if IsNotFoundError(err) {
			return ReconcileResult{}
		}
		return ReconcileResult{
			Error:   fmt.Errorf("failed to stop service: %w", err),
			Requeue: true,
		}
	}

	logging.Info("MCPServerReconciler", "Successfully deleted MCPServer service: %s", req.Name)
	return ReconcileResult{}
}

// needsRestart determines if a service needs to be restarted due to config changes.
func (r *MCPServerReconciler) needsRestart(desired *api.MCPServerInfo, actual api.ServiceInfo) bool {
	// Get the service data which contains the current configuration
	serviceData := actual.GetServiceData()
	if serviceData == nil {
		return true
	}

	// Compare key fields
	// URL change requires restart
	if url, ok := serviceData["url"].(string); ok && url != desired.URL {
		return true
	}

	// Command change requires restart
	if cmd, ok := serviceData["command"].(string); ok && cmd != desired.Command {
		return true
	}

	// Type change requires restart
	if typ, ok := serviceData["type"].(string); ok && typ != desired.Type {
		return true
	}

	// Check if AutoStart changed from false to true
	if autoStart, ok := serviceData["autoStart"].(bool); ok {
		if !autoStart && desired.AutoStart {
			return true
		}
	}

	// Args change requires restart
	if len(desired.Args) > 0 || serviceData["args"] != nil {
		existingArgs, ok := serviceData["args"].([]string)
		if !ok {
			// Type mismatch or nil, needs restart if desired has args
			if len(desired.Args) > 0 {
				return true
			}
		} else if !slices.Equal(existingArgs, desired.Args) {
			return true
		}
	}

	return false
}
