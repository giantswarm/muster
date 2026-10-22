package aggregator

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/giantswarm/mcp-oauth/security"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/giantswarm/muster/v5/internal/chatgptlogin"
	"github.com/giantswarm/muster/v5/internal/chatgptlogin/chatgptlogintest"
	pkgoauth "github.com/giantswarm/muster/v5/pkg/oauth"
)

func newTestValkeyClient(t *testing.T) valkey.Client {
	t.Helper()
	srv := miniredis.RunT(t)
	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{srv.Addr()}, DisableCache: true})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	return client
}

// twoReplicas is a device fixture whose aggregator, a, and a second one, b,
// share a Valkey store, as two muster replicas do.
type twoReplicas struct {
	*deviceFixture
	a, b           *AggregatorServer
	beatsA, beatsB chan time.Time
}

func newTwoReplicas(t *testing.T) *twoReplicas {
	t.Helper()
	f := newDeviceFixture(t)
	client := newTestValkeyClient(t)
	r := &twoReplicas{deviceFixture: f, beatsA: make(chan time.Time), beatsB: make(chan time.Time)}
	r.a = f.replica(t, newValkeyDeviceSignInStore(client, "muster:", nil), r.beatsA)
	r.b = f.replica(t, newValkeyDeviceSignInStore(client, "muster:", nil), r.beatsB)
	return r
}

func statusOn(t *testing.T, agg *AggregatorServer) pkgoauth.ProviderAppAuthStatus {
	t.Helper()
	got := NewAuthToolProvider(agg).providerAppStatuses(deviceCtx(), appSession, appSubject, true)
	require.Len(t, got, 1)
	return got[0]
}

func loginOn(t *testing.T, agg *AggregatorServer) map[string]any {
	t.Helper()
	result, err := NewAuthToolProvider(agg).handleAuthLogin(deviceCtx(), map[string]any{"app": "chatgpt"})
	require.NoError(t, err)
	require.False(t, result.IsError, "%v", result.Content)
	return structured(t, result)
}

func (r *twoReplicas) polling(agg *AggregatorServer) bool {
	agg.deviceSignIns.mu.Lock()
	defer agg.deviceSignIns.mu.Unlock()
	return len(agg.deviceSignIns.polling) > 0
}

func TestChatGPTApp_TwoReplicas_OneCodePolledOnce(t *testing.T) {
	r := newTwoReplicas(t)

	first := loginOn(t, r.a)
	assert.Equal(t, "pending", first["status"])
	second := loginOn(t, r.b)
	assert.Equal(t, first["userCode"], second["userCode"])
	assert.Equal(t, first["expiresAt"], second["expiresAt"], "the outstanding code, not a new one")
	assert.Equal(t, 1, r.openai.Codes(), "one code across the replicas")

	status := statusOn(t, r.b)
	assert.Equal(t, pkgoauth.SessionServerStatusPending, status.Status)
	assert.Equal(t, chatgptlogintest.UserCode, status.UserCode)
	assert.True(t, r.polling(r.a))
	assert.False(t, r.polling(r.b), "the replica holding the lease polls it, no other")

	r.openai.Enter()
	r.ticks <- struct{}{}
	<-r.handler.storedC
	r.a.deviceSignIns.polls.Wait()

	assert.Equal(t, pkgoauth.SessionServerStatusConnected, statusOn(t, r.b).Status)
	assert.Equal(t, 1, r.openai.Exchanges())
	code, err := r.b.deviceSignIns.store.outstanding(context.Background(), deviceSignInKey{appSubject, "chatgpt"})
	require.NoError(t, err)
	assert.Nil(t, code, "an entered code is no longer outstanding")
}

func TestChatGPTApp_TwoReplicas_ACodeOutlivesItsPoller(t *testing.T) {
	r := newTwoReplicas(t)
	loginOn(t, r.a)
	r.a.deviceSignIns.shutdown() // a roll: the replica that polled the code stops

	status := statusOn(t, r.b)
	assert.Equal(t, pkgoauth.SessionServerStatusPending, status.Status)
	assert.Equal(t, chatgptlogintest.UserCode, status.UserCode)
	assert.True(t, r.polling(r.b), "the replica asked takes the code over")

	r.openai.Enter()
	r.ticks <- struct{}{}
	<-r.handler.storedC
	r.b.deviceSignIns.polls.Wait()
	assert.NotNil(t, r.handler.grants[appSubject+"|"+r.openai.URL])
	assert.Equal(t, 1, r.openai.Codes())
}

func TestChatGPTApp_TwoReplicas_DisconnectEndsThePollElsewhere(t *testing.T) {
	r := newTwoReplicas(t)
	loginOn(t, r.a)

	result, err := NewAuthToolProvider(r.b).handleAuthLogout(deviceCtx(), map[string]any{"app": "chatgpt"})
	require.NoError(t, err)
	require.False(t, result.IsError, "%v", result.Content)

	r.beatsA <- time.Now()
	r.a.deviceSignIns.polls.Wait()
	assert.False(t, r.polling(r.a))

	status := statusOn(t, r.a)
	assert.Equal(t, pkgoauth.SessionServerStatusAuthRequired, status.Status)
	assert.Empty(t, status.LastFailure)
	assert.Zero(t, r.openai.Exchanges())
}

func TestChatGPTApp_TwoReplicas_FailureReportedEverywhere(t *testing.T) {
	r := newTwoReplicas(t)
	loginOn(t, r.a)
	r.advance(chatgptlogin.CodeLifetime)
	r.ticks <- struct{}{}
	r.a.deviceSignIns.polls.Wait()

	status := statusOn(t, r.b)
	assert.Equal(t, pkgoauth.SessionServerStatusAuthRequired, status.Status)
	assert.Equal(t, chatgptlogin.ErrExpired.Error(), status.LastFailure)
}

func testCode(t *testing.T, userCode string) *chatgptlogin.Code {
	t.Helper()
	var code chatgptlogin.Code
	require.NoError(t, json.Unmarshal([]byte(`{"userCode":"`+userCode+`","verificationUrl":"https://auth.example/codex/device",`+
		`"expiresAt":"`+time.Now().Add(chatgptlogin.CodeLifetime).UTC().Format(time.RFC3339)+`","deviceAuthId":"da-`+userCode+`","interval":5000000000}`), &code))
	return &code
}

func TestDeviceSignInStores(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	encryptor, err := security.NewEncryptor(key)
	require.NoError(t, err)

	stores := map[string]func(t *testing.T) deviceSignInStore{
		"memory": func(*testing.T) deviceSignInStore { return newMemoryDeviceSignInStore() },
		"valkey": func(t *testing.T) deviceSignInStore {
			return newValkeyDeviceSignInStore(newTestValkeyClient(t), "muster:", nil)
		},
		"valkey encrypted": func(t *testing.T) deviceSignInStore {
			return newValkeyDeviceSignInStore(newTestValkeyClient(t), "muster:", encryptor)
		},
	}
	for name, newStore := range stores {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s := newStore(t)
			k := deviceSignInKey{"alice", "chatgpt"}
			other := deviceSignInKey{"bob", "chatgpt"}

			got, err := s.outstanding(ctx, k)
			require.NoError(t, err)
			assert.Nil(t, got)

			first, second := testCode(t, "AAAA-1111"), testCode(t, "BBBB-2222")
			got, err = s.claim(ctx, k, first)
			require.NoError(t, err)
			assert.Equal(t, "AAAA-1111", got.UserCode)
			got, err = s.claim(ctx, k, second)
			require.NoError(t, err)
			assert.Equal(t, "AAAA-1111", got.UserCode, "the outstanding code stays the person's")
			gotJSON, _ := json.Marshal(got)
			firstJSON, _ := json.Marshal(first)
			assert.JSONEq(t, string(firstJSON), string(gotJSON), "the code keeps what its poll needs")
			got, err = s.outstanding(ctx, other)
			require.NoError(t, err)
			assert.Nil(t, got, "per person")

			released, err := s.release(ctx, k, "BBBB-2222")
			require.NoError(t, err)
			assert.False(t, released, "another code is not released")
			released, err = s.release(ctx, k, "AAAA-1111")
			require.NoError(t, err)
			assert.True(t, released)
			got, err = s.outstanding(ctx, k)
			require.NoError(t, err)
			assert.Nil(t, got)

			_, err = s.claim(ctx, k, second)
			require.NoError(t, err)
			released, err = s.release(ctx, k, "")
			require.NoError(t, err)
			assert.True(t, released, "any code")

			held, err := s.lease(ctx, k, "a", time.Minute)
			require.NoError(t, err)
			assert.True(t, held)
			held, err = s.lease(ctx, k, "b", time.Minute)
			require.NoError(t, err)
			assert.False(t, held, "another holder has it")
			held, err = s.lease(ctx, k, "a", time.Minute)
			require.NoError(t, err)
			assert.True(t, held, "renewed")
			require.NoError(t, s.unlease(ctx, k, "b"))
			held, err = s.lease(ctx, k, "b", time.Minute)
			require.NoError(t, err)
			assert.False(t, held, "only its holder gives a lease up")
			require.NoError(t, s.unlease(ctx, k, "a"))
			held, err = s.lease(ctx, k, "b", time.Minute)
			require.NoError(t, err)
			assert.True(t, held)

			reason, err := s.failure(ctx, k)
			require.NoError(t, err)
			assert.Empty(t, reason)
			require.NoError(t, s.setFailure(ctx, k, "expired"))
			reason, err = s.failure(ctx, k)
			require.NoError(t, err)
			assert.Equal(t, "expired", reason)
			require.NoError(t, s.setFailure(ctx, k, ""))
			reason, err = s.failure(ctx, k)
			require.NoError(t, err)
			assert.Empty(t, reason)
		})
	}
}
