package agentgateway

import "context"

// Applier persists an agentgateway Config to a backend-specific representation:
// Kubernetes objects in cluster mode, agentgateway native YAML in filesystem
// mode.
//
// Construction conventions differ by mode:
//
//   - Filesystem mode: yaml.NewApplier(dir) is called once at startup and the
//     same instance serves every reconcile.
//   - Cluster mode: k8s.NewApplier(client, ownerRef, cfg) is called per
//     reconcile so the K8s adapter can stamp emitted objects with an
//     ownerReference for cascade deletion. The reconciler builds it inline.
//
// Implementations must:
//
//   - Be idempotent: re-applying an identical Config produces no observable
//     change.
//   - Reconcile prior state: when an Apply drops entities previously
//     persisted, the persisted view is updated so it matches the latest
//     Config.
//   - Honor context cancellation: a canceled ctx returns a non-nil error
//     that satisfies errors.Is against the context's error.
//   - Wrap returned errors as fmt.Errorf("context: %w", err) so callers
//     can errors.Is/As the underlying cause.
type Applier interface {
	Apply(ctx context.Context, config Config) error
}
