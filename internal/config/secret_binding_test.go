package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_SecretBinding(t *testing.T) {
	load := func(t *testing.T, yaml string) (MusterConfig, error) {
		t.Helper()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, configFileName), []byte(yaml), 0o600))
		return LoadConfig(dir)
	}

	t.Run("protected Secrets", func(t *testing.T) {
		cfg, err := load(t, "secretBinding:\n  protectedSecrets: [muster-oauth]\n")
		require.NoError(t, err)
		assert.Equal(t, []string{"muster-oauth"}, cfg.SecretBinding.ProtectedSecrets)
	})

	t.Run("a config rendered with a mode still loads", func(t *testing.T) {
		cfg, err := load(t, "secretBinding:\n  mode: enforce\n  protectedSecrets: [muster-oauth]\n")
		require.NoError(t, err)
		assert.Equal(t, []string{"muster-oauth"}, cfg.SecretBinding.ProtectedSecrets)
	})
}
