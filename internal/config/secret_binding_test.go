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

	t.Run("defaults to warn", func(t *testing.T) {
		assert.Equal(t, SecretBindingModeWarn, GetDefaultConfigWithRoles().SecretBinding.Mode)
		cfg, err := load(t, "secretBinding:\n  protectedSecrets: [muster-oauth]\n")
		require.NoError(t, err)
		assert.Equal(t, SecretBindingModeWarn, cfg.SecretBinding.Mode)
		assert.Equal(t, []string{"muster-oauth"}, cfg.SecretBinding.ProtectedSecrets)
	})

	t.Run("enforce", func(t *testing.T) {
		cfg, err := load(t, "secretBinding:\n  mode: enforce\n")
		require.NoError(t, err)
		assert.Equal(t, SecretBindingModeEnforce, cfg.SecretBinding.Mode)
	})

	t.Run("refuses an unknown mode", func(t *testing.T) {
		_, err := load(t, "secretBinding:\n  mode: off\n")
		require.ErrorContains(t, err, `secretBinding.mode "off"`)
	})
}
