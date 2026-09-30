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

	"github.com/giantswarm/muster/v5/internal/config"
	oauthstore "github.com/giantswarm/muster/v5/internal/oauth/store"
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

// A Valkey that stays away for minutes is still waited for -- muster never
// exits on connection refused, it would only crash-loop and page
// (giantswarm/muster#1364). The pauses back off and stay capped.
func TestConnectValkey_KeepsWaitingForAValkeyThatStaysAwayForMinutes(t *testing.T) {
	srv, addr := stoppedValkey(t)
	var pauses []time.Duration
	var waited time.Duration
	awayForTwoMinutes := func(ctx context.Context, d time.Duration) error {
		require.NoError(t, ctx.Err())
		pauses = append(pauses, d)
		waited += d
		if waited >= 2*time.Minute {
			require.NoError(t, srv.Restart(), "Valkey comes back after two minutes")
		}
		return nil
	}

	client, err := connectValkey(context.Background(), config.ValkeyConfig{URL: addr}, awayForTwoMinutes)
	require.NoError(t, err)
	t.Cleanup(client.Close)

	require.GreaterOrEqual(t, len(pauses), 4)
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}, pauses[:4],
		"the backoff doubles from the initial pause")
	for _, d := range pauses[4:] {
		assert.Equal(t, sessionStoreMaxBackoff, d, "and stays at the cap")
	}
}

// A Valkey that answers and refuses the credentials will refuse them however
// long muster waits: the start ends at once with the reason.
func TestConnectValkey_ExitsAtOnceWhenValkeyRefusesThePassword(t *testing.T) {
	srv := miniredis.RunT(t)
	srv.RequireAuth("right")

	client, err := connectValkey(context.Background(), config.ValkeyConfig{URL: srv.Addr(), Password: "wrong"}, noWait(t))

	require.Error(t, err)
	assert.Nil(t, client)
	assert.ErrorContains(t, err, "refused the configuration")
}

func TestConnectValkey_ExitsAtOnceOnAMalformedAddress(t *testing.T) {
	client, err := connectValkey(context.Background(), config.ValkeyConfig{URL: "valkey:6379:6379"}, noWait(t))

	require.Error(t, err)
	assert.Nil(t, client)
	assert.ErrorContains(t, err, "refused the configuration")
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
