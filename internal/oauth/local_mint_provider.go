package oauth

import (
	"context"
	"fmt"

	oauthserver "github.com/giantswarm/mcp-oauth/server"
)

// localMintProvider implements CredentialProvider for the "local-mint" broker
// target type. It mints an RFC 9068 JWT signed by muster's own access-token
// signing key via mcp-oauth's LocalMintExchanger.
//
// The token carries:
//   - sub  = the validated human subject (MintRequest.Subject)
//   - act  = { iss, sub } of the validated agent actor (MintRequest.Actor), when present
//   - iss  = muster's configured BaseURL (the issuer that mcp-kubernetes trusts)
//   - aud  = the broker target audience (MintRequest.Target)
//
// mcp-oauth enforces ActorDelegationPolicy before Exchange is called, so by
// the time Mint runs, both subject and actor identities are already validated
// and authorized. No re-validation is performed here.
//
// Requires enableJWTMode to be true; returns a configuration error otherwise.
type localMintProvider struct {
	exchanger *oauthserver.LocalMintExchanger
}

func (p *localMintProvider) Mint(ctx context.Context, req MintRequest) (*MintResult, error) {
	if p.exchanger == nil {
		return nil, fmt.Errorf("local-mint target requires JWT access-token mode (enableJWTMode: true and jwtSigningKeyFile set)")
	}

	exchangerReq := &oauthserver.ExchangerRequest{
		Subject:  &oauthserver.SubjectIdentity{Subject: req.Subject},
		Actor:    req.Actor,
		Audience: req.Target,
	}

	result, err := p.exchanger.Exchange(ctx, exchangerReq)
	if err != nil {
		return nil, fmt.Errorf("local-mint exchange for target %q: %w", req.Target, err)
	}

	return &MintResult{
		AccessToken:     result.AccessToken,
		IssuedTokenType: result.IssuedTokenType,
		ExpiresAt:       result.ExpiresAt,
	}, nil
}
