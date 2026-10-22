package api

import "context"

// SubjectContextKey is the context key for storing the authenticated user's subject claim.
// This is set by the OAuth middleware after token validation and used by downstream
// handlers for user identity.
type SubjectContextKey struct{}

// GetSubjectFromContext extracts the authenticated user's subject from context.
// Returns the subject if found, or empty string if not present.
func GetSubjectFromContext(ctx context.Context) string {
	if subject, ok := ctx.Value(SubjectContextKey{}).(string); ok {
		return subject
	}
	return ""
}

// WithSubject returns a new context with the authenticated user's subject set.
func WithSubject(ctx context.Context, subject string) context.Context {
	return context.WithValue(ctx, SubjectContextKey{}, subject)
}

// sessionIDContextKey is the context key for storing the session ID (token family ID).
// The session ID is derived from the mcp-oauth token family and provides per-login-session
// state isolation. Unlike the subject (user identity), the session ID changes on each
// new login, enabling multi-device isolation.
type sessionIDContextKey struct{}

// GetSessionIDFromContext extracts the session ID from context.
// Returns the session ID if found, or empty string if not present.
func GetSessionIDFromContext(ctx context.Context) string {
	if sessionID, ok := ctx.Value(sessionIDContextKey{}).(string); ok {
		return sessionID
	}
	return ""
}

// WithSessionID returns a new context with the session ID set.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDContextKey{}, sessionID)
}

// Caller kinds, the caller_kind of tool-call metrics and log lines.
const (
	// CallerKindPerson is a caller authenticated as a person.
	CallerKindPerson = "person"
	// CallerKindAgent is a Session's sandbox authenticated with its actor
	// JWT, served as the person its binding names.
	CallerKindAgent = "agent"
)

// agentCallerContextKey marks a request served for an actor-JWT caller.
type agentCallerContextKey struct{}

// WithAgentCaller marks ctx as served for an agent: a Session's sandbox
// acting as the person its binding names.
func WithAgentCaller(ctx context.Context) context.Context {
	return context.WithValue(ctx, agentCallerContextKey{}, true)
}

// CallerKindFromContext is CallerKindAgent for a context WithAgentCaller
// marked, CallerKindPerson otherwise.
func CallerKindFromContext(ctx context.Context) string {
	if agent, _ := ctx.Value(agentCallerContextKey{}).(bool); agent {
		return CallerKindAgent
	}
	return CallerKindPerson
}
