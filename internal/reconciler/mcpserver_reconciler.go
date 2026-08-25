package reconciler

import (
	"context"
	"fmt"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/pkg/logging"
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

	// suspendedMu guards suspended.
	suspendedMu sync.Mutex

	// suspended tracks servers currently held down because spec.suspended is
	// true, so that the transition back to false is recognized as a resume and
	// the service is started again — including autoStart=false servers that
	// were running when they were suspended. Level-triggered "!suspended →
	// start" is not an option: it would also start servers stopped through the
	// imperative core_service_stop path, which still exists for filesystem
	// mode (issue #1057 switched the tools over only for caller writes).
	// ponytail: in-memory only — a suspend+resume that both happen while
	// muster is down degrades to autoStart semantics, same as any stopped
	// service across a restart.
	suspended map[string]bool
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
		suspended:        make(map[string]bool),
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

// ResyncNames implements ResyncLister: the union of all MCPServer definition
// names and all registered MCPServer service names. Including registry-only
// names lets resync heal a lost delete event (service running without a
// definition); definition names heal lost create/update events.
func (r *MCPServerReconciler) ResyncNames() []string {
	seen := make(map[string]struct{})
	for _, info := range r.mcpServerManager.ListMCPServers() {
		seen[info.Name] = struct{}{}
	}
	for _, svc := range r.serviceRegistry.GetAll() {
		if svc.GetType() == api.TypeMCPServer {
			seen[svc.GetName()] = struct{}{}
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	return names
}

// Reconcile processes a single MCPServer reconciliation request.
//
// After successful reconciliation, this returns RequeueAfter to enable periodic
// status sync. This ensures that runtime state changes (service crashes, health
// check failures, etc.) are eventually reflected in the CRD status even if
// state change events are missed.
func (r *MCPServerReconciler) Reconcile(ctx context.Context, req ReconcileRequest) ReconcileResult {
	logging.Info("MCPServerReconciler", "Reconciling MCPServer: %s", req.Name)

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
	var processedRestart *time.Time

	if mcpServerInfo.Suspended {
		result = r.reconcileSuspend(req, exists, existingService)
		// A restart requested while suspended is consumed without action:
		// suspension wins. Leaving it pending would fire a surprise restart
		// whenever the server is resumed later.
		if pending := pendingRestart(mcpServerInfo); pending != nil && result.Error == nil {
			logging.Info("MCPServerReconciler", "Ignoring restart request for suspended MCPServer %s", req.Name)
			processedRestart = pending
		}
	} else {
		// startedThisPass tracks whether resume/create/update already
		// (re)started the service in this pass, so a pending restart request
		// is consumed instead of bouncing the service a second time.
		var startedThisPass bool
		result, startedThisPass = r.reconcileResume(req, exists, existingService)

		if result.Error == nil && result.RequeueAfter == 0 {
			var started bool
			if !exists {
				// Service doesn't exist, create it
				result, started = r.reconcileCreate(ctx, req, mcpServerInfo)
			} else {
				// Service exists, check if update is needed
				result, started = r.reconcileUpdate(ctx, req, mcpServerInfo, existingService)
			}
			startedThisPass = startedThisPass || started
		}

		if result.Error == nil && result.RequeueAfter == 0 {
			if pending := pendingRestart(mcpServerInfo); pending != nil {
				if startedThisPass {
					logging.Info("MCPServerReconciler", "Consuming restart request for MCPServer %s: service was (re)started in this pass", req.Name)
					processedRestart = pending
				} else if restartResult := r.reconcileRestart(req, *pending); restartResult.Error != nil {
					result = restartResult
				} else {
					processedRestart = pending
				}
			}
		}
	}

	// Sync status back to CRD after reconciliation
	r.syncStatus(ctx, req.Name, req.Namespace, result.Error, processedRestart)

	// If reconciliation succeeded, schedule periodic requeue for status sync.
	// This implements the idiomatic Kubernetes controller pattern where status
	// is periodically refreshed to ensure eventual consistency. A shorter
	// requeue already requested by a step (e.g. resume waiting out a stop in
	// flight) is kept.
	if result.Error == nil && !result.Requeue && result.RequeueAfter == 0 {
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
func (r *MCPServerReconciler) syncStatus(ctx context.Context, name, namespace string, reconcileErr error, processedRestart *time.Time) {
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
		r.applyStatusFromService(server, name, reconcileErr, processedRestart)

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
func (r *MCPServerReconciler) applyStatusFromService(server *musterv1alpha1.MCPServer, name string, reconcileErr error, processedRestart *time.Time) {
	// Record the processed restart request so it is not re-processed on the
	// next reconcile (spec-is-desired / status-is-observed, issue #1055).
	if processedRestart != nil {
		t := metav1.NewTime(*processedRestart)
		server.Status.LastRestartedAt = &t
	}

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

// pendingRestart returns the spec.restartRequestedAt value if it has not been
// processed yet (differs from status.lastRestartedAt), nil otherwise.
func pendingRestart(info *api.MCPServerInfo) *time.Time {
	if info.RestartRequestedAt == nil {
		return nil
	}
	if info.LastRestartedAt != nil && info.LastRestartedAt.Equal(*info.RestartRequestedAt) {
		return nil
	}
	return info.RestartRequestedAt
}

func (r *MCPServerReconciler) markSuspended(name string) {
	r.suspendedMu.Lock()
	defer r.suspendedMu.Unlock()
	r.suspended[name] = true
}

func (r *MCPServerReconciler) clearSuspended(name string) {
	r.suspendedMu.Lock()
	defer r.suspendedMu.Unlock()
	delete(r.suspended, name)
}

func (r *MCPServerReconciler) isSuspended(name string) bool {
	r.suspendedMu.Lock()
	defer r.suspendedMu.Unlock()
	return r.suspended[name]
}

// reconcileSuspend drives a server with spec.suspended=true to stopped and
// records the suspension so a later spec.suspended=false is recognized as a
// resume. Create/update reconciliation is skipped entirely while suspended;
// config changes are picked up on resume by the following reconcile.
func (r *MCPServerReconciler) reconcileSuspend(req ReconcileRequest, exists bool, existingService api.ServiceInfo) ReconcileResult {
	// Mark first: even when nothing is running, resume must know this server
	// is held down by suspension rather than by autoStart=false.
	r.markSuspended(req.Name)

	if !exists {
		return ReconcileResult{}
	}

	state := existingService.GetState()
	if state == api.StateStopped || state == api.StateStopping {
		return ReconcileResult{}
	}

	logging.Info("MCPServerReconciler", "Suspending MCPServer service %s (spec.suspended=true)", req.Name)
	if err := r.orchestratorAPI.StopService(req.Name); err != nil {
		return ReconcileResult{
			Error:   fmt.Errorf("failed to suspend service: %w", err),
			Requeue: true,
		}
	}
	return ReconcileResult{}
}

// reconcileResume starts a service again after its spec.suspended flag went
// back to false. No-op for servers this reconciler never saw suspended, so
// servers stopped through the imperative core_service_stop path (still used
// in filesystem mode) stay stopped. The bool reports whether a start
// was performed in this pass.
func (r *MCPServerReconciler) reconcileResume(req ReconcileRequest, exists bool, existingService api.ServiceInfo) (ReconcileResult, bool) {
	if !r.isSuspended(req.Name) {
		return ReconcileResult{}, false
	}

	if exists && !api.IsDownState(existingService.GetState()) {
		if existingService.GetState() == api.StateStopping {
			// The suspend's stop is still completing; clearing the marker now
			// would turn this resume into a silent no-op once the stop lands.
			// Keep the marker and retry shortly.
			logging.Debug("MCPServerReconciler", "Resume of MCPServer %s waiting for in-flight stop to settle", req.Name)
			return ReconcileResult{RequeueAfter: 2 * time.Second}, false
		}
		// Already running or on its way up.
		r.clearSuspended(req.Name)
		return ReconcileResult{}, false
	}

	logging.Info("MCPServerReconciler", "Resuming MCPServer service %s (spec.suspended=false)", req.Name)
	if err := r.orchestratorAPI.StartService(req.Name); err != nil {
		if api.IsAuthRequiredError(err) {
			// Auth Required is a stable state, not a failure (see reconcileCreate).
			// The start was attempted — a pending restart would hit the same wall.
			r.clearSuspended(req.Name)
			return ReconcileResult{}, true
		}
		return ReconcileResult{
			Error:   fmt.Errorf("failed to resume service: %w", err),
			Requeue: true,
		}, false
	}
	r.clearSuspended(req.Name)
	return ReconcileResult{}, true
}

// reconcileRestart performs the one-shot restart requested via
// spec.restartRequestedAt. The caller records the processed value in
// status.lastRestartedAt only when this returns without error, so failed
// restarts are retried and successful ones are never repeated.
func (r *MCPServerReconciler) reconcileRestart(req ReconcileRequest, requestedAt time.Time) ReconcileResult {
	if _, exists := r.serviceRegistry.Get(req.Name); !exists {
		// No service registered (autoStart=false and never started). A restart
		// request is an explicit "make it run now" — the CR-driven
		// core_service_start writes it for exactly this case (issue #1057) —
		// so start the service; StartService registers definitions lazily
		// (issue #680).
		logging.Info("MCPServerReconciler", "Starting unregistered MCPServer service %s (restartRequestedAt=%s)", req.Name, requestedAt.Format(time.RFC3339))
		if err := r.orchestratorAPI.StartService(req.Name); err != nil {
			if api.IsAuthRequiredError(err) {
				logging.Info("MCPServerReconciler", "MCPServer %s requires authentication after requested start", req.Name)
				return ReconcileResult{}
			}
			return ReconcileResult{
				Error:   fmt.Errorf("failed to start service for restart request: %w", err),
				Requeue: true,
			}
		}
		return ReconcileResult{}
	}

	logging.Info("MCPServerReconciler", "Restarting MCPServer service %s (restartRequestedAt=%s)", req.Name, requestedAt.Format(time.RFC3339))
	if err := r.orchestratorAPI.RestartService(req.Name); err != nil {
		if api.IsAuthRequiredError(err) {
			logging.Info("MCPServerReconciler", "MCPServer %s requires authentication after requested restart", req.Name)
			return ReconcileResult{}
		}
		return ReconcileResult{
			Error:   fmt.Errorf("failed to restart service: %w", err),
			Requeue: true,
		}
	}
	return ReconcileResult{}
}

// reconcileCreate handles creating a new MCPServer service. The bool reports
// whether a start was performed (or attempted up to Auth Required) in this pass.
func (r *MCPServerReconciler) reconcileCreate(ctx context.Context, req ReconcileRequest, info *api.MCPServerInfo) (ReconcileResult, bool) {
	logging.Info("MCPServerReconciler", "Creating MCPServer service: %s", req.Name)

	// Only create if AutoStart is enabled
	if !info.AutoStart {
		logging.Debug("MCPServerReconciler", "Skipping MCPServer %s: AutoStart=false", req.Name)
		return ReconcileResult{}, false
	}

	// Start the service via orchestrator
	if err := r.orchestratorAPI.StartService(req.Name); err != nil {
		// Auth Required is a stable state, not a failure. The service is registered
		// and will be activated via SSO when a user authenticates.
		if api.IsAuthRequiredError(err) {
			logging.Info("MCPServerReconciler", "MCPServer %s requires authentication (Auth Required)", req.Name)
			return ReconcileResult{}, true
		}
		logging.Debug("MCPServerReconciler", "Failed to start service %s: %v", req.Name, err)
		return ReconcileResult{
			Error:   fmt.Errorf("failed to start service: %w", err),
			Requeue: true,
		}, false
	}

	logging.Info("MCPServerReconciler", "Successfully created MCPServer service: %s", req.Name)
	return ReconcileResult{}, true
}

// reconcileUpdate handles updating an existing MCPServer service. The bool
// reports whether a restart was performed (or attempted up to Auth Required)
// in this pass.
func (r *MCPServerReconciler) reconcileUpdate(ctx context.Context, req ReconcileRequest, info *api.MCPServerInfo, existingService api.ServiceInfo) (ReconcileResult, bool) {
	logging.Debug("MCPServerReconciler", "Checking MCPServer service for updates: %s", req.Name)

	newConfig := infoToMCPServer(info)

	configurableService, ok := existingService.(api.ConfigurableService)
	if !ok {
		logging.Debug("MCPServerReconciler", "Service %s does not implement ConfigurableService, skipping update", req.Name)
		return ReconcileResult{}, false
	}

	if !configurableService.ConfigurationChanged(newConfig) {
		logging.Debug("MCPServerReconciler", "MCPServer %s is up to date", req.Name)
		return ReconcileResult{}, false
	}

	logging.Info("MCPServerReconciler", "MCPServer %s configuration changed, updating and restarting", req.Name)

	if err := configurableService.UpdateConfiguration(newConfig); err != nil {
		return ReconcileResult{
			Error:   fmt.Errorf("failed to update service configuration: %w", err),
			Requeue: true,
		}, false
	}
	logging.Debug("MCPServerReconciler", "Updated configuration for MCPServer %s", req.Name)

	if err := r.orchestratorAPI.RestartService(req.Name); err != nil {
		if api.IsAuthRequiredError(err) {
			logging.Info("MCPServerReconciler", "MCPServer %s requires authentication after config update", req.Name)
			return ReconcileResult{}, true
		}
		return ReconcileResult{
			Error:   fmt.Errorf("failed to restart service: %w", err),
			Requeue: true,
		}, false
	}

	logging.Info("MCPServerReconciler", "Successfully updated MCPServer service: %s", req.Name)
	return ReconcileResult{}, true
}

// infoToMCPServer converts an MCPServerInfo (API/reconciler view) to an MCPServer
// (service-layer configuration struct).
func infoToMCPServer(info *api.MCPServerInfo) *api.MCPServer {
	return &api.MCPServer{
		Name:        info.Name,
		Type:        api.MCPServerType(info.Type),
		Description: info.Description,
		ToolPrefix:  info.ToolPrefix,
		Family:      info.Family,
		AutoStart:   info.AutoStart,
		Command:     info.Command,
		Args:        info.Args,
		URL:         info.URL,
		Env:         info.Env,
		Headers:     info.Headers,
		Timeout:     info.Timeout,
		Auth:        info.Auth,
	}
}

// reconcileDelete handles deleting an MCPServer service.
func (r *MCPServerReconciler) reconcileDelete(ctx context.Context, req ReconcileRequest) ReconcileResult {
	logging.Info("MCPServerReconciler", "Deleting MCPServer service: %s", req.Name)

	r.clearSuspended(req.Name)

	// Check if service exists
	_, exists := r.serviceRegistry.Get(req.Name)
	if !exists {
		logging.Debug("MCPServerReconciler", "MCPServer service %s already deleted", req.Name)
		return ReconcileResult{}
	}

	// Stop the service and remove it from the registry, so a later re-create
	// with the same name is reconciled as a fresh service instead of matching
	// the stale registry entry (which would leave it stopped forever).
	if err := r.orchestratorAPI.RemoveService(req.Name); err != nil {
		// If service not found, it's already removed
		if IsNotFoundError(err) {
			return ReconcileResult{}
		}
		return ReconcileResult{
			Error:   fmt.Errorf("failed to remove service: %w", err),
			Requeue: true,
		}
	}

	logging.Info("MCPServerReconciler", "Successfully deleted MCPServer service: %s", req.Name)
	return ReconcileResult{}
}
