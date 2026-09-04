package mcpserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

func TestConvertAuth_AuthorizationServerPinRoundTrips(t *testing.T) {
	crd := &musterv1alpha1.MCPServerAuth{
		Type: "oauth",
		AuthorizationServer: &musterv1alpha1.MCPServerAuthAuthorizationServer{
			Issuer:                "https://github.com/login/oauth/",
			Scopes:                "repo read:org",
			AuthorizationEndpoint: "https://github.com/login/oauth/authorize/",
			TokenEndpoint:         "https://github.com/login/oauth/access_token",
			ClientCredentialsSecretRef: &musterv1alpha1.ClientCredentialsSecretRef{
				Name:      "github-oauth-client",
				Namespace: "agent-platform",
			},
			GrantScope: api.GrantScopeSubject,
		},
	}

	converted := convertCRDAuthToAPI(crd)
	require.NotNil(t, converted)
	require.NotNil(t, converted.AuthorizationServer)
	as := converted.AuthorizationServer
	assert.Equal(t, "https://github.com/login/oauth", as.Issuer, "issuer is normalized")
	assert.Equal(t, "https://github.com/login/oauth/authorize", as.AuthorizationEndpoint, "endpoints are normalized")
	assert.Equal(t, "https://github.com/login/oauth/access_token", as.TokenEndpoint)
	assert.True(t, as.HasPinnedEndpoints())
	assert.True(t, as.SubjectScoped())
	require.NotNil(t, as.ClientCredentialsSecretRef)
	assert.Equal(t, "github-oauth-client", as.ClientCredentialsSecretRef.Name)
	assert.Equal(t, "agent-platform", as.ClientCredentialsSecretRef.Namespace)

	back := convertAPIAuthToCRD(converted)
	require.NotNil(t, back)
	require.NotNil(t, back.AuthorizationServer)
	assert.Equal(t, musterv1alpha1.IssuerURL("https://github.com/login/oauth/authorize"), back.AuthorizationServer.AuthorizationEndpoint)
	assert.Equal(t, musterv1alpha1.IssuerURL("https://github.com/login/oauth/access_token"), back.AuthorizationServer.TokenEndpoint)
	assert.Equal(t, api.GrantScopeSubject, back.AuthorizationServer.GrantScope)
	require.NotNil(t, back.AuthorizationServer.ClientCredentialsSecretRef)
	assert.Equal(t, "github-oauth-client", back.AuthorizationServer.ClientCredentialsSecretRef.Name)
}

func TestConvertAuth_PlainIssuerOverrideStaysPlain(t *testing.T) {
	converted := convertCRDAuthToAPI(&musterv1alpha1.MCPServerAuth{
		Type:                "oauth",
		AuthorizationServer: &musterv1alpha1.MCPServerAuthAuthorizationServer{Issuer: "https://cf.mcp.example.com"},
	})
	require.NotNil(t, converted.AuthorizationServer)
	assert.False(t, converted.AuthorizationServer.HasPinnedEndpoints())
	assert.False(t, converted.AuthorizationServer.SubjectScoped())
	assert.Nil(t, converted.AuthorizationServer.ClientCredentialsSecretRef)
}
