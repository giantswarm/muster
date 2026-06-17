package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
)

// stubCredentialsHandler is a SecretCredentialsHandler test double.
type stubCredentialsHandler struct {
	creds            *api.ClientCredentials
	err              error
	gotRef           *api.ClientCredentialsSecretRef
	gotDefaultNS     string
	loadCallObserved bool
}

func (s *stubCredentialsHandler) LoadClientCredentials(_ context.Context, ref *api.ClientCredentialsSecretRef, defaultNamespace string) (*api.ClientCredentials, error) {
	s.loadCallObserved = true
	s.gotRef = ref
	s.gotDefaultNS = defaultNamespace
	if s.err != nil {
		return nil, s.err
	}
	return s.creds, nil
}

func (s *stubCredentialsHandler) LoadSecretKey(_ context.Context, _ *api.ClientCredentialsSecretRef, _ string, _ string) ([]byte, error) {
	return nil, fmt.Errorf("LoadSecretKey not implemented in stubCredentialsHandler")
}

// withCredentialsHandler registers h for the duration of the test and restores
// the previous handler afterwards (the registry is a package-level global).
func withCredentialsHandler(t *testing.T, h api.SecretCredentialsHandler) {
	t.Helper()
	prev := api.GetSecretCredentialsHandler()
	api.RegisterSecretCredentialsHandler(h)
	t.Cleanup(func() { api.RegisterSecretCredentialsHandler(prev) })
}

func newTestBroker(cfg config.TokenExchangeBrokerConfig, httpClient *http.Client) *BrokerExchanger {
	b := NewBrokerExchanger(cfg, nil)
	if httpClient != nil {
		b.exchanger = NewTokenExchangerWithOptions(TokenExchangerOptions{
			AllowPrivateIP: true,
			HTTPClient:     httpClient,
		})
	}
	return b
}

func subjectIdentity(sub string) *oauthserver.SubjectIdentity {
	return &oauthserver.SubjectIdentity{Subject: sub, Issuer: "https://dex.main.example.com"}
}

func TestBrokerExchanger_UnknownAudience(t *testing.T) {
	broker := newTestBroker(config.TokenExchangeBrokerConfig{
		Targets: map[string]config.BrokerTargetConfig{},
	}, nil)

	_, err := broker.Exchange(t.Context(), &oauthserver.ExchangerRequest{
		Audience:     "unmapped",
		Subject:      subjectIdentity("user-1"),
		SubjectToken: "subject-token",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, oauthserver.ErrInvalidTarget), "unmapped audience must wrap ErrInvalidTarget, got: %v", err)
}

func TestBrokerExchanger_Exchange(t *testing.T) {
	var gotForm map[string]string
	var gotUser, gotPass string
	var gotBasicAuth bool

	downstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		gotForm = map[string]string{}
		for k := range r.PostForm {
			gotForm[k] = r.PostForm.Get(k)
		}
		gotUser, gotPass, gotBasicAuth = r.BasicAuth()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":      "downstream-token",
			"issued_token_type": "urn:ietf:params:oauth:token-type:access_token",
			"token_type":        "bearer",
			"expires_in":        1800,
		})
	}))
	defer downstream.Close()

	handler := &stubCredentialsHandler{
		creds: &api.ClientCredentials{ClientID: "exchange-client", ClientSecret: "exchange-secret"},
	}
	withCredentialsHandler(t, handler)

	scopes := "openid profile email groups audience:server:client_id:dex-k8s-authenticator"
	broker := newTestBroker(config.TokenExchangeBrokerConfig{
		Targets: map[string]config.BrokerTargetConfig{
			"cluster-a": {
				DexTokenEndpoint: downstream.URL + "/token",
				ConnectorID:      "main-dex",
				Scopes:           scopes,
				ClientCredentialsSecretRef: &config.BrokerSecretRefConfig{
					Name: "muster-token-exchange-cluster-a",
				},
			},
		},
		DefaultSecretNamespace: "muster-system",
	}, downstream.Client())

	result, err := broker.Exchange(t.Context(), &oauthserver.ExchangerRequest{
		Audience:         "cluster-a",
		ClientID:         "portal-backend",
		Subject:          subjectIdentity("user-1"),
		SubjectToken:     "main-dex-id-token",
		SubjectTokenType: "urn:ietf:params:oauth:token-type:id_token",
		Scope:            "client-requested-scope-must-be-ignored",
	})
	require.NoError(t, err)

	// Result propagation.
	assert.Equal(t, "downstream-token", result.AccessToken)
	assert.Equal(t, "urn:ietf:params:oauth:token-type:access_token", result.IssuedTokenType)
	assert.WithinDuration(t, time.Now().Add(1800*time.Second), result.ExpiresAt, 30*time.Second)

	// Downstream request: subject token forwarded verbatim, operator-configured
	// scopes (not the client-requested ones), connector pinned, creds from secret.
	assert.Equal(t, "urn:ietf:params:oauth:grant-type:token-exchange", gotForm["grant_type"])
	assert.Equal(t, "main-dex-id-token", gotForm["subject_token"])
	assert.Equal(t, "urn:ietf:params:oauth:token-type:id_token", gotForm["subject_token_type"])
	assert.Equal(t, "main-dex", gotForm["connector_id"])
	assert.Equal(t, scopes, gotForm["scope"])
	assert.True(t, gotBasicAuth)
	assert.Equal(t, "exchange-client", gotUser)
	assert.Equal(t, "exchange-secret", gotPass)

	// Secret resolution went through the registered handler with the broker's
	// default namespace.
	require.NotNil(t, handler.gotRef)
	assert.Equal(t, "muster-token-exchange-cluster-a", handler.gotRef.Name)
	assert.Equal(t, "muster-system", handler.gotDefaultNS)

	// Second exchange for the same user is served from the per-(endpoint,
	// connector, user) cache and keeps a usable expiry.
	cached, err := broker.Exchange(t.Context(), &oauthserver.ExchangerRequest{
		Audience:         "cluster-a",
		ClientID:         "portal-backend",
		Subject:          subjectIdentity("user-1"),
		SubjectToken:     "main-dex-id-token",
		SubjectTokenType: "urn:ietf:params:oauth:token-type:id_token",
	})
	require.NoError(t, err)
	assert.Equal(t, "downstream-token", cached.AccessToken)
	assert.True(t, cached.ExpiresAt.After(time.Now()))
	stats := broker.exchanger.GetCacheStats()
	assert.Equal(t, 1, stats.CurrentEntries)
}

func TestBrokerExchanger_CredentialsErrors(t *testing.T) {
	target := config.BrokerTargetConfig{
		DexTokenEndpoint: "https://dex.cluster-a.example.com/token",
		ConnectorID:      "main-dex",
		ClientCredentialsSecretRef: &config.BrokerSecretRefConfig{
			Name: "muster-token-exchange-cluster-a",
		},
	}
	cfg := config.TokenExchangeBrokerConfig{
		Targets: map[string]config.BrokerTargetConfig{"cluster-a": target},
	}
	req := &oauthserver.ExchangerRequest{
		Audience:     "cluster-a",
		Subject:      subjectIdentity("user-1"),
		SubjectToken: "subject-token",
	}

	t.Run("no handler registered", func(t *testing.T) {
		withCredentialsHandler(t, nil)
		_, err := newTestBroker(cfg, nil).Exchange(t.Context(), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no secret credentials handler registered")
		assert.False(t, errors.Is(err, oauthserver.ErrInvalidTarget))
	})

	t.Run("secret load failure", func(t *testing.T) {
		withCredentialsHandler(t, &stubCredentialsHandler{err: fmt.Errorf("secret not found")})
		_, err := newTestBroker(cfg, nil).Exchange(t.Context(), req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "secret not found")
		assert.False(t, errors.Is(err, oauthserver.ErrInvalidTarget))
	})
}

func TestBrokerExchanger_DownstreamError(t *testing.T) {
	downstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
	}))
	defer downstream.Close()

	broker := newTestBroker(config.TokenExchangeBrokerConfig{
		Targets: map[string]config.BrokerTargetConfig{
			"cluster-a": {
				DexTokenEndpoint: downstream.URL + "/token",
				ConnectorID:      "main-dex",
			},
		},
	}, downstream.Client())

	_, err := broker.Exchange(t.Context(), &oauthserver.ExchangerRequest{
		Audience:     "cluster-a",
		Subject:      subjectIdentity("user-1"),
		SubjectToken: "subject-token",
	})
	require.Error(t, err)
	// Downstream failures must not map to invalid_target; mcp-oauth reports
	// them to the client as a generic invalid_grant.
	assert.False(t, errors.Is(err, oauthserver.ErrInvalidTarget))
}

// funcProvider is a CredentialProvider backed by a function, used in tests to
// capture or stub Mint calls without a full provider implementation.
type funcProvider struct {
	fn func(context.Context, MintRequest) (*MintResult, error)
}

func (p *funcProvider) Mint(ctx context.Context, req MintRequest) (*MintResult, error) {
	return p.fn(ctx, req)
}

// TestBrokerExchanger_DelegatedExchange_ActorThreaded verifies that the RFC 8693
// §4.4 acting party (ExchangerRequest.Actor) is forwarded to MintRequest.Actor
// without modification through the broker dispatch.
func TestBrokerExchanger_DelegatedExchange_ActorThreaded(t *testing.T) {
	var capturedReq MintRequest

	broker := newTestBroker(config.TokenExchangeBrokerConfig{
		Targets: map[string]config.BrokerTargetConfig{
			"cluster-a": {Type: config.TargetTypeOIDCExchange},
		},
	}, nil)
	broker.registry.factories[config.TargetTypeOIDCExchange] = func(_ config.BrokerTargetConfig, _ providerDeps) CredentialProvider {
		return &funcProvider{fn: func(_ context.Context, req MintRequest) (*MintResult, error) {
			capturedReq = req
			return &MintResult{
				AccessToken:     "delegated-token",
				IssuedTokenType: issuedTokenType,
				ExpiresAt:       time.Now().Add(time.Hour),
			}, nil
		}}
	}

	actor := subjectIdentity("system:serviceaccount:default:agent-sa")
	result, err := broker.Exchange(t.Context(), &oauthserver.ExchangerRequest{
		Audience:         "cluster-a",
		Subject:          subjectIdentity("user-1"),
		SubjectToken:     "subject-token",
		SubjectTokenType: "urn:ietf:params:oauth:token-type:id_token",
		Actor:            actor,
	})
	require.NoError(t, err)
	assert.Equal(t, "delegated-token", result.AccessToken)

	require.NotNil(t, capturedReq.Actor, "Actor must be threaded through broker dispatch")
	assert.Equal(t, actor.Subject, capturedReq.Actor.Subject)
	assert.Equal(t, actor.Issuer, capturedReq.Actor.Issuer)
	assert.Equal(t, "user-1", capturedReq.Subject)
}

func TestTokenExchangeBrokerConfig_Enabled(t *testing.T) {
	assert.False(t, config.TokenExchangeBrokerConfig{}.Enabled())
	assert.True(t, config.TokenExchangeBrokerConfig{
		Targets: map[string]config.BrokerTargetConfig{"a": {}},
	}.Enabled())
}
