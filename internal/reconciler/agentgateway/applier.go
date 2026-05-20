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

// Applier persists an agentgateway Config behind an adapter-specific backend.
//
// Implementations must:
//
//   - Be idempotent: re-applying an identical Config produces no observable
//     change. Delete may be called for a name never Applied; implementations
//     return nil in that case.
//   - Reconcile prior state: when an Apply drops entities previously
//     persisted, the persisted view is updated so it matches the latest
//     Config.
//   - Honor context cancellation: a canceled ctx returns a non-nil error
//     that satisfies errors.Is against the context's error.
//   - Wrap returned errors as fmt.Errorf("context: %w", err) so callers
//     can errors.Is/As the underlying cause.
//
// Delete removes the persisted state for the named MCPServer. K8s adapters
// return nil — cleanup cascades via OwnerReferences. YAML adapters remove the
// route entry and rewrite the combined file.
type Applier interface {
	Apply(ctx context.Context, config Config) error
	Delete(ctx context.Context, name string) error
}

// Deleter removes the persisted representation of a Config by its
// identifying name. Implementations must:
//
//   - Be idempotent: Delete of an absent name returns nil.
//   - Honor context cancellation.
//   - Wrap returned errors.
//
// Held separately from Applier because not every Applier needs an explicit
// delete path. Callers that don't need it can pass nil.
type Deleter interface {
	Delete(ctx context.Context, name string) error
}
