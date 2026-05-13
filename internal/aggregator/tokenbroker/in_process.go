package tokenbroker

import (
	"context"
	"fmt"

	"github.com/giantswarm/muster/internal/aggregator"
	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/broker"
	"github.com/giantswarm/muster/pkg/logging"
)

// InProcess implements [aggregator.TokenBroker] against an in-process
// *broker.Manager. A nil manager short-circuits every method with
// [aggregator.ErrBrokerDisabled].
type InProcess struct {
	manager *broker.Manager
}

func NewInProcess(manager *broker.Manager) *InProcess {
	return &InProcess{manager: manager}
}

var _ aggregator.TokenBroker = (*InProcess)(nil)

func (a *InProcess) Enabled() bool {
	return a.manager != nil && a.manager.IsEnabled()
}

func (a *InProcess) BeginOAuthFlow(ctx context.Context, req aggregator.BeginRequest) (aggregator.FlowURL, error) {
	if a.manager == nil {
		return aggregator.FlowURL{}, aggregator.ErrBrokerDisabled
	}
	challenge, err := a.manager.CreateAuthChallenge(ctx, req.SessionID, req.Subject, req.ServerName, req.Issuer, req.Scope)
	if err != nil {
		return aggregator.FlowURL{}, err
	}
	return aggregator.FlowURL{
		URL:        challenge.AuthURL,
		ServerName: challenge.ServerName,
		Message:    challenge.Message,
	}, nil
}

func (a *InProcess) GetToken(_ context.Context, sessionID, issuer string) (aggregator.Token, error) {
	if a.manager == nil {
		return aggregator.Token{}, aggregator.ErrBrokerDisabled
	}
	cached := a.manager.GetTokenByIssuer(sessionID, issuer)
	if cached == nil {
		return aggregator.Token{}, fmt.Errorf("session=%s issuer=%s: %w", logging.TruncateIdentifier(sessionID), issuer, aggregator.ErrTokenNotFound)
	}
	return aggregator.Token{
		AccessToken:  cached.AccessToken,
		TokenType:    cached.TokenType,
		RefreshToken: cached.RefreshToken,
		ExpiresAt:    cached.ExpiresAt,
		Scope:        cached.Scope,
		IDToken:      cached.IDToken,
		Issuer:       cached.Issuer,
	}, nil
}

func (a *InProcess) ExchangeToken(ctx context.Context, req aggregator.ExchangeRequest) (aggregator.Token, error) {
	if a.manager == nil {
		return aggregator.Token{}, aggregator.ErrBrokerDisabled
	}
	if req.Config.TokenEndpoint == "" {
		return aggregator.Token{}, fmt.Errorf("exchange request missing token endpoint for audience %q", req.Audience)
	}

	accessToken, issuedTokenType, err := a.manager.ExchangeTokenForRemoteCluster(
		ctx, req.SubjectToken, req.Subject, translateExchangeConfig(req.Config),
	)
	if err != nil {
		return aggregator.Token{}, err
	}
	return aggregator.Token{
		AccessToken: accessToken,
		TokenType:   tokenTypeFromIssued(issuedTokenType),
	}, nil
}

// tokenTypeFromIssued maps the RFC 8693 §2.2.1 issued_token_type URI to the
// HTTP Authorization scheme. Empty or unknown values fall back to Bearer.
func tokenTypeFromIssued(issued string) string {
	switch issued {
	case "urn:ietf:params:oauth:token-type:access_token", "":
		return "Bearer"
	default:
		return issued
	}
}

// translateExchangeConfig maps the port-owned [aggregator.ExchangeConfig]
// to [api.TokenExchangeConfig]. Enabled is hard-set: the port does not
// model the flag because gating happens consumer-side; the broker still
// checks defensively.
func translateExchangeConfig(cfg aggregator.ExchangeConfig) *api.TokenExchangeConfig {
	return &api.TokenExchangeConfig{
		Enabled:          true,
		DexTokenEndpoint: cfg.TokenEndpoint,
		ExpectedIssuer:   cfg.ExpectedIssuer,
		ConnectorID:      cfg.ConnectorID,
		ClientID:         cfg.ClientID,
		ClientSecret:     cfg.ClientSecret,
		Scopes:           cfg.Scopes,
	}
}

func (a *InProcess) InvalidateToken(_ context.Context, sessionID, issuer string) error {
	if a.manager == nil {
		return aggregator.ErrBrokerDisabled
	}
	a.manager.ClearTokenByIssuer(sessionID, issuer)
	return nil
}

func (a *InProcess) SessionIssuer(ctx context.Context, sessionID string) (string, error) {
	if a.manager == nil {
		return "", aggregator.ErrBrokerDisabled
	}
	return a.manager.SessionIssuer(ctx, sessionID)
}
