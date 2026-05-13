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
// ExchangeRequest.Audience is the distinct RFC 8693 audience for token
// exchange.
type TokenBroker interface {
	// Enabled reports whether the broker is wired and accepting
	// requests. Disabled brokers return [ErrBrokerDisabled] from every
	// other method.
	Enabled() bool
	// BeginOAuthFlow starts an OAuth 2.1 authorization-code flow against
	// the issuer in req and returns the authorization URL the user
	// agent should visit, together with a human-readable challenge
	// message.
	BeginOAuthFlow(ctx context.Context, req BeginRequest) (FlowURL, error)
	// GetToken returns the cached bearer token for (sessionID, issuer)
	// or [ErrTokenNotFound] if none is cached. Callers distinguish
	// "not cached" from other errors via [errors.Is].
	GetToken(ctx context.Context, sessionID, issuer string) (Token, error)
	ExchangeToken(ctx context.Context, req ExchangeRequest) (Token, error)
	// InvalidateToken is the consumer-side signal "the token last issued
	// for (sessionID, issuer) was rejected downstream". The broker decides
	// how to react — cache eviction, blacklisting, telemetry — without
	// the consumer directing storage. A gRPC-fronted broker pod can
	// implement this without exposing its store.
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

// ExchangeRequest carries the inputs for an RFC 8693 token exchange.
// All fields are port-owned and gRPC-safe; callers translate from any
// CRD-shaped config into Config before invoking ExchangeToken.
type ExchangeRequest struct {
	SessionID    string
	Subject      string
	SubjectToken string
	Audience     string
	Config       ExchangeConfig
}

// ExchangeConfig is the port-owned shape of an RFC 8693 exchange
// configuration. Credentials must be fully resolved before the request
// reaches the port; the broker does not load K8s secrets or perform any
// CRD lookups on the consumer's behalf.
//
// Scopes is the RFC 6749 §3.3 wire form: space-separated scope tokens.
type ExchangeConfig struct {
	TokenEndpoint  string
	ExpectedIssuer string
	ConnectorID    string
	ClientID       string
	ClientSecret   string
	Scopes         string
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
