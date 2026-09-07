package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
	pkgoauth "github.com/giantswarm/muster/pkg/oauth"
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

	token, err := client.ExchangeCode(context.Background(), "code", "verifier", server.URL, "", "openid profile email")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if token.Scope != "openid profile email" {
		t.Errorf("scope = %q, want the requested scope when the response omits one", token.Scope)
	}

	responseScope = "openid"
	token, err = client.ExchangeCode(context.Background(), "code", "verifier", server.URL, "", "openid profile email")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if token.Scope != "openid" {
		t.Errorf("scope = %q, want the scope the response granted", token.Scope)
	}
}
