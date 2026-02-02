package reconciler

import (
	"context"
	"fmt"
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
//   - Connected: TCP connection established (may still require auth)
//   - Connecting: Attempting to establish connection
//   - Disconnected: Not connected
//   - Failed: Endpoint unreachable
//
// Note: auth_required means the server IS reachable (returned 401), so that's Connected state.
// Per-user authentication state is tracked in the Session Registry, not in CRD State.
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
		// This is infrastructure Connected state - the auth status is per-user session state
		if isRemote {
			return musterv1alpha1.MCPServerStateConnected
		}
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
		logging.Debug("MCPServerReconciler", "Config change detected: url changed")
		return true
	}

	// Command change requires restart
	if cmd, ok := serviceData["command"].(string); ok && cmd != desired.Command {
		logging.Debug("MCPServerReconciler", "Config change detected: command changed")
		return true
	}

	// Type change requires restart
	// Handle both string and api.MCPServerType since the type in serviceData
	// may be stored as the concrete MCPServerType type
	if typ, ok := serviceData["type"].(string); ok {
		if typ != desired.Type {
			logging.Debug("MCPServerReconciler", "Config change detected: type changed")
			return true
		}
	} else if typ, ok := serviceData["type"].(api.MCPServerType); ok {
		if string(typ) != desired.Type {
			logging.Debug("MCPServerReconciler", "Config change detected: type changed")
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
				logging.Debug("MCPServerReconciler", "Config change detected: args added")
				return true
			}
		} else if !slices.Equal(existingArgs, desired.Args) {
			logging.Debug("MCPServerReconciler", "Config change detected: args changed")
			return true
		}
	}

	// Env change requires restart
	if !mapsEqual(serviceData["env"], desired.Env) {
		logging.Debug("MCPServerReconciler", "Config change detected: env changed")
		return true
	}

	// Headers change requires restart
	if !mapsEqual(serviceData["headers"], desired.Headers) {
		logging.Debug("MCPServerReconciler", "Config change detected: headers changed")
		return true
	}

	// Timeout change requires restart
	if timeout, ok := serviceData["timeout"].(int); ok {
		if timeout != desired.Timeout {
			logging.Debug("MCPServerReconciler", "Config change detected: timeout changed")
			return true
		}
	} else if desired.Timeout != 0 {
		// No existing timeout but desired has one
		logging.Debug("MCPServerReconciler", "Config change detected: timeout added")
		return true
	}

	// ToolPrefix change requires restart
	if toolPrefix, ok := serviceData["toolPrefix"].(string); ok {
		if toolPrefix != desired.ToolPrefix {
			logging.Debug("MCPServerReconciler", "Config change detected: toolPrefix changed")
			return true
		}
	} else if desired.ToolPrefix != "" {
		// No existing toolPrefix but desired has one
		logging.Debug("MCPServerReconciler", "Config change detected: toolPrefix added")
		return true
	}

	// Description change does NOT require restart (metadata only)
	// But we log it for debugging purposes
	if desc, ok := serviceData["description"].(string); ok {
		if desc != desired.Description {
			logging.Debug("MCPServerReconciler", "Description changed (no restart needed)")
		}
	}

	// Auth change requires restart - authentication config affects server connectivity
	if !authConfigEqual(serviceData["auth"], desired.Auth) {
		logging.Debug("MCPServerReconciler", "Config change detected: auth changed")
		return true
	}

	return false
}

// authConfigEqual compares auth configurations for equality.
// It handles the case where existing auth comes from serviceData as interface{}.
func authConfigEqual(existing interface{}, desired *api.MCPServerAuth) bool {
	// Both nil means equal
	if existing == nil && desired == nil {
		return true
	}
	// One nil, one not means not equal
	if existing == nil || desired == nil {
		return false
	}

	// Try to cast existing to *api.MCPServerAuth
	existingAuth, ok := existing.(*api.MCPServerAuth)
	if !ok {
		// If we can't cast, assume they're different (safer to restart)
		return false
	}

	// Compare Type
	if existingAuth.Type != desired.Type {
		return false
	}

	// Compare ForwardToken
	if existingAuth.ForwardToken != desired.ForwardToken {
		return false
	}

	// Compare RequiredAudiences
	if !slices.Equal(existingAuth.RequiredAudiences, desired.RequiredAudiences) {
		return false
	}

	// Compare TokenExchange
	if !tokenExchangeEqual(existingAuth.TokenExchange, desired.TokenExchange) {
		return false
	}

	// Compare Teleport
	if !teleportAuthEqual(existingAuth.Teleport, desired.Teleport) {
		return false
	}

	return true
}

// tokenExchangeEqual compares TokenExchangeConfig for equality.
func tokenExchangeEqual(existing, desired *api.TokenExchangeConfig) bool {
	if existing == nil && desired == nil {
		return true
	}
	if existing == nil || desired == nil {
		return false
	}

	if existing.Enabled != desired.Enabled {
		return false
	}
	if existing.DexTokenEndpoint != desired.DexTokenEndpoint {
		return false
	}
	if existing.ExpectedIssuer != desired.ExpectedIssuer {
		return false
	}
	if existing.ConnectorID != desired.ConnectorID {
		return false
	}
	if existing.Scopes != desired.Scopes {
		return false
	}
	// Note: We don't compare ClientCredentialsSecretRef as secret refs
	// don't change the auth behavior, only where credentials come from
	return true
}

// teleportAuthEqual compares TeleportAuth for equality.
func teleportAuthEqual(existing, desired *api.TeleportAuth) bool {
	if existing == nil && desired == nil {
		return true
	}
	if existing == nil || desired == nil {
		return false
	}

	if existing.IdentityDir != desired.IdentityDir {
		return false
	}
	if existing.IdentitySecretName != desired.IdentitySecretName {
		return false
	}
	if existing.IdentitySecretNamespace != desired.IdentitySecretNamespace {
		return false
	}
	if existing.AppName != desired.AppName {
		return false
	}
	return true
}

// mapsEqual compares two maps for equality.
// It handles the case where serviceData contains interface{} values.
func mapsEqual(existing interface{}, desired map[string]string) bool {
	if existing == nil && len(desired) == 0 {
		return true
	}
	if existing == nil {
		return false
	}

	existingMap, ok := existing.(map[string]string)
	if !ok {
		// Try map[string]interface{} which is common from JSON unmarshaling
		if existingIface, ok := existing.(map[string]interface{}); ok {
			if len(existingIface) != len(desired) {
				return false
			}
			for k, v := range desired {
				if ev, exists := existingIface[k]; !exists {
					return false
				} else if evStr, ok := ev.(string); !ok || evStr != v {
					return false
				}
			}
			return true
		}
		return false
	}

	if len(existingMap) != len(desired) {
		return false
	}
	for k, v := range desired {
		if ev, exists := existingMap[k]; !exists || ev != v {
			return false
		}
	}
	return true
}
