package workflow

import (
	"context"
	"time"
)

// TokenBroker is the workflow engine's view of an OAuth/OIDC broker.
// Narrower than the aggregator's view: workflow steps obtain tokens for
// downstream calls but never initiate authorization flows.
type TokenBroker interface {
	GetToken(ctx context.Context, sessionID, audience string) (Token, error)
}

// Token is a bearer credential bound to a downstream audience.
// AccessToken may be opaque or a JWT; callers do not branch on format.
type Token struct {
	AccessToken  string
	TokenType    string
	RefreshToken string
	ExpiresAt    time.Time
	Scope        string
	IDToken      string
	Issuer       string
}
