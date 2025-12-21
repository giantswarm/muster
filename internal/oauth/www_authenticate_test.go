package oauth

import (
	"testing"
)

func TestParseWWWAuthenticate(t *testing.T) {
	tests := []struct {
		name           string
		header         string
		wantScheme     string
		wantRealm      string
		wantScope      string
		wantError      string
		wantErrorDesc  string
		wantResMetaURL string
		wantNil        bool
	}{
		{
			name:       "basic bearer with realm",
			header:     `Bearer realm="https://auth.example.com"`,
			wantScheme: "Bearer",
			wantRealm:  "https://auth.example.com",
		},
		{
			name:       "bearer with realm and scope",
			header:     `Bearer realm="https://auth.example.com", scope="openid profile email"`,
			wantScheme: "Bearer",
			wantRealm:  "https://auth.example.com",
			wantScope:  "openid profile email",
		},
		{
			name:           "bearer with resource_metadata",
			header:         `Bearer realm="https://auth.example.com", resource_metadata="https://mcp.example.com/.well-known/oauth-authorization-server"`,
			wantScheme:     "Bearer",
			wantRealm:      "https://auth.example.com",
			wantResMetaURL: "https://mcp.example.com/.well-known/oauth-authorization-server",
		},
		{
			name:          "bearer with error",
			header:        `Bearer realm="https://auth.example.com", error="invalid_token", error_description="The token has expired"`,
			wantScheme:    "Bearer",
			wantRealm:     "https://auth.example.com",
			wantError:     "invalid_token",
			wantErrorDesc: "The token has expired",
		},
		{
			name:       "scheme only",
			header:     "Bearer",
			wantScheme: "Bearer",
		},
		{
			name:    "empty header",
			header:  "",
			wantNil: true,
		},
		{
			name:       "basic auth (not OAuth)",
			header:     `Basic realm="Access to staging site"`,
			wantScheme: "Basic",
			wantRealm:  "Access to staging site",
		},
		{
			name:           "bearer with multiple parameters",
			header:         `Bearer realm="https://dex.example.com", scope="openid groups", resource_metadata="https://mcp.example.com/.well-known/oauth-authorization-server"`,
			wantScheme:     "Bearer",
			wantRealm:      "https://dex.example.com",
			wantScope:      "openid groups",
			wantResMetaURL: "https://mcp.example.com/.well-known/oauth-authorization-server",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			params := ParseWWWAuthenticate(tc.header)

			if tc.wantNil {
				if params != nil {
					t.Errorf("Expected nil, got %+v", params)
				}
				return
			}

			if params == nil {
				t.Fatal("Expected non-nil params, got nil")
			}

			if params.Scheme != tc.wantScheme {
				t.Errorf("Scheme: expected %q, got %q", tc.wantScheme, params.Scheme)
			}

			if params.Realm != tc.wantRealm {
				t.Errorf("Realm: expected %q, got %q", tc.wantRealm, params.Realm)
			}

			if params.Scope != tc.wantScope {
				t.Errorf("Scope: expected %q, got %q", tc.wantScope, params.Scope)
			}

			if params.Error != tc.wantError {
				t.Errorf("Error: expected %q, got %q", tc.wantError, params.Error)
			}

			if params.ErrorDescription != tc.wantErrorDesc {
				t.Errorf("ErrorDescription: expected %q, got %q", tc.wantErrorDesc, params.ErrorDescription)
			}

			if params.ResourceMetadataURL != tc.wantResMetaURL {
				t.Errorf("ResourceMetadataURL: expected %q, got %q", tc.wantResMetaURL, params.ResourceMetadataURL)
			}
		})
	}
}

func TestWWWAuthenticateParams_IsOAuthChallenge(t *testing.T) {
	tests := []struct {
		name     string
		params   *WWWAuthenticateParams
		expected bool
	}{
		{
			name:     "nil params",
			params:   nil,
			expected: false,
		},
		{
			name: "bearer with realm",
			params: &WWWAuthenticateParams{
				Scheme: "Bearer",
				Realm:  "https://auth.example.com",
			},
			expected: true,
		},
		{
			name: "bearer with resource_metadata",
			params: &WWWAuthenticateParams{
				Scheme:              "Bearer",
				ResourceMetadataURL: "https://mcp.example.com/.well-known/oauth-authorization-server",
			},
			expected: true,
		},
		{
			name: "bearer without realm or resource_metadata",
			params: &WWWAuthenticateParams{
				Scheme: "Bearer",
			},
			expected: false,
		},
		{
			name: "basic auth (not OAuth)",
			params: &WWWAuthenticateParams{
				Scheme: "Basic",
				Realm:  "https://auth.example.com",
			},
			expected: false,
		},
		{
			name: "case insensitive bearer",
			params: &WWWAuthenticateParams{
				Scheme: "bearer",
				Realm:  "https://auth.example.com",
			},
			expected: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.params.IsOAuthChallenge()
			if result != tc.expected {
				t.Errorf("Expected IsOAuthChallenge to be %v, got %v", tc.expected, result)
			}
		})
	}
}

func TestWWWAuthenticateParams_GetIssuer(t *testing.T) {
	tests := []struct {
		name     string
		params   *WWWAuthenticateParams
		expected string
	}{
		{
			name:     "nil params",
			params:   nil,
			expected: "",
		},
		{
			name: "with realm",
			params: &WWWAuthenticateParams{
				Scheme: "Bearer",
				Realm:  "https://auth.example.com",
			},
			expected: "https://auth.example.com",
		},
		{
			name: "empty realm",
			params: &WWWAuthenticateParams{
				Scheme: "Bearer",
			},
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.params.GetIssuer()
			if result != tc.expected {
				t.Errorf("Expected GetIssuer to return %q, got %q", tc.expected, result)
			}
		})
	}
}
