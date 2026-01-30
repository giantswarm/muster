package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOAuthClientConfig_GetEffectiveClientID(t *testing.T) {
	tests := []struct {
		name     string
		config   OAuthClientConfig
		expected string
	}{
		{
			name: "explicit clientId is returned as-is",
			config: OAuthClientConfig{
				ClientID:  "https://external.example.com/oauth-client.json",
				PublicURL: "https://muster.example.com",
			},
			expected: "https://external.example.com/oauth-client.json",
		},
		{
			name: "auto-derived from publicUrl when clientId is empty",
			config: OAuthClientConfig{
				ClientID:  "",
				PublicURL: "https://muster.example.com",
			},
			expected: "https://muster.example.com/.well-known/oauth-client.json",
		},
		{
			name: "auto-derived with custom CIMD path",
			config: OAuthClientConfig{
				ClientID:  "",
				PublicURL: "https://muster.example.com",
				CIMD: OAuthCIMDConfig{
					Path: "/oauth/client.json",
				},
			},
			expected: "https://muster.example.com/oauth/client.json",
		},
		{
			name: "trailing slash in publicUrl is handled",
			config: OAuthClientConfig{
				ClientID:  "",
				PublicURL: "https://muster.example.com/",
			},
			expected: "https://muster.example.com/.well-known/oauth-client.json",
		},
		{
			name: "returns empty when both clientId and publicUrl are empty",
			config: OAuthClientConfig{
				ClientID:  "",
				PublicURL: "",
			},
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.config.GetEffectiveClientID()
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestOAuthClientConfig_ShouldServeCIMD(t *testing.T) {
	tests := []struct {
		name     string
		config   OAuthClientConfig
		expected bool
	}{
		{
			name: "true when enabled, publicUrl set, clientId empty",
			config: OAuthClientConfig{
				Enabled:   true,
				PublicURL: "https://muster.example.com",
				ClientID:  "",
			},
			expected: true,
		},
		{
			name: "true when clientId matches auto-derived value",
			config: OAuthClientConfig{
				Enabled:   true,
				PublicURL: "https://muster.example.com",
				ClientID:  "https://muster.example.com/.well-known/oauth-client.json",
			},
			expected: true,
		},
		{
			name: "false when OAuth is disabled",
			config: OAuthClientConfig{
				Enabled:   false,
				PublicURL: "https://muster.example.com",
				ClientID:  "",
			},
			expected: false,
		},
		{
			name: "false when publicUrl is empty",
			config: OAuthClientConfig{
				Enabled:   true,
				PublicURL: "",
				ClientID:  "",
			},
			expected: false,
		},
		{
			name: "false when clientId is external",
			config: OAuthClientConfig{
				Enabled:   true,
				PublicURL: "https://muster.example.com",
				ClientID:  "https://external.example.com/oauth-client.json",
			},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.config.ShouldServeCIMD()
			if result != tc.expected {
				t.Errorf("Expected %v, got %v", tc.expected, result)
			}
		})
	}
}

func TestOAuthClientConfig_GetCIMDPath(t *testing.T) {
	tests := []struct {
		name     string
		config   OAuthClientConfig
		expected string
	}{
		{
			name: "returns configured path",
			config: OAuthClientConfig{
				CIMD: OAuthCIMDConfig{
					Path: "/oauth/client.json",
				},
			},
			expected: "/oauth/client.json",
		},
		{
			name: "returns default when not configured",
			config: OAuthClientConfig{
				CIMD: OAuthCIMDConfig{
					Path: "",
				},
			},
			expected: DefaultOAuthCIMDPath,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.config.GetCIMDPath()
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestOAuthClientConfig_GetRedirectURI(t *testing.T) {
	tests := []struct {
		name     string
		config   OAuthClientConfig
		expected string
	}{
		{
			name: "constructs redirect URI correctly",
			config: OAuthClientConfig{
				PublicURL:    "https://muster.example.com",
				CallbackPath: "/oauth/callback",
			},
			expected: "https://muster.example.com/oauth/callback",
		},
		{
			name: "handles trailing slash in publicUrl",
			config: OAuthClientConfig{
				PublicURL:    "https://muster.example.com/",
				CallbackPath: "/oauth/callback",
			},
			expected: "https://muster.example.com/oauth/callback",
		},
		{
			name: "uses default proxy callback path when not set",
			config: OAuthClientConfig{
				PublicURL:    "https://muster.example.com",
				CallbackPath: "",
			},
			expected: "https://muster.example.com/oauth/proxy/callback",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.config.GetRedirectURI()
			if result != tc.expected {
				t.Errorf("Expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestAuthConfig_IsSilentRefreshEnabled(t *testing.T) {
	t.Run("returns true when SilentRefresh is nil (default)", func(t *testing.T) {
		cfg := AuthConfig{}
		assert.True(t, cfg.IsSilentRefreshEnabled())
	})

	t.Run("returns true when SilentRefresh is explicitly true", func(t *testing.T) {
		silentRefresh := true
		cfg := AuthConfig{SilentRefresh: &silentRefresh}
		assert.True(t, cfg.IsSilentRefreshEnabled())
	})

	t.Run("returns false when SilentRefresh is explicitly false", func(t *testing.T) {
		silentRefresh := false
		cfg := AuthConfig{SilentRefresh: &silentRefresh}
		assert.False(t, cfg.IsSilentRefreshEnabled())
	})
}
