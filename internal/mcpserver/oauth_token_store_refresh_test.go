package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newRefreshingAuthServer serves authorization server metadata naming its own
// URL as the issuer and a token endpoint that answers every refresh with
// "refreshed-access-token".
func newRefreshingAuthServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 srv.URL,
			"authorization_endpoint": srv.URL + "/authorize",
			"token_endpoint":         srv.URL + "/token",
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") != "refresh_token" {
			http.Error(w, "unexpected grant", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "refreshed-access-token",
			"token_type":    "Bearer",
			"refresh_token": "rotated-refresh-token",
			"expires_in":    3600,
		})
	})
	return srv
}

// TestMusterTokenStore_MCPGoRefreshesAStoredToken pins that mcp-go refreshes
// an expired token the store holds under a namespaced issuer key: mcp-go
// refreshes only a token whose issuer is the metadata's, so the store hands it
// the plain issuer, not the key.
func TestMusterTokenStore_MCPGoRefreshesAStoredToken(t *testing.T) {
	srv := newRefreshingAuthServer(t)
	issuerKey := api.IssuerKey("org-a", srv.URL)
	require.NotEqual(t, srv.URL, issuerKey)

	handler := &stubOAuthHandler{
		enabled: true,
		fullToken: &api.OAuthToken{
			AccessToken:  "expired-access-token",
			RefreshToken: "stored-refresh-token",
			ExpiresAt:    time.Now().Add(-time.Minute),
		},
	}
	store := NewMusterTokenStore("session-1", "test-user", issuerKey, handler)

	oauth := transport.NewOAuthHandler(transport.OAuthConfig{
		ClientID:              "muster",
		TokenStore:            store,
		AuthServerMetadataURL: srv.URL + "/.well-known/oauth-authorization-server",
	})

	header, err := oauth.GetAuthorizationHeader(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "Bearer refreshed-access-token", header)

	saved := handler.storedTokens["session-1|"+issuerKey]
	require.NotNil(t, saved, "the refreshed token is stored under the store's key")
	assert.Equal(t, "rotated-refresh-token", saved.RefreshToken)
}
