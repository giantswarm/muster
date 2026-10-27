package oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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

// The person's login ID token is the one that lives longest among their live
// sessions, read when asked: a session's revocation or a sign out everywhere
// takes it out at once.
func TestAdapter_PersonLoginIDToken_LongestLivedOfThePersonsSessions(t *testing.T) {
	manager := NewManager(config.OAuthMCPClientConfig{
		Enabled: true, PublicURL: "https://muster.example.com", ClientID: "client-id", CallbackPath: "/oauth/proxy/callback",
	})
	defer manager.Stop()
	adapter := NewAdapter(manager)
	const issuer = "https://dex.example.com/dex"
	now := time.Now()

	if got := adapter.PersonLoginIDToken("alice", issuer, ""); got != nil {
		t.Fatalf("person token before any login = %+v, want nil", got)
	}
	api.StoreLoginIDToken(adapter, "cli", "alice", issuer, "cli-token", now.Add(2*time.Hour))
	api.StoreLoginIDToken(adapter, "portal", "alice", issuer, "portal-token", now.Add(time.Hour))
	api.StoreLoginIDToken(adapter, "copy", "", issuer, "copy-token", now.Add(5*time.Hour))
	api.StoreLoginIDToken(adapter, "bob-cli", "bob", issuer, "bob-token", now.Add(4*time.Hour))
	manager.StoreToken("grant", "alice", issuer, &pkgoauth.Token{AccessToken: "at", IDToken: "grant-id", ExpiresAt: now.Add(6 * time.Hour), Issuer: issuer})
	if got := api.PersonLoginIDToken(adapter, "alice", issuer, ""); got == nil || got.IDToken != "cli-token" {
		t.Fatalf("person token = %+v, want the login that lives longest, no copy, grant or other person", got)
	}
	if got := api.PersonLoginIDToken(adapter, "alice", "https://other.example.com", ""); got != nil {
		t.Fatalf("another issuer = %+v, want nil", got)
	}

	manager.DeleteTokensBySession("cli") // the CLI login is revoked
	if got := api.PersonLoginIDToken(adapter, "alice", issuer, ""); got == nil || got.IDToken != "portal-token" {
		t.Fatalf("person token after a revocation = %+v, want the remaining login's", got)
	}

	api.ClearLoginIDToken(adapter, "copy", issuer)
	if got := api.LoginIDToken(adapter, "copy", issuer); got != nil && got.IDToken == "copy-token" {
		t.Fatalf("cleared login token = %+v, want gone", got)
	}
	if got := api.PersonLoginIDToken(adapter, "", issuer, ""); got != nil {
		t.Fatalf("no subject = %+v, want nil", got)
	}
	if got := api.PersonLoginIDToken(&legacyHandler{}, "alice", issuer, ""); got != nil {
		t.Fatalf("a handler without the mirror = %+v, want nil", got)
	}
	api.ClearLoginIDToken(&legacyHandler{}, "copy", issuer) // no mirror: nothing to do

	manager.client.tokenStore.DeleteByUser("alice")
	if got := api.PersonLoginIDToken(adapter, "alice", issuer, ""); got != nil {
		t.Fatalf("person token after sign out everywhere = %+v, want nil", got)
	}
}

// A login without the audience the caller needs is never the person's token
// for that caller, however long it lives: muster's own connection to a server
// it fronts forwards a token minted for another client back into muster, and
// it is filed as a login of the person.
func TestAdapter_PersonLoginIDToken_OnlyALoginCarryingTheAudience(t *testing.T) {
	manager := NewManager(config.OAuthMCPClientConfig{
		Enabled: true, PublicURL: "https://muster.example.com", ClientID: "client-id", CallbackPath: "/oauth/proxy/callback",
	})
	defer manager.Stop()
	adapter := NewAdapter(manager)
	const issuer = "https://dex.example.com/dex"
	now := time.Now()
	kube := audienceJWT(t, "muster", "dex-k8s-authenticator")
	forwarded := audienceJWT(t, "agent-platform")

	api.StoreLoginIDToken(adapter, "login", "alice", issuer, kube, now.Add(time.Hour))
	api.StoreLoginIDToken(adapter, "forwarded", "alice", issuer, forwarded, now.Add(2*time.Hour))

	require.Equal(t, kube, api.PersonLoginIDToken(adapter, "alice", issuer, "dex-k8s-authenticator").IDToken)
	require.Equal(t, forwarded, api.PersonLoginIDToken(adapter, "alice", issuer, "").IDToken,
		"without an audience to carry the longest-lived login serves")

	manager.DeleteTokensBySession("login")
	require.Nil(t, api.PersonLoginIDToken(adapter, "alice", issuer, "dex-k8s-authenticator"))
}

func audienceJWT(t *testing.T, audiences ...string) string {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"sub": "alice", "aud": audiences, "exp": time.Now().Add(time.Hour).Unix()})
	require.NoError(t, err)
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}
