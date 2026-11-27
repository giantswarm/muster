package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInitialAccessTokenConfig(t *testing.T) {
	for _, test := range []struct {
		name        string
		config      string
		environment map[string]string
		want        string
		deprecated  bool
	}{
		{name: "new key", config: "initialAccessToken: current-secret", want: "current-secret"},
		{name: "old key", config: "registrationToken: legacy-secret", want: "legacy-secret", deprecated: true},
		{name: "new key wins", config: "initialAccessToken: current-secret\nregistrationToken: legacy-secret", want: "current-secret", deprecated: true},
		{name: "new file", config: "initialAccessTokenFile: TOKEN_FILE", want: "file-secret"},
		{name: "old file", config: "registrationTokenFile: TOKEN_FILE", want: "file-secret", deprecated: true},
		{name: "new inline beats old file", config: "initialAccessToken: current-secret\nregistrationTokenFile: missing", want: "current-secret", deprecated: true},
		{name: "new file beats old inline", config: "initialAccessTokenFile: TOKEN_FILE\nregistrationToken: legacy-secret", want: "file-secret", deprecated: true},
		{name: "new env", environment: map[string]string{"OAUTH_INITIAL_ACCESS_TOKEN": "current-secret"}, want: "current-secret"},
		{name: "old env", environment: map[string]string{"OAUTH_REGISTRATION_ACCESS_TOKEN": "legacy-secret"}, want: "legacy-secret", deprecated: true},
		{name: "new env wins", environment: map[string]string{"OAUTH_INITIAL_ACCESS_TOKEN": "current-secret", "OAUTH_REGISTRATION_ACCESS_TOKEN": "legacy-secret"}, want: "current-secret", deprecated: true},
		{name: "new env file", environment: map[string]string{"OAUTH_INITIAL_ACCESS_TOKEN_FILE": "TOKEN_FILE"}, want: "file-secret"},
		{name: "old env file", environment: map[string]string{"OAUTH_REGISTRATION_ACCESS_TOKEN_FILE": "TOKEN_FILE"}, want: "file-secret", deprecated: true},
		{name: "new config beats old env", config: "initialAccessToken: current-secret", environment: map[string]string{"OAUTH_REGISTRATION_ACCESS_TOKEN_FILE": "missing"}, want: "current-secret", deprecated: true},
		{name: "new env file beats old config", config: "registrationToken: legacy-secret", environment: map[string]string{"OAUTH_INITIAL_ACCESS_TOKEN_FILE": "TOKEN_FILE"}, want: "file-secret", deprecated: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "token")
			require.NoError(t, os.WriteFile(path, []byte("file-secret\n"), 0600))
			for _, name := range []string{"OAUTH_INITIAL_ACCESS_TOKEN", "OAUTH_INITIAL_ACCESS_TOKEN_FILE", "OAUTH_REGISTRATION_ACCESS_TOKEN", "OAUTH_REGISTRATION_ACCESS_TOKEN_FILE"} {
				value := test.environment[name]
				if value == "TOKEN_FILE" {
					value = path
				}
				t.Setenv(name, value)
			}
			var output bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })
			if test.config != "" {
				content := "aggregator:\n  oauth:\n    server:\n      " + strings.ReplaceAll(strings.ReplaceAll(test.config, "TOKEN_FILE", path), "\n", "\n      ") + "\n"
				require.NoError(t, os.WriteFile(filepath.Join(directory, "config.yaml"), []byte(content), 0600))
			}
			configuration, err := LoadConfig(directory)
			require.NoError(t, err)
			require.Equal(t, test.want, configuration.Aggregator.OAuth.Server.InitialAccessToken)
			if test.deprecated {
				require.Contains(t, output.String(), "DEPRECATED:")
				require.Regexp(t, "initialAccessToken|OAUTH_INITIAL_ACCESS_TOKEN", output.String())
			} else {
				require.NotContains(t, output.String(), "DEPRECATED:")
			}
			for _, secret := range []string{"current-secret", "legacy-secret", "file-secret"} {
				require.NotContains(t, output.String(), secret)
			}
		})
	}
}
