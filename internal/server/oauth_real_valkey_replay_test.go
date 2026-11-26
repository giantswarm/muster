package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	valkeystore "github.com/giantswarm/mcp-oauth/storage/valkey"

	"github.com/giantswarm/muster/v5/internal/config"
)

func TestPrivateKeyJWTReplayAcrossRealValkeyReplicas(t *testing.T) {
	address := os.Getenv("VALKEY_TEST_ADDR")
	if address == "" {
		t.Skip("VALKEY_TEST_ADDR is required for the real Valkey integration test")
	}
	prefix := fmt.Sprintf("muster-replay:%d:", time.Now().UnixNano())
	newReplica := func() *OAuthHTTPServer {
		server, err := NewOAuthHTTPServer(config.OAuthServerConfig{
			Enabled: true, BaseURL: "https://muster.example.com", Provider: OAuthProviderGoogle,
			Google: config.GoogleConfig{ClientID: "muster", ClientSecret: "fixture-secret"}, InitialAccessToken: "fixture-registration",
			Storage: config.OAuthStorageConfig{Type: "valkey", Valkey: config.ValkeyConfig{URL: address, KeyPrefix: prefix}},
		}, http.NotFoundHandler(), false)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, server.Shutdown(context.WithoutCancel(t.Context()))) })
		return server
	}
	first, second := newReplica(), newReplica()
	clientID, key := registerPrivateKeyJWTClient(t, first)
	values := privateKeyJWTRequest(t, key, clientID, "shared-real-assertion")
	response := sendPrivateKeyJWTRequest(first, values)
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Contains(t, response.Body.String(), "invalid_grant")
	response = sendPrivateKeyJWTRequest(second, values)
	require.Equal(t, http.StatusUnauthorized, response.Code)
	require.Contains(t, response.Body.String(), "invalid_client")

	dpop := first.oauthServer.DPoPReplayCache()
	assertions := valkeystore.NewClientAssertionReplayStore(second.replayValkeyClient, prefix+"client-assertion:")
	seen, err := dpop.Seen(t.Context(), "namespace-check", time.Minute)
	require.NoError(t, err)
	require.False(t, seen)
	seen, err = assertions.Seen(t.Context(), "namespace-check", time.Minute)
	require.NoError(t, err)
	require.False(t, seen)
	seen, err = second.oauthServer.DPoPReplayCache().Seen(t.Context(), "namespace-check", time.Minute)
	require.NoError(t, err)
	require.True(t, seen)
}
