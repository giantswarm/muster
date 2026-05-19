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
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/reconciler/agentgateway"
	k8sapply "github.com/giantswarm/muster/internal/reconciler/agentgateway/k8s"
	"github.com/giantswarm/muster/pkg/logging"
)

// ConditionTypeNotSupportedInCluster is the MCPServer status condition the
// reconciler raises when a Backend cannot be emitted in cluster mode (today,
// stdio MCPServers).
const ConditionTypeNotSupportedInCluster = "NotSupportedInCluster"

const (
	mcpServerAPIVersion = "muster.giantswarm.io/v1alpha1"
	mcpServerKind       = "MCPServer"
)

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
}

// NewMCPServerReconcilerFilesystem builds a reconciler wired to the
// long-lived yaml Applier used in filesystem mode. yamlApplier and deleter
// are typically the same instance (yaml.Applier satisfies both ports).
func NewMCPServerReconcilerFilesystem(
	mcpServerManager MCPServerManager,
	yamlApplier agentgateway.Applier,
	deleter agentgateway.Deleter,
) *MCPServerReconciler {
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
// objects cascade-delete with the MCPServer.
func NewMCPServerReconcilerCluster(
	mcpServerManager MCPServerManager,
	k8sClient ctrlclient.Client,
	k8sApplierCfg k8sapply.Config,
) *MCPServerReconciler {
	if k8sClient == nil {
		panic("reconciler: NewMCPServerReconcilerCluster requires a non-nil client")
	}
	return &MCPServerReconciler{
		BaseStatusConfig: BaseStatusConfig{Namespace: DefaultNamespace},
		mcpServerManager: mcpServerManager,
		k8sClient:        k8sClient,
		k8sApplierCfg:    k8sApplierCfg,
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

// InitialResources enumerates every MCPServer the manager knows about so the
// reconcile Manager can queue a first reconcile for each. Filesystem-mode
// fsnotify watches otherwise miss MCPServer YAMLs that exist before muster
// starts; before PR 11 the orchestrator's processAutoStartMCPServers
// covered that gap.
func (r *MCPServerReconciler) InitialResources(_ context.Context) ([]ReconcileRequest, error) {
	if r.mcpServerManager == nil {
		return nil, nil
	}
	infos := r.mcpServerManager.ListMCPServers()
	requests := make([]ReconcileRequest, 0, len(infos))
	for _, info := range infos {
		requests = append(requests, ReconcileRequest{
			Name:      info.Name,
			Namespace: r.GetNamespace(""),
		})
	}
	return requests, nil
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
	spec := infoToMCPServerSpec(info)
	namespace := r.GetNamespace(req.Namespace)
	config, err := agentgateway.NewConfig(req.Name, namespace, spec)
	if err != nil {
		logging.Debug("MCPServerReconciler", "NewConfig failed for MCPServer %s: %v", req.Name, err)
		r.syncStatus(ctx, req.Name, req.Namespace, info.Type, err)
		return ReconcileResult{Error: fmt.Errorf("agentgateway: %w", err)}, true
	}

	applier := r.applierFor(ctx, req.Name, namespace)

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
//     MCPServer's ownerRef so emitted objects cascade-delete with it.
func (r *MCPServerReconciler) applierFor(ctx context.Context, name, namespace string) agentgateway.Applier {
	if r.yamlApplier != nil {
		return r.yamlApplier
	}
	ownerRef := r.resolveOwnerRef(ctx, name, namespace)
	return k8sapply.NewApplier(r.k8sClient, ownerRef, r.k8sApplierCfg)
}

// resolveOwnerRef builds the metav1.OwnerReference for an MCPServer reconcile
// request in cluster mode. The K8s applier rejects empty UID/APIVersion/Kind,
// so cluster-mode callers MUST wire a StatusUpdater that can fetch the live
// CRD.
func (r *MCPServerReconciler) resolveOwnerRef(ctx context.Context, name, namespace string) metav1.OwnerReference {
	ref := metav1.OwnerReference{
		APIVersion:         mcpServerAPIVersion,
		Kind:               mcpServerKind,
		Name:               name,
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}
	if r.StatusUpdater == nil {
		return ref
	}
	server, err := r.StatusUpdater.GetMCPServer(ctx, name, namespace)
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

func infoToMCPServerSpec(info *api.MCPServerInfo) musterv1alpha1.MCPServerSpec {
	spec := musterv1alpha1.MCPServerSpec{
		Type:        info.Type,
		ToolPrefix:  info.ToolPrefix,
		Description: info.Description,
		AutoStart:   info.AutoStart,
		Command:     info.Command,
		Args:        info.Args,
		URL:         info.URL,
		Env:         info.Env,
		Headers:     info.Headers,
		Timeout:     info.Timeout,
	}
	if info.Auth != nil {
		spec.Auth = mcpServerAuthFromAPI(info.Auth)
	}
	return spec
}

func mcpServerAuthFromAPI(auth *api.MCPServerAuth) *musterv1alpha1.MCPServerAuth {
	out := &musterv1alpha1.MCPServerAuth{
		Type:              auth.Type,
		ForwardToken:      auth.ForwardToken,
		RequiredAudiences: auth.RequiredAudiences,
	}
	if auth.TokenExchange != nil {
		out.TokenExchange = tokenExchangeFromAPI(auth.TokenExchange)
	}
	if auth.AuthorizationServer != nil {
		out.AuthorizationServer = &musterv1alpha1.MCPServerAuthAuthorizationServer{
			Issuer: musterv1alpha1.IssuerURL(auth.AuthorizationServer.Issuer),
			Scopes: auth.AuthorizationServer.Scopes,
		}
	}
	return out
}

func tokenExchangeFromAPI(tx *api.TokenExchangeConfig) *musterv1alpha1.TokenExchangeConfig {
	out := &musterv1alpha1.TokenExchangeConfig{
		Enabled:          tx.Enabled,
		DexTokenEndpoint: tx.DexTokenEndpoint,
		ExpectedIssuer:   tx.ExpectedIssuer,
		ConnectorID:      tx.ConnectorID,
		Scopes:           tx.Scopes,
	}
	if tx.ClientCredentialsSecretRef != nil {
		out.ClientCredentialsSecretRef = &musterv1alpha1.ClientCredentialsSecretRef{
			Name:            tx.ClientCredentialsSecretRef.Name,
			Namespace:       tx.ClientCredentialsSecretRef.Namespace,
			ClientIDKey:     tx.ClientCredentialsSecretRef.ClientIDKey,
			ClientSecretKey: tx.ClientCredentialsSecretRef.ClientSecretKey,
		}
	}
	return out
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
