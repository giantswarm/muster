package tokenbroker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

	require.False(t, a.Enabled())

	_, err := a.GetToken(ctx, "sid", "https://idp")
	require.ErrorIs(t, err, aggregator.ErrBrokerDisabled)

	_, err = a.SessionIssuer(ctx, "sid")
	require.ErrorIs(t, err, aggregator.ErrBrokerDisabled)

	_, err = a.BeginOAuthFlow(ctx, aggregator.BeginRequest{SessionID: "sid"})
	require.ErrorIs(t, err, aggregator.ErrBrokerDisabled)

	require.ErrorIs(t, a.InvalidateToken(ctx, "sid", "https://idp"), aggregator.ErrBrokerDisabled)
}

func TestInProcess_Enabled_TracksManagerState(t *testing.T) {
	m := newManagerForTest(t)
	require.True(t, NewInProcess(m).Enabled(), "active manager should be enabled")
	require.False(t, NewInProcess(nil).Enabled(), "nil manager should be disabled")
}

func TestInProcess_BeginOAuthFlow_BuildsAuthURL(t *testing.T) {
	// Spin up a synthetic OAuth metadata endpoint so the broker's
	// authorization-URL generation can complete without external network.
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != pkgoauth.WellKnownAuthorizationServer {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pkgoauth.Metadata{
			Issuer:                        server.URL,
			AuthorizationEndpoint:         server.URL + "/authorize",
			TokenEndpoint:                 server.URL + "/token",
			CodeChallengeMethodsSupported: []string{"S256"},
		})
	}))
	t.Cleanup(server.Close)

	m := newManagerForTest(t)
	a := NewInProcess(m)

	flow, err := a.BeginOAuthFlow(context.Background(), aggregator.BeginRequest{
		SessionID:  "session-flow",
		Subject:    "alice",
		ServerName: "mcp-kubernetes",
		Issuer:     server.URL,
		Scope:      "openid profile",
	})
	require.NoError(t, err)
	require.Contains(t, flow.URL, server.URL+"/authorize", "auth URL should target the issuer's authorize endpoint")
	require.Equal(t, "mcp-kubernetes", flow.ServerName)
	require.NotEmpty(t, flow.Message)
}

func TestInProcess_InvalidateToken_EvictsFromCache(t *testing.T) {
	m := newManagerForTest(t)
	a := NewInProcess(m)
	ctx := context.Background()

	const (
		sessionID = "session-invalidate"
		userID    = "bob"
		issuer    = "https://dex.example.com"
	)
	m.StoreToken(sessionID, userID, issuer, &pkgoauth.Token{
		AccessToken: "access",
		ExpiresAt:   time.Now().Add(time.Hour),
		Issuer:      issuer,
	})

	// Precondition: token reachable.
	got, err := a.GetToken(ctx, sessionID, issuer)
	require.NoError(t, err)
	require.Equal(t, "access", got.AccessToken)

	require.NoError(t, a.InvalidateToken(ctx, sessionID, issuer))

	// Postcondition: gone.
	_, err = a.GetToken(ctx, sessionID, issuer)
	require.ErrorIs(t, err, aggregator.ErrTokenNotFound)
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
