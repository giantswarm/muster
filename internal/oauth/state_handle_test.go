package oauth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
)

func TestStateHandle(t *testing.T) {
	for _, backend := range []string{"memory", "valkey"} {
		t.Run(backend, func(t *testing.T) {
			var store StateStorer
			if backend == "memory" {
				store = NewStateStore()
			} else {
				client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{miniredis.RunT(t).Addr()}, DisableCache: true})
				require.NoError(t, err)
				t.Cleanup(client.Close)
				store = NewValkeyStateStore(client, "test:", nil)
			}
			t.Cleanup(store.Stop)
			params := StateParams{SessionID: strings.Repeat("session", 1000), UserID: "person@example.com", CodeVerifier: "private-verifier"}
			handle, err := store.GenerateState(params, func(state string) (string, error) {
				require.Len(t, state, 44)
				return "https://issuer.example/authorize?state=" + state, nil
			})
			require.NoError(t, err)
			random, err := base64.URLEncoding.Strict().DecodeString(handle)
			require.NoError(t, err)
			require.Len(t, random, 32)
			require.False(t, json.Valid(random))
			require.NotContains(t, handle, params.UserID)
			require.NotNil(t, store.Update(handle, func(state *OAuthState) { state.Scope = "read" }))
			require.Nil(t, store.ValidateState("!"+handle[1:]))
			state := store.ValidateState(handle)
			require.NotNil(t, state)
			require.Equal(t, params.SessionID, state.SessionID)
			require.Equal(t, params.CodeVerifier, state.CodeVerifier)
			require.Equal(t, "read", state.Scope)
			require.Nil(t, store.ValidateState(handle))

			legacy, err := store.GenerateState(params, nil)
			require.NoError(t, err)
			encoded, err := json.Marshal(OAuthState{Nonce: legacy, UserID: "untrusted-user"})
			require.NoError(t, err)
			legacyState := base64.URLEncoding.EncodeToString(encoded)
			require.NotNil(t, store.Update(legacyState, func(state *OAuthState) { state.Scope = "legacy" }))
			restored := store.ValidateState(legacyState)
			require.NotNil(t, restored)
			require.Equal(t, params.UserID, restored.UserID)
			require.Equal(t, "legacy", restored.Scope)
			require.Nil(t, store.ValidateState(legacy))

			handle, err = store.GenerateState(params, nil)
			require.NoError(t, err)
			var winners atomic.Int32
			var workers sync.WaitGroup
			for range 32 {
				workers.Go(func() {
					if store.ValidateState(handle) != nil {
						winners.Add(1)
					}
				})
			}
			workers.Wait()
			require.EqualValues(t, 1, winners.Load())
		})
	}
}
