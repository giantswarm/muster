package mcpserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// discoveredSubjectRequest is a create request for an OAuth server that
// discovers its authorization server and files its grants under the person:
// the connector the developer portal registers.
func discoveredSubjectRequest() *api.MCPServerCreateRequest {
	return &api.MCPServerCreateRequest{
		Name: "connector",
		Type: string(api.MCPServerTypeStreamableHTTP),
		URL:  "https://connector.example.com/mcp",
		Auth: &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth, GrantScope: api.GrantScopeSubject},
	}
}

// TestGrantScopeSurvivesTheConversionRoundTrip: a dropped spec.auth.grantScope
// fails nowhere -- the server is simply session-scoped -- so the plumbing
// through create, read and update is asserted directly.
func TestGrantScopeSurvivesTheConversionRoundTrip(t *testing.T) {
	adapter := &Adapter{namespace: "muster"}

	crd := adapter.convertRequestToCRD(discoveredSubjectRequest())
	require.NotNil(t, crd.Spec.Auth)
	assert.Equal(t, api.GrantScopeSubject, crd.Spec.Auth.GrantScope)
	assert.Nil(t, crd.Spec.Auth.AuthorizationServer)

	info := convertCRDToInfo(crd)
	require.NotNil(t, info.Auth)
	assert.Equal(t, api.GrantScopeSubject, info.Auth.GrantScope)
	assert.True(t, info.Auth.DiscoversSubjectScopedIssuer())

	assert.Equal(t, crd.Spec.Auth, convertAPIAuthToCRD(info.Auth))
}

// TestValidateMCPServerGrantScope is the check core_mcpserver_create, update
// and validate run in both modes: an OAuth server without a pinned issuer is
// accepted with grantScope subject; one that cannot have a grant of its own,
// or whose pin says otherwise, is refused.
func TestValidateMCPServerGrantScope(t *testing.T) {
	adapter := &Adapter{namespace: "muster"}

	require.NoError(t, adapter.validateMCPServer(adapter.convertRequestToCRD(discoveredSubjectRequest())))

	forwarding := discoveredSubjectRequest()
	forwarding.Auth.ForwardToken = true
	err := adapter.validateMCPServer(adapter.convertRequestToCRD(forwarding))
	require.ErrorContains(t, err, "auth.grantScope")

	disagreeing := discoveredSubjectRequest()
	disagreeing.Auth.GrantScope = api.GrantScopeSession
	disagreeing.Auth.AuthorizationServer = &api.MCPServerAuthAuthorizationServer{Issuer: "https://github.com/login/oauth", GrantScope: api.GrantScopeSubject}
	err = adapter.validateMCPServer(adapter.convertRequestToCRD(disagreeing))
	require.ErrorContains(t, err, "disagree")
}
