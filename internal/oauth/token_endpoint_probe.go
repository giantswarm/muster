package oauth

import (
	"context"
	"fmt"
	"strings"

	"github.com/giantswarm/mcp-oauth/providers/oidc"

	"github.com/giantswarm/muster/v5/internal/api"
)

// probeSubjectToken is the subject token of a token endpoint probe. It is no
// token any issuer signed, so an authorization server that answers rejects it
// with an OAuth error; only a transport failure or a non-OAuth answer means
// the endpoint is not there.
const probeSubjectToken = "muster-token-endpoint-probe"

// Probe checks that the token endpoint of config answers as an authorization
// server, with the client credentials and HTTP client every exchange uses. It
// sends an exchange with a subject token no issuer signed and returns the
// error that produced -- an OAuth rejection of the subject token when the
// endpoint is up (see api.ClassifyTokenExchangeError) -- or nil in the
// unlikely case the endpoint accepted it. Nothing is cached.
func (e *TokenExchanger) Probe(ctx context.Context, config *api.TokenExchangeConfig) error {
	if config == nil {
		return fmt.Errorf("token exchange config is nil")
	}
	if !strings.HasPrefix(config.DexTokenEndpoint, "https://") {
		return fmt.Errorf("dex token endpoint must use HTTPS (got: %s)", config.DexTokenEndpoint)
	}
	_, scopes := getExchangeDefaults(&ExchangeRequest{Config: config})
	_, err := e.client.Exchange(ctx, oidc.TokenExchangeRequest{
		TokenEndpoint:      config.DexTokenEndpoint,
		SubjectToken:       probeSubjectToken,
		SubjectTokenType:   oidc.TokenTypeIDToken,
		ConnectorID:        config.ConnectorID,
		Scope:              scopes,
		RequestedTokenType: oidc.TokenTypeIDToken,
		ClientID:           config.ClientID,
		ClientSecret:       config.ClientSecret,
	})
	return err
}
