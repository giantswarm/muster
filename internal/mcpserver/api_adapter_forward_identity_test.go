package mcpserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// forwardIdentityRequest is a create request for a server pinned to its own
// authorization server, with or without the pin.
func forwardIdentityRequest(pinned bool) *api.MCPServerCreateRequest {
	auth := &api.MCPServerAuth{Type: api.MCPServerAuthTypeOAuth, ForwardIdentity: true}
	if pinned {
		auth.AuthorizationServer = &api.MCPServerAuthAuthorizationServer{Issuer: "https://github.com/login/oauth"}
	}
	return &api.MCPServerCreateRequest{
		Name: "two-hats",
		Type: string(api.MCPServerTypeStreamableHTTP),
		URL:  "https://two-hats.example.com/mcp",
		Auth: auth,
	}
}

// TestForwardIdentitySurvivesTheConversionRoundTrip: a dropped field fails
// nowhere -- the server simply never receives the ID token -- so the plumbing
// through create, read and update is asserted directly.
func TestForwardIdentitySurvivesTheConversionRoundTrip(t *testing.T) {
	adapter := &Adapter{namespace: "muster"}

	crd := adapter.convertRequestToCRD(forwardIdentityRequest(true))
	require.NotNil(t, crd.Spec.Auth)
	assert.True(t, crd.Spec.Auth.ForwardIdentity)

	info := convertCRDToInfo(crd)
	require.NotNil(t, info.Auth)
	assert.True(t, info.Auth.ForwardIdentity)
	assert.True(t, info.Auth.ForwardsIdentity())

	assert.Equal(t, crd.Spec.Auth, convertAPIAuthToCRD(info.Auth))
}

// TestValidateMCPServerForwardIdentity is the check core_mcpserver_create,
// update and validate run in both modes -- the only one filesystem mode has,
// since the CRD's CEL rule runs in the apiserver.
func TestValidateMCPServerForwardIdentity(t *testing.T) {
	adapter := &Adapter{namespace: "muster"}

	require.NoError(t, adapter.validateMCPServer(adapter.convertRequestToCRD(forwardIdentityRequest(true))))

	err := adapter.validateMCPServer(adapter.convertRequestToCRD(forwardIdentityRequest(false)))
	require.ErrorContains(t, err, "auth.forwardIdentity")
	require.ErrorContains(t, err, "auth.authorizationServer")
}
