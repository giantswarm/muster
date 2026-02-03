package reconciler

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	"muster/internal/api"
	musterv1alpha1 "muster/pkg/apis/muster/v1alpha1"
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
// This function implements retry-on-conflict logic to handle optimistic locking
// failures that occur when the CRD is modified between read and update operations.
// The retry logic re-fetches the CRD and re-applies the status on each attempt.
//
// Status sync is a best-effort operation - failures are logged with backoff
// to avoid log spam when a resource continuously fails. Failures are tracked
// in metrics for monitoring.
func (r *MCPServerReconciler) syncStatus(ctx context.Context, name, namespace string, reconcileErr error) {
	if r.StatusUpdater == nil {
		return
	}

	namespace = r.GetNamespace(namespace)

	// Initialize status sync helper
	helper := NewStatusSyncHelper(ResourceTypeMCPServer, name, "MCPServerReconciler")
	helper.RecordAttempt()

	// Use retry-on-conflict to handle optimistic locking failures.
	// Each retry re-fetches the CRD with the latest resource version
	// and re-applies the status changes.
	var lastErr error
	retryErr := retry.OnError(StatusSyncRetryBackoff, IsConflictError, func() error {
		// Get the current CRD (re-fetch on each attempt to get latest resource version)
		server, err := r.StatusUpdater.GetMCPServer(ctx, name, namespace)
		if err != nil {
			lastErr = err
			return nil // Return nil to exit retry loop (non-retryable)
		}

		// Apply status from current service state
		r.applyStatusFromService(server, name, reconcileErr)

		// Update the CRD status
		if err := r.StatusUpdater.UpdateMCPServerStatus(ctx, server); err != nil {
			lastErr = err
			return err // Return error to trigger retry if it's a conflict
		}
		lastErr = nil
		return nil
	})

	// Handle the result and log on success
	helper.HandleResult(retryErr, lastErr)
	if helper.WasSuccessful(retryErr, lastErr) {
		logging.Debug("MCPServerReconciler", "Synced MCPServer %s status", name)
	}
}

// applyStatusFromService applies the current service state to the MCPServer status.
// This is extracted to allow re-application during retry-on-conflict.
//
// This function sets Status based on infrastructure state, using context-appropriate
// terminology based on server type:
//   - stdio servers: Running, Starting, Stopped, Failed
//   - remote servers: Connected, Connecting, Disconnected, Failed
//
// Status is independent of user session state (which is tracked in Session Registry).
func (r *MCPServerReconciler) applyStatusFromService(server *musterv1alpha1.MCPServer, name string, reconcileErr error) {
	// Get the current service state
	service, exists := r.serviceRegistry.Get(name)

	if exists {
		state := service.GetState()

		// Set State based on infrastructure state and server type
		// State terminology differs based on server type (stdio vs remote)
		server.Status.State = r.determineState(state, server.Spec.Type)

		if service.GetLastError() != nil {
			// Sanitize error message to remove sensitive data before CRD exposure
			// Note: Per-user auth errors are tracked in Session Registry, not here
			server.Status.LastError = SanitizeErrorMessage(service.GetLastError().Error())
		} else {
			server.Status.LastError = ""
		}

		// Update LastConnected if service is running/connected
		if api.IsActiveState(state) {
			now := metav1.NewTime(time.Now())
			server.Status.LastConnected = &now
		}

		// Sync failure tracking fields for unreachable server detection
		serviceData := service.GetServiceData()
		if serviceData != nil {
			if failures, ok := serviceData["consecutiveFailures"].(int); ok {
				server.Status.ConsecutiveFailures = failures
			}
			if lastAttempt, ok := serviceData["lastAttempt"].(time.Time); ok {
				t := metav1.NewTime(lastAttempt)
				server.Status.LastAttempt = &t
			}
			if nextRetry, ok := serviceData["nextRetryAfter"].(time.Time); ok {
				t := metav1.NewTime(nextRetry)
				server.Status.NextRetryAfter = &t
			}
		}
	} else {
		// Service doesn't exist - use appropriate initial state based on server type
		isRemote := server.Spec.Type == "streamable-http" || server.Spec.Type == "sse"
		if isRemote {
			server.Status.State = musterv1alpha1.MCPServerStateDisconnected
		} else {
			server.Status.State = musterv1alpha1.MCPServerStateStopped
		}
		if reconcileErr != nil {
			// Sanitize error message to remove sensitive data before CRD exposure
			server.Status.LastError = SanitizeErrorMessage(reconcileErr.Error())
		}
	}
}

// determineState converts service state to MCPServer State using context-appropriate terminology.
//
// For stdio (local process) servers:
//   - Running: Process is running and responding
//   - Starting: Process is being started
//   - Stopped: Process is not running
//   - Failed: Process crashed or cannot be started
//
// For remote (streamable-http, sse) servers:
//   - Connected: TCP connection established and authenticated
//   - Auth Required: Server is reachable but requires authentication (401 response)
//   - Connecting: Attempting to establish connection
//   - Disconnected: Not connected
//   - Failed: Endpoint unreachable
func (r *MCPServerReconciler) determineState(state api.ServiceState, serverType string) musterv1alpha1.MCPServerStateValue {
	isRemote := serverType == "streamable-http" || serverType == "sse"

	switch state {
	case api.StateRunning, api.StateConnected:
		// Infrastructure is working
		if isRemote {
			return musterv1alpha1.MCPServerStateConnected
		}
		return musterv1alpha1.MCPServerStateRunning

	case api.StateAuthRequired:
		// auth_required means the server IS reachable (it returned a 401 response)
		// Per issue #337, expose this as "Auth Required" to give users clear feedback
		// that the server is reachable but needs authentication
		if isRemote {
			return musterv1alpha1.MCPServerStateAuthRequired
		}
		// For stdio servers, auth_required is unlikely but treat as running
		return musterv1alpha1.MCPServerStateRunning

	case api.StateStarting, api.StateWaiting, api.StateRetrying:
		// Transitional states - starting up or retrying
		if isRemote {
			return musterv1alpha1.MCPServerStateConnecting
		}
		return musterv1alpha1.MCPServerStateStarting

	case api.StateStopping:
		// Stopping - treat as still running/connected until fully stopped
		if isRemote {
			return musterv1alpha1.MCPServerStateConnected
		}
		return musterv1alpha1.MCPServerStateRunning

	case api.StateStopped, api.StateUnknown:
		// Not yet started or stopped
		if isRemote {
			return musterv1alpha1.MCPServerStateDisconnected
		}
		return musterv1alpha1.MCPServerStateStopped

	case api.StateDisconnected:
		// Disconnected - different from failed (intentional disconnect vs error)
		if isRemote {
			return musterv1alpha1.MCPServerStateDisconnected
		}
		return musterv1alpha1.MCPServerStateStopped

	case api.StateFailed, api.StateError, api.StateUnreachable:
		// Infrastructure failure
		return musterv1alpha1.MCPServerStateFailed

	default:
		if isRemote {
			return musterv1alpha1.MCPServerStateDisconnected
		}
		return musterv1alpha1.MCPServerStateStopped
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
			Auth:        info.Auth,
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
		logging.Debug("MCPServerReconciler", "Config change detected: url changed from %q to %q", url, desired.URL)
		return true
	}

	// Command change requires restart
	if cmd, ok := serviceData["command"].(string); ok && cmd != desired.Command {
		logging.Debug("MCPServerReconciler", "Config change detected: command changed from %q to %q", cmd, desired.Command)
		return true
	}

	// Type change requires restart
	// Handle both string and api.MCPServerType since the type in serviceData
	// may be stored as the concrete MCPServerType type
	if typ, ok := serviceData["type"].(string); ok {
		if typ != desired.Type {
			logging.Debug("MCPServerReconciler", "Config change detected: type changed from %q to %q", typ, desired.Type)
			return true
		}
	} else if typ, ok := serviceData["type"].(api.MCPServerType); ok {
		if string(typ) != desired.Type {
			logging.Debug("MCPServerReconciler", "Config change detected: type changed from %q to %q", typ, desired.Type)
			return true
		}
	}

	// Check if AutoStart changed from false to true
	if autoStart, ok := serviceData["autoStart"].(bool); ok {
		if !autoStart && desired.AutoStart {
			logging.Debug("MCPServerReconciler", "Config change detected: autoStart changed from false to true")
			return true
		}
	}

	// Args change requires restart
	if len(desired.Args) > 0 || serviceData["args"] != nil {
		existingArgs, ok := serviceData["args"].([]string)
		if !ok {
			// Type mismatch or nil, needs restart if desired has args
			if len(desired.Args) > 0 {
				logging.Debug("MCPServerReconciler", "Config change detected: args added: %v", desired.Args)
				return true
			}
		} else if !slices.Equal(existingArgs, desired.Args) {
			logging.Debug("MCPServerReconciler", "Config change detected: args changed from %v to %v", existingArgs, desired.Args)
			return true
		}
	}

	// Env change requires restart
	if !mapsEqual(serviceData["env"], desired.Env) {
		logging.Debug("MCPServerReconciler", "Config change detected: env changed")
		return true
	}

	// Headers change requires restart (don't log values as they may contain secrets)
	if !mapsEqual(serviceData["headers"], desired.Headers) {
		logging.Debug("MCPServerReconciler", "Config change detected: headers changed")
		return true
	}

	// Timeout change requires restart
	if changed, existing := intFieldChanged(serviceData, "timeout", desired.Timeout); changed {
		logging.Debug("MCPServerReconciler", "Config change detected: timeout changed from %d to %d", existing, desired.Timeout)
		return true
	}

	// ToolPrefix change requires restart
	if changed, existing := stringFieldChanged(serviceData, "toolPrefix", desired.ToolPrefix); changed {
		logging.Debug("MCPServerReconciler", "Config change detected: toolPrefix changed from %q to %q", existing, desired.ToolPrefix)
		return true
	}

	// Description change does NOT require restart (metadata only)
	// But we log it for debugging purposes
	if desc, ok := serviceData["description"].(string); ok {
		if desc != desired.Description {
			logging.Debug("MCPServerReconciler", "Description changed (no restart needed): %q -> %q", desc, desired.Description)
		}
	}

	// Auth change requires restart - authentication config affects server connectivity
	if changed, reason := authConfigChanged(serviceData["auth"], desired.Auth); changed {
		logging.Debug("MCPServerReconciler", "Config change detected: auth changed (%s)", reason)
		return true
	}

	return false
}

// authConfigChanged compares auth configurations and returns whether they differ
// along with a reason string for logging.
//
// This function uses reflect.DeepEqual for comparison to future-proof against
// new fields being added to auth structs. The performance cost is acceptable
// since this runs during reconciliation, not in hot paths.
//
// Note: The existing auth comes from serviceData which preserves the concrete
// *api.MCPServerAuth type since it's set directly from the service definition,
// not from JSON unmarshaling.
func authConfigChanged(existing interface{}, desired *api.MCPServerAuth) (changed bool, reason string) {
	// Both nil means equal
	if existing == nil && desired == nil {
		return false, ""
	}

	// Auth removed
	if existing != nil && desired == nil {
		return true, "auth removed"
	}

	// Auth added
	if existing == nil && desired != nil {
		return true, "auth added"
	}

	// Try to cast existing to *api.MCPServerAuth
	existingAuth, ok := existing.(*api.MCPServerAuth)
	if !ok {
		// If we can't cast, assume they're different (safer to restart)
		return true, "type mismatch"
	}

	// Quick check on type field for better logging
	if existingAuth.Type != desired.Type {
		return true, fmt.Sprintf("type changed from %q to %q", existingAuth.Type, desired.Type)
	}

	// Use reflect.DeepEqual for comprehensive comparison
	// This ensures we don't miss any fields when new ones are added
	if !reflect.DeepEqual(existingAuth, desired) {
		return true, "configuration differs"
	}

	return false, ""
}

// mapsEqual compares two maps for equality.
// It handles the case where serviceData contains interface{} values and
// treats nil and empty maps as equivalent.
//
// Note: This correctly handles nil maps since len(nil) == 0 in Go.
func mapsEqual(existing interface{}, desired map[string]string) bool {
	existingMap := toStringMap(existing)

	// Treat nil and empty as equivalent (len(nil) == 0 in Go)
	if len(existingMap) == 0 && len(desired) == 0 {
		return true
	}

	return maps.Equal(existingMap, desired)
}

// toStringMap converts an interface{} to map[string]string.
// It handles both map[string]string and map[string]interface{} (from JSON unmarshaling).
// Returns nil if the conversion is not possible.
func toStringMap(v interface{}) map[string]string {
	if v == nil {
		return nil
	}

	// Direct type match
	if m, ok := v.(map[string]string); ok {
		return m
	}

	// Handle map[string]interface{} which is common from JSON unmarshaling
	if m, ok := v.(map[string]interface{}); ok {
		result := make(map[string]string, len(m))
		for k, val := range m {
			if s, ok := val.(string); ok {
				result[k] = s
			} else {
				// Non-string value, can't convert cleanly
				return nil
			}
		}
		return result
	}

	return nil
}

// stringFieldChanged checks if a string field has changed between existing and desired values.
// It handles the case where the existing field may not exist in serviceData.
// Returns (changed, existingValue) for logging purposes.
func stringFieldChanged(serviceData map[string]interface{}, key string, desired string) (changed bool, existing string) {
	if existingVal, ok := serviceData[key].(string); ok {
		if existingVal != desired {
			return true, existingVal
		}
		return false, existingVal
	}
	// Field doesn't exist in serviceData - changed if desired is non-empty
	if desired != "" {
		return true, ""
	}
	return false, ""
}

// intFieldChanged checks if an int field has changed between existing and desired values.
// It handles the case where the existing field may not exist in serviceData.
// Returns (changed, existingValue) for logging purposes.
func intFieldChanged(serviceData map[string]interface{}, key string, desired int) (changed bool, existing int) {
	if existingVal, ok := serviceData[key].(int); ok {
		if existingVal != desired {
			return true, existingVal
		}
		return false, existingVal
	}
	// Field doesn't exist in serviceData - changed if desired is non-zero
	if desired != 0 {
		return true, 0
	}
	return false, 0
}
