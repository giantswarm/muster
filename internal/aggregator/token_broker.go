package aggregator

import (
	"context"
	"errors"
	"time"
)

var (
	ErrTokenNotFound  = errors.New("broker: no cached token for session/issuer")
	ErrBrokerDisabled = errors.New("broker: OAuth proxy is disabled")
)

// TokenBroker is the aggregator's port for the OAuth/OIDC broker.
// Tokens are keyed by (sessionID, issuer) — the IdP that minted them.
type TokenBroker interface {
	Enabled() bool
	BeginOAuthFlow(ctx context.Context, req BeginRequest) (FlowURL, error)
	GetToken(ctx context.Context, sessionID, issuer string) (Token, error)
	// InvalidateToken is the consumer-side signal "the token last issued
	// for (sessionID, issuer) was rejected downstream". The broker
	// decides how to react — cache eviction, blacklisting, telemetry —
	// without the consumer directing storage. Distinct from a cache
	// mutator: a gRPC-fronted broker pod can implement this without
	// exposing its store.
	InvalidateToken(ctx context.Context, sessionID, issuer string) error
	SessionIssuer(ctx context.Context, sessionID string) (string, error)
}

// BeginRequest carries the inputs for starting an OAuth 2.1 authorization
// code flow against a backend MCP server.
type BeginRequest struct {
	SessionID  string
	Subject    string
	ServerName string
	Issuer     string
	Scope      string
}

// FlowURL is the authorization URL the user agent visits to complete the
// flow, together with the broker-supplied human-readable challenge.
type FlowURL struct {
	URL        string
	ServerName string
	Message    string
}

// Token is a bearer credential issued by an OIDC IdP.
type Token struct {
	AccessToken  string
	TokenType    string
	RefreshToken string
	ExpiresAt    time.Time
	Scope        string
	IDToken      string
	Issuer       string
}
