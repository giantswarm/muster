package config

import (
	"testing"
)

func TestOAuthMCPClientConfig_GetEffectiveClientID(t *testing.T) {
	tests := []struct {
		name     string
		config   OAuthMCPClientConfig
		expected string
	}{
		{
			name: "explicit clientId is returned as-is",
			config: OAuthMCPClientConfig{
				ClientID:  "https://external.example.com/oauth-client.json",
				PublicURL: "https://muster.example.com",
			},
			expected: "https://external.example.com/oauth-client.json",
		},
		{
			name: "auto-derived from publicUrl when clientId is empty",
			config: OAuthMCPClientConfig{
				ClientID:  "",
				PublicURL: "https://muster.example.com",
			},
			expected: "https://muster.example.com/.well-known/oauth-client.json",
		},
		{
			name: "auto-derived with custom CIMD path",
			config: OAuthMCPClientConfig{
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
			config: OAuthMCPClientConfig{
				ClientID:  "",
				PublicURL: "https://muster.example.com/",
			},
			expected: "https://muster.example.com/.well-known/oauth-client.json",
		},
		{
			name: "returns empty when both clientId and publicUrl are empty",
			config: OAuthMCPClientConfig{
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

func TestOAuthMCPClientConfig_ShouldServeCIMD(t *testing.T) {
	tests := []struct {
		name     string
		config   OAuthMCPClientConfig
		expected bool
	}{
		{
			name: "true when enabled, publicUrl set, clientId empty",
			config: OAuthMCPClientConfig{
				Enabled:   true,
				PublicURL: "https://muster.example.com",
				ClientID:  "",
			},
			expected: true,
		},
		{
			name: "true when clientId matches auto-derived value",
			config: OAuthMCPClientConfig{
				Enabled:   true,
				PublicURL: "https://muster.example.com",
				ClientID:  "https://muster.example.com/.well-known/oauth-client.json",
			},
			expected: true,
		},
		{
			name: "false when OAuth is disabled",
			config: OAuthMCPClientConfig{
				Enabled:   false,
				PublicURL: "https://muster.example.com",
				ClientID:  "",
			},
			expected: false,
		},
		{
			name: "false when publicUrl is empty",
			config: OAuthMCPClientConfig{
				Enabled:   true,
				PublicURL: "",
				ClientID:  "",
			},
			expected: false,
		},
		{
			name: "false when clientId is external",
			config: OAuthMCPClientConfig{
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

func TestOAuthMCPClientConfig_GetCIMDPath(t *testing.T) {
	tests := []struct {
		name     string
		config   OAuthMCPClientConfig
		expected string
	}{
		{
			name: "returns configured path",
			config: OAuthMCPClientConfig{
				CIMD: OAuthCIMDConfig{
					Path: "/oauth/client.json",
				},
			},
			expected: "/oauth/client.json",
		},
		{
			name: "returns default when not configured",
			config: OAuthMCPClientConfig{
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

func TestOAuthMCPClientConfig_GetRedirectURI(t *testing.T) {
	tests := []struct {
		name     string
		config   OAuthMCPClientConfig
		expected string
	}{
		{
			name: "constructs redirect URI correctly",
			config: OAuthMCPClientConfig{
				PublicURL:    "https://muster.example.com",
				CallbackPath: "/oauth/callback",
			},
			expected: "https://muster.example.com/oauth/callback",
		},
		{
			name: "handles trailing slash in publicUrl",
			config: OAuthMCPClientConfig{
				PublicURL:    "https://muster.example.com/",
				CallbackPath: "/oauth/callback",
			},
			expected: "https://muster.example.com/oauth/callback",
		},
		{
			name: "uses default proxy callback path when not set",
			config: OAuthMCPClientConfig{
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

// TestAuthConfig_IsSilentRefreshEnabled removed - AuthConfig is deprecated.
// CLI authentication settings are now controlled via command-line flags only.
