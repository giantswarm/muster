package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestRequestStateKeyFile(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "root-key")
	require.NoError(t, os.WriteFile(keyFile, []byte("fixture-root-key\n"), 0600))
	settings := MusterConfig{Aggregator: AggregatorConfig{RequestState: RequestStateConfig{KeyFile: keyFile}}}
	require.NoError(t, resolveSecretFiles(&settings))
	require.Equal(t, "fixture-root-key", settings.Aggregator.RequestState.Key)
	require.Empty(t, settings.Aggregator.OAuth.Server.EncryptionKey)
	serialized, err := yaml.Marshal(settings)
	require.NoError(t, err)
	require.NotContains(t, string(serialized), "fixture-root-key")
	settings.Aggregator.RequestState = RequestStateConfig{KeyFile: filepath.Join(t.TempDir(), "missing")}
	require.ErrorContains(t, resolveSecretFiles(&settings), "requestState root key")
}
