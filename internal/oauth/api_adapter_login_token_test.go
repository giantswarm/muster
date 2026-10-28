package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

// The session's login ID token (what SSO forwarding hands to backends) and a
// proxy grant from the same issuer must coexist: an MCPServer may pin muster's
// own IdP as its authorization server (giantswarm/muster#1174).
func TestAdapter_LoginIDToken_KeptApartFromGrantOfSameIssuer(t *testing.T) {
	manager := NewManager(config.OAuthMCPClientConfig{
		Enabled: true, PublicURL: "https://muster.example.com", ClientID: "client-id", CallbackPath: "/oauth/proxy/callback",
	})
	defer manager.Stop()
	adapter := NewAdapter(manager)
	const issuer = "https://dex.example.com/dex"
	exp := time.Now().Add(time.Hour)

	// The login token is mirrored on every request that carries an external
	// bearer; the grant arrives from the proxy callback with the scope Dex
	// omitted (an empty scope, before the RFC 6749 §5.1 fallback).
	api.StoreLoginIDToken(adapter, "sess", "user", issuer, "login-id-token", exp)
	manager.StoreToken("sess", "user", issuer, &pkgoauth.Token{AccessToken: "grant-at", TokenType: "Bearer", IDToken: "grant-id-token", ExpiresAt: exp, Issuer: issuer})
	api.StoreLoginIDToken(adapter, "sess", "user", issuer, "login-id-token-2", exp)

	// The per-server client finds the grant ...
	got := api.FullTokenByIssuerForUser(adapter, "sess", "user", issuer)
	if got == nil || got.AccessToken != "grant-at" {
		t.Fatalf("grant lookup = %+v, want the grant's access token", got)
	}
	// ... and SSO forwarding finds the (latest) login token, not the grant's id_token.
	if login := api.LoginIDToken(adapter, "sess", issuer); login == nil || login.IDToken != "login-id-token-2" {
		t.Fatalf("login token = %+v, want the mirrored login ID token", login)
	}

	// A handler without the mirror falls back to the issuer lookup: the entry
	// an earlier muster stored under the plain issuer.
	plain := &api.OAuthToken{IDToken: "legacy-id-token", ExpiresAt: exp}
	legacy := &legacyHandler{token: plain}
	if got := api.LoginIDToken(legacy, "sess", issuer); got != plain {
		t.Fatalf("fallback = %+v, want the plain issuer lookup", got)
	}
	api.StoreLoginIDToken(legacy, "sess", "user", issuer, "x", exp)
	if legacy.stored == nil || legacy.stored.IDToken != "x" {
		t.Fatalf("fallback store = %+v, want StoreToken", legacy.stored)
	}
}

// legacyHandler is an api.OAuthHandler without the login-token mirror.
type legacyHandler struct {
	api.OAuthHandler
	token  *api.OAuthToken
	stored *api.OAuthToken
}

func (h *legacyHandler) GetFullTokenByIssuer(string, string) *api.OAuthToken { return h.token }
func (h *legacyHandler) StoreToken(_, _, _ string, token *api.OAuthToken)    { h.stored = token }

// RFC 6749 §5.1: a token response without `scope` granted the requested scope.
func TestClient_ExchangeCode_ScopeFromRequestWhenOmitted(t *testing.T) {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"issuer":                           server.URL,
			"authorization_endpoint":           server.URL + "/authorize",
			"token_endpoint":                   server.URL + "/token",
			"response_types_supported":         []string{"code"},
			"code_challenge_methods_supported": []string{"S256"},
		})
	})
	var responseScope string
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]interface{}{"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "refresh_token": "rt"}
		if responseScope != "" {
			resp["scope"] = responseScope
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	client := NewClient("client-id", "https://muster.example.com", "/oauth/proxy/callback", "openid")
	defer client.Stop()

	token, err := client.ExchangeCode(context.Background(), &OAuthState{CodeVerifier: "verifier", Issuer: server.URL, Scope: "openid profile email"}, "code")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if token.Scope != "openid profile email" {
		t.Errorf("scope = %q, want the requested scope when the response omits one", token.Scope)
	}

	responseScope = "openid"
	token, err = client.ExchangeCode(context.Background(), &OAuthState{CodeVerifier: "verifier", Issuer: server.URL, Scope: "openid profile email"}, "code")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if token.Scope != "openid" {
		t.Errorf("scope = %q, want the scope the response granted", token.Scope)
	}
}

// newLoginTestAdapter returns an adapter on the backend's token store and a
// restart that returns a new adapter on the same stored state.
func newLoginTestAdapter(t *testing.T, backend string) (*Adapter, func() *Adapter) {
	t.Helper()
	newAdapter := func() *Adapter {
		manager := NewManager(config.OAuthMCPClientConfig{
			Enabled: true, PublicURL: "https://muster.example.com", ClientID: "client-id", CallbackPath: "/oauth/proxy/callback",
		})
		t.Cleanup(manager.Stop)
		return NewAdapter(manager)
	}
	if backend == "valkey" {
		srv := miniredis.RunT(t)
		newAdapter = func() *Adapter {
			client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
			require.NoError(t, err)
			t.Cleanup(client.Close)
			manager := NewManager(config.OAuthMCPClientConfig{
				Enabled: true, PublicURL: "https://muster.example.com", ClientID: "client-id", CallbackPath: "/oauth/proxy/callback",
			}, WithValkeyTokenStore(NewValkeyTokenStore(client, time.Hour, "muster:", nil)))
			t.Cleanup(manager.Stop)
			return NewAdapter(manager)
		}
	}
	return newAdapter(), newAdapter
}

// A bind request's bearer names the session that filed it as its login: the
// login family muster signed in, never an actor's copy, and a family over a
// forwarded session that filed the same token (muster's own connection to a
// server it fronts forwards the family's token back into muster).
func TestAdapter_LoginSessionForIDToken(t *testing.T) {
	for _, backend := range []string{"memory", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			adapter, restart := newLoginTestAdapter(t, backend)
			const issuer = "https://dex.example.com/dex"
			exp := time.Now().Add(time.Hour)
			familyToken := audienceJWT(t, "muster", "dex-k8s-authenticator")
			portalToken := audienceJWT(t, "backstage")

			require.Empty(t, api.LoginSessionForIDToken(adapter, familyToken), "no session filed it yet")

			api.StoreLoginIDToken(adapter, "family-1", "alice", issuer, familyToken, exp)
			api.StoreLoginIDToken(adapter, "ext-reentered", "alice", issuer, familyToken, exp)
			api.StoreLoginIDToken(adapter, "actor-0123", "", issuer, familyToken, exp)
			api.StoreLoginIDToken(adapter, "actor-0123", "alice", issuer, familyToken, exp)
			require.Equal(t, "family-1", api.LoginSessionForIDToken(adapter, familyToken), "an actor's copy is never indexed, whoever it is filed for")

			api.StoreLoginIDToken(adapter, "ext-portal", "alice@example.com", issuer, portalToken, exp)
			require.Equal(t, "ext-portal", api.LoginSessionForIDToken(adapter, portalToken))

			refreshed := audienceJWT(t, "muster", "dex-k8s-authenticator", "refreshed")
			api.StoreLoginIDToken(adapter, "ext-reentered", "alice", issuer, refreshed, exp)
			api.StoreLoginIDToken(adapter, "family-1", "alice", issuer, refreshed, exp)
			require.Equal(t, "family-1", api.LoginSessionForIDToken(adapter, refreshed), "a family takes the record from a forwarded session")

			expired := audienceJWT(t, "kagent")
			api.StoreLoginIDToken(adapter, "family-3", "alice", issuer, expired, time.Now().Add(-time.Minute))
			require.Empty(t, api.LoginSessionForIDToken(adapter, expired), "an expired login names no session")

			require.Empty(t, api.LoginSessionForIDToken(adapter, ""))
			require.Empty(t, api.LoginSessionForIDToken(&legacyHandler{}, familyToken), "a handler without the mirror")

			if backend == "valkey" {
				require.Equal(t, "family-1", api.LoginSessionForIDToken(restart(), familyToken), "the record outlives a restart")
			}
		})
	}
}

// The pinned session's login ID token is that session's own, never a grant
// another client obtained from the same issuer, and goes with the session.
func TestAdapter_SessionLoginIDToken(t *testing.T) {
	for _, backend := range []string{"memory", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			adapter, restart := newLoginTestAdapter(t, backend)
			const issuer = "https://dex.example.com/dex"
			exp := time.Now().Add(time.Hour)

			adapter.manager.StoreToken("family-1", "alice", issuer, &pkgoauth.Token{AccessToken: "at", IDToken: "grant-id", ExpiresAt: exp, Issuer: issuer})
			require.Nil(t, api.SessionLoginIDToken(adapter, "family-1", issuer), "a grant is no login")

			api.StoreLoginIDToken(adapter, "family-1", "alice", issuer, "login-id", exp)
			require.Equal(t, "login-id", api.SessionLoginIDToken(adapter, "family-1", issuer).IDToken)
			require.Nil(t, api.SessionLoginIDToken(adapter, "family-1", "https://other.example.com"))
			require.Nil(t, api.SessionLoginIDToken(adapter, "", issuer))
			require.Nil(t, api.SessionLoginIDToken(&legacyHandler{}, "family-1", issuer))
			if backend == "valkey" {
				adapter = restart()
				require.Equal(t, "login-id", api.SessionLoginIDToken(adapter, "family-1", issuer).IDToken, "the login outlives a restart")
			}

			adapter.manager.DeleteTokensBySession("family-1")
			require.Nil(t, api.SessionLoginIDToken(adapter, "family-1", issuer), "a revoked session's login is gone")

			api.StoreLoginIDToken(adapter, "actor-0123", "", issuer, "copy", exp)
			api.ClearLoginIDToken(adapter, "actor-0123", issuer)
			require.Nil(t, api.SessionLoginIDToken(adapter, "actor-0123", issuer))
			api.ClearLoginIDToken(&legacyHandler{}, "actor-0123", issuer)
		})
	}
}

func audienceJWT(t *testing.T, audiences ...string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"sub": "alice", "aud": audiences, "exp": time.Now().Add(time.Hour).Unix()})
	require.NoError(t, err)
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}
