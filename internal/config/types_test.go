package config

import "testing"

func TestOAuthConfig_GetEffectiveClientID(t *testing.T) {
	tests := []struct {
		name     string
		config   OAuthConfig
		expected string
	}{
		{
			name: "explicit clientId is returned as-is",
			config: OAuthConfig{
				ClientID:  "https://giantswarm.github.io/muster/oauth-client.json",
				PublicURL: "https://muster.example.com",
			},
			expected: "https://giantswarm.github.io/muster/oauth-client.json",
		},
		{
			name: "auto-derived from publicUrl when clientId is empty",
			config: OAuthConfig{
				ClientID:  "",
				PublicURL: "https://muster.example.com",
			},
			expected: "https://muster.example.com/.well-known/oauth-client.json",
		},
		{
			name: "auto-derived with custom CIMD path",
			config: OAuthConfig{
				ClientID:  "",
				PublicURL: "https://muster.example.com",
				CIMDPath:  "/oauth/client.json",
			},
			expected: "https://muster.example.com/oauth/client.json",
		},
		{
			name: "trailing slash in publicUrl is handled",
			config: OAuthConfig{
				ClientID:  "",
				PublicURL: "https://muster.example.com/",
			},
			expected: "https://muster.example.com/.well-known/oauth-client.json",
		},
		{
			name: "falls back to default when both are empty",
			config: OAuthConfig{
				ClientID:  "",
				PublicURL: "",
			},
			expected: DefaultOAuthClientID,
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

func TestOAuthConfig_ShouldServeCIMD(t *testing.T) {
	tests := []struct {
		name     string
		config   OAuthConfig
		expected bool
	}{
		{
			name: "true when enabled, publicUrl set, clientId empty",
			config: OAuthConfig{
				Enabled:   true,
				PublicURL: "https://muster.example.com",
				ClientID:  "",
			},
			expected: true,
		},
		{
			name: "true when clientId matches auto-derived value",
			config: OAuthConfig{
				Enabled:   true,
				PublicURL: "https://muster.example.com",
				ClientID:  "https://muster.example.com/.well-known/oauth-client.json",
			},
			expected: true,
		},
		{
			name: "false when OAuth is disabled",
			config: OAuthConfig{
				Enabled:   false,
				PublicURL: "https://muster.example.com",
				ClientID:  "",
			},
			expected: false,
		},
		{
			name: "false when publicUrl is empty",
			config: OAuthConfig{
				Enabled:   true,
				PublicURL: "",
				ClientID:  "",
			},
			expected: false,
		},
		{
			name: "false when clientId is external",
			config: OAuthConfig{
				Enabled:   true,
				PublicURL: "https://muster.example.com",
				ClientID:  "https://giantswarm.github.io/muster/oauth-client.json",
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

func TestOAuthConfig_GetCIMDPath(t *testing.T) {
	tests := []struct {
		name     string
		config   OAuthConfig
		expected string
	}{
		{
			name: "returns configured path",
			config: OAuthConfig{
				CIMDPath: "/oauth/client.json",
			},
			expected: "/oauth/client.json",
		},
		{
			name: "returns default when not configured",
			config: OAuthConfig{
				CIMDPath: "",
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

func TestOAuthConfig_GetRedirectURI(t *testing.T) {
	tests := []struct {
		name     string
		config   OAuthConfig
		expected string
	}{
		{
			name: "constructs redirect URI correctly",
			config: OAuthConfig{
				PublicURL:    "https://muster.example.com",
				CallbackPath: "/oauth/callback",
			},
			expected: "https://muster.example.com/oauth/callback",
		},
		{
			name: "handles trailing slash in publicUrl",
			config: OAuthConfig{
				PublicURL:    "https://muster.example.com/",
				CallbackPath: "/oauth/callback",
			},
			expected: "https://muster.example.com/oauth/callback",
		},
		{
			name: "uses default callback path when not set",
			config: OAuthConfig{
				PublicURL:    "https://muster.example.com",
				CallbackPath: "",
			},
			expected: "https://muster.example.com/oauth/callback",
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

func TestTrimTrailingSlash(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"https://example.com/", "https://example.com"},
		{"https://example.com", "https://example.com"},
		{"/", ""},
		{"", ""},
	}

	for _, tc := range tests {
		result := trimTrailingSlash(tc.input)
		if result != tc.expected {
			t.Errorf("trimTrailingSlash(%q) = %q, expected %q", tc.input, result, tc.expected)
		}
	}
}
