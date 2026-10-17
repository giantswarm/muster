package credprovider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/giantswarm/mcp-oauth/security"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
)

const testActor = "spiffe://substrate-actor.local/actor/kagent/session-a"

// storeCase is one Store under test plus a way to move its clock past ttl.
type storeCase struct {
	name    string
	store   Store
	advance func(time.Duration)
}

func storeCases(t *testing.T) []storeCase {
	t.Helper()
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	mem := NewMemoryStore()
	mem.now = func() time.Time { return now }

	srv := miniredis.RunT(t)
	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	key, err := security.GenerateKey()
	require.NoError(t, err)
	enc, err := security.NewEncryptor(key)
	require.NoError(t, err)

	return []storeCase{
		{"memory", mem, func(d time.Duration) { now = now.Add(d) }},
		{"valkey", NewValkeyStore(client, "muster:", enc), srv.FastForward},
	}
}

func TestStoreBindingLifecycle(t *testing.T) {
	ctx := context.Background()
	for _, tc := range storeCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			b := Binding{Subject: "alice", Session: "kagent/s1", Providers: []string{"github"}}
			require.NoError(t, tc.store.Put(ctx, testActor, b, time.Hour))

			got, err := tc.store.Get(ctx, testActor)
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, "alice", got.Subject)
			assert.True(t, got.Allows("github"))
			assert.False(t, got.Allows("gitlab"))

			assert.ErrorIs(t, tc.store.Put(ctx, testActor, Binding{Subject: "mallory"}, time.Hour), ErrBoundToAnother,
				"nobody rebinds another person's sandbox")
			assert.ErrorIs(t, tc.store.Delete(ctx, testActor, "mallory"), ErrBoundToAnother,
				"nobody unbinds another person's sandbox")

			b.Providers = []string{"github", "github-enterprise"}
			require.NoError(t, tc.store.Put(ctx, testActor, b, time.Hour), "the person rebinds their own")

			require.NoError(t, tc.store.Delete(ctx, testActor, "alice"))
			got, err = tc.store.Get(ctx, testActor)
			require.NoError(t, err)
			assert.Nil(t, got, "unbound")
			assert.NoError(t, tc.store.Delete(ctx, testActor, "alice"), "unbinding an unbound actor succeeds")
		})
	}
}

func TestStoreBindingExpiresWithoutUse(t *testing.T) {
	ctx := context.Background()
	for _, tc := range storeCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, tc.store.Put(ctx, testActor, Binding{Subject: "alice"}, time.Hour))

			tc.advance(50 * time.Minute)
			require.NoError(t, tc.store.Touch(ctx, testActor, time.Hour))
			tc.advance(50 * time.Minute)
			got, err := tc.store.Get(ctx, testActor)
			require.NoError(t, err)
			assert.NotNil(t, got, "a touched binding lives on")

			tc.advance(61 * time.Minute)
			got, err = tc.store.Get(ctx, testActor)
			require.NoError(t, err)
			assert.Nil(t, got, "a binding expires a TTL after its last use")

			require.NoError(t, tc.store.Put(ctx, testActor, Binding{Subject: "bob"}, time.Hour),
				"an expired binding no longer holds the actor")
			assert.NoError(t, tc.store.Touch(ctx, "spiffe://substrate-actor.local/actor/x/none", time.Hour))
		})
	}
}

func TestValkeyStoreSealsAndHashes(t *testing.T) {
	srv := miniredis.RunT(t)
	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	key, err := security.GenerateKey()
	require.NoError(t, err)
	enc, err := security.NewEncryptor(key)
	require.NoError(t, err)

	store := NewValkeyStore(client, "muster:", enc)
	require.NoError(t, store.Put(context.Background(), testActor, Binding{Subject: "alice", Session: "kagent/s1"}, time.Hour))

	keys := srv.Keys()
	require.Len(t, keys, 1)
	assert.True(t, strings.HasPrefix(keys[0], "muster:credprovider:binding:"))
	assert.NotContains(t, keys[0], "session-a", "the key names no actor")
	value, err := srv.Get(keys[0])
	require.NoError(t, err)
	assert.NotContains(t, value, "alice", "the value is sealed")
	assert.Equal(t, time.Hour, srv.TTL(keys[0]))
}
