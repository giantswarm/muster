package testing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	valkeystore "github.com/giantswarm/mcp-oauth/storage/valkey"
)

func TestValkeyFixtureSupportsStandaloneAtomicRefreshAfterRestart(t *testing.T) {
	manager := newStorageTestManager(t)
	require.NoError(t, manager.startValkey(t.Context(), "standalone", &MusterPreConfiguration{Storage: &StorageConfig{Type: StorageValkey}}, manager.logger))
	backend := manager.valkeyFor("standalone")
	for _, phase := range []string{"initial", "restarted"} {
		t.Run(phase, func(t *testing.T) {
			if phase == "restarted" {
				require.NoError(t, manager.StopValkey("standalone"))
				require.NoError(t, manager.StartValkey("standalone"))
			}
			store, err := valkeystore.New(valkeystore.Config{Address: backend.addr(), KeyPrefix: phase + ":"})
			require.NoError(t, err)
			t.Cleanup(store.Close)
			require.NoError(t, store.SaveRefreshTokenWithFamily(t.Context(), "refresh", "person", "client", "family", 0, time.Now().Add(time.Hour)))
			userID, clientID, err := store.AtomicConsumeRefreshToken(t.Context(), "refresh")
			require.NoError(t, err)
			require.Equal(t, "person", userID)
			require.Equal(t, "client", clientID)
			require.NoError(t, store.DeleteToken(t.Context(), "refresh"))
		})
	}
}
