package oauth

import "testing"

// TestValidateAdvertisedMetadataURL pins the rule mcp-go applies to a metadata
// pointer parsed from an untrusted WWW-Authenticate header: the document must
// live on the origin of the protected resource that advertised it.
func TestValidateAdvertisedMetadataURL(t *testing.T) {
	const serverURL = "https://mcp.example.com/mcp"

	tests := []struct {
		name      string
		candidate string
		serverURL string
		wantErr   bool
	}{
		{
			name:      "same origin, other path",
			candidate: "https://mcp.example.com/.well-known/oauth-protected-resource/mcp",
			serverURL: serverURL,
		},
		{
			name:      "scheme and host differ only in case",
			candidate: "HTTPS://MCP.Example.COM/.well-known/oauth-protected-resource",
			serverURL: serverURL,
		},
		{
			name:      "another host",
			candidate: "https://attacker.example.net/.well-known/oauth-protected-resource",
			serverURL: serverURL,
			wantErr:   true,
		},
		{
			name:      "another scheme",
			candidate: "http://mcp.example.com/.well-known/oauth-protected-resource",
			serverURL: serverURL,
			wantErr:   true,
		},
		{
			name:      "another port on the same host",
			candidate: "https://mcp.example.com:8443/.well-known/oauth-protected-resource",
			serverURL: serverURL,
			wantErr:   true,
		},
		{
			name:      "relative candidate",
			candidate: "/.well-known/oauth-protected-resource",
			serverURL: serverURL,
			wantErr:   true,
		},
		{
			name:      "server URL is not absolute",
			candidate: "https://mcp.example.com/.well-known/oauth-protected-resource",
			serverURL: "mcp.example.com/mcp",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAdvertisedMetadataURL(tt.candidate, tt.serverURL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateAdvertisedMetadataURL(%q, %q) error = %v, wantErr = %v",
					tt.candidate, tt.serverURL, err, tt.wantErr)
			}
		})
	}
}

// TestValidateAdvertisedResource pins that a resource identifier declared in a
// document an advertised pointer named must be on the MCP server's own origin.
// Without the check a backend could bind muster's token to another party's
// resource. The path may differ: an mcp-oauth backend serving at "<base>/mcp"
// declares "<base>".
func TestValidateAdvertisedResource(t *testing.T) {
	tests := []struct {
		name      string
		declared  string
		serverURL string
		wantErr   bool
	}{
		{
			name:      "declares the server itself",
			declared:  "https://mcp.example.com/mcp",
			serverURL: "https://mcp.example.com/mcp",
		},
		{
			name:      "declares the base while serving a transport path",
			declared:  "https://mcp.example.com",
			serverURL: "https://mcp.example.com/mcp",
		},
		{
			name:      "host case is not a difference",
			declared:  "https://MCP.Example.com/mcp",
			serverURL: "https://mcp.example.com/mcp",
		},
		{
			name:      "declares another party",
			declared:  "https://payments.example.com/api",
			serverURL: "https://mcp.example.com/mcp",
			wantErr:   true,
		},
		{
			name:      "declares another port on the same host",
			declared:  "https://mcp.example.com:8443/mcp",
			serverURL: "https://mcp.example.com/mcp",
			wantErr:   true,
		},
		{
			name:      "declared value is relative",
			declared:  "/mcp",
			serverURL: "https://mcp.example.com/mcp",
			wantErr:   true,
		},
		{
			name:      "server URL is not absolute",
			declared:  "https://mcp.example.com/mcp",
			serverURL: "mcp.example.com/mcp",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAdvertisedResource(tt.declared, tt.serverURL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateAdvertisedResource(%q, %q) error = %v, wantErr = %v",
					tt.declared, tt.serverURL, err, tt.wantErr)
			}
		})
	}
}
