package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestValidateForwardIdentity is the filesystem-mode counterpart of the CRD's
// forwardIdentity CEL rule (TestForwardIdentityCELEnvtest): the field needs
// type oauth and an authorizationServer pin. ForwardsIdentity agrees with it,
// so a configuration that fails it never sends the ID token.
func TestValidateForwardIdentity(t *testing.T) {
	pin := &MCPServerAuthAuthorizationServer{Issuer: "https://github.com/login/oauth"}

	tests := []struct {
		name      string
		auth      *MCPServerAuth
		wantError string
		forwards  bool
	}{
		{name: "no auth block"},
		{name: "field unset on a pinned server", auth: &MCPServerAuth{Type: MCPServerAuthTypeOAuth, AuthorizationServer: pin}},
		{
			name:     "pinned oauth server",
			auth:     &MCPServerAuth{Type: MCPServerAuthTypeOAuth, AuthorizationServer: pin, ForwardIdentity: true},
			forwards: true,
		},
		{
			name:      "oauth without a pin",
			auth:      &MCPServerAuth{Type: MCPServerAuthTypeOAuth, ForwardIdentity: true},
			wantError: `needs auth.type "oauth" and auth.authorizationServer`,
		},
		{
			name:      "no type",
			auth:      &MCPServerAuth{AuthorizationServer: pin, ForwardIdentity: true},
			wantError: `needs auth.type "oauth" and auth.authorizationServer`,
		},
		{
			name:      "type none",
			auth:      &MCPServerAuth{Type: "none", ForwardIdentity: true},
			wantError: `needs auth.type "oauth" and auth.authorizationServer`,
		},
		{
			name:      "sigv4",
			auth:      &MCPServerAuth{Type: MCPServerAuthTypeSigV4, SigV4: &MCPServerSigV4{Region: testSigV4Region}, ForwardIdentity: true},
			wantError: `needs auth.type "oauth" and auth.authorizationServer`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateForwardIdentity(tt.auth)
			if tt.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantError)
			}
			require.Equal(t, tt.forwards, tt.auth.ForwardsIdentity())
		})
	}
}
