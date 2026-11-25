package approval

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/api"
)

var (
	alice = api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "alice"}
	bob   = api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "bob"}
)

func newRequest(t *testing.T, id string, identity api.SignInIdentity, session string, args map[string]any) *api.ApprovalRequest {
	t.Helper()
	canonical, hash, err := Canonical(args)
	require.NoError(t, err)
	return &api.ApprovalRequest{
		ID:            id,
		ElicitationID: "elicit-" + fmt.Sprint(len(id)*7919),
		Identity:      identity,
		SessionID:     session,
		CallerKind:    api.CallerKindPerson,
		Target:        api.ApprovalTarget{Kind: api.ApprovalTargetTool, Server: "github", UID: "u1", Generation: 2, Name: "merge_pull_request"},
		Arguments:     canonical,
		ArgumentsHash: hash,
		CreatedAt:     time.Now(),
		ExpiresAt:     time.Now().Add(5 * time.Minute),
	}
}

func newValkeyStoreForTest(t *testing.T) (*ValkeyStore, *miniredis.Miniredis, valkey.Client) {
	t.Helper()
	srv := miniredis.RunT(t)
	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return NewValkeyStore(client, "muster:", nil), srv, client
}

// stores runs a test against both store implementations.
func stores(t *testing.T, run func(t *testing.T, store api.ApprovalStore)) {
	t.Run("memory", func(t *testing.T) { run(t, NewMemoryStore()) })
	t.Run("valkey", func(t *testing.T) {
		store, _, _ := newValkeyStoreForTest(t)
		run(t, store)
	})
}

func TestStore_RoundTrip(t *testing.T) {
	stores(t, func(t *testing.T, store api.ApprovalStore) {
		ctx := context.Background()
		request := newRequest(t, "r1", alice, "s1", map[string]any{"pr": 1.0})

		created, err := store.Create(ctx, request)
		require.NoError(t, err)
		assert.Equal(t, api.ApprovalStatusPending, created.Status)

		// An identical call reuses the pending request and its link.
		again, err := store.Create(ctx, newRequest(t, "r2", alice, "s1", map[string]any{"pr": 1.0}))
		require.NoError(t, err)
		assert.Equal(t, "r1", again.ID)

		// Not decided: a retry gets it pending and it is kept.
		pending, err := store.Consume(ctx, request.BindingKey())
		require.NoError(t, err)
		assert.Equal(t, api.ApprovalStatusPending, pending.Status)

		require.NoError(t, store.Decide(ctx, "r1", true, alice))
		require.ErrorIs(t, store.Decide(ctx, "r1", false, alice), api.ErrApprovalDecided, "decided once")

		got, err := store.Get(ctx, "r1")
		require.NoError(t, err)
		assert.Equal(t, api.ApprovalStatusApproved, got.Status)
		assert.Equal(t, alice, got.DecidedBy)

		consumed, err := store.Consume(ctx, request.BindingKey())
		require.NoError(t, err)
		assert.Equal(t, api.ApprovalStatusApproved, consumed.Status)
		assert.Equal(t, request.Arguments, consumed.Arguments)

		// Single use: the approval is gone.
		_, err = store.Consume(ctx, request.BindingKey())
		require.ErrorIs(t, err, api.ErrApprovalNotFound)
		_, err = store.Get(ctx, "r1")
		require.ErrorIs(t, err, api.ErrApprovalNotFound)
	})
}

// The binding covers identity, Session, target and arguments: changing any
// of them is another request.
func TestStore_Binding(t *testing.T) {
	stores(t, func(t *testing.T, store api.ApprovalStore) {
		ctx := context.Background()
		request := newRequest(t, "r1", alice, "s1", map[string]any{"pr": 1.0})
		_, err := store.Create(ctx, request)
		require.NoError(t, err)
		require.NoError(t, store.Decide(ctx, "r1", true, alice))

		other := []*api.ApprovalRequest{
			newRequest(t, "x", bob, "s1", map[string]any{"pr": 1.0}),
			newRequest(t, "x", alice, "s2", map[string]any{"pr": 1.0}),
			newRequest(t, "x", alice, "s1", map[string]any{"pr": 2.0}),
			func() *api.ApprovalRequest {
				r := newRequest(t, "x", alice, "s1", map[string]any{"pr": 1.0})
				r.Target.Generation = 3
				return r
			}(),
			func() *api.ApprovalRequest {
				r := newRequest(t, "x", alice, "s1", map[string]any{"pr": 1.0})
				r.Identity.Issuer = "https://other.example.com"
				return r
			}(),
		}
		for _, r := range other {
			_, err := store.Consume(ctx, r.BindingKey())
			require.ErrorIs(t, err, api.ErrApprovalNotFound)
		}
	})
}

func TestStore_Decline(t *testing.T) {
	stores(t, func(t *testing.T, store api.ApprovalStore) {
		ctx := context.Background()
		request := newRequest(t, "r1", alice, "s1", nil)
		_, err := store.Create(ctx, request)
		require.NoError(t, err)
		require.NoError(t, store.Decide(ctx, "r1", false, alice))
		consumed, err := store.Consume(ctx, request.BindingKey())
		require.NoError(t, err)
		assert.Equal(t, api.ApprovalStatusDeclined, consumed.Status)
		_, err = store.Consume(ctx, request.BindingKey())
		require.ErrorIs(t, err, api.ErrApprovalNotFound)
	})
}

func TestStore_Limits(t *testing.T) {
	stores(t, func(t *testing.T, store api.ApprovalStore) {
		ctx := context.Background()
		for i := range MaxPendingPerSession {
			_, err := store.Create(ctx, newRequest(t, string(rune('a'+i)), alice, "s1", map[string]any{"i": float64(i)}))
			require.NoError(t, err)
		}
		_, err := store.Create(ctx, newRequest(t, "over", alice, "s1", map[string]any{"i": 99.0}))
		require.ErrorIs(t, err, api.ErrApprovalLimit)
		// Another Session of the same person still asks.
		_, err = store.Create(ctx, newRequest(t, "other", alice, "s2", map[string]any{"i": 99.0}))
		require.NoError(t, err)
	})
}

func TestStore_Expiry(t *testing.T) {
	t.Run("memory", func(t *testing.T) {
		store := NewMemoryStore()
		now := time.Now()
		store.SetClock(func() time.Time { return now })
		ctx := context.Background()
		request := newRequest(t, "r1", alice, "s1", nil)
		_, err := store.Create(ctx, request)
		require.NoError(t, err)
		require.NoError(t, store.Decide(ctx, "r1", true, alice))
		now = now.Add(6 * time.Minute)
		_, err = store.Consume(ctx, request.BindingKey())
		require.ErrorIs(t, err, api.ErrApprovalNotFound)
	})
	t.Run("valkey", func(t *testing.T) {
		store, srv, _ := newValkeyStoreForTest(t)
		ctx := context.Background()
		request := newRequest(t, "r1", alice, "s1", nil)
		_, err := store.Create(ctx, request)
		require.NoError(t, err)
		require.NoError(t, store.Decide(ctx, "r1", true, alice))
		srv.FastForward(6 * time.Minute)
		_, err = store.Consume(ctx, request.BindingKey())
		require.ErrorIs(t, err, api.ErrApprovalNotFound)
		require.ErrorIs(t, store.Decide(ctx, "r1", true, alice), api.ErrApprovalNotFound)
	})
}

// An approval decided on one replica is consumed on another, once, however
// many retries race for it.
func TestValkeyStore_ReplicasConsumeOnce(t *testing.T) {
	replicaA, _, client := newValkeyStoreForTest(t)
	replicaB := NewValkeyStore(client, "muster:", nil)
	ctx := context.Background()
	request := newRequest(t, "r1", alice, "s1", map[string]any{"pr": 1.0})
	_, err := replicaA.Create(ctx, request)
	require.NoError(t, err)
	require.NoError(t, replicaB.Decide(ctx, "r1", true, alice))

	var approved atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			store := replicaA
			if approved.Load()%2 == 0 {
				store = replicaB
			}
			if r, err := store.Consume(ctx, request.BindingKey()); err == nil && r.Status == api.ApprovalStatusApproved {
				approved.Add(1)
			}
		})
	}
	wg.Wait()
	assert.Equal(t, int32(1), approved.Load())
}

// The store refuses rather than guesses when Valkey is gone.
func TestValkeyStore_Unavailable(t *testing.T) {
	store, srv, _ := newValkeyStoreForTest(t)
	srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := store.Create(ctx, newRequest(t, "r1", alice, "s1", nil))
	require.Error(t, err)
	require.NotErrorIs(t, err, api.ErrApprovalNotFound)
	_, err = store.Consume(ctx, "anything")
	require.Error(t, err)
	require.NotErrorIs(t, err, api.ErrApprovalNotFound)
}

func TestStore_Flows(t *testing.T) {
	stores(t, func(t *testing.T, store api.ApprovalStore) {
		ctx := context.Background()
		_, err := store.GetFlow(ctx, "k")
		require.ErrorIs(t, err, api.ErrApprovalNotFound)
		flow := api.ApprovalPageFlow{Nonce: "n", Token: "t", Identity: alice, Redirect: "https://portal.example.com/s"}
		require.NoError(t, store.PutFlow(ctx, "k", flow, time.Minute))
		got, err := store.GetFlow(ctx, "k")
		require.NoError(t, err)
		assert.Equal(t, flow, *got)
		require.NoError(t, store.DeleteFlow(ctx, "k"))
		_, err = store.GetFlow(ctx, "k")
		require.ErrorIs(t, err, api.ErrApprovalNotFound)
	})
}
