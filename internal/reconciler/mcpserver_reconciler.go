package reconciler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/reconciler/agentgateway"
	"github.com/giantswarm/muster/internal/reconciler/agentgateway/translate"
	"github.com/giantswarm/muster/pkg/logging"
)

// ConditionTypeNotSupportedInCluster is the MCPServer status condition the
// reconciler raises when a Backend cannot be emitted in cluster mode (today,
// stdio MCPServers).
const ConditionTypeNotSupportedInCluster = "NotSupportedInCluster"

const mcpServerKind = "MCPServer"

// mcpServerAPIVersion is derived from the canonical GroupVersion on the
// muster API package so a bump there propagates here.
var mcpServerAPIVersion = musterv1alpha1.GroupVersion.String()

const (
	reasonStdioInClusterMode = "StdioInClusterMode"
	reasonSuspended          = "Suspended"
)

// MCPServerManager is an interface for accessing MCPServer definitions.
type MCPServerManager interface {
	ListMCPServers() []api.MCPServerInfo
	GetMCPServer(name string) (*api.MCPServerInfo, error)
}

// ApplierFunc resolves the agentgateway.Applier to use for one Reconcile.
// Filesystem mode closes over a long-lived yaml.Applier; cluster mode
// constructs a fresh k8s.Applier with a per-MCPServer ownerRef. The reconciler
// is mode-agnostic and only sees this closure.
type ApplierFunc func(ctx context.Context, name, namespace string) agentgateway.Applier

// MCPServerReconciler reconciles MCPServer resources by emitting the
// agentgateway config stack and federating muster's aggregator to dial
// agentgateway for the named MCPServer.
//
// Each Reconcile:
//   - calls agentgateway.NewConfig + applier.Apply via the wired ApplierFunc,
//   - on a clean apply with AutoStart=true, calls
//     api.GetAggregator().RegisterUpstream so the aggregator opens its
//     federated connection to <UpstreamProxy>/mcp/<name>.
//
// reconcileDelete deregisters the upstream and calls Delete on the same
// applier the create path would use.
type MCPServerReconciler struct {
	BaseStatusConfig

	mcpServerManager MCPServerManager

	applierFn ApplierFunc
}

// NewMCPServerReconciler wires the reconciler with a per-request ApplierFunc.
// Filesystem-mode callers pass a closure returning the same long-lived
// yaml.Applier; cluster-mode callers pass a closure that constructs a fresh
// k8s.Applier with the MCPServer's ownerRef bound for cascade deletion.
func NewMCPServerReconciler(
	mcpServerManager MCPServerManager,
	applierFn ApplierFunc,
) *MCPServerReconciler {
	if applierFn == nil {
		panic("reconciler: NewMCPServerReconciler requires a non-nil ApplierFunc")
	}
	return &MCPServerReconciler{
		BaseStatusConfig: BaseStatusConfig{Namespace: DefaultNamespace},
		mcpServerManager: mcpServerManager,
		applierFn:        applierFn,
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
func (r *MCPServerReconciler) Reconcile(ctx context.Context, req ReconcileRequest) ReconcileResult {
	logging.Debug("MCPServerReconciler", "Reconciling MCPServer: %s", req.Name)

	mcpServerInfo, err := r.mcpServerManager.GetMCPServer(req.Name)
	if err != nil {
		if IsNotFoundError(err) {
			return r.reconcileDelete(ctx, req)
		}
		return ReconcileResult{
			Error:   fmt.Errorf("failed to get MCPServer definition: %w", err),
			Requeue: true,
		}
	}

	if isSuspended(mcpServerInfo) {
		return r.reconcileSuspended(ctx, req, mcpServerInfo)
	}

	if result, stop := r.applyConfig(ctx, req, mcpServerInfo); stop {
		return result
	}

	if mcpServerInfo.AutoStart {
		if err := r.registerUpstream(ctx, req.Name); err != nil {
			logging.Debug("MCPServerReconciler", "RegisterUpstream for %s failed: %v", req.Name, err)
			r.syncStatus(ctx, req.Name, req.Namespace, mcpServerInfo.Type, err)
			return ReconcileResult{
				Error:   fmt.Errorf("register upstream: %w", err),
				Requeue: true,
			}
		}
	}

	r.clearSuspendedCondition(ctx, req.Name, req.Namespace)
	r.syncStatus(ctx, req.Name, req.Namespace, mcpServerInfo.Type, nil)
	return ReconcileResult{RequeueAfter: DefaultStatusSyncInterval}
}

// isSuspended returns true when the MCPServer.spec.suspended pointer is set
// to true. nil and *false both mean "reconcile normally".
func isSuspended(info *api.MCPServerInfo) bool {
	return info != nil && info.Suspended != nil && *info.Suspended
}

// reconcileSuspended tears down the agentgateway config for the MCPServer
// and deregisters the aggregator upstream, then sets the Suspended status
// condition. A subsequent reconcile with spec.suspended=false re-emits the
// config and re-registers the upstream through the normal Reconcile path.
func (r *MCPServerReconciler) reconcileSuspended(ctx context.Context, req ReconcileRequest, info *api.MCPServerInfo) ReconcileResult {
	logging.Info("MCPServerReconciler", "MCPServer %s suspended; tearing down agentgateway config and deregistering upstream", req.Name)

	namespace := r.GetNamespace(req.Namespace)
	applier := r.applierFn(ctx, req.Name, namespace)
	if err := applier.Delete(ctx, req.Name); err != nil {
		logging.Debug("MCPServerReconciler", "Applier.Delete for suspended MCPServer %s failed: %v", req.Name, err)
		return ReconcileResult{
			Error:   fmt.Errorf("suspend delete: %w", err),
			Requeue: true,
		}
	}

	if err := r.deregisterUpstream(ctx, req.Name); err != nil {
		logging.Debug("MCPServerReconciler", "DeregisterUpstream for suspended MCPServer %s failed: %v", req.Name, err)
	}

	r.setSuspendedCondition(ctx, req.Name, req.Namespace, info.Type)
	return ReconcileResult{RequeueAfter: DefaultStatusSyncInterval}
}

// applyConfig compiles the MCPServer spec into an agentgateway.Config and
// hands it to the Applier appropriate for the current mode.
func (r *MCPServerReconciler) applyConfig(ctx context.Context, req ReconcileRequest, info *api.MCPServerInfo) (ReconcileResult, bool) {
	spec := translate.InfoToMCPServerSpec(info)
	namespace := r.GetNamespace(req.Namespace)
	config, err := agentgateway.NewConfig(req.Name, namespace, spec)
	if err != nil {
		logging.Debug("MCPServerReconciler", "NewConfig failed for MCPServer %s: %v", req.Name, err)
		r.syncStatus(ctx, req.Name, req.Namespace, info.Type, err)
		return ReconcileResult{Error: fmt.Errorf("agentgateway: %w", err)}, true
	}

	applier := r.applierFn(ctx, req.Name, namespace)

	if err := applier.Apply(ctx, config); err != nil {
		if errors.Is(err, agentgateway.ErrUnsupportedTransport) {
			logging.Info("MCPServerReconciler", "MCPServer %s uses stdio; cluster mode does not support it yet — marking NotSupportedInCluster", req.Name)
			r.setNotSupportedInClusterCondition(ctx, req.Name, req.Namespace, err)
			return ReconcileResult{RequeueAfter: DefaultStatusSyncInterval}, true
		}
		logging.Debug("MCPServerReconciler", "Apply failed for MCPServer %s: %v", req.Name, err)
		r.syncStatus(ctx, req.Name, req.Namespace, info.Type, err)
		return ReconcileResult{
			Error:   fmt.Errorf("apply config: %w", err),
			Requeue: true,
		}, true
	}

	r.clearNotSupportedInClusterCondition(ctx, req.Name, req.Namespace)
	return ReconcileResult{}, false
}

// registerUpstream calls api.GetAggregator().RegisterUpstream. It's a no-op
// when the aggregator handler is not yet registered (boot-order edge cases:
// the reconcile manager starts after the aggregator in runOrchestrator, so
// in production this should not happen, but defensive nil-check keeps tests
// that skip aggregator wiring buildable).
func (r *MCPServerReconciler) registerUpstream(ctx context.Context, name string) error {
	agg := api.GetAggregator()
	if agg == nil {
		logging.Debug("MCPServerReconciler", "Aggregator handler not registered; skipping RegisterUpstream for %s", name)
		return nil
	}
	return agg.RegisterUpstream(ctx, name)
}

// deregisterUpstream is the symmetric DeregisterUpstream call.
func (r *MCPServerReconciler) deregisterUpstream(ctx context.Context, name string) error {
	agg := api.GetAggregator()
	if agg == nil {
		return nil
	}
	return agg.DeregisterUpstream(ctx, name)
}

// OwnerRefFor builds the metav1.OwnerReference for an MCPServer reconcile
// request. When getter is non-nil, UID + APIVersion + Kind are read from the
// live CRD — required because the K8s applier rejects empty UID. Callers that
// don't need cascade deletion (filesystem mode, tests without a client) pass
// nil getter and receive a UID-less skeleton.
func OwnerRefFor(ctx context.Context, getter StatusUpdater, name, namespace string) metav1.OwnerReference {
	ref := metav1.OwnerReference{
		APIVersion:         mcpServerAPIVersion,
		Kind:               mcpServerKind,
		Name:               name,
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}
	if getter == nil {
		return ref
	}
	server, err := getter.GetMCPServer(ctx, name, namespace)
	if err != nil || server == nil {
		return ref
	}
	if server.APIVersion != "" {
		ref.APIVersion = server.APIVersion
	}
	if server.Kind != "" {
		ref.Kind = server.Kind
	}
	ref.UID = server.UID
	return ref
}

func (r *MCPServerReconciler) setNotSupportedInClusterCondition(ctx context.Context, name, namespace string, cause error) {
	cond := metav1.Condition{
		Type:    ConditionTypeNotSupportedInCluster,
		Status:  metav1.ConditionTrue,
		Reason:  reasonStdioInClusterMode,
		Message: cause.Error(),
	}
	r.mutateMCPServerStatus(ctx, name, namespace, "set NotSupportedInCluster", func(server *musterv1alpha1.MCPServer) bool {
		changed := meta.SetStatusCondition(&server.Status.Conditions, cond)
		sanitized := SanitizeErrorMessage(cause.Error())
		if server.Status.LastError != sanitized {
			server.Status.LastError = sanitized
			changed = true
		}
		return changed
	})
}

func (r *MCPServerReconciler) clearNotSupportedInClusterCondition(ctx context.Context, name, namespace string) {
	r.mutateMCPServerStatus(ctx, name, namespace, "clear NotSupportedInCluster", func(server *musterv1alpha1.MCPServer) bool {
		return meta.RemoveStatusCondition(&server.Status.Conditions, ConditionTypeNotSupportedInCluster)
	})
}

// setSuspendedCondition marks the MCPServer as suspended in CRD status and
// drives State to the appropriate terminal value (Stopped for stdio,
// Disconnected for remote). LastError is cleared because suspend is
// intentional, not a failure.
func (r *MCPServerReconciler) setSuspendedCondition(ctx context.Context, name, namespace, serverType string) {
	cond := metav1.Condition{
		Type:    musterv1alpha1.ConditionTypeSuspended,
		Status:  metav1.ConditionTrue,
		Reason:  reasonSuspended,
		Message: "spec.suspended is true; agentgateway config removed and aggregator upstream deregistered",
	}
	r.mutateMCPServerStatus(ctx, name, namespace, "set Suspended", func(server *musterv1alpha1.MCPServer) bool {
		changed := meta.SetStatusCondition(&server.Status.Conditions, cond)
		effectiveType := serverType
		if effectiveType == "" {
			effectiveType = server.Spec.Type
		}
		var target musterv1alpha1.MCPServerStateValue
		if effectiveType == "streamable-http" || effectiveType == "sse" {
			target = musterv1alpha1.MCPServerStateDisconnected
		} else {
			target = musterv1alpha1.MCPServerStateStopped
		}
		if server.Status.State != target {
			server.Status.State = target
			changed = true
		}
		if server.Status.LastError != "" {
			server.Status.LastError = ""
			changed = true
		}
		return changed
	})
}

// clearSuspendedCondition removes the Suspended condition on the next
// non-suspended reconcile.
func (r *MCPServerReconciler) clearSuspendedCondition(ctx context.Context, name, namespace string) {
	r.mutateMCPServerStatus(ctx, name, namespace, "clear Suspended", func(server *musterv1alpha1.MCPServer) bool {
		return meta.RemoveStatusCondition(&server.Status.Conditions, musterv1alpha1.ConditionTypeSuspended)
	})
}

func (r *MCPServerReconciler) mutateMCPServerStatus(ctx context.Context, name, namespace, op string, mutate func(*musterv1alpha1.MCPServer) bool) {
	if r.StatusUpdater == nil {
		return
	}
	ns := r.GetNamespace(namespace)
	helper := NewStatusSyncHelper(ResourceTypeMCPServer, name, "MCPServerReconciler")
	helper.RecordAttempt()

	var lastErr error
	retryErr := retry.OnError(StatusSyncRetryBackoff, IsConflictError, func() error {
		server, err := r.StatusUpdater.GetMCPServer(ctx, name, ns)
		if err != nil {
			lastErr = err
			return nil
		}
		if !mutate(server) {
			lastErr = nil
			return nil
		}
		if err := r.StatusUpdater.UpdateMCPServerStatus(ctx, server); err != nil {
			lastErr = err
			return err
		}
		lastErr = nil
		return nil
	})

	helper.HandleResult(retryErr, lastErr)
	if helper.WasSuccessful(retryErr, lastErr) {
		logging.Debug("MCPServerReconciler", "%s for MCPServer %s", op, name)
	}
}

func (r *MCPServerReconciler) syncStatus(ctx context.Context, name, namespace, serverType string, reconcileErr error) {
	if r.StatusUpdater == nil {
		return
	}

	namespace = r.GetNamespace(namespace)

	helper := NewStatusSyncHelper(ResourceTypeMCPServer, name, "MCPServerReconciler")
	helper.RecordAttempt()

	var lastErr error
	retryErr := retry.OnError(StatusSyncRetryBackoff, IsConflictError, func() error {
		server, err := r.StatusUpdater.GetMCPServer(ctx, name, namespace)
		if err != nil {
			lastErr = err
			return nil
		}

		r.applyStatusFromAggregator(server, name, serverType, reconcileErr)

		if err := r.StatusUpdater.UpdateMCPServerStatus(ctx, server); err != nil {
			lastErr = err
			return err
		}
		lastErr = nil
		return nil
	})

	helper.HandleResult(retryErr, lastErr)
	if helper.WasSuccessful(retryErr, lastErr) {
		logging.Debug("MCPServerReconciler", "Synced MCPServer %s status", name)
	}
}

// applyStatusFromAggregator maps the aggregator's view of an upstream
// MCPServer onto the CRD status. Connected upstreams set State=Connected
// (or =Running for stdio in filesystem mode) and stamp LastConnected.
// Absent upstreams fall back to Disconnected (remote) or Stopped (stdio).
// MCPServer.status.consecutiveFailures / lastAttempt / nextRetryAfter are no
// longer updated — the per-service retry state machine was removed in PR 11;
// the fields stay on the CRD for forward compatibility.
func (r *MCPServerReconciler) applyStatusFromAggregator(server *musterv1alpha1.MCPServer, name, serverType string, reconcileErr error) {
	if serverType == "" {
		serverType = server.Spec.Type
	}
	isRemote := serverType == "streamable-http" || serverType == "sse"
	state := upstreamState(name)

	switch state {
	case api.UpstreamServerConnected:
		if isRemote {
			server.Status.State = musterv1alpha1.MCPServerStateConnected
		} else {
			server.Status.State = musterv1alpha1.MCPServerStateRunning
		}
		now := metav1.NewTime(time.Now())
		server.Status.LastConnected = &now
		server.Status.LastError = ""
	case api.UpstreamServerAuthRequired:
		if isRemote {
			server.Status.State = musterv1alpha1.MCPServerStateAuthRequired
		} else {
			server.Status.State = musterv1alpha1.MCPServerStateRunning
		}
		server.Status.LastError = ""
	default:
		if isRemote {
			server.Status.State = musterv1alpha1.MCPServerStateDisconnected
		} else {
			server.Status.State = musterv1alpha1.MCPServerStateStopped
		}
		if reconcileErr != nil {
			server.Status.LastError = SanitizeErrorMessage(reconcileErr.Error())
		}
	}
}

// upstreamState is a tiny indirection around api.GetAggregator that returns
// Absent when the aggregator handler is not yet registered (test setups,
// boot-order races). Production code reaches the production aggregator.
func upstreamState(name string) api.UpstreamServerState {
	agg := api.GetAggregator()
	if agg == nil {
		return api.UpstreamServerAbsent
	}
	return agg.UpstreamServerState(name)
}

// reconcileDelete handles deleting an MCPServer service. It deregisters the
// federated upstream and asks the applier to drop its persisted state. The
// K8s applier's Delete is a no-op — OwnerReferences cascade.
func (r *MCPServerReconciler) reconcileDelete(ctx context.Context, req ReconcileRequest) ReconcileResult {
	logging.Info("MCPServerReconciler", "Deleting MCPServer service: %s", req.Name)

	if err := r.deregisterUpstream(ctx, req.Name); err != nil {
		logging.Debug("MCPServerReconciler", "DeregisterUpstream for %s failed: %v", req.Name, err)
	}

	applier := r.applierFn(ctx, req.Name, r.GetNamespace(req.Namespace))
	if err := applier.Delete(ctx, req.Name); err != nil {
		logging.Debug("MCPServerReconciler", "Applier.Delete for %s failed: %v", req.Name, err)
		return ReconcileResult{
			Error:   fmt.Errorf("delete config: %w", err),
			Requeue: true,
		}
	}

	logging.Info("MCPServerReconciler", "Successfully deleted MCPServer service: %s", req.Name)
	return ReconcileResult{}
}
