package server

import (
	"context"

	oauth "github.com/giantswarm/mcp-oauth"

	"github.com/giantswarm/muster/internal/api"
)

// brokerTokenMinter implements api.BackendTokenMinter on top of the embedded
// mcp-oauth server. It routes every mint through WorkloadExchangeSubjectToken so
// the server's enforcement (subject and actor token validation,
// ActorDelegationPolicy, WorkloadAudiences) runs before muster's broker mints
// the token. It performs no policy of its own.
type brokerTokenMinter struct {
	server *oauth.Server
}

var _ api.BackendTokenMinter = (*brokerTokenMinter)(nil)

// MintBackendToken mints a per-backend token. An empty ActorToken selects the
// M2M path; a non-empty ActorToken selects RFC 8693 delegation.
func (m *brokerTokenMinter) MintBackendToken(ctx context.Context, req api.BackendMintRequest) (api.BackendMintResult, error) {
	result, err := m.server.WorkloadExchangeSubjectToken(ctx,
		req.SubjectToken, req.SubjectTokenType,
		req.ActorToken, req.ActorTokenType,
		req.Audience, req.Resource, req.Scope)
	if err != nil {
		return api.BackendMintResult{}, err
	}
	return api.BackendMintResult{
		AccessToken: result.AccessToken,
		ExpiresAt:   result.ExpiresAt,
	}, nil
}
