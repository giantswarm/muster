package translator

import "context"

// ConfigEmitter persists a translator Model into a backend-specific
// representation: Kubernetes objects in cluster mode, agentgateway native
// YAML in filesystem mode.
//
// Implementations must:
//
//   - Be idempotent: re-emitting an identical Model produces no observable
//     change.
//   - Reconcile prior state: when a re-emit drops entities previously
//     persisted, the persisted view is updated so it matches the latest
//     Model.
//   - Honor context cancellation: a canceled ctx returns a non-nil error
//     that satisfies errors.Is against the context's error.
//   - Wrap returned errors as fmt.Errorf("context: %w", err) so callers
//     can errors.Is/As the underlying cause.
type ConfigEmitter interface {
	Emit(ctx context.Context, m Model) error
}
