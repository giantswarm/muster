package config

import "testing"

// A callback path that overlaps the namespace callbacks lets an authorization
// server that matches redirect URIs by prefix accept a namespace's redirect
// URI for a client of muster's namespace, or the reverse (N16): muster refuses
// to start with one.
func TestOAuthMCPClientConfig_ValidateNamespaceCallback(t *testing.T) {
	tests := []struct {
		callbackPath string
		wantErr      bool
	}{
		{callbackPath: "", wantErr: false}, // the default /oauth/proxy/callback
		{callbackPath: "/oauth/proxy/callback", wantErr: false},
		{callbackPath: "/callback", wantErr: false},
		{callbackPath: "/oauth", wantErr: true},
		{callbackPath: "/oauth/ns/", wantErr: true},
		{callbackPath: "/oauth/ns/org-a/callback", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.callbackPath, func(t *testing.T) {
			cfg := OAuthMCPClientConfig{CallbackPath: tc.callbackPath}
			if err := cfg.ValidateNamespaceCallback(); (err != nil) != tc.wantErr {
				t.Errorf("ValidateNamespaceCallback(%q) = %v, want error %v", tc.callbackPath, err, tc.wantErr)
			}
		})
	}
}
