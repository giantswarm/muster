package oauth

import (
	"net/http"
	"testing"
)

func TestParseWWWAuthenticate(t *testing.T) {
	tests := []struct {
		name           string
		header         string
		wantScheme     string
		wantRealm      string
		wantIssuer     string
		wantResourceMD string
		wantScope      string
		wantError      bool
	}{
		{
			name:       "Basic Bearer",
			header:     `Bearer realm="https://dex.example.com"`,
			wantScheme: "Bearer",
			wantRealm:  "https://dex.example.com",
			wantIssuer: "https://dex.example.com",
		},
		{
			name:           "Bearer with resource_metadata",
			header:         `Bearer realm="https://dex.example.com", resource_metadata="https://muster.example.com/.well-known/oauth-protected-resource"`,
			wantScheme:     "Bearer",
			wantRealm:      "https://dex.example.com",
			wantIssuer:     "https://dex.example.com",
			wantResourceMD: "https://muster.example.com/.well-known/oauth-protected-resource",
		},
		{
			name:       "Bearer with scope",
			header:     `Bearer realm="https://dex.example.com", scope="openid profile email"`,
			wantScheme: "Bearer",
			wantRealm:  "https://dex.example.com",
			wantIssuer: "https://dex.example.com",
			wantScope:  "openid profile email",
		},
		{
			name:       "Basic scheme (non-OAuth)",
			header:     `Basic realm="Secure Area"`,
			wantScheme: "Basic",
			wantRealm:  "Secure Area",
		},
		{
			name:       "Bearer only (no params)",
			header:     "Bearer",
			wantScheme: "Bearer",
		},
		{
			name:      "Empty header",
			header:    "",
			wantError: true,
		},
		{
			name:       "Bearer with error",
			header:     `Bearer realm="https://dex.example.com", error="invalid_token", error_description="Token expired"`,
			wantScheme: "Bearer",
			wantRealm:  "https://dex.example.com",
			wantIssuer: "https://dex.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			challenge, err := ParseWWWAuthenticate(tt.header)

			if tt.wantError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if challenge.Scheme != tt.wantScheme {
				t.Errorf("Scheme = %q, want %q", challenge.Scheme, tt.wantScheme)
			}

			if challenge.Realm != tt.wantRealm {
				t.Errorf("Realm = %q, want %q", challenge.Realm, tt.wantRealm)
			}

			if challenge.Issuer != tt.wantIssuer {
				t.Errorf("Issuer = %q, want %q", challenge.Issuer, tt.wantIssuer)
			}

			if challenge.ResourceMetadataURL != tt.wantResourceMD {
				t.Errorf("ResourceMetadataURL = %q, want %q", challenge.ResourceMetadataURL, tt.wantResourceMD)
			}

			if challenge.Scope != tt.wantScope {
				t.Errorf("Scope = %q, want %q", challenge.Scope, tt.wantScope)
			}
		})
	}
}

func TestExtractAuthChallengeFromResponse(t *testing.T) {
	// Helper to create response with WWW-Authenticate header
	makeResp := func(statusCode int, wwwAuth string) *http.Response {
		resp := &http.Response{
			StatusCode: statusCode,
			Header:     make(http.Header),
		}
		if wwwAuth != "" {
			resp.Header.Set("WWW-Authenticate", wwwAuth)
		}
		return resp
	}

	tests := []struct {
		name       string
		resp       *http.Response
		wantNil    bool
		wantIssuer string
	}{
		{
			name:       "401 with WWW-Authenticate",
			resp:       makeResp(http.StatusUnauthorized, `Bearer realm="https://dex.example.com"`),
			wantNil:    false,
			wantIssuer: "https://dex.example.com",
		},
		{
			name:    "401 without WWW-Authenticate",
			resp:    makeResp(http.StatusUnauthorized, ""),
			wantNil: true,
		},
		{
			name:    "200 response (not 401)",
			resp:    makeResp(http.StatusOK, `Bearer realm="https://dex.example.com"`),
			wantNil: true,
		},
		{
			name:    "nil response",
			resp:    nil,
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			challenge := ExtractAuthChallengeFromResponse(tt.resp)

			if tt.wantNil {
				if challenge != nil {
					t.Error("Expected nil challenge, got non-nil")
				}
				return
			}

			if challenge == nil {
				t.Fatal("Expected challenge, got nil")
			}

			if challenge.Issuer != tt.wantIssuer {
				t.Errorf("Issuer = %q, want %q", challenge.Issuer, tt.wantIssuer)
			}
		})
	}
}

func TestIs401Unauthorized(t *testing.T) {
	tests := []struct {
		name string
		resp *http.Response
		want bool
	}{
		{
			name: "401 response",
			resp: &http.Response{StatusCode: http.StatusUnauthorized},
			want: true,
		},
		{
			name: "200 response",
			resp: &http.Response{StatusCode: http.StatusOK},
			want: false,
		},
		{
			name: "403 response",
			resp: &http.Response{StatusCode: http.StatusForbidden},
			want: false,
		},
		{
			name: "nil response",
			resp: nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Is401Unauthorized(tt.resp)
			if got != tt.want {
				t.Errorf("Is401Unauthorized() = %v, want %v", got, tt.want)
			}
		})
	}
}
