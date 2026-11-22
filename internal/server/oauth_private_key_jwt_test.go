package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/config"
)

func TestPrivateKeyJWTRegistrationToken(t *testing.T) {
	server, err := NewOAuthHTTPServer(config.OAuthServerConfig{
		Enabled: true, BaseURL: "https://muster.example.com", Provider: OAuthProviderGoogle,
		Google:            config.GoogleConfig{ClientID: "muster", ClientSecret: "secret"},
		RegistrationToken: "registration-test",
	}, http.NotFoundHandler(), false)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.WithoutCancel(t.Context()))) })
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	metadata, err := json.Marshal(map[string]any{
		"client_name":                     "key client",
		"redirect_uris":                   []string{"http://127.0.0.1:54650/callback"},
		"token_endpoint_auth_method":      "private_key_jwt",
		"token_endpoint_auth_signing_alg": "ES256",
		"jwks":                            jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "primary", Algorithm: "ES256", Use: "sig"}}},
	})
	require.NoError(t, err)
	mux := server.CreateMux()
	for _, test := range []struct {
		name, token string
		status      int
	}{
		{"missing", "", http.StatusUnauthorized}, {"wrong", "wrong-token", http.StatusUnauthorized}, {"valid", "registration-test", http.StatusCreated},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(string(metadata)))
			request.Header.Set("Content-Type", "application/json")
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			require.Equal(t, test.status, response.Code, response.Body.String())
			if test.status == http.StatusCreated {
				var registration map[string]any
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &registration))
				require.Equal(t, "private_key_jwt", registration["token_endpoint_auth_method"])
				require.Equal(t, "confidential", registration["client_type"])
				require.NotContains(t, registration, "client_secret")
			}
		})
	}
}
