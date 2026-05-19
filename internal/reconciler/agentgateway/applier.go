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
//   - Be idempotent: re-applying an identical Config or re-deleting an absent
//     name produces no observable change.
//   - Reconcile prior state for entities the implementation owns end-to-end
//     (yaml: the combined config file; k8s: the per-MCPServer Policy slot,
//     which is created or removed based on Authn.RequiresPolicy()).
//   - Delete drops the persisted representation by name. Cluster mode relies
//     on Kubernetes ownerReference cascade for the per-MCPServer Backend /
//     HTTPRoute, so the k8s adapter's Delete is a no-op for those and only
//     removes the Policy slot when present. Filesystem mode removes the
//     MCPServer's entries from the combined config file.
//   - Honor context cancellation: a canceled ctx returns a non-nil error
//     that satisfies errors.Is against the context's error.
//   - Wrap returned errors so callers can use errors.Is / errors.As to
//     reach the underlying cause.
//
// No applier_test.go lives alongside this port file because the contract is
// exercised through the concrete adapters (internal/reconciler/agentgateway/k8s
// and internal/reconciler/agentgateway/yaml). Add a port-level fake-driven
// test only if a third adapter lands.
type Applier interface {
	Apply(ctx context.Context, config Config) error
	Delete(ctx context.Context, name string) error
}
