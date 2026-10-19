package aggregator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// A login to a server outside muster's namespace starts its flow under the
// issuer key of its namespace, and muster always checks that the resource it
// declares is on its own origin, so the sign-in never gets a token bound to
// another server (D9). A server in muster's namespace keeps its plain issuer
// and its declared resource, as before.
func TestHandleAuthLogin_ChallengeOfATenantServer(t *testing.T) {
	const issuer = "https://idp.example.com"
	tests := []struct {
		name         string
		server       string
		declared     string
		wantIssuer   string
		wantResource string
	}{
		{name: "tenant, resource of another server", server: "org-a/miro", declared: "https://shared.example.com/mcp",
			wantIssuer: "ns:org-a:" + issuer, wantResource: "http://127.0.0.1:1/mcp"},
		{name: "tenant, resource on its own origin", server: "org-a/miro", declared: "http://127.0.0.1:1/other",
			wantIssuer: "ns:org-a:" + issuer, wantResource: "http://127.0.0.1:1/other"},
		{name: "muster's namespace", server: "miro", declared: "https://shared.example.com/mcp",
			wantIssuer: issuer, wantResource: "https://shared.example.com/mcp"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := newSuspensionTestServer(t)
			registerSuspendableServer(t, a.registry, tc.server, false, api.StateAuthRequired)
			getServerInfo(t, a.registry, tc.server).SetAuthInfo(&AuthInfo{Issuer: issuer, Scope: "mcp:read", Resource: tc.declared})
			handler := &challengeMockOAuthHandler{issuerMockOAuthHandler: issuerMockOAuthHandler{enabled: true}}
			api.RegisterOAuthHandler(handler)
			t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

			result, err := NewAuthToolProvider(a).handleAuthLogin(testSessionCtx(), map[string]any{"server": tc.server})
			require.NoError(t, err)
			require.False(t, result.IsError, "%v", result.Content)

			require.Len(t, handler.challenged, 1)
			assert.Equal(t, tc.wantIssuer, handler.challenged[0].Issuer)
			assert.Equal(t, tc.wantResource, handler.challenged[0].Resource)
		})
	}
}
