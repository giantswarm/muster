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
// pod?". Introspect and WatchAuthEvents are Phase-4 surface and will be
// added when the broker grows the underlying capability.
type TokenBroker interface {
	BeginOAuthFlow(ctx context.Context, req BeginRequest) (FlowURL, error)
	CompleteOAuthFlow(ctx context.Context, code, state string) (Session, error)
	GetToken(ctx context.Context, sessionID, audience string) (Token, error)
	ExchangeToken(ctx context.Context, req ExchangeRequest) (Token, error)
	RevokeSession(ctx context.Context, sessionID string) error
	RevokeUser(ctx context.Context, subject string) error
	SessionIssuer(ctx context.Context, sessionID string) (string, error)
}

// BeginRequest carries the inputs for starting an OAuth 2.1 authorization
// code flow against a backend MCP server.
type BeginRequest struct {
	SessionID   string
	Subject     string
	ServerName  string
	Issuer      string
	Scope       string
	RedirectURI string
}

// FlowURL is the authorization URL the user agent visits to complete the
// authorization step, paired with the opaque state value the broker expects
// back on the redirect.
type FlowURL struct {
	URL   string
	State string
}

// Session identifies a user session established by completing an OAuth flow.
type Session struct {
	ID      string
	Subject string
	Issuer  string
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
