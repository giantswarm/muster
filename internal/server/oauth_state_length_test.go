package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	oauthhandler "github.com/giantswarm/mcp-oauth/handler"
	"github.com/giantswarm/mcp-oauth/providers/mock"
	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/giantswarm/mcp-oauth/storage/memory"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/config"
)

// The OpenAI Codex CLI sends 16 random bytes of state in unpadded base64url:
// 22 characters.
const codexState = "amXAojVgd6OlYylADsqKFg"

func TestOAuthAuthorize_AcceptsCodexStateLength(t *testing.T) {
	const redirectURI = "http://127.0.0.1:54650/callback"

	cfg := newOAuthServerConfig(config.OAuthServerConfig{
		BaseURL:                    "https://muster.example.com",
		AllowLocalhostRedirectURIs: true,
	}, oauthTokenLifetimes{})
	store := memory.New()
	t.Cleanup(store.Stop)
	srv, err := oauthserver.New(mock.NewProvider(), store, store, store, cfg, nil)
	require.NoError(t, err)

	client, _, err := srv.RegisterClient(context.Background(), "Codex", oauthserver.ClientTypePublic, "none",
		[]string{redirectURI}, []string{"openid"}, "192.0.2.1", 10)
	require.NoError(t, err)

	verifier := strings.Repeat("v", 43)
	challenge := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {client.ClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {"openid"},
		"state":                 {codexState},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+query.Encode(), nil)
	rec := httptest.NewRecorder()

	oauthhandler.New(srv, nil).ServeAuthorization(rec, req)

	require.Equal(t, http.StatusFound, rec.Code, rec.Body.String())
	location, err := url.Parse(rec.Header().Get("Location"))
	require.NoError(t, err)
	require.Empty(t, location.Query().Get("error"), "authorize refused the request: %s", location.Query().Get("error_description"))
	require.NotEqual(t, "127.0.0.1:54650", location.Host, "authorize redirected back to the client instead of to the provider")
}
