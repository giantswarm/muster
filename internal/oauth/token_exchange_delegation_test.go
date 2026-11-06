package oauth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

func unsignedJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	require.NoError(t, err)
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"none"}`)) + "." + enc(payload) + "." + enc([]byte("sig"))
}

// A delegated exchange is an RFC 8693 delegation at the subject token's own
// Dex: the delegated token as an access-token subject, muster's workload token
// as actor_token, the backend as audience, no connector_id and no cross-client
// scope.
func TestTokenExchanger_ExchangeDelegation(t *testing.T) {
	var (
		mu    sync.Mutex
		forms []url.Values
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		mu.Lock()
		forms = append(forms, r.PostForm)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"access_token":      unsignedJWT(t, map[string]any{"iss": "https://" + r.Host}),
			"issued_token_type": "urn:ietf:params:oauth:token-type:id_token",
			"token_type":        "bearer",
			"expires_in":        600,
		}))
	}))
	defer srv.Close()

	exchanger := NewTokenExchangerWithOptions(TokenExchangerOptions{AllowPrivateIP: true, HTTPClient: srv.Client()})
	cfg := &api.TokenExchangeConfig{
		Enabled:          true,
		DexTokenEndpoint: srv.URL + "/token",
		Scopes:           "openid email audience:server:client_id:mcp-kubernetes",
		ClientID:         "muster",
		ClientSecret:     "secret",
		Delegation:       &api.TokenExchangeDelegation{ActorToken: "muster-workload-token", Audience: "mcp-kubernetes"},
	}

	_, err := exchanger.Exchange(t.Context(), &ExchangeRequest{Config: cfg, SubjectToken: "delegated-token", UserID: "delegated/ext-a/k8s"})
	require.NoError(t, err)
	require.Len(t, forms, 1)
	form := forms[0]
	require.Equal(t, "delegated-token", form.Get("subject_token"))
	require.Equal(t, "urn:ietf:params:oauth:token-type:access_token", form.Get("subject_token_type"))
	require.Equal(t, "muster-workload-token", form.Get("actor_token"))
	require.Equal(t, "urn:ietf:params:oauth:token-type:jwt", form.Get("actor_token_type"))
	require.Equal(t, "mcp-kubernetes", form.Get("audience"))
	require.Equal(t, "openid email", form.Get("scope"))
	require.NotContains(t, form, "connector_id")

	t.Run("cached per session and server", func(t *testing.T) {
		res, err := exchanger.Exchange(t.Context(), &ExchangeRequest{Config: cfg, SubjectToken: "delegated-token", UserID: "delegated/ext-a/k8s"})
		require.NoError(t, err)
		require.True(t, res.FromCache)
		_, err = exchanger.Exchange(t.Context(), &ExchangeRequest{Config: cfg, SubjectToken: "delegated-token", UserID: "delegated/ext-b/k8s"})
		require.NoError(t, err)
		require.Len(t, forms, 2, "another session exchanges its own token")
	})

	t.Run("an actor token is required", func(t *testing.T) {
		noActor := *cfg
		noActor.Delegation = &api.TokenExchangeDelegation{Audience: "mcp-kubernetes"}
		_, err := exchanger.Exchange(t.Context(), &ExchangeRequest{Config: &noActor, SubjectToken: "delegated-token", UserID: "delegated/ext-c/k8s"})
		require.ErrorContains(t, err, "actor token is required")
	})
}

// The person path exchanges exactly as before: the ID token through the
// configured connector, never with muster's workload token as actor.
func TestTokenExchanger_PersonExchangeSendsNoActor(t *testing.T) {
	var form url.Values
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		form = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"access_token": unsignedJWT(t, map[string]any{"iss": "https://" + r.Host}), "token_type": "bearer", "expires_in": 600,
		}))
	}))
	defer srv.Close()

	exchanger := NewTokenExchangerWithOptions(TokenExchangerOptions{AllowPrivateIP: true, HTTPClient: srv.Client()})
	cfg := &api.TokenExchangeConfig{Enabled: true, DexTokenEndpoint: srv.URL + "/token", ConnectorID: "local-dex", ClientID: "muster", ClientSecret: "secret",
		Scopes: "openid email audience:server:client_id:mcp-kubernetes"}
	_, err := exchanger.Exchange(t.Context(), &ExchangeRequest{Config: cfg, SubjectToken: "person-id-token", UserID: "alice"})
	require.NoError(t, err)
	require.Equal(t, "local-dex", form.Get("connector_id"))
	require.Equal(t, "urn:ietf:params:oauth:token-type:id_token", form.Get("subject_token_type"))
	require.Equal(t, "openid email audience:server:client_id:mcp-kubernetes", form.Get("scope"))
	require.NotContains(t, form, "actor_token")
	require.NotContains(t, form, "actor_token_type")
	require.NotContains(t, form, "audience")
}

// A person's exchange sends its configured connector and is refused before
// any request without one: only a delegated caller's exchange goes to the Dex
// that issued its token with no connector_id.
func TestTokenExchanger_PersonExchangeNeedsConnectorID(t *testing.T) {
	var forms []url.Values
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		forms = append(forms, r.PostForm)
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"access_token": unsignedJWT(t, map[string]any{"iss": "https://" + r.Host}), "token_type": "bearer", "expires_in": 600,
		}))
	}))
	defer srv.Close()
	exchanger := NewTokenExchangerWithOptions(TokenExchangerOptions{AllowPrivateIP: true, HTTPClient: srv.Client()})

	cfg := &api.TokenExchangeConfig{Enabled: true, DexTokenEndpoint: srv.URL + "/token"}
	_, err := exchanger.Exchange(t.Context(), &ExchangeRequest{Config: cfg, SubjectToken: "person-id-token", UserID: "alice"})
	require.ErrorContains(t, err, "connector ID is required")
	require.Empty(t, forms, "no request leaves muster")

	cfg.ConnectorID = "local-dex"
	_, err = exchanger.Exchange(t.Context(), &ExchangeRequest{Config: cfg, SubjectToken: "person-id-token", UserID: "alice"})
	require.NoError(t, err)
	require.Len(t, forms, 1)
	require.Equal(t, "local-dex", forms[0].Get("connector_id"))
}

// A refused exchange names the token endpoint once and says "token exchange
// failed" once: the exchange client's error already says it.
func TestTokenExchanger_RefusalNamesTheEndpointOnce(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant", "error_description": "subject token is expired"}))
	}))
	defer srv.Close()

	exchanger := NewTokenExchangerWithOptions(TokenExchangerOptions{AllowPrivateIP: true, HTTPClient: srv.Client()})
	cfg := &api.TokenExchangeConfig{Enabled: true, DexTokenEndpoint: srv.URL + "/token", ConnectorID: "local-dex", ClientID: "muster", ClientSecret: "secret"}
	_, err := exchanger.Exchange(t.Context(), &ExchangeRequest{Config: cfg, SubjectToken: "person-id-token", UserID: "alice"})
	require.Error(t, err)
	require.Equal(t, "token endpoint "+srv.URL+"/token: token exchange failed: invalid_grant - subject token is expired", err.Error())
	require.NotNil(t, errors.Unwrap(err), "the exchange client's error stays wrapped")
}
