package config

import (
	"fmt"
	"testing"
)

// A callback path that overlaps the namespace callbacks lets an authorization
// server that matches redirect URIs by prefix accept a namespace's redirect
// URI for a client of muster's namespace, or the reverse (N16): muster refuses
// to start with one when it serves other namespaces; otherwise no flow uses a
// namespace callback and nothing changes.
func TestOAuthMCPClientConfig_ValidateNamespaceCallback(t *testing.T) {
	tests := []struct {
		callbackPath string
		otherNS      bool
		wantErr      bool
	}{
		{callbackPath: "/oauth", otherNS: false, wantErr: false},
		{callbackPath: "", otherNS: true, wantErr: false}, // the default /oauth/proxy/callback
		{callbackPath: "/oauth/proxy/callback", otherNS: true, wantErr: false},
		{callbackPath: "/callback", otherNS: true, wantErr: false},
		{callbackPath: "/oauth", otherNS: true, wantErr: true},
		{callbackPath: "/oauth/ns/", otherNS: true, wantErr: true},
		{callbackPath: "/oauth/ns/org-a/callback", otherNS: true, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("%s other namespaces %v", tc.callbackPath, tc.otherNS), func(t *testing.T) {
			cfg := OAuthMCPClientConfig{CallbackPath: tc.callbackPath, ServesOtherNamespaces: tc.otherNS}
			if err := cfg.ValidateNamespaceCallback(); (err != nil) != tc.wantErr {
				t.Errorf("ValidateNamespaceCallback(%q) = %v, want error %v", tc.callbackPath, err, tc.wantErr)
			}
		})
	}
}
