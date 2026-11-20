package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	miniredisserver "github.com/alicebob/miniredis/v2/server"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/config"
)

func newPrivateKeyJWTReplica(t *testing.T, backend *miniredis.Miniredis, prefix string) *OAuthHTTPServer {
	t.Helper()
	server, err := NewOAuthHTTPServer(config.OAuthServerConfig{
		Enabled: true, BaseURL: "https://muster.example.com", Provider: OAuthProviderGoogle,
		Google: config.GoogleConfig{ClientID: "muster", ClientSecret: "fixture-secret"}, RegistrationToken: "fixture-registration",
		Storage: config.OAuthStorageConfig{Type: "valkey", Valkey: config.ValkeyConfig{URL: backend.Addr(), KeyPrefix: prefix}},
	}, http.NotFoundHandler(), false)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.WithoutCancel(t.Context()))) })
	return server
}

func registerPrivateKeyJWTClient(t *testing.T, server *OAuthHTTPServer) (string, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	metadata, err := json.Marshal(map[string]any{
		"client_name": "replay client", "redirect_uris": []string{"http://127.0.0.1:54650/callback"}, "scope": "mcp",
		"token_endpoint_auth_method": "private_key_jwt", "token_endpoint_auth_signing_alg": "ES256",
		"jwks": jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "primary", Algorithm: "ES256", Use: "sig"}}},
	})
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(string(metadata)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer fixture-registration")
	response := httptest.NewRecorder()
	server.CreateMux().ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	var registration struct {
		ClientID string `json:"client_id"`
		Scope    string `json:"scope"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &registration))
	require.Equal(t, "mcp", registration.Scope)
	return registration.ClientID, key
}

func privateKeyJWTRequest(t *testing.T, key *ecdsa.PrivateKey, clientID, identifier string) url.Values {
	t.Helper()
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: jose.JSONWebKey{Key: key, KeyID: "primary"}}, (&jose.SignerOptions{}).WithType("client-authentication+jwt"))
	require.NoError(t, err)
	now := time.Now()
	assertion, err := jwt.Signed(signer).Claims(jwt.Claims{
		Issuer: clientID, Subject: clientID, Audience: jwt.Audience{"https://muster.example.com"},
		IssuedAt: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(time.Minute)), ID: identifier,
	}).Serialize()
	require.NoError(t, err)
	return url.Values{
		"grant_type": {"authorization_code"}, "code": {"unissued-code"},
		"redirect_uri": {"http://127.0.0.1:54650/callback"}, "code_verifier": {strings.Repeat("v", 50)},
		"client_id": {clientID}, "client_assertion": {assertion},
		"client_assertion_type": {"urn:ietf:params:oauth:client-assertion-type:jwt-bearer"},
	}
}

func sendPrivateKeyJWTRequest(server *OAuthHTTPServer, values url.Values) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(values.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	server.CreateMux().ServeHTTP(response, request)
	return response
}

func TestPrivateKeyJWTReplayAcrossValkeyReplicas(t *testing.T) {
	backend := miniredis.RunT(t)
	first := newPrivateKeyJWTReplica(t, backend, "replay:")
	second := newPrivateKeyJWTReplica(t, backend, "replay:")
	clientID, key := registerPrivateKeyJWTClient(t, first)
	values := privateKeyJWTRequest(t, key, clientID, "shared-assertion")
	response := sendPrivateKeyJWTRequest(first, values)
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "invalid_grant")
	response = sendPrivateKeyJWTRequest(second, values)
	require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "invalid_client")

	isolated := newPrivateKeyJWTReplica(t, backend, "isolated:")
	client, err := first.oauthServer.GetClient(t.Context(), clientID)
	require.NoError(t, err)
	require.NoError(t, isolated.oauthServer.SaveClient(t.Context(), client))
	response = sendPrivateKeyJWTRequest(isolated, values)
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "invalid_grant")
}

func TestPrivateKeyJWTReplayStoreOutageHasNoMemoryFallback(t *testing.T) {
	backend := miniredis.RunT(t)
	first := newPrivateKeyJWTReplica(t, backend, "replay:")
	second := newPrivateKeyJWTReplica(t, backend, "replay:")
	clientID, key := registerPrivateKeyJWTClient(t, first)
	values := privateKeyJWTRequest(t, key, clientID, "retryable-assertion")
	var unavailable atomic.Bool
	unavailable.Store(true)
	var failedWrites atomic.Int32
	backend.Server().SetPreHook(func(peer *miniredisserver.Peer, command string, arguments ...string) bool {
		if unavailable.Load() && strings.EqualFold(command, "SET") && len(arguments) > 0 && strings.HasPrefix(arguments[0], "replay:client-assertion:") {
			failedWrites.Add(1)
			peer.WriteError("ERR assertion replay write unavailable")
			return true
		}
		return false
	})
	response := sendPrivateKeyJWTRequest(first, values)
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	require.NotEmpty(t, response.Header().Get("Retry-After"))
	require.Greater(t, failedWrites.Load(), int32(0))
	unavailable.Store(false)
	response = sendPrivateKeyJWTRequest(second, values)
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "invalid_grant")
	response = sendPrivateKeyJWTRequest(first, values)
	require.Equal(t, http.StatusUnauthorized, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "invalid_client")
}
