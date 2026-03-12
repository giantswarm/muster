package api

import "context"

// ClientSessionIDHeader is the standard MCP session ID header.
// This is the same header that mcp-go uses natively for session management.
// Both the server and CLI clients use this header for session identification.
const ClientSessionIDHeader = "Mcp-Session-Id"

// SubjectContextKey is the context key for storing the authenticated user's subject claim.
// This is set by the OAuth middleware after token validation and used by the session
// middleware for identity binding.
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
