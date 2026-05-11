package aggregator

import (
	"context"
	"time"
)

// TokenBroker is the aggregator's view of an OAuth 2.1 / OIDC token broker.
//
// The interface is defined by the consumer (this package), not by the broker
// implementation. The broker domain provides an adapter that structurally
// satisfies it. Workflow defines its own narrower view in
// internal/workflow/token_broker.go.
//
// The surface is intent-level: storage mutators (cache writes, deletes by
// key) are deliberately absent. Storage is implementation detail of the
// broker bounded context; the litmus test for membership here is "would
// this method make sense over gRPC when the broker extracts to its own
// pod?". Storage mutators fail it.
//
// The port grows as call sites are migrated:
//
//   - PR 4 (this seam) consumes SessionIssuer.
//   - PR 5 (auth_tools, connection_helper, server) widens with
//     BeginOAuthFlow / CompleteOAuthFlow as the manual-auth flow is
//     migrated off api.GetOAuthHandler().
//   - Phase 4 adds Introspect and a WatchAuthEvents event stream once the
//     broker grows the underlying capability.
type TokenBroker interface {
	GetToken(ctx context.Context, sessionID, audience string) (Token, error)
	ExchangeToken(ctx context.Context, req ExchangeRequest) (Token, error)
	RevokeSession(ctx context.Context, sessionID string) error
	RevokeUser(ctx context.Context, subject string) error
	SessionIssuer(ctx context.Context, sessionID string) (string, error)
}

// Token is a bearer credential issued by the broker for a downstream MCP
// server or audience. The AccessToken field may be opaque or a JWT — the
// broker decides at deployment time; callers treat it as opaque.
type Token struct {
	AccessToken  string
	TokenType    string
	RefreshToken string
	ExpiresAt    time.Time
	Scope        string
	IDToken      string
	Issuer       string
}

// ExchangeRequest carries the inputs for an RFC 8693 token exchange.
type ExchangeRequest struct {
	SessionID        string
	SubjectToken     string
	SubjectTokenType string
	Audience         string
	Scope            string
}
