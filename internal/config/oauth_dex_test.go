package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadConfigDexJWKSSettings(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		settings      string
		wantCanonical bool
		wantAlias     bool
		wantError     bool
	}{
		{name: "default", settings: "{}"},
		{name: "private JWKS", settings: "{allowPrivateIPJWKS: true}", wantCanonical: true},
		{name: "public JWKS", settings: "{allowPrivateIPJWKS: false}"},
		{name: "alias false", settings: "{allowPrivateIPOIDC: false}"},
		{name: "alias true", settings: "{allowPrivateIPOIDC: true}", wantAlias: true},
		{name: "alias true and canonical false", settings: "{allowPrivateIPOIDC: true, allowPrivateIPJWKS: false}", wantAlias: true},
		{name: "alias and canonical true", settings: "{allowPrivateIPOIDC: true, allowPrivateIPJWKS: true}", wantAlias: true, wantCanonical: true},
		{name: "alias false and canonical true", settings: "{allowPrivateIPOIDC: false, allowPrivateIPJWKS: true}", wantCanonical: true},
		{name: "invalid canonical boolean", settings: "{allowPrivateIPJWKS: invalid}", wantError: true},
		{name: "invalid alias boolean", settings: "{allowPrivateIPOIDC: invalid}", wantError: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			configDirectory := t.TempDir()
			contents := "aggregator:\n  oauth:\n    server:\n      dex: " + testCase.settings + "\n"
			require.NoError(t, os.WriteFile(filepath.Join(configDirectory, configFileName), []byte(contents), 0o600))

			loaded, err := LoadConfig(configDirectory)
			if testCase.wantError {
				require.ErrorContains(t, err, "cannot unmarshal")
				return
			}
			require.NoError(t, err)
			require.Equal(t, testCase.wantCanonical, loaded.Aggregator.OAuth.Server.Dex.AllowPrivateIPJWKS)
			require.Equal(t, testCase.wantAlias, loaded.Aggregator.OAuth.Server.Dex.AllowPrivateIPOIDC)
		})
	}
}
