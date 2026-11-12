package testing

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A muster_broker or provider_apps section the harness cannot render fails
// the scenario's setup instead of starting muster without it.
func TestConfigureOAuthForInstance_RefusesUnrenderableSections(t *testing.T) {
	m := &musterInstanceManager{logger: NewSilentLogger(false, false)}
	idp := MockOAuthServerConfig{Name: "idp"}

	cases := map[string]struct {
		config  *MusterPreConfiguration
		wantErr string
	}{
		"broker without muster's own OAuth server": {
			config:  &MusterPreConfiguration{MockOAuthServers: []MockOAuthServerConfig{idp}, MusterBroker: &MusterBrokerConfig{}},
			wantErr: "use_as_muster_oauth_server",
		},
		"provider App naming an unknown OAuth server": {
			config: &MusterPreConfiguration{
				MockOAuthServers: []MockOAuthServerConfig{idp},
				ProviderApps:     []ProviderAppTestConfig{{Name: "github", OAuthServerRef: "githb-app"}},
			},
			wantErr: `unknown OAuth server "githb-app"`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := m.configureOAuthForInstance(map[string]interface{}{}, tc.config, 20000, "instance", t.TempDir(), m.logger)
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}
