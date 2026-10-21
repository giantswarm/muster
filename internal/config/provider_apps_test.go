package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestValidateProviderApps(t *testing.T) {
	github := ProviderAppConfig{
		Name:                  "github",
		Issuer:                "https://github.com/apps/giantswarm-workspaces",
		AuthorizationEndpoint: "https://github.com/login/oauth/authorize",
		TokenEndpoint:         "https://github.com/login/oauth/access_token",
		ExpectedIssuer:        "https://github.com/login/oauth",
	}
	gitlab := ProviderAppConfig{Name: "gitlab", Issuer: "https://gitlab.example.com", GitUser: "oauth2", RevocationEndpoint: "https://gitlab.example.com/oauth/revoke"}
	require.NoError(t, ValidateProviderApps(nil))
	require.NoError(t, ValidateProviderApps([]ProviderAppConfig{github, gitlab}))

	for name, apps := range map[string][]ProviderAppConfig{
		"name with capitals":      {{Name: "GitHub", Issuer: github.Issuer}},
		"name with a colon":       {{Name: "app:github", Issuer: github.Issuer}},
		"duplicate name":          {github, {Name: "github", Issuer: "https://other.example.com"}},
		"duplicate issuer":        {github, {Name: "github-2", Issuer: github.Issuer + "/"}},
		"relative issuer":         {{Name: "x", Issuer: "/oauth"}},
		"missing issuer":          {{Name: "x"}},
		"one endpoint":            {{Name: "x", Issuer: github.Issuer, AuthorizationEndpoint: github.AuthorizationEndpoint}},
		"expected issuer alone":   {{Name: "x", Issuer: github.Issuer, ExpectedIssuer: github.ExpectedIssuer}},
		"bad revocation":          {{Name: "x", Issuer: github.Issuer, RevocationEndpoint: "ftp://x"}},
		"secret ref without name": {{Name: "x", Issuer: github.Issuer, ClientCredentialsSecretRef: &BrokerSecretRefConfig{}}},
		"git user with a colon":   {{Name: "x", Issuer: github.Issuer, GitUser: "a:b"}},
		"client id and secret":    {{Name: "x", Issuer: github.Issuer, ClientID: "c", ClientCredentialsSecretRef: &BrokerSecretRefConfig{Name: "s"}}},
		"unknown flow":            {{Name: "x", Issuer: github.Issuer, Flow: "deviceCode"}},
		"device code with secret": {{Name: "x", Issuer: github.Issuer, Flow: ProviderAppFlowChatGPTDeviceCode, ClientCredentialsSecretRef: &BrokerSecretRefConfig{Name: "s"}}},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Error(t, ValidateProviderApps(apps))
		})
	}
}

func TestProviderAppsFromYAML(t *testing.T) {
	var cfg MusterConfig
	require.NoError(t, yaml.Unmarshal([]byte(`
aggregator:
  providerApps:
    - name: gitlab
      issuer: https://gitlab.example.com
      revocationEndpoint: https://gitlab.example.com/oauth/revoke
      clientCredentialsSecretRef:
        name: gitlab-app
      scopes: api read_user
      gitUser: oauth2
`), &cfg))
	require.Len(t, cfg.Aggregator.ProviderApps, 1)
	app := cfg.Aggregator.ProviderApps[0]
	assert.Equal(t, "gitlab", app.Name)
	assert.Equal(t, "gitlab-app", app.ClientCredentialsSecretRef.Name)
	assert.Equal(t, "oauth2", app.GitUser)
	assert.Equal(t, "api read_user", app.Scopes)
}

func TestProviderAppsWithDefaults(t *testing.T) {
	assert.Nil(t, ProviderAppsWithDefaults(nil))

	got := ProviderAppsWithDefaults([]ProviderAppConfig{
		{Name: "chatgpt", Flow: ProviderAppFlowChatGPTDeviceCode},
		{Name: "gitlab", Issuer: "https://gitlab.example.com"},
	})
	assert.Equal(t, ProviderAppConfig{
		Name:                  "chatgpt",
		Flow:                  ProviderAppFlowChatGPTDeviceCode,
		Issuer:                "https://auth.openai.com",
		ClientID:              "app_EMoamEEZ73f0CkXaXp7hrann",
		AuthorizationEndpoint: "https://auth.openai.com/oauth/authorize",
		TokenEndpoint:         "https://auth.openai.com/oauth/token",
		Scopes:                "openid profile email offline_access",
	}, got[0], "the Codex CLI's client and OpenAI's endpoints")
	assert.Equal(t, ProviderAppConfig{Name: "gitlab", Issuer: "https://gitlab.example.com"}, got[1], "another App is left alone")
	require.NoError(t, ValidateProviderApps(got))

	own := ProviderAppsWithDefaults([]ProviderAppConfig{
		{Name: "chatgpt", Flow: ProviderAppFlowChatGPTDeviceCode, Issuer: "https://auth.example.com/", ClientID: "own", Scopes: "openid"},
	})[0]
	assert.Equal(t, "own", own.ClientID)
	assert.Equal(t, "openid", own.Scopes)
	assert.Equal(t, "https://auth.example.com/oauth/token", own.TokenEndpoint)
}
