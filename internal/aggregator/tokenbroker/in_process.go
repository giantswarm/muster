package tokenbroker

import (
	"context"
	"fmt"

	"github.com/giantswarm/muster/internal/aggregator"
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

func (a *InProcess) SessionIssuer(ctx context.Context, sessionID string) (string, error) {
	if a.manager == nil {
		return "", aggregator.ErrBrokerDisabled
	}
	return a.manager.SessionIssuer(ctx, sessionID)
}
