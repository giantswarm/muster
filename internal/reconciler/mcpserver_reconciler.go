package reconciler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/reconciler/agentgateway"
	k8sapply "github.com/giantswarm/muster/internal/reconciler/agentgateway/k8s"
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

const reasonStdioInClusterMode = "StdioInClusterMode"

// MCPServerManager is an interface for accessing MCPServer definitions.
type MCPServerManager interface {
	ListMCPServers() []api.MCPServerInfo
	GetMCPServer(name string) (*api.MCPServerInfo, error)
}

// MCPServerReconciler reconciles MCPServer resources by emitting the
// agentgateway config stack and federating muster's aggregator to dial
// agentgateway for the named MCPServer.
//
// Each Reconcile:
//   - calls agentgateway.NewConfig + applier.Apply (per-mode K8s or YAML),
//   - on a clean apply with AutoStart=true, calls
//     api.GetAggregator().RegisterUpstream so the aggregator opens its
//     federated connection to <UpstreamProxy>/mcp/<name>.
//
// reconcileDelete deregisters the upstream and clears persisted YAML.
type MCPServerReconciler struct {
	BaseStatusConfig

	mcpServerManager MCPServerManager

	yamlApplier agentgateway.Applier

	k8sClient     ctrlclient.Client
	k8sApplierCfg k8sapply.Config

	deleter agentgateway.Deleter

	// ownerRefs caches OwnerReferences resolved from the live MCPServer in
	// cluster mode so periodic status-sync requeues don't re-fetch and
	// re-build the K8s Applier every reconcile.
	ownerRefMu sync.RWMutex
	ownerRefs  map[types.NamespacedName]metav1.OwnerReference
}

// NewMCPServerReconcilerFilesystem builds a reconciler wired to the
// long-lived yaml Applier used in filesystem mode. yamlApplier and deleter
// are typically the same instance (yaml.Applier satisfies both ports).
func NewMCPServerReconcilerFilesystem(
	mcpServerManager MCPServerManager,
	yamlApplier agentgateway.Applier,
	deleter agentgateway.Deleter,
) *MCPServerReconciler {
	if mcpServerManager == nil {
		panic("reconciler: NewMCPServerReconcilerFilesystem requires a non-nil MCPServerManager")
	}
	if yamlApplier == nil {
		panic("reconciler: NewMCPServerReconcilerFilesystem requires a non-nil yaml Applier")
	}
	return &MCPServerReconciler{
		BaseStatusConfig: BaseStatusConfig{Namespace: DefaultNamespace},
		mcpServerManager: mcpServerManager,
		yamlApplier:      yamlApplier,
		deleter:          deleter,
	}
}

// NewMCPServerReconcilerCluster builds a reconciler that constructs a fresh
// k8s.Applier per Reconcile. ownerRef is bound on each construction so emitted
// objects cascade-delete with the MCPServer; cleanup is handled by Kubernetes
// garbage collection, so no Deleter is needed.
//
// statusUpdater is required: the K8s Applier rejects empty UID, so
// resolveOwnerRef MUST be able to fetch the live MCPServer.
func NewMCPServerReconcilerCluster(
	mcpServerManager MCPServerManager,
	k8sClient ctrlclient.Client,
	k8sApplierCfg k8sapply.Config,
	statusUpdater StatusUpdater,
	namespace string,
) *MCPServerReconciler {
	if mcpServerManager == nil {
		panic("reconciler: NewMCPServerReconcilerCluster requires a non-nil MCPServerManager")
	}
	if k8sClient == nil {
		panic("reconciler: NewMCPServerReconcilerCluster requires a non-nil client")
	}
	if statusUpdater == nil {
		panic("reconciler: NewMCPServerReconcilerCluster requires a non-nil StatusUpdater")
	}
	r := &MCPServerReconciler{
		BaseStatusConfig: BaseStatusConfig{Namespace: DefaultNamespace},
		mcpServerManager: mcpServerManager,
		k8sClient:        k8sClient,
		k8sApplierCfg:    k8sApplierCfg,
	}
	r.SetStatusUpdater(statusUpdater, namespace)
	return r
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

	if mcpServerInfo.Suspended {
		if err := r.deregisterUpstream(ctx, req.Name); err != nil {
			logging.Debug("MCPServerReconciler", "DeregisterUpstream for suspended %s failed: %v", req.Name, err)
		}
		r.syncStatus(ctx, req.Name, req.Namespace, mcpServerInfo.Type, nil)
		return ReconcileResult{RequeueAfter: DefaultStatusSyncInterval}
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

	r.syncStatus(ctx, req.Name, req.Namespace, mcpServerInfo.Type, nil)
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

	applier, err := r.applierFor(ctx, req.Name, namespace)
	if err != nil {
		logging.Debug("MCPServerReconciler", "applierFor failed for MCPServer %s: %v", req.Name, err)
		r.syncStatus(ctx, req.Name, req.Namespace, info.Type, err)
		return ReconcileResult{
			Error:   fmt.Errorf("resolve applier: %w", err),
			Requeue: true,
		}, true
	}

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

// applierFor picks the Applier for this Reconcile:
//
//   - Filesystem mode: returns the long-lived yamlApplier set at startup.
//   - Cluster mode: constructs a fresh k8s.Applier bound to the live
//     MCPServer's ownerRef so emitted objects cascade-delete with it. The
//     resolved OwnerReference is cached on the reconciler keyed by
//     namespaced name; subsequent reconciles reuse it without a fresh GET.
func (r *MCPServerReconciler) applierFor(ctx context.Context, name, namespace string) (agentgateway.Applier, error) {
	if r.yamlApplier != nil {
		return r.yamlApplier, nil
	}
	ownerRef, err := r.resolveOwnerRef(ctx, name, namespace)
	if err != nil {
		return nil, err
	}
	return k8sapply.NewApplier(r.k8sClient, ownerRef, r.k8sApplierCfg), nil
}

// resolveOwnerRef builds the metav1.OwnerReference for an MCPServer reconcile
// request in cluster mode. The K8s applier rejects empty UID, so a failed
// GetMCPServer propagates as an error and the caller requeues.
//
// Filesystem mode never reaches this function (yamlApplier is returned first
// in applierFor) and tolerates a nil StatusUpdater.
func (r *MCPServerReconciler) resolveOwnerRef(ctx context.Context, name, namespace string) (metav1.OwnerReference, error) {
	key := types.NamespacedName{Namespace: namespace, Name: name}
	if cached, ok := r.loadOwnerRef(key); ok {
		return cached, nil
	}

	ref := metav1.OwnerReference{
		APIVersion:         mcpServerAPIVersion,
		Kind:               mcpServerKind,
		Name:               name,
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}
	if r.StatusUpdater == nil {
		return metav1.OwnerReference{}, fmt.Errorf("resolveOwnerRef: cluster mode requires a non-nil StatusUpdater")
	}
	server, err := r.StatusUpdater.GetMCPServer(ctx, name, namespace)
	if err != nil {
		return metav1.OwnerReference{}, fmt.Errorf("get MCPServer %s/%s: %w", namespace, name, err)
	}
	if server == nil {
		return metav1.OwnerReference{}, fmt.Errorf("get MCPServer %s/%s: nil server", namespace, name)
	}
	if server.APIVersion != "" {
		ref.APIVersion = server.APIVersion
	}
	if server.Kind != "" {
		ref.Kind = server.Kind
	}
	ref.UID = server.UID
	if ref.UID == "" {
		return metav1.OwnerReference{}, fmt.Errorf("get MCPServer %s/%s: UID is empty", namespace, name)
	}

	r.storeOwnerRef(key, ref)
	return ref, nil
}

func (r *MCPServerReconciler) loadOwnerRef(key types.NamespacedName) (metav1.OwnerReference, bool) {
	r.ownerRefMu.RLock()
	defer r.ownerRefMu.RUnlock()
	ref, ok := r.ownerRefs[key]
	return ref, ok
}

func (r *MCPServerReconciler) storeOwnerRef(key types.NamespacedName, ref metav1.OwnerReference) {
	r.ownerRefMu.Lock()
	defer r.ownerRefMu.Unlock()
	if r.ownerRefs == nil {
		r.ownerRefs = make(map[types.NamespacedName]metav1.OwnerReference)
	}
	r.ownerRefs[key] = ref
}

func (r *MCPServerReconciler) invalidateOwnerRef(key types.NamespacedName) {
	r.ownerRefMu.Lock()
	defer r.ownerRefMu.Unlock()
	delete(r.ownerRefs, key)
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

// reconcileDelete handles deleting an MCPServer service.
//
// If a Deleter is wired (yaml applier in filesystem mode), Delete is called so
// the persisted config file is removed. Cluster mode leaves deleter nil —
// emitted objects cascade-delete via OwnerReferences.
func (r *MCPServerReconciler) reconcileDelete(ctx context.Context, req ReconcileRequest) ReconcileResult {
	logging.Info("MCPServerReconciler", "Deleting MCPServer service: %s", req.Name)

	if err := r.deregisterUpstream(ctx, req.Name); err != nil {
		logging.Debug("MCPServerReconciler", "DeregisterUpstream for %s failed: %v", req.Name, err)
	}
	r.invalidateOwnerRef(types.NamespacedName{Namespace: r.GetNamespace(req.Namespace), Name: req.Name})

	if r.deleter != nil {
		if err := r.deleter.Delete(ctx, req.Name); err != nil {
			logging.Debug("MCPServerReconciler", "Deleter for %s failed: %v", req.Name, err)
			return ReconcileResult{
				Error:   fmt.Errorf("delete config: %w", err),
				Requeue: true,
			}
		}
	}

	logging.Info("MCPServerReconciler", "Successfully deleted MCPServer service: %s", req.Name)
	return ReconcileResult{}
}
