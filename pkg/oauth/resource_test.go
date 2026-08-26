package oauth

import (
	"slices"
	"testing"
)

// TestDeriveResourceURI pins the derivation to the rule mcp-go applies to the
// same URL: drop the query and the fragment, change nothing else. mcp-go sends
// its own derived value on token refresh, so any further normalization here
// would bind the initial token and the refreshed token to different audiences.
func TestDeriveResourceURI(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    string
		wantErr bool
	}{
		{
			name: "keeps the most specific form",
			raw:  "https://mcp.example.com/mcp",
			want: "https://mcp.example.com/mcp",
		},
		{
			name: "keeps a trailing slash",
			raw:  "https://mcp.example.com/mcp/",
			want: "https://mcp.example.com/mcp/",
		},
		{
			name: "keeps the root slash",
			raw:  "https://mcp.example.com/",
			want: "https://mcp.example.com/",
		},
		{
			name: "drops the fragment",
			raw:  "https://mcp.example.com/mcp#section",
			want: "https://mcp.example.com/mcp",
		},
		{
			name: "drops the query",
			raw:  "https://mcp.example.com/mcp?tenant=a",
			want: "https://mcp.example.com/mcp",
		},
		{
			name: "keeps the host case",
			raw:  "https://MCP.Example.COM/MCP",
			want: "https://MCP.Example.COM/MCP",
		},
		{
			name: "keeps the default https port",
			raw:  "https://mcp.example.com:443/mcp",
			want: "https://mcp.example.com:443/mcp",
		},
		{
			name: "keeps the default http port",
			raw:  "http://localhost:80/mcp",
			want: "http://localhost:80/mcp",
		},
		{
			name: "keeps a non-default port",
			raw:  "https://mcp.example.com:8443/mcp",
			want: "https://mcp.example.com:8443/mcp",
		},
		{
			name: "keeps a percent-encoded path segment",
			raw:  "https://mcp.example.com/team%2Fa/mcp",
			want: "https://mcp.example.com/team%2Fa/mcp",
		},
		{
			name:    "rejects a relative URI",
			raw:     "/mcp",
			wantErr: true,
		},
		{
			name:    "rejects a URI without a host",
			raw:     "https://",
			wantErr: true,
		},
		{
			name:    "rejects an empty URI",
			raw:     "  ",
			wantErr: true,
		},
		{
			name:    "rejects userinfo",
			raw:     "https://user:pass@mcp.example.com/mcp",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DeriveResourceURI(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %q, got %q", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

// TestValidateResourceURI covers the checks a declared resource identifier
// must pass. A value that is valid but not canonical passes unchanged: the
// declaring server, not muster, decides its own identifier.
func TestValidateResourceURI(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{
			name: "accepts a value that is not canonical",
			raw:  "https://MCP.example.com:443/mcp/",
		},
		{
			name: "accepts a query",
			raw:  "https://mcp.example.com/mcp?tenant=a",
		},
		{
			name:    "refuses a fragment",
			raw:     "https://mcp.example.com/mcp#section",
			wantErr: true,
		},
		{
			name:    "refuses userinfo",
			raw:     "https://user:pass@mcp.example.com/mcp",
			wantErr: true,
		},
		{
			name:    "refuses a relative URI",
			raw:     "/mcp",
			wantErr: true,
		},
		{
			name:    "refuses an empty value",
			raw:     "   ",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateResourceURI(tc.raw)
			if tc.wantErr && err == nil {
				t.Fatalf("expected %q to be refused", tc.raw)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected %q to be accepted, got: %v", tc.raw, err)
			}
		})
	}
}

// TestResolveResourceIndicator pins the choice between a declared value and a
// derived one. A declared value that parses is sent exactly as declared; one
// that does not is reported and the login still proceeds with a derived value.
func TestResolveResourceIndicator(t *testing.T) {
	tests := []struct {
		name           string
		declared       string
		targetURL      string
		want           string
		wantDeclaredEr bool
		wantErr        bool
	}{
		{
			name:      "declared value wins and is not rewritten",
			declared:  "https://mcp.example.com:443/mcp/",
			targetURL: "https://mcp.example.com/mcp",
			want:      "https://mcp.example.com:443/mcp/",
		},
		{
			name:      "no declared value derives from the target",
			targetURL: "https://mcp.example.com/mcp?tenant=a#frag",
			want:      "https://mcp.example.com/mcp",
		},
		{
			name:           "unusable declared value falls back to a derived one",
			declared:       "not-a-uri",
			targetURL:      "https://mcp.example.com/mcp",
			want:           "https://mcp.example.com/mcp",
			wantDeclaredEr: true,
		},
		{
			name:           "unusable declared value and no target yields nothing",
			declared:       "not-a-uri",
			wantDeclaredEr: true,
		},
		{
			name: "no declared value and no target yields nothing",
		},
		{
			name:      "unusable target is an error",
			targetURL: "not-a-uri",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveResourceIndicator(tt.declared, tt.targetURL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr = %v", err, tt.wantErr)
			}
			if got.Value != tt.want {
				t.Errorf("Value = %q, want %q", got.Value, tt.want)
			}
			if (got.DeclaredErr != nil) != tt.wantDeclaredEr {
				t.Errorf("DeclaredErr = %v, want one = %v", got.DeclaredErr, tt.wantDeclaredEr)
			}
		})
	}
}

func TestProtectedResourceMetadataURLs(t *testing.T) {
	tests := []struct {
		name      string
		serverURL string
		want      []string
	}{
		{
			name:      "path-inserted first, then root",
			serverURL: "https://mcp.example.com/v1/mcp",
			want: []string{
				"https://mcp.example.com/.well-known/oauth-protected-resource/v1/mcp",
				"https://mcp.example.com/.well-known/oauth-protected-resource",
			},
		},
		{
			name:      "trailing slash is not a path segment",
			serverURL: "https://mcp.example.com/mcp/",
			want: []string{
				"https://mcp.example.com/.well-known/oauth-protected-resource/mcp",
				"https://mcp.example.com/.well-known/oauth-protected-resource",
			},
		},
		{
			name:      "no path yields the root form only",
			serverURL: "https://mcp.example.com/",
			want:      []string{"https://mcp.example.com/.well-known/oauth-protected-resource"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ProtectedResourceMetadataURLs(tt.serverURL)
			if err != nil {
				t.Fatalf("ProtectedResourceMetadataURLs: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestProtectedResourceMetadataURLs_RejectsRelativeURL(t *testing.T) {
	if _, err := ProtectedResourceMetadataURLs("/mcp"); err == nil {
		t.Error("expected a relative URL to be refused")
	}
}
