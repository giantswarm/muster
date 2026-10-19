package app

import (
	"testing"

	"github.com/giantswarm/muster/v5/internal/config"
)

// muster lists MCP servers with the session's Dex ID token, so
// mcpServersInAllNamespaces needs the OAuth server on Dex (agent-permissions
// D9, Q13): with Google, or without the OAuth server, muster refuses to start
// instead of showing every person no server.
func TestValidateListsAsCaller(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.OAuthServerConfig
		wantErr bool
	}{
		{name: "dex", cfg: config.OAuthServerConfig{Enabled: true, Provider: "dex"}},
		{name: "google", cfg: config.OAuthServerConfig{Enabled: true, Provider: "google"}, wantErr: true},
		{name: "no OAuth server", cfg: config.OAuthServerConfig{Provider: "dex"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateListsAsCaller(tc.cfg); (err != nil) != tc.wantErr {
				t.Errorf("validateListsAsCaller = %v, want error %v", err, tc.wantErr)
			}
		})
	}
}
