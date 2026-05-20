package k8s

import (
	"context"
	"errors"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/giantswarm/muster/internal/reconciler/agentgateway"
)

// ErrStdioNotSupportedInCluster is returned by Apply when any Backend in the
// Config carries a StdioTarget. Wraps agentgateway.ErrUnsupportedTransport so
// adapter-agnostic callers (the reconciler) route on the port-level sentinel
// without importing this package; operators reading logs and the K8s
// applier's own tests still see the precise wording.
var ErrStdioNotSupportedInCluster = fmt.Errorf("k8s applier: stdio MCPServers are not supported in cluster mode: %w", agentgateway.ErrUnsupportedTransport)

// Config configures an Applier at construction.
type Config struct {
	// GatewayName is the metadata.name of the Gateway HTTPRoutes attach to.
	GatewayName string
	// GatewayNamespace is the namespace of the Gateway. Empty means the
	// HTTPRoute targets a Gateway in the same namespace as the MCPServer.
	GatewayNamespace string
}

// Applier persists an agentgateway.Config into a Kubernetes cluster.
// Each instance is bound to one MCPServer via ownerRef.
//
// Ownership semantics:
//
//   - AgentgatewayBackend, HTTPRoute and AgentgatewayPolicy emitted for an
//     MCPServer are wholly owned by the reconciler. Apply replaces .Spec
//     wholesale on every reconcile, so external edits (Filters, extra
//     ParentRefs, additional TargetRefs) are reverted on the next pass.
//   - The MCPServer's ownerRef is stamped (Controller + BlockOwnerDeletion
//     default to true if the caller leaves them nil) so deletion cascades
//     through the Kubernetes garbage collector. applyOwner replaces a stale
//     ownerRef in place (matched by Name+Kind+APIVersion) rather than
//     appending — recreating an MCPServer with a new UID still yields
//     exactly one ownerRef.
type Applier struct {
	client   client.Client
	ownerRef metav1.OwnerReference
	config   Config
}

// NewApplier returns an Applier writing through client, with ownerRef
// stamped on every emitted object so deletion of the MCPServer cascades
// to the agentgateway stack. Defaults are applied for Config fields left
// at the zero value.
func NewApplier(client client.Client, ownerRef metav1.OwnerReference, config Config) *Applier {
	return &Applier{client: client, ownerRef: ownerRef, config: config}
}

// Apply reconciles every object derived from config into the cluster. It is
// idempotent: re-applying an identical Config produces no observable change.
// If any Backend carries a StdioTarget, Apply returns
// ErrStdioNotSupportedInCluster before touching the API server.
func (a *Applier) Apply(ctx context.Context, config agentgateway.Config) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.validate(config); err != nil {
		return fmt.Errorf("k8s applier: %w", err)
	}
	for i := range config.Backends {
		if config.Backends[i].Target != nil && config.Backends[i].Target.Kind() == agentgateway.TargetStdio {
			return ErrStdioNotSupportedInCluster
		}
	}

	for i := range config.Backends {
		if err := a.reconcileBackend(ctx, config.Namespace, config.Backends[i]); err != nil {
			return fmt.Errorf("k8s applier: backend %q: %w", config.Backends[i].Name, err)
		}
	}
	for i := range config.Routes {
		if err := a.reconcileRoute(ctx, config.Namespace, config.Routes[i]); err != nil {
			return fmt.Errorf("k8s applier: route %q: %w", config.Routes[i].Name, err)
		}
	}
	for i := range config.Policies {
		if err := a.reconcilePolicy(ctx, config.Namespace, config.Policies[i]); err != nil {
			return fmt.Errorf("k8s applier: policy %q: %w", config.Policies[i].Name, err)
		}
	}
	return nil
}

// Delete is a no-op: emitted objects are owned by the MCPServer via
// OwnerReferences, so cluster deletion cascades without any work from the
// applier.
func (a *Applier) Delete(_ context.Context, _ string) error { return nil }

func (a *Applier) validate(config agentgateway.Config) error {
	if config.Namespace == "" {
		return errors.New("Config.Namespace is required")
	}
	switch {
	case a.ownerRef.Name == "":
		return errors.New("ownerRef.Name is required")
	case a.ownerRef.UID == "":
		return errors.New("ownerRef.UID is required")
	case a.ownerRef.APIVersion == "":
		return errors.New("ownerRef.APIVersion is required")
	case a.ownerRef.Kind == "":
		return errors.New("ownerRef.Kind is required")
	}
	return nil
}
