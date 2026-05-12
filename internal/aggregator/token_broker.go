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
	GetToken(ctx context.Context, sessionID, issuer string) (Token, error)
	SessionIssuer(ctx context.Context, sessionID string) (string, error)
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
