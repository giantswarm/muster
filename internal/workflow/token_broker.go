package workflow

import (
	"context"
	"time"
)

// TokenBroker is the workflow engine's view of an OAuth 2.1 / OIDC token
// broker. It is intentionally narrower than the aggregator's view: workflow
// steps need to obtain tokens for downstream calls but never initiate or
// complete authorization flows.
//
// The interface is consumer-defined; the broker domain provides an adapter
// that structurally satisfies both this and [aggregator.TokenBroker].
type TokenBroker interface {
	GetToken(ctx context.Context, sessionID, audience string) (Token, error)
	ExchangeToken(ctx context.Context, req ExchangeRequest) (Token, error)
}

// Token is a bearer credential the workflow attaches to downstream calls.
// The AccessToken field may be opaque or a JWT; callers treat it as opaque.
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
