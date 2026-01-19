package api

import "context"

// ClientSessionIDHeader is the HTTP header name for client-provided session IDs.
// This enables CLI tools to maintain persistent session identity across invocations.
//
// When present, this header takes precedence over the random session ID generated
// by mcp-go for token storage. This is critical for CLI tools where each invocation
// creates a new connection - without it, MCP server tokens would be lost between
// CLI invocations because the mcp-go session ID changes on each connection.
//
// Security: The client-provided session ID is trusted because:
//   - It's sent by the authenticated CLI client (aggregator auth validates the user)
//   - Token lookup still requires matching (sessionID, issuer, scope)
//   - A malicious client can only access tokens it previously stored with that session ID
const ClientSessionIDHeader = "X-Muster-Session-ID"

// ClientSessionIDContextKey is the context key for storing client-provided session IDs.
// This type is defined in the api package to ensure both the aggregator and server
// packages use the same type identity when setting/getting context values.
//
// Usage:
//
//	// Setting the value (in middleware)
//	ctx := context.WithValue(r.Context(), api.ClientSessionIDContextKey{}, sessionID)
//
//	// Getting the value
//	if sessionID, ok := ctx.Value(api.ClientSessionIDContextKey{}).(string); ok {
//	    // use sessionID
//	}
type ClientSessionIDContextKey struct{}

// GetClientSessionIDFromContext extracts the client-provided session ID from context.
// Returns the session ID and true if found, or empty string and false if not present.
//
// This is a convenience function that encapsulates the context key type assertion.
func GetClientSessionIDFromContext(ctx context.Context) (string, bool) {
	if sessionID, ok := ctx.Value(ClientSessionIDContextKey{}).(string); ok && sessionID != "" {
		return sessionID, true
	}
	return "", false
}

// WithClientSessionID returns a new context with the client session ID set.
// This is a convenience function for setting the session ID in context.
func WithClientSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, ClientSessionIDContextKey{}, sessionID)
}
