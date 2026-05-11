package aggregator

import (
	"context"
	"time"
)

// TokenBroker is the aggregator's port for the OAuth/OIDC broker.
//
// Tokens are stored and looked up by (sessionID, issuer): the IdP that
// minted them. The RFC 8707 "audience" concept (resource server the
// token is bound to) only surfaces on RFC 8693 token-exchange requests
// added by later PRs.
type TokenBroker interface {
	GetToken(ctx context.Context, sessionID, issuer string) (Token, error)
	SessionIssuer(ctx context.Context, sessionID string) (string, error)
}

// Token is a bearer credential issued by an OIDC IdP.
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
