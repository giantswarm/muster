package tokenbroker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/aggregator"
	"github.com/giantswarm/muster/internal/broker"
	"github.com/giantswarm/muster/internal/config"
	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
)

func newManagerForTest(t *testing.T) *broker.Manager {
	t.Helper()
	m := broker.NewManager(config.OAuthMCPClientConfig{
		Enabled:      true,
		PublicURL:    "https://muster.test",
		ClientID:     "muster-test",
		CallbackPath: "/oauth/proxy/callback",
	})
	require.NotNil(t, m, "NewManager returned nil")
	t.Cleanup(m.Stop)
	return m
}

func TestInProcess_NilManager_ReturnsBrokerDisabled(t *testing.T) {
	a := NewInProcess(nil)
	ctx := context.Background()

	_, err := a.GetToken(ctx, "sid", "https://idp")
	require.ErrorIs(t, err, aggregator.ErrBrokerDisabled)

	_, err = a.SessionIssuer(ctx, "sid")
	require.ErrorIs(t, err, aggregator.ErrBrokerDisabled)
}

func TestInProcess_GetToken_RoundTrip(t *testing.T) {
	m := newManagerForTest(t)
	a := NewInProcess(m)
	ctx := context.Background()

	const (
		sessionID = "session-1"
		userID    = "alice"
		issuer    = "https://dex.example.com"
	)
	stored := &pkgoauth.Token{
		AccessToken: "access-1",
		TokenType:   "Bearer",
		ExpiresAt:   time.Now().Add(1 * time.Hour),
		Scope:       "openid",
		IDToken:     "id-1",
		Issuer:      issuer,
	}
	m.StoreToken(sessionID, userID, issuer, stored)

	got, err := a.GetToken(ctx, sessionID, issuer)
	require.NoError(t, err)
	require.Equal(t, "access-1", got.AccessToken)
	require.Equal(t, "id-1", got.IDToken)
	require.Equal(t, issuer, got.Issuer)

	_, err = a.GetToken(ctx, sessionID, "https://other-idp")
	require.ErrorIs(t, err, aggregator.ErrTokenNotFound)
}

func TestInProcess_SessionIssuer_RoundTrip(t *testing.T) {
	m := newManagerForTest(t)
	a := NewInProcess(m)
	ctx := context.Background()

	const (
		sessionID = "session-2"
		userID    = "bob"
		issuer    = "https://dex.example.com"
	)
	m.StoreToken(sessionID, userID, issuer, &pkgoauth.Token{
		AccessToken: "access-2",
		ExpiresAt:   time.Now().Add(1 * time.Hour),
		IDToken:     "id-2",
		Issuer:      issuer,
	})

	got, err := a.SessionIssuer(ctx, sessionID)
	require.NoError(t, err)
	require.Equal(t, issuer, got)

	_, err = a.SessionIssuer(ctx, "unknown-session")
	require.True(t, errors.Is(err, broker.ErrSessionUnknown))
}
