package tokenbroker

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	a := NewInProcess(nil, nil)
	ctx := t.Context()

	require.False(t, a.Enabled())

	_, err := a.GetToken(ctx, "sid", "https://idp")
	require.ErrorIs(t, err, aggregator.ErrBrokerDisabled)

	_, err = a.SessionIssuer(ctx, "sid")
	require.ErrorIs(t, err, aggregator.ErrBrokerDisabled)

	_, err = a.BeginOAuthFlow(ctx, aggregator.BeginRequest{SessionID: "sid"})
	require.ErrorIs(t, err, aggregator.ErrBrokerDisabled)

	_, err = a.ExchangeToken(ctx, aggregator.ExchangeRequest{})
	require.ErrorIs(t, err, aggregator.ErrBrokerDisabled)

	require.ErrorIs(t, a.InvalidateToken(ctx, "sid", "https://idp"), aggregator.ErrBrokerDisabled)
}

func TestInProcess_Enabled_TracksManagerState(t *testing.T) {
	m := newManagerForTest(t)
	require.True(t, NewInProcess(m, nil).Enabled(), "active manager should be enabled")
	require.False(t, NewInProcess(nil, nil).Enabled(), "nil manager should be disabled")
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
	a := NewInProcess(m, nil)

	flow, err := a.BeginOAuthFlow(t.Context(), aggregator.BeginRequest{
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

	// Pin OAuth 2.1 / RFC 7636 / RFC 6749 shape on the produced authorization URL.
	parsed, err := url.Parse(flow.URL)
	require.NoError(t, err)
	q := parsed.Query()
	require.Equal(t, "code", q.Get("response_type"))
	require.Equal(t, "S256", q.Get("code_challenge_method"), "OAuth 2.1 forbids plain; S256 is the only accepted method")
	require.NotEmpty(t, q.Get("code_challenge"), "PKCE code_challenge must be present")
	require.NotEmpty(t, q.Get("state"), "state must be present for CSRF protection")
}

func TestInProcess_InvalidateToken_EvictsFromCache(t *testing.T) {
	m := newManagerForTest(t)
	a := NewInProcess(m, nil)
	ctx := t.Context()

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

	got, err := a.GetToken(ctx, sessionID, issuer)
	require.NoError(t, err)
	require.Equal(t, "access", got.AccessToken)

	require.NoError(t, a.InvalidateToken(ctx, sessionID, issuer))

	_, err = a.GetToken(ctx, sessionID, issuer)
	require.ErrorIs(t, err, aggregator.ErrTokenNotFound)
}

func TestInProcess_ExchangeToken_RejectsEmptyConfig(t *testing.T) {
	m := newManagerForTest(t)
	a := NewInProcess(m, nil)

	_, err := a.ExchangeToken(t.Context(), aggregator.ExchangeRequest{
		SessionID:    "sid",
		Subject:      "user",
		SubjectToken: "id-token",
		Audience:     "mcp-kubernetes",
		Config:       aggregator.ExchangeConfig{},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing token endpoint")
}

// captureTransportResolver records which audience was asked for, returns
// the prepared client (or nil to signal "use default").
type captureTransportResolver struct {
	audience string
	client   *http.Client
}

func (r *captureTransportResolver) HTTPClientFor(_ context.Context, audience string) (*http.Client, error) {
	r.audience = audience
	return r.client, nil
}

func TestInProcess_ExchangeToken_ConsultsResolver(t *testing.T) {
	m := newManagerForTest(t)
	resolver := &captureTransportResolver{client: nil}
	a := NewInProcess(m, resolver)

	// Best-effort: ExchangeToken will fail because the synthetic config has
	// no reachable endpoint. We only assert that the resolver was consulted
	// with the audience name before the broker tried to talk to the IdP.
	_, _ = a.ExchangeToken(t.Context(), aggregator.ExchangeRequest{
		SessionID:    "sid",
		Subject:      "user",
		SubjectToken: "id-token",
		Audience:     "mcp-kubernetes",
		Config: aggregator.ExchangeConfig{ //nolint:gosec // test fixture, not credentials
			TokenEndpoint: "http://127.0.0.1:1/token",
			ConnectorID:   "c",
		},
	})
	require.Equal(t, "mcp-kubernetes", resolver.audience, "resolver should be consulted with the audience")
}

// erroringTransportResolver always fails resolution.
type erroringTransportResolver struct {
	err error
}

func (r *erroringTransportResolver) HTTPClientFor(_ context.Context, _ string) (*http.Client, error) {
	return nil, r.err
}

// perAudienceTransportResolver returns a different *http.Client per audience.
type perAudienceTransportResolver struct {
	clients map[string]*http.Client
	calls   []string
}

func (r *perAudienceTransportResolver) HTTPClientFor(_ context.Context, audience string) (*http.Client, error) {
	r.calls = append(r.calls, audience)
	return r.clients[audience], nil
}

func TestInProcess_ExchangeToken_RoutesPerAudience(t *testing.T) {
	m := newManagerForTest(t)
	clientA := &http.Client{}
	clientB := &http.Client{}
	resolver := &perAudienceTransportResolver{clients: map[string]*http.Client{
		"audience-a": clientA,
		"audience-b": clientB,
	}}
	a := NewInProcess(m, resolver)

	ctx := t.Context()
	exchange := func(audience string) {
		_, _ = a.ExchangeToken(ctx, aggregator.ExchangeRequest{
			SessionID:    "sid",
			Subject:      "user",
			SubjectToken: "id-token",
			Audience:     audience,
			Config: aggregator.ExchangeConfig{ //nolint:gosec // test fixture, not credentials
				TokenEndpoint: "https://idp.test/token",
				ConnectorID:   "c",
			},
		})
	}
	exchange("audience-a")
	exchange("audience-b")
	exchange("audience-a")

	require.Equal(t, []string{"audience-a", "audience-b", "audience-a"}, resolver.calls,
		"resolver must be consulted with the per-call audience (regression: ignoring audience would return the same client every time)")
}

func TestInProcess_ExchangeToken_PropagatesResolverError(t *testing.T) {
	m := newManagerForTest(t)
	resolverErr := errors.New("teleport cert unavailable")
	a := NewInProcess(m, &erroringTransportResolver{err: resolverErr})

	_, err := a.ExchangeToken(t.Context(), aggregator.ExchangeRequest{
		SessionID:    "sid",
		Subject:      "user",
		SubjectToken: "id-token",
		Audience:     "mcp-kubernetes",
		Config: aggregator.ExchangeConfig{ //nolint:gosec // test fixture, not credentials
			TokenEndpoint: "http://127.0.0.1:1/token",
			ConnectorID:   "c",
		},
	})
	require.ErrorIs(t, err, resolverErr, "resolver failures must surface to callers (no silent fallback)")
}

func TestTranslateExchangeConfig_SetsEnabledAndCopiesFields(t *testing.T) {
	cfg := aggregator.ExchangeConfig{ //nolint:gosec // test fixture, not credentials
		TokenEndpoint:  "https://dex.example.com/token",
		ExpectedIssuer: "https://dex.example.com",
		ConnectorID:    "github",
		ClientID:       "muster",
		ClientSecret:   "shh",
		Scopes:         "openid profile",
	}

	got := translateExchangeConfig(cfg)

	require.True(t, got.Enabled, "broker validator rejects requests where !Config.Enabled; translation must set the flag")
	require.Equal(t, cfg.TokenEndpoint, got.DexTokenEndpoint)
	require.Equal(t, cfg.ExpectedIssuer, got.ExpectedIssuer)
	require.Equal(t, cfg.ConnectorID, got.ConnectorID)
	require.Equal(t, cfg.ClientID, got.ClientID)
	require.Equal(t, cfg.ClientSecret, got.ClientSecret)
	require.Equal(t, cfg.Scopes, got.Scopes)
}

func TestInProcess_GetToken_RoundTrip(t *testing.T) {
	m := newManagerForTest(t)
	a := NewInProcess(m, nil)
	ctx := t.Context()

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
	a := NewInProcess(m, nil)
	ctx := t.Context()

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
