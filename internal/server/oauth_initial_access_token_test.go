package server

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/config"
)

func TestOAuthInitialAccessTokenRegistration(t *testing.T) {
	for _, test := range []struct{ name, current, legacy, want string }{
		{"new setting", "current-secret", "", "current-secret"},
		{"deprecated setting", "", "legacy-secret", "legacy-secret"},
		{"new setting wins", "current-secret", "legacy-secret", "current-secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			server, err := NewOAuthHTTPServer(config.OAuthServerConfig{
				Enabled: true, BaseURL: "https://muster.example.com", Provider: OAuthProviderGoogle,
				Google:             config.GoogleConfig{ClientID: "muster", ClientSecret: "provider-secret"},
				InitialAccessToken: test.current, RegistrationToken: test.legacy,
			}, http.NotFoundHandler(), false)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, server.Shutdown(context.WithoutCancel(t.Context()))) })
			mux := server.CreateMux()
			for _, token := range []string{"", "wrong-secret", test.want} {
				request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/oauth/register", strings.NewReader(`{"client_name":"fixture","redirect_uris":["http://127.0.0.1:50123/callback"],"token_endpoint_auth_method":"client_secret_basic"}`))
				request.Header.Set("Content-Type", "application/json")
				if token != "" {
					request.Header.Set("Authorization", "Bearer "+token)
				}
				response := httptest.NewRecorder()
				mux.ServeHTTP(response, request)
				status := http.StatusUnauthorized
				if token == test.want {
					status = http.StatusCreated
				}
				require.Equal(t, status, response.Code, response.Body.String())
			}
			if test.legacy != "" {
				require.Contains(t, output.String(), "DEPRECATED:")
				require.Contains(t, output.String(), "initialAccessToken")
			}
			require.NotContains(t, output.String(), "current-secret")
			require.NotContains(t, output.String(), "legacy-secret")
		})
	}
}
