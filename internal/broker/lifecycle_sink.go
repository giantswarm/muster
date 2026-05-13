package broker

import "context"

// LifecycleSink receives OAuth token-family lifecycle events from the
// broker after the broker has handled its own persistence. The aggregator
// implements this interface to wire domain side-effects (SSO setup,
// upstream-refresh-failure cleanup, session teardown) without owning the
// event source.
//
// Semantically the broker is the source of truth for these events — it
// hosts the mcp-oauth server that emits them. The sink is the consumer.
// When the broker becomes a separate pod (Phase 8), the sink becomes a
// gRPC stream from broker to aggregator; the in-process implementation
// is a direct method dispatch.
type LifecycleSink interface {
	// OnSessionCreated fires after a new token family is created during
	// authorization-code exchange and the broker has persisted the
	// session's ID token. The aggregator initialises SSO connections
	// for the new session.
	OnSessionCreated(ctx context.Context, sessionID, userID, idToken string)

	// OnTokenRefreshFailed fires when the broker rejects a refreshed
	// token (e.g. malformed ID token, missing exp claim) OR when the
	// upstream refresh returns no ID token at all. The aggregator
	// evicts pooled connections, revokes the auth-store session, and
	// marks SSO failed so downstream MCP servers stop retrying with
	// credentials the broker rejected. The reason string is for logging
	// only — callers should not branch on its content.
	OnTokenRefreshFailed(ctx context.Context, sessionID, userID, reason string)

	// OnSessionRevoked fires after the broker has cleared every cached
	// entry for sessionID. The aggregator tears down per-session
	// connection-pool entries, capability-store rows, and auth-store
	// state.
	OnSessionRevoked(ctx context.Context, sessionID string)
}
