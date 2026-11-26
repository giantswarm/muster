package credprovider

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/api"
)

// A binding a sender wrote keeps its sender seal while it is used, whether or
// not it names an agent, in both stores: the person cannot rebind the actor
// without the sender in the TTL after the binding ended.
func TestStoreSealOfASenderBindingFollowsTouch(t *testing.T) {
	ctx := context.Background()
	for _, withAgent := range []bool{false, true} {
		for _, tc := range storeCases(t) {
			name := tc.name + "/agentless"
			agent := ""
			if withAgent {
				name, agent = tc.name+"/agent", "kagent/a"
			}
			t.Run(name, func(t *testing.T) {
				sent := Binding{Subject: "alice", ApprovalSender: true, Sender: "am", Agent: agent, Approval: &api.AgentApproval{Default: "Ask"}}
				require.NoError(t, tc.store.Put(ctx, testActor, sent, time.Hour))
				// The Session is used every 30 minutes for 2.5 hours,
				// longer than the two TTLs the seal lasts untouched.
				for range 5 {
					tc.advance(30 * time.Minute)
					b, err := tc.store.Get(ctx, testActor)
					require.NoError(t, err)
					require.NotNil(t, b, "binding expired while used")
					require.NoError(t, tc.store.Touch(ctx, testActor, b.Agent, time.Hour))
				}
				// It then idles for one TTL: the binding ended, the seal holds one more.
				tc.advance(61 * time.Minute)
				err := tc.store.Put(ctx, testActor, Binding{Subject: "alice"}, time.Hour)
				assert.ErrorIs(t, err, ErrSenderSealed, "the person rebound the actor without the sender")
			})
		}
	}
}

// putScript writes only while the key still holds the value the owner check
// read; a binding written by someone else in between fails the write.
func TestPutScriptWritesOnlyOverTheValueRead(t *testing.T) {
	ctx := context.Background()
	srv := miniredis.RunT(t)
	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	exec := func(read, write string) int64 {
		n, err := putScript.Exec(ctx, client, []string{"k"}, []string{read, write, "60000"}).AsInt64()
		require.NoError(t, err)
		return n
	}

	assert.EqualValues(t, 1, exec("", "v1"), "a missing key is written")
	assert.EqualValues(t, 0, exec("stale", "v2"), "a key changed since the read is not written")
	got, err := srv.Get("k")
	require.NoError(t, err)
	assert.Equal(t, "v1", got)
	assert.EqualValues(t, 1, exec("v1", "v2"), "the value read is replaced")
	got, err = srv.Get("k")
	require.NoError(t, err)
	assert.Equal(t, "v2", got)
}
