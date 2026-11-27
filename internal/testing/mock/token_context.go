package mock

import "context"

// receivedToken carries the bearer token an OAuth-protected backend was called
// with, plus its decoded claims, from the auth middleware down to the tool
// handler so an echo_token tool can return them.
type receivedToken struct {
	Raw           string
	Claims        *tokenClaims
	Authorization *authorizationObservation
}

type authorizationObservation struct {
	Subject  string   `json:"subject"`
	Issuer   string   `json:"issuer"`
	ClientID string   `json:"client_id,omitempty"`
	Scopes   []string `json:"scopes,omitempty"`
}

type receivedTokenKey struct{}

func withReceivedToken(ctx context.Context, tok *receivedToken) context.Context {
	return context.WithValue(ctx, receivedTokenKey{}, tok)
}

func receivedTokenFrom(ctx context.Context) *receivedToken {
	tok, _ := ctx.Value(receivedTokenKey{}).(*receivedToken)
	return tok
}
