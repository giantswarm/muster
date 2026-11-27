package credprovider

import (
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/giantswarm/mcp-oauth/security"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
)

func TestValkeyStoreRecoversUnreadableBindings(t *testing.T) {
	for _, encoding := range []string{"plaintext", "another key", "damaged ciphertext"} {
		t.Run(encoding, func(t *testing.T) {
			server := miniredis.RunT(t)
			client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{server.Addr()}, DisableCache: true})
			require.NoError(t, err)
			t.Cleanup(client.Close)
			key, err := security.GenerateKey()
			require.NoError(t, err)
			encryptor, err := security.NewEncryptor(key)
			require.NoError(t, err)
			store := NewValkeyStore(client, "muster:", encryptor)
			seedStore := NewValkeyStore(client, "muster:", nil)
			switch encoding {
			case "another key":
				previousKey, err := security.GenerateKey()
				require.NoError(t, err)
				seedStore.encryptor, err = security.NewEncryptor(previousKey)
				require.NoError(t, err)
			case "damaged ciphertext":
				seedStore.encryptor = encryptor
			}
			stored, err := seedStore.seal(Binding{Subject: "alice", Providers: []string{"github"}})
			require.NoError(t, err)
			if encoding == "damaged ciphertext" {
				stored = "!" + stored[1:]
			}

			for _, operation := range []string{"read", "rebind", "unbind"} {
				t.Run(operation, func(t *testing.T) {
					require.NoError(t, server.Set(store.key(testActor), stored))
					switch operation {
					case "read":
						binding, err := store.Get(t.Context(), testActor)
						require.NoError(t, err)
						require.Nil(t, binding)
					case "rebind":
						require.NoError(t, store.Put(t.Context(), testActor, Binding{Subject: "bob"}, time.Hour))
						binding, err := store.Get(t.Context(), testActor)
						require.NoError(t, err)
						require.NotNil(t, binding)
						require.Equal(t, "bob", binding.Subject)
						value, err := server.Get(store.key(testActor))
						require.NoError(t, err)
						require.False(t, strings.Contains(value, "bob"))
						require.Equal(t, time.Hour, server.TTL(store.key(testActor)))
					case "unbind":
						require.NoError(t, store.Delete(t.Context(), testActor, "alice"))
						require.False(t, server.Exists(store.key(testActor)))
					}
				})
			}
		})
	}
}

func TestValkeyStoreRejectsMalformedAuthenticatedBindings(t *testing.T) {
	server := miniredis.RunT(t)
	client, err := valkey.NewClient(valkey.ClientOption{InitAddress: []string{server.Addr()}, DisableCache: true})
	require.NoError(t, err)
	t.Cleanup(client.Close)
	key, err := security.GenerateKey()
	require.NoError(t, err)
	encryptor, err := security.NewEncryptor(key)
	require.NoError(t, err)
	store := NewValkeyStore(client, "muster:", encryptor)
	stored, err := encryptor.Encrypt("{")
	require.NoError(t, err)
	require.NoError(t, server.Set(store.key(testActor), stored))

	_, err = store.Get(t.Context(), testActor)
	require.ErrorContains(t, err, "decode binding")
	require.ErrorContains(t, store.Put(t.Context(), testActor, Binding{Subject: "alice"}, time.Hour), "decode binding")
	require.ErrorContains(t, store.Delete(t.Context(), testActor, "alice"), "decode binding")
}
