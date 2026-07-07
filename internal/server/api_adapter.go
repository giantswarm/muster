package server

import (
	"context"

	oauth "github.com/giantswarm/mcp-oauth"
	oauthserver "github.com/giantswarm/mcp-oauth/server"

	"github.com/giantswarm/muster/internal/api"
)

// brokerTokenMinter implements api.BackendTokenMinter on top of the embedded
// mcp-oauth server. It routes every mint through the credential-less self-issued
// exchange (Server.SelfIssuedExchange) so the server's enforcement (subject and
// actor token validation, the email-verified gate, and the allowed-resource
// gate) runs before muster mints the token. It performs no policy of its own.
type brokerTokenMinter struct {
	server *oauth.Server
}

var _ api.BackendTokenMinter = (*brokerTokenMinter)(nil)

// MintBackendToken mints a per-backend token. An empty ActorToken mints a
// subject-only token; a non-empty ActorToken selects RFC 8693 delegation. The
// backend resource identifier (req.Audience) becomes the issued token's aud via
// the RFC 8707 resource parameter; an empty req.Resource falls back to it.
func (m *brokerTokenMinter) MintBackendToken(ctx context.Context, req api.BackendMintRequest) (api.BackendMintResult, error) {
	resource := req.Resource
	if resource == "" {
		resource = req.Audience
	}
	result, err := m.server.SelfIssuedExchange(ctx, oauthserver.SelfIssuedExchangeRequest{
		SubjectExchange: oauthserver.SubjectExchange{
			Subject:  oauthserver.TypedToken{Token: req.SubjectToken, Type: req.SubjectTokenType},
			Actor:    oauthserver.TypedToken{Token: req.ActorToken, Type: req.ActorTokenType},
			Resource: resource,
			Scope:    req.Scope,
		},
	})
	if err != nil {
		return api.BackendMintResult{}, err
	}
	return api.BackendMintResult{
		AccessToken: result.AccessToken,
		ExpiresAt:   result.ExpiresAt,
	}, nil
}
