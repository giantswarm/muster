package aggregator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/giantswarm/muster/internal/config"
	oauthstore "github.com/giantswarm/muster/internal/oauth/store"
)

// valkeyAggregatorConfig configures the session stores on the Valkey at addr,
// the shape the Helm chart renders (oauth.server.storage.type: valkey).
func valkeyAggregatorConfig(addr string) AggregatorConfig {
	return AggregatorConfig{
		Host: "localhost",
		Port: 0,
		OAuthServer: OAuthServerConfig{
			Enabled: true,
			Config: config.OAuthServerConfig{
				Storage: config.OAuthStorageConfig{
					Type:   "valkey",
					Valkey: config.ValkeyConfig{URL: addr},
				},
			},
		},
	}
}

// stoppedValkey is a Valkey that is not listening -- a server that ran once
// and was closed, so its address is known to be free of anything else -- and
// that address. Restart brings the server back on the same address.
func stoppedValkey(t *testing.T) (*miniredis.Miniredis, string) {
	t.Helper()
	srv := miniredis.RunT(t)
	addr := srv.Addr()
	srv.Close()
	return srv, addr
}

// noWait stands in for the pause between attempts in tests that must never
// reach it.
func noWait(t *testing.T) func(context.Context, time.Duration) error {
	return func(context.Context, time.Duration) error {
		t.Fatal("connectValkey waited although Valkey answered")
		return nil
	}
}

func TestCreateStores_InMemoryWhenNoValkeyIsConfigured(t *testing.T) {
	reader := setupMeter(t)

	stores, err := createStores(context.Background(), AggregatorConfig{Host: "localhost", Port: 0})
	require.NoError(t, err)

	assert.IsType(t, &oauthstore.InMemorySessionAuthStore{}, stores.authStore)
	assert.IsType(t, &oauthstore.InMemoryCapabilityStore{}, stores.capabilityStore)
	assert.Nil(t, stores.valkeyClient)
	assert.Equal(t, config.DefaultValkeyKeyPrefix, stores.keyPrefix)
	assertSessionStoreBackend(t, reader, sessionStoreBackendMemory)
}

func TestCreateStores_ValkeyWhenItAnswers(t *testing.T) {
	reader := setupMeter(t)
	srv := miniredis.RunT(t)

	stores, err := createStores(context.Background(), valkeyAggregatorConfig(srv.Addr()))
	require.NoError(t, err)
	t.Cleanup(stores.valkeyClient.Close)

	assert.IsType(t, &oauthstore.ValkeySessionAuthStore{}, stores.authStore)
	assert.IsType(t, &oauthstore.ValkeyCapabilityStore{}, stores.capabilityStore)
	assert.Equal(t, config.DefaultValkeyKeyPrefix, stores.keyPrefix)
	assertSessionStoreBackend(t, reader, sessionStoreBackendValkey)
}

// The order this fix is about: muster starts, its Valkey is not up yet and
// comes up a moment later. The stores must end up on Valkey, never in memory.
func TestConnectValkey_WaitsForAValkeyThatStartsLater(t *testing.T) {
	srv, addr := stoppedValkey(t)
	waits := 0
	startValkeyInsteadOfSleeping := func(ctx context.Context, d time.Duration) error {
		waits++
		require.NoError(t, ctx.Err(), "the wait runs under the connect deadline")
		assert.Equal(t, sessionStoreInitialBackoff, d, "first pause is the initial backoff")
		require.NoError(t, srv.Restart(), "Valkey comes up while muster waits")
		return nil
	}

	client, err := connectValkey(context.Background(), config.ValkeyConfig{URL: addr}, startValkeyInsteadOfSleeping)
	require.NoError(t, err)
	t.Cleanup(client.Close)

	assert.Equal(t, 1, waits, "one failed attempt, one pause, then Valkey answered")
	require.NoError(t, client.Do(context.Background(), client.B().Ping().Build()).Error(),
		"the returned client talks to the Valkey that came up")
}

func TestConnectValkey_ReturnsAtOnceWhenValkeyAnswers(t *testing.T) {
	srv := miniredis.RunT(t)

	client, err := connectValkey(context.Background(), config.ValkeyConfig{URL: srv.Addr()}, noWait(t))
	require.NoError(t, err)
	client.Close()
}

// A Valkey that stays away is an error naming the deadline, not a fallback:
// the caller refuses to start and the kubelet restarts the pod.
func TestConnectValkey_GivesUpWhenValkeyStaysAway(t *testing.T) {
	_, addr := stoppedValkey(t)
	deadlinePassed := func(context.Context, time.Duration) error { return context.DeadlineExceeded }

	client, err := connectValkey(context.Background(), config.ValkeyConfig{URL: addr}, deadlinePassed)

	require.Error(t, err)
	assert.Nil(t, client)
	assert.ErrorContains(t, err, "did not answer within "+sessionStoreConnectTimeout.String())
	assert.ErrorContains(t, err, "valkey connect:", "the last dial error is kept for the operator")
}

func TestConnectValkey_StopsWhenTheStartIsCancelled(t *testing.T) {
	_, addr := stoppedValkey(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	client, err := connectValkey(ctx, config.ValkeyConfig{URL: addr}, sleepCtx)

	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, client)
}

// The constructor refuses a start whose configured Valkey is unreachable
// instead of handing out in-memory stores; nothing above it has to check.
func TestNewAggregatorServer_RefusesToStartOnMemoryWhenValkeyIsConfiguredButAway(t *testing.T) {
	_, addr := stoppedValkey(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	server, err := NewAggregatorServer(ctx, valkeyAggregatorConfig(addr), nil)

	require.Error(t, err)
	assert.Nil(t, server)
	assert.ErrorContains(t, err, "session stores:")
	assert.True(t, errors.Is(err, context.Canceled), "the wait ended with the start's context: %v", err)
}

// assertSessionStoreBackend reads muster.session_store.backend and checks
// that exactly the given backend is reported in use.
func assertSessionStoreBackend(t *testing.T, reader interface {
	Collect(context.Context, *metricdata.ResourceMetrics) error
}, want string) {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	got := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "muster.session_store.backend" {
				continue
			}
			gauge, ok := m.Data.(metricdata.Gauge[int64])
			require.True(t, ok, "muster.session_store.backend is an int64 gauge, got %T", m.Data)
			for _, dp := range gauge.DataPoints {
				backend, _ := dp.Attributes.Value(attribute.Key("backend"))
				got[backend.AsString()] = dp.Value
			}
		}
	}
	want1, want0 := sessionStoreBackendValkey, sessionStoreBackendMemory
	if want == sessionStoreBackendMemory {
		want1, want0 = want0, want1
	}
	assert.Equal(t, map[string]int64{want1: 1, want0: 0}, got)
}
