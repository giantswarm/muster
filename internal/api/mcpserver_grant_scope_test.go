package api

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestValidateGrantScope is the filesystem-mode counterpart of the CRD's
// spec.auth.grantScope CEL rules (TestGrantScopeCELEnvtest): one of session
// and subject, only for an OAuth server with a sign-in of its own, and in
// agreement with authorizationServer.grantScope. SubjectScoped and
// DiscoversSubjectScopedIssuer read the result the way the aggregator does.
func TestValidateGrantScope(t *testing.T) {
	pin := func(grantScope string) *MCPServerAuthAuthorizationServer {
		return &MCPServerAuthAuthorizationServer{Issuer: "https://github.com/login/oauth", GrantScope: grantScope}
	}

	tests := []struct {
		name       string
		auth       *MCPServerAuth
		wantError  string
		subject    bool
		discovered bool
	}{
		{name: "no auth block"},
		{name: "oauth without a grant scope", auth: &MCPServerAuth{Type: MCPServerAuthTypeOAuth}},
		{
			name:       "subject on a discovered authorization server",
			auth:       &MCPServerAuth{Type: MCPServerAuthTypeOAuth, GrantScope: GrantScopeSubject},
			subject:    true,
			discovered: true,
		},
		{
			name: "session on a discovered authorization server",
			auth: &MCPServerAuth{Type: MCPServerAuthTypeOAuth, GrantScope: GrantScopeSession},
		},
		{
			name:    "subject next to a pin without a grant scope applies to the pinned issuer",
			auth:    &MCPServerAuth{Type: MCPServerAuthTypeOAuth, GrantScope: GrantScopeSubject, AuthorizationServer: pin("")},
			subject: true,
		},
		{
			name:    "both saying subject",
			auth:    &MCPServerAuth{Type: MCPServerAuthTypeOAuth, GrantScope: GrantScopeSubject, AuthorizationServer: pin(GrantScopeSubject)},
			subject: true,
		},
		{
			name:    "the pin alone saying subject",
			auth:    &MCPServerAuth{Type: MCPServerAuthTypeOAuth, AuthorizationServer: pin(GrantScopeSubject)},
			subject: true,
		},
		{
			name:      "disagreeing with the pin",
			auth:      &MCPServerAuth{Type: MCPServerAuthTypeOAuth, GrantScope: GrantScopeSession, AuthorizationServer: pin(GrantScopeSubject)},
			wantError: "disagree",
			subject:   true,
		},
		{
			name:      "an unknown value",
			auth:      &MCPServerAuth{Type: MCPServerAuthTypeOAuth, GrantScope: "person"},
			wantError: `must be "session" or "subject"`,
		},
		{
			name:      "no type",
			auth:      &MCPServerAuth{GrantScope: GrantScopeSubject},
			wantError: `needs auth.type "oauth"`,
			subject:   true,
		},
		{
			name:      "type none",
			auth:      &MCPServerAuth{Type: "none", GrantScope: GrantScopeSubject},
			wantError: `needs auth.type "oauth"`,
			subject:   true,
		},
		{
			name:      "next to forwardToken",
			auth:      &MCPServerAuth{Type: MCPServerAuthTypeOAuth, GrantScope: GrantScopeSubject, ForwardToken: true},
			wantError: "neither auth.forwardToken nor auth.tokenExchange",
			subject:   true,
		},
		{
			name:      "next to an enabled token exchange",
			auth:      &MCPServerAuth{Type: MCPServerAuthTypeOAuth, GrantScope: GrantScopeSubject, TokenExchange: &TokenExchangeConfig{Enabled: true}},
			wantError: "neither auth.forwardToken nor auth.tokenExchange",
			subject:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateGrantScope(tt.auth)
			if tt.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantError)
			}
			require.Equal(t, tt.subject, tt.auth.SubjectScoped())
			require.Equal(t, tt.discovered, tt.auth.DiscoversSubjectScopedIssuer())
		})
	}
}
