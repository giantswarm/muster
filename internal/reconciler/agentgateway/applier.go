package agentgateway

import (
	"context"
	"errors"
)

// ErrUnsupportedTransport is returned (typically wrapped) by Applier
// implementations when a Backend's transport variant is not representable on
// the target backend — e.g. the k8s Applier rejecting stdio Backends in
// cluster mode. Callers route on this sentinel via errors.Is to short-circuit
// retries: an unsupported configuration is deterministic, so a status
// condition surface is more useful than exponential backoff.
//
// Adapters wrap this sentinel with their own descriptive error
// (fmt.Errorf("…: %w", ErrUnsupportedTransport)) so adapter-agnostic callers
// can keep the abstraction clean while operators still see precise wording in
// logs and status messages.
var ErrUnsupportedTransport = errors.New("agentgateway: backend transport not supported by this applier")

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
//   - Reconcile prior state for entities the implementation owns end-to-end
//     (yaml: the combined config file; k8s: the per-MCPServer Policy slot,
//     which is created or removed based on Authn.RequiresPolicy()). Cleanup
//     of Backend / HTTPRoute objects on Config deletion is handled by the
//     Deleter port (cluster mode) or by removing entries from the combined
//     file on the next Apply (filesystem mode); per-Apply pruning of
//     Backend/HTTPRoute objects within a single MCPServer's slot is not
//     required because the Config produced by NewConfig holds exactly one
//     Backend per MCPServer.
//   - Honor context cancellation: a canceled ctx returns a non-nil error
//     that satisfies errors.Is against the context's error.
//   - Wrap returned errors so callers can use errors.Is / errors.As to
//     reach the underlying cause (the exact format string is left to the
//     implementation; carry adapter-identifying context).
//
// No applier_test.go lives alongside this port file because the contract is
// exercised through the concrete adapters (internal/reconciler/agentgateway/k8s
// and internal/reconciler/agentgateway/yaml). Add a port-level fake-driven
// test only if a third adapter lands.
type Applier interface {
	Apply(ctx context.Context, config Config) error
}

// Deleter removes the entire persisted representation of a Config by its
// identifying name. Cluster mode relies on Kubernetes ownerReference cascade
// (no explicit delete needed for the per-MCPServer Backend/HTTPRoute), so the
// k8s adapter implements Deleter as a no-op for those resources and only
// removes the Policy slot when it exists. Filesystem mode must implement
// Deleter to drop the MCPServer's entries from the combined config file.
//
// Kept separate from Applier because (a) cluster-mode callers may pass an
// Applier that never needs an explicit delete and (b) the two operations
// have different idempotence semantics (Delete of an absent name is a no-op,
// Apply of an absent config is meaningless).
type Deleter interface {
	Delete(ctx context.Context, name string) error
}
