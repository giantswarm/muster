package aggregator

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	valkeystore "github.com/giantswarm/mcp-oauth/storage/valkey"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestInvalidRequestStateKeyRefusesStartupBeforeStorage(t *testing.T) {
	settings := valkeyAggregatorConfig("127.0.0.1:1")
	settings.RequestState.Key = "invalid"
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := NewAggregatorServer(ctx, settings, nil)
	require.ErrorContains(t, err, "requestState")
}

func TestRequestStateKeyPreservesPlaintextTokens(t *testing.T) {
	server := miniredis.RunT(t)
	settings := valkeyAggregatorConfig(server.Addr())
	settings.RequestState.Key = newKey(t)
	stores, err := createStores(t.Context(), settings)
	require.NoError(t, err)
	t.Cleanup(stores.valkeyClient.Close)
	legacy, err := valkeystore.New(valkeystore.Config{Address: server.Addr(), KeyPrefix: stores.keyPrefix})
	require.NoError(t, err)
	t.Cleanup(legacy.Close)
	want := &oauth2.Token{AccessToken: "fixture-access", RefreshToken: "fixture-refresh", Expiry: time.Now().Add(time.Hour).UTC().Truncate(time.Second)}
	require.NoError(t, legacy.SaveToken(t.Context(), "alice", want))
	current, err := valkeystore.New(valkeystore.Config{Address: server.Addr(), KeyPrefix: stores.keyPrefix}, valkeystore.WithEncryptor(stores.encryptor))
	require.NoError(t, err)
	t.Cleanup(current.Close)
	got, err := current.GetToken(t.Context(), "alice")
	require.NoError(t, err)
	require.Equal(t, want, got)
	sealer, err := newRequestStateSealer(settings.RequestState, "")
	require.NoError(t, err)
	_, err = sealer.Seal(testCaller(), "call", []byte("state"), requestStateTTL)
	require.NoError(t, err)
	want.AccessToken = "fixture-renewed"
	require.NoError(t, current.SaveToken(t.Context(), "alice", want))
	got, err = legacy.GetToken(t.Context(), "alice")
	require.NoError(t, err)
	require.Equal(t, want, got)
}
