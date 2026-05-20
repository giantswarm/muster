package agentgateway

import "context"

// Applier persists an agentgateway Config behind an adapter-specific backend.
//
// Implementations must:
//
//   - Be idempotent: re-applying an identical Config produces no observable
//     change.
//   - Reconcile prior state so the persisted representation matches the input
//     Config — entries that no longer belong to this Config are removed.
//   - Honor context cancellation: a canceled ctx returns a non-nil error
//     that satisfies errors.Is against the context's error.
//   - Wrap returned errors so callers can use errors.Is / errors.As to
//     reach the underlying cause.
type Applier interface {
	Apply(ctx context.Context, config Config) error
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
