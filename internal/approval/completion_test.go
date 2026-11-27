package approval

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
)

// collect subscribes to bus and returns what arrives; it returns once the
// subscription is live.
func collect(t *testing.T, bus Completions, live func() bool) (get func() []Completion) {
	t.Helper()
	var mu sync.Mutex
	var got []Completion
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = bus.Subscribe(ctx, func(c Completion) {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, c)
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	require.Eventually(t, live, 5*time.Second, 5*time.Millisecond)
	return func() []Completion {
		mu.Lock()
		defer mu.Unlock()
		return append([]Completion(nil), got...)
	}
}

func TestMemoryCompletionsReachEverySubscriber(t *testing.T) {
	bus := NewMemoryCompletions()
	first := collect(t, bus, func() bool { return bus.Subscribers() >= 1 })
	second := collect(t, bus, func() bool { return bus.Subscribers() >= 2 })

	want := Completion{Kind: CompletionApproval, Key: "req-1", ElicitationID: "el-1"}
	require.NoError(t, bus.Publish(t.Context(), want))

	require.Equal(t, []Completion{want}, first())
	require.Equal(t, []Completion{want}, second())
}

func TestValkeyCompletionsReachEveryReplica(t *testing.T) {
	_, srv, client := newValkeyStoreForTest(t)
	// Two replicas: each its own client, on one shared Valkey.
	other, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
	require.NoError(t, err)
	t.Cleanup(other.Close)
	replicaA := NewValkeyCompletions(client, "muster:")
	replicaB := NewValkeyCompletions(other, "muster:")
	live := func(n int) func() bool {
		return func() bool { return srv.PubSubNumSub(CompletionChannel("muster:"))[CompletionChannel("muster:")] >= n }
	}
	gotA := collect(t, replicaA, live(1))
	gotB := collect(t, replicaB, live(2))

	want := Completion{Kind: CompletionSignIn, Key: SignInKey("session", "github")}
	require.NoError(t, replicaB.Publish(t.Context(), want))

	require.Eventually(t, func() bool { return len(gotA()) == 1 && len(gotB()) == 1 }, 5*time.Second, 5*time.Millisecond)
	require.Equal(t, want, gotA()[0])
	require.Equal(t, want, gotB()[0])
}

func TestSignInKeyHidesTheSession(t *testing.T) {
	key := SignInKey("session-secret", "github")
	require.NotContains(t, key, "session-secret")
	require.NotEqual(t, key, SignInKey("session-secret", "gitlab"))
	require.Equal(t, key, SignInKey("session-secret", "github"))
}

func TestValkeyPublishFailsWhenTheStoreIsGone(t *testing.T) {
	_, srv, client := newValkeyStoreForTest(t)
	bus := NewValkeyCompletions(client, "muster:")
	srv.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	require.Error(t, bus.Publish(ctx, Completion{Kind: CompletionApproval, Key: "r"}))
}
