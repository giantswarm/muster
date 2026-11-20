package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/config"
)

func TestClientManagementConfiguration(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		configuration := config.OAuthServerConfig{EnableClientManagementEndpoint: enabled}
		require.Equal(t, enabled, newOAuthServerConfig(configuration, oauthTokenLifetimes{}).EnableClientManagementEndpoint)
	}
}

func TestClientManagementRoutes(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			server, err := NewOAuthHTTPServer(config.OAuthServerConfig{
				Enabled: true, BaseURL: "https://muster.example.com", Provider: OAuthProviderGoogle,
				Google:            config.GoogleConfig{ClientID: "muster", ClientSecret: "fixture-secret"},
				RegistrationToken: "fixture-registration", EnableClientManagementEndpoint: enabled,
			}, http.NotFoundHandler(), false)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, server.Shutdown(context.WithoutCancel(t.Context()))) })
			mux := server.CreateMux()
			request := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(`{"client_name":"management client","redirect_uris":["http://127.0.0.1:54650/callback"],"scope":"mcp","token_endpoint_auth_method":"client_secret_basic"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Authorization", "Bearer fixture-registration")
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
			var registration struct {
				ClientID string `json:"client_id"`
				URI      string `json:"registration_client_uri"`
				Token    string `json:"registration_access_token"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &registration))
			if !enabled {
				require.Empty(t, registration.URI)
				require.Empty(t, registration.Token)
				request = httptest.NewRequest(http.MethodGet, "/oauth/register/"+registration.ClientID, nil)
				response = httptest.NewRecorder()
				mux.ServeHTTP(response, request)
				require.Equal(t, http.StatusNotFound, response.Code)
				return
			}
			require.NotEmpty(t, registration.Token)
			uri, err := url.Parse(registration.URI)
			require.NoError(t, err)
			for _, method := range []string{http.MethodGet, http.MethodDelete} {
				request = httptest.NewRequest(method, uri.RequestURI(), nil)
				request.Header.Set("Authorization", "Bearer "+registration.Token)
				response = httptest.NewRecorder()
				mux.ServeHTTP(response, request)
				if method == http.MethodGet {
					require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				} else {
					require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
				}
			}
		})
	}
}
