package oauth

import "testing"

func TestCanonicalResourceURI(t *testing.T) {
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
			name: "drops a trailing slash",
			raw:  "https://mcp.example.com/mcp/",
			want: "https://mcp.example.com/mcp",
		},
		{
			name: "drops the root slash",
			raw:  "https://mcp.example.com/",
			want: "https://mcp.example.com",
		},
		{
			name: "drops the fragment",
			raw:  "https://mcp.example.com/mcp#section",
			want: "https://mcp.example.com/mcp",
		},
		{
			name: "lowercases scheme and host",
			raw:  "HTTPS://MCP.Example.COM/MCP",
			want: "https://mcp.example.com/MCP",
		},
		{
			name: "keeps a non-default port",
			raw:  "https://mcp.example.com:8443/mcp",
			want: "https://mcp.example.com:8443/mcp",
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CanonicalResourceURI(tc.raw)
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

func TestCanonicalResourceURI_PreservesEncodingAndStripsDefaultPort(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "keeps a percent-encoded path segment",
			raw:  "https://mcp.example.com/team%2Fa/mcp",
			want: "https://mcp.example.com/team%2Fa/mcp",
		},
		{
			name: "strips the default https port",
			raw:  "https://mcp.example.com:443/mcp",
			want: "https://mcp.example.com/mcp",
		},
		{
			name: "strips the default http port",
			raw:  "http://localhost:80/mcp",
			want: "http://localhost/mcp",
		},
		{
			name: "keeps the query",
			raw:  "https://mcp.example.com/mcp?tenant=a",
			want: "https://mcp.example.com/mcp?tenant=a",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CanonicalResourceURI(tc.raw)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestCanonicalResourceURI_RejectsUserinfo(t *testing.T) {
	if _, err := CanonicalResourceURI("https://user:pass@mcp.example.com/mcp"); err == nil {
		t.Fatal("expected userinfo to be refused")
	}
}
