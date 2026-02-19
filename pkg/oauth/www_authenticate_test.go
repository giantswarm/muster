package oauth

import (
	"errors"
	"net/http"
	"testing"
)

func TestParseWWWAuthenticate(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		want    *AuthChallenge
		wantErr bool
	}{
		{
			name:   "simple bearer",
			header: "Bearer",
			want: &AuthChallenge{
				Scheme: "Bearer",
			},
		},
		{
			name:   "bearer with realm",
			header: `Bearer realm="https://auth.example.com"`,
			want: &AuthChallenge{
				Scheme: "Bearer",
				Realm:  "https://auth.example.com",
				Issuer: "https://auth.example.com",
			},
		},
		{
			name:   "bearer with realm and scope",
			header: `Bearer realm="https://auth.example.com", scope="openid profile"`,
			want: &AuthChallenge{
				Scheme: "Bearer",
				Realm:  "https://auth.example.com",
				Issuer: "https://auth.example.com",
				Scope:  "openid profile",
			},
		},
		{
			name:   "bearer with resource_metadata",
			header: `Bearer realm="https://auth.example.com", resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource"`,
			want: &AuthChallenge{
				Scheme:              "Bearer",
				Realm:               "https://auth.example.com",
				Issuer:              "https://auth.example.com",
				ResourceMetadataURL: "https://mcp.example.com/.well-known/oauth-protected-resource",
			},
		},
		{
			name:   "bearer with error",
			header: `Bearer error="invalid_token", error_description="The token has expired"`,
			want: &AuthChallenge{
				Scheme:           "Bearer",
				Error:            "invalid_token",
				ErrorDescription: "The token has expired",
			},
		},
		{
			name:   "non-url realm",
			header: `Bearer realm="My Application"`,
			want: &AuthChallenge{
				Scheme: "Bearer",
				Realm:  "My Application",
				Issuer: "", // Non-URL realm doesn't become issuer
			},
		},
		{
			name:    "empty header",
			header:  "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseWWWAuthenticate(tt.header)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseWWWAuthenticate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			if got.Scheme != tt.want.Scheme {
				t.Errorf("Scheme = %q, want %q", got.Scheme, tt.want.Scheme)
			}
			if got.Realm != tt.want.Realm {
				t.Errorf("Realm = %q, want %q", got.Realm, tt.want.Realm)
			}
			if got.Issuer != tt.want.Issuer {
				t.Errorf("Issuer = %q, want %q", got.Issuer, tt.want.Issuer)
			}
			if got.Scope != tt.want.Scope {
				t.Errorf("Scope = %q, want %q", got.Scope, tt.want.Scope)
			}
			if got.ResourceMetadataURL != tt.want.ResourceMetadataURL {
				t.Errorf("ResourceMetadataURL = %q, want %q", got.ResourceMetadataURL, tt.want.ResourceMetadataURL)
			}
			if got.Error != tt.want.Error {
				t.Errorf("Error = %q, want %q", got.Error, tt.want.Error)
			}
			if got.ErrorDescription != tt.want.ErrorDescription {
				t.Errorf("ErrorDescription = %q, want %q", got.ErrorDescription, tt.want.ErrorDescription)
			}
		})
	}
}

func TestAuthChallenge_IsOAuthChallenge(t *testing.T) {
	tests := []struct {
		name      string
		challenge *AuthChallenge
		want      bool
	}{
		{
			name:      "nil challenge",
			challenge: nil,
			want:      false,
		},
		{
			name: "bearer with realm",
			challenge: &AuthChallenge{
				Scheme: "Bearer",
				Realm:  "https://auth.example.com",
			},
			want: true,
		},
		{
			name: "bearer with issuer",
			challenge: &AuthChallenge{
				Scheme: "Bearer",
				Issuer: "https://auth.example.com",
			},
			want: true,
		},
		{
			name: "bearer with resource_metadata",
			challenge: &AuthChallenge{
				Scheme:              "Bearer",
				ResourceMetadataURL: "https://mcp.example.com/.well-known/oauth-protected-resource",
			},
			want: true,
		},
		{
			name: "bearer without any",
			challenge: &AuthChallenge{
				Scheme: "Bearer",
			},
			want: false,
		},
		{
			name: "basic auth",
			challenge: &AuthChallenge{
				Scheme: "Basic",
				Realm:  "My App",
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.challenge.IsOAuthChallenge(); got != tt.want {
				t.Errorf("IsOAuthChallenge() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAuthChallenge_GetIssuer(t *testing.T) {
	tests := []struct {
		name      string
		challenge *AuthChallenge
		want      string
	}{
		{
			name:      "nil challenge",
			challenge: nil,
			want:      "",
		},
		{
			name: "explicit issuer",
			challenge: &AuthChallenge{
				Issuer: "https://issuer.example.com",
				Realm:  "https://realm.example.com",
			},
			want: "https://issuer.example.com",
		},
		{
			name: "url realm as issuer",
			challenge: &AuthChallenge{
				Realm: "https://auth.example.com",
			},
			want: "https://auth.example.com",
		},
		{
			name: "non-url realm",
			challenge: &AuthChallenge{
				Realm: "My Application",
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.challenge.GetIssuer(); got != tt.want {
				t.Errorf("GetIssuer() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseWWWAuthenticateFromResponse(t *testing.T) {
	tests := []struct {
		name       string
		resp       *http.Response
		wantNil    bool
		wantIssuer string
	}{
		{
			name:    "nil response",
			resp:    nil,
			wantNil: true,
		},
		{
			name: "200 OK",
			resp: &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Www-Authenticate": []string{`Bearer realm="https://auth.example.com"`}},
			},
			wantNil: true,
		},
		{
			name: "401 without header",
			resp: &http.Response{
				StatusCode: 401,
				Header:     http.Header{},
			},
			wantNil: true,
		},
		{
			name: "401 with header",
			resp: &http.Response{
				StatusCode: 401,
				Header:     http.Header{"Www-Authenticate": []string{`Bearer realm="https://auth.example.com"`}},
			},
			wantNil:    false,
			wantIssuer: "https://auth.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseWWWAuthenticateFromResponse(tt.resp)
			if tt.wantNil {
				if got != nil {
					t.Errorf("ParseWWWAuthenticateFromResponse() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("ParseWWWAuthenticateFromResponse() = nil, want non-nil")
			}
			if got.GetIssuer() != tt.wantIssuer {
				t.Errorf("Issuer = %q, want %q", got.GetIssuer(), tt.wantIssuer)
			}
		})
	}
}

func TestIs401Error(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "nil",
			err:  nil,
			want: false,
		},
		{
			name: "401 in message",
			err:  errors.New("failed with status 401"),
			want: true,
		},
		{
			name: "unauthorized in message",
			err:  errors.New("Unauthorized access"),
			want: true,
		},
		{
			name: "other error",
			err:  errors.New("connection timeout"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Is401Error(tt.err); got != tt.want {
				t.Errorf("Is401Error() = %v, want %v", got, tt.want)
			}
		})
	}
}
