package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	oauthserver "github.com/giantswarm/mcp-oauth/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/config"
)

// The OpenAI Codex CLI sends 16 random bytes of state in unpadded base64url:
// 22 characters.
const codexState = "amXAojVgd6OlYylADsqKFg"

// TestOAuthAuthorize_StateLength sends authorization requests through the
// /oauth/authorize route of the server NewOAuthHTTPServer builds.
func TestOAuthAuthorize_StateLength(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		state        string
		wantHost     string
		wantErrorKey string
	}{
		{name: "the Codex state is sent on to the provider", state: codexState, wantHost: "accounts.google.com"},
		{name: "a 21-character state is refused", state: codexState[:21], wantErrorKey: "invalid_request"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			response, mux := authorizeWithState(t, tt.state)
			if tt.wantErrorKey != "" {
				require.Equal(t, http.StatusFound, response.Code)
				target, err := url.Parse(response.Header().Get("Location"))
				require.NoError(t, err)
				require.Equal(t, tt.wantErrorKey, target.Query().Get("error"))
				require.Equal(t, tt.state, target.Query().Get("state"))
				return
			}
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			match := regexp.MustCompile(`name="consent_id" value="([^"]+)"`).FindStringSubmatch(response.Body.String())
			require.Len(t, match, 2)
			form := url.Values{"consent_id": {match[1]}, "decision": {"approve"}}
			approval := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(form.Encode()))
			approval.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			approval.Header.Set("Origin", "https://muster.example.com")
			for _, cookie := range response.Result().Cookies() {
				approval.AddCookie(cookie)
			}
			response = httptest.NewRecorder()
			mux.ServeHTTP(response, approval)
			require.Equal(t, http.StatusFound, response.Code, response.Body.String())
			location, err := url.Parse(response.Header().Get("Location"))
			require.NoError(t, err)
			require.Equal(t, tt.wantHost, location.Host, "redirect: %s", location)
			require.Equal(t, tt.wantErrorKey, location.Query().Get("error"), "error_description: %s", location.Query().Get("error_description"))
		})
	}
}

func authorizeWithState(t *testing.T, state string) (*httptest.ResponseRecorder, http.Handler) {
	t.Helper()
	const redirectURI = "http://127.0.0.1:54650/callback"

	srv, err := NewOAuthHTTPServer(config.OAuthServerConfig{
		Enabled:                    true,
		BaseURL:                    "https://muster.example.com",
		Provider:                   OAuthProviderGoogle,
		Google:                     config.GoogleConfig{ClientID: "muster", ClientSecret: "secret"},
		AllowLocalhostRedirectURIs: true,
	}, http.NotFoundHandler(), false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srv.Shutdown(context.WithoutCancel(t.Context())) })

	client, _, err := srv.oauthServer.RegisterClient(t.Context(), "Codex", oauthserver.ClientTypePublic, "none",
		[]string{redirectURI}, []string{"openid"}, "192.0.2.1", 10)
	require.NoError(t, err)

	verifier := strings.Repeat("v", 43)
	challenge := sha256.Sum256([]byte(verifier))
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {client.ClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {"openid"},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	rec := httptest.NewRecorder()
	mux := srv.CreateMux()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+query.Encode(), nil))
	return rec, mux
}
