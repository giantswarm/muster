package store

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
)

func newValkeySessionAuthStoreForTest(t *testing.T) (*ValkeySessionAuthStore, *miniredis.Miniredis) {
	t.Helper()
	srv := miniredis.RunT(t)
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress:  []string{srv.Addr()},
		DisableCache: true,
	})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return NewValkeySessionAuthStore(client, time.Hour, "muster:"), srv
}

// The servers a session is authenticated to come back in one command, so a
// caller deciding per server asks once instead of once per server (#1225).
func TestValkeySessionAuthStore_AuthenticatedServers(t *testing.T) {
	store, srv := newValkeySessionAuthStoreForTest(t)
	ctx := context.Background()

	servers, err := store.AuthenticatedServers(ctx, "unknown")
	require.NoError(t, err)
	assert.Empty(t, servers)

	require.NoError(t, store.MarkAuthenticated(ctx, "s", "github"))
	require.NoError(t, store.MarkAuthenticated(ctx, "s", "pro"))
	require.NoError(t, store.MarkAuthenticated(ctx, "other", "github"))

	before := srv.CommandCount()
	servers, err = store.AuthenticatedServers(ctx, "s")
	require.NoError(t, err)
	assert.Equal(t, map[string]struct{}{"github": {}, "pro": {}}, servers)
	assert.Equal(t, 1, srv.CommandCount()-before, "one HKEYS")

	require.NoError(t, store.Revoke(ctx, "s", "pro"))
	servers, err = store.AuthenticatedServers(ctx, "s")
	require.NoError(t, err)
	assert.Equal(t, map[string]struct{}{"github": {}}, servers)
}
