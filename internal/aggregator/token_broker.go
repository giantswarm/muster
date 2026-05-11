package aggregator

import (
	"context"
	"time"
)

// TokenBroker is the aggregator's port for the OAuth/OIDC broker.
// Consumer-defined: the broker domain supplies an adapter that structurally
// satisfies it. Workflow has its own narrower view.
//
// Storage mutators are intentionally absent so a gRPC-fronted remote broker
// can answer the same calls without exposing its store.
type TokenBroker interface {
	GetToken(ctx context.Context, sessionID, audience string) (Token, error)
	RevokeSession(ctx context.Context, sessionID string) error
	RevokeUser(ctx context.Context, subject string) error
	SessionIssuer(ctx context.Context, sessionID string) (string, error)
}

// Token is a bearer credential bound to a downstream audience.
// AccessToken may be opaque or a JWT depending on broker deployment;
// callers do not branch on the format.
type Token struct {
	AccessToken  string
	TokenType    string
	RefreshToken string
	ExpiresAt    time.Time
	Scope        string
	IDToken      string
	Issuer       string
}
