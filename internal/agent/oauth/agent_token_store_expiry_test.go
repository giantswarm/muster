package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"

	"golang.org/x/oauth2"
)

// tokenServer is an authorization server whose token endpoint renews a
// refresh token, or refuses it with invalid_grant when refuse is set.
func tokenServer(t *testing.T, refuse bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var refreshes atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":                 server.URL,
				"authorization_endpoint": server.URL + "/authorize",
				"token_endpoint":         server.URL + "/token",
			})
		case "/token":
			refreshes.Add(1)
			if refuse {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "renewed-access-token",
				"token_type":    "Bearer",
				"refresh_token": "renewed-refresh-token",
				"expires_in":    3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, &refreshes
}

// connectWith stores token for the server, has mcp-go's OAuth handler build
// the Authorization header a connection sends, and reports what the store
// says afterwards -- the check `muster auth token` makes after connecting.
func connectWith(t *testing.T, server *httptest.Server, token *oauth2.Token) (header string, headerErr error, valid bool) {
	t.Helper()
	dir := t.TempDir()
	cfg, _, err := SetupOAuthConfigWithDir(server.URL, dir)
	if err != nil {
		t.Fatalf("SetupOAuthConfigWithDir failed: %v", err)
	}
	cfg.AuthServerMetadataURL = server.URL + "/.well-known/oauth-authorization-server"

	store, err := NewTokenStore(TokenStoreConfig{StorageDir: dir, FileMode: true})
	if err != nil {
		t.Fatalf("NewTokenStore failed: %v", err)
	}
	if err := store.StoreToken(server.URL, server.URL, token); err != nil {
		t.Fatalf("StoreToken failed: %v", err)
	}

	handler := transport.NewOAuthHandler(*cfg)
	handler.SetBaseURL(server.URL)
	header, headerErr = handler.GetAuthorizationHeader(context.Background())

	reread, err := NewTokenStore(TokenStoreConfig{StorageDir: dir, FileMode: true})
	if err != nil {
		t.Fatalf("NewTokenStore failed: %v", err)
	}
	return header, headerErr, reread.HasValidToken(server.URL)
}

// A token in the store's last minute -- not yet expired for the backend, no
// longer valid for the store -- is renewed by the connection, so the store
// holds a valid token after it and the person is never told to sign in.
func TestAgentTokenStore_TokenInItsLastMinuteIsRenewedOnConnect(t *testing.T) {
	server, refreshes := tokenServer(t, false)
	header, err, valid := connectWith(t, server, &oauth2.Token{
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(tokenExpiryBuffer / 2),
	})
	if err != nil {
		t.Fatalf("GetAuthorizationHeader failed: %v", err)
	}
	if header != "Bearer renewed-access-token" {
		t.Errorf("expected the renewed access token, got %q", header)
	}
	if refreshes.Load() != 1 {
		t.Errorf("expected one refresh, got %d", refreshes.Load())
	}
	if !valid {
		t.Error("expected the store to hold a valid token after the connection")
	}
}

// A token well inside its lifetime is used as it is.
func TestAgentTokenStore_ValidTokenIsNotRenewed(t *testing.T) {
	server, refreshes := tokenServer(t, false)
	header, err, valid := connectWith(t, server, &oauth2.Token{
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("GetAuthorizationHeader failed: %v", err)
	}
	if header != "Bearer old-access-token" {
		t.Errorf("expected the stored access token, got %q", header)
	}
	if refreshes.Load() != 0 {
		t.Errorf("expected no refresh, got %d", refreshes.Load())
	}
	if !valid {
		t.Error("expected the store to hold a valid token")
	}
}

// Without a usable grant -- no refresh token, or one the authorization
// server refuses -- the connection asks for a sign-in and the store holds no
// valid token, so the caller still answers auth_required.
func TestAgentTokenStore_NoUsableGrantStillRequiresSignIn(t *testing.T) {
	cases := map[string]struct {
		refuse       bool
		refreshToken string
	}{
		"no refresh token":      {refreshToken: ""},
		"refresh token refused": {refuse: true, refreshToken: "revoked-refresh-token"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			server, _ := tokenServer(t, tc.refuse)
			_, err, valid := connectWith(t, server, &oauth2.Token{
				AccessToken:  "old-access-token",
				RefreshToken: tc.refreshToken,
				TokenType:    "Bearer",
				Expiry:       time.Now().Add(tokenExpiryBuffer / 2),
			})
			if !errors.Is(err, transport.ErrOAuthAuthorizationRequired) {
				t.Errorf("expected ErrOAuthAuthorizationRequired, got %v", err)
			}
			if valid {
				t.Error("expected no valid token in the store")
			}
		})
	}
}

func TestValidUntil(t *testing.T) {
	if got := validUntil(time.Time{}); !got.IsZero() {
		t.Errorf("expected no expiry to stay zero, got %v", got)
	}
	expiry := time.Date(2026, 10, 9, 1, 0, 0, 0, time.UTC)
	if got := validUntil(expiry); !got.Equal(expiry.Add(-tokenExpiryBuffer)) {
		t.Errorf("expected %v, got %v", expiry.Add(-tokenExpiryBuffer), got)
	}
}
