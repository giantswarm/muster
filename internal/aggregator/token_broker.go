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
// Introspect accepts any bearer (opaque or JWT) and returns a uniform
// [Claims] shape: the broker validates either format regardless of which
// it issues, so callers do not branch on token format.
type TokenBroker interface {
	BeginOAuthFlow(ctx context.Context, req BeginRequest) (FlowURL, error)
	CompleteOAuthFlow(ctx context.Context, code, state string) (Session, error)
	GetToken(ctx context.Context, sessionID, audience string) (Token, error)
	ExchangeToken(ctx context.Context, req ExchangeRequest) (Token, error)
	RevokeSession(ctx context.Context, sessionID string) error
	Introspect(ctx context.Context, bearer string) (Claims, error)
	WatchAuthEvents(ctx context.Context) <-chan AuthEvent
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

// Claims is the introspection result for a bearer token, populated whether
// the token is opaque (RFC 7662 introspection) or a JWT (local verification).
type Claims struct {
	Active    bool
	Subject   string
	Issuer    string
	Audience  []string
	Scope     string
	ExpiresAt time.Time
	Extra     map[string]any
}

// AuthEventType discriminates lifecycle transitions emitted on the channel
// returned by [TokenBroker.WatchAuthEvents].
type AuthEventType int

const (
	AuthEventUnknown AuthEventType = iota
	AuthEventSessionEstablished
	AuthEventSessionRevoked
	AuthEventTokenRefreshed
	AuthEventReauthRequired
)

// AuthEvent is emitted by the broker when session state changes.
type AuthEvent struct {
	Type      AuthEventType
	SessionID string
	Subject   string
	Server    string
	At        time.Time
}
