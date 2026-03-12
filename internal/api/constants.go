package api

import "context"

// ClientSessionIDHeader is the HTTP header name for client-provided session IDs.
// This is still sent by CLI clients for backwards compatibility, but the server
// no longer uses it for session management (Phase 3 session ID elimination).
// TODO: Remove this constant once all CLI clients have been updated to stop sending it.
const ClientSessionIDHeader = "X-Muster-Session-ID"

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
