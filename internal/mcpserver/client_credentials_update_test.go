package mcpserver

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/v5/internal/api"
)

// generatedServer is test-server in test-ns with a client stored under name.
func generatedServer(name string) *musterv1alpha1.MCPServer {
	obj := existingServer()
	obj.UID = testServerUID
	obj.Spec.URL = "https://mcp.example.com/mcp"
	obj.Spec.Auth = &musterv1alpha1.MCPServerAuth{
		Type: "oauth",
		AuthorizationServer: &musterv1alpha1.MCPServerAuthAuthorizationServer{
			Issuer:                     "https://as.example.com",
			ClientCredentialsSecretRef: &musterv1alpha1.ClientCredentialsSecretRef{Name: name},
		},
	}
	return obj
}

func TestClientCredentials_Replace(t *testing.T) {
	logs := captureBindingLogs(t)
	const old = "test-server-oauth-client-aaaaa"
	h := newCredsHarness(t, generatedServer(old))

	text, isErr := h.call(t, "mcpserver_update", map[string]interface{}{
		"name":              "test-server",
		"clientCredentials": map[string]interface{}{"clientId": testClientID, "clientSecret": testClientSecret},
	})
	require.False(t, isErr, text)

	secrets := h.secrets(t)
	require.Len(t, secrets, 1)
	secret := secrets[0]
	assert.NotEqual(t, old, secret.Name)
	assert.True(t, isGeneratedClientSecretName("test-server", secret.Name))
	require.Len(t, secret.OwnerReferences, 1)
	assert.Equal(t, testServerUID, secret.OwnerReferences[0].UID)

	assert.Equal(t, secret.Name, h.server(t).Spec.Auth.AuthorizationServer.ClientCredentialsSecretRef.Name)
	assert.Equal(t, []string{"create Secret", "update MCPServer"}, h.writes, "no update or delete on Secrets")
	assert.Contains(t, text, "updated successfully")
	assert.NotContains(t, text, testClientSecret)
	assert.NotContains(t, logs.String(), testClientSecret)
}

// A server whose client is a GitOps Secret switches to a stored one.
func TestClientCredentials_ReplaceExplicitRef(t *testing.T) {
	h := newCredsHarness(t, generatedServer("gitops-client"))
	text, isErr := h.call(t, "mcpserver_update", map[string]interface{}{
		"name":              "test-server",
		"clientCredentials": map[string]interface{}{"clientId": testClientID, "clientSecret": testClientSecret},
	})
	require.False(t, isErr, text)
	ref := h.server(t).Spec.Auth.AuthorizationServer.ClientCredentialsSecretRef
	assert.True(t, isGeneratedClientSecretName("test-server", ref.Name), ref.Name)
	assert.Equal(t, []string{"create Secret", "update MCPServer"}, h.writes)
}

func TestClientCredentials_ReplaceSecretForbidden(t *testing.T) {
	h := newCredsHarness(t, generatedServer("test-server-oauth-client-aaaaa"))
	h.secretErr = forbiddenSecretCreate()

	text, isErr := h.call(t, "mcpserver_update", map[string]interface{}{
		"name":              "test-server",
		"clientCredentials": map[string]interface{}{"clientId": testClientID, "clientSecret": testClientSecret},
	})
	require.True(t, isErr)
	assert.Contains(t, text, "so it was not updated")
	assert.Contains(t, text, "may not create Secret")
	assert.Equal(t, []string{"create Secret"}, h.writes)
}

func TestClientCredentials_ReplaceUpdateFails(t *testing.T) {
	h := newCredsHarness(t, generatedServer("test-server-oauth-client-aaaaa"))
	h.updateErr = errors.New("conflict")

	text, isErr := h.call(t, "mcpserver_update", map[string]interface{}{
		"name":              "test-server",
		"clientCredentials": map[string]interface{}{"clientId": testClientID, "clientSecret": testClientSecret},
	})
	require.True(t, isErr)
	secrets := h.secrets(t)
	require.Len(t, secrets, 1)
	assert.Contains(t, text, "conflict")
	assert.Contains(t, text, fmt.Sprintf("stored in Secret '%s'", secrets[0].Name))
	assert.Contains(t, text, "deleted with the server")
}

func TestClientCredentials_UpdateAuthKeepsGeneratedRef(t *testing.T) {
	const generated = "test-server-oauth-client-aaaaa"
	auth := func(as map[string]interface{}) map[string]interface{} {
		a := map[string]interface{}{"type": "oauth"}
		if as != nil {
			a["authorizationServer"] = as
		}
		return a
	}
	tests := []struct {
		name     string
		previous string
		auth     map[string]interface{}
		want     *musterv1alpha1.ClientCredentialsSecretRef
	}{
		{"auth re-sent without ref keeps it", generated,
			auth(map[string]interface{}{"issuer": "https://as.example.com", "scopes": "crm"}),
			&musterv1alpha1.ClientCredentialsSecretRef{Name: generated}},
		{"explicit ref wins", generated,
			auth(map[string]interface{}{"issuer": "https://as.example.com", "clientCredentialsSecretRef": map[string]interface{}{"name": "gitops"}}),
			&musterv1alpha1.ClientCredentialsSecretRef{Name: "gitops"}},
		{"auth without authorizationServer drops it", generated, auth(nil), nil},
		{"a ref not generated is not kept", "gitops",
			auth(map[string]interface{}{"issuer": "https://as.example.com"}), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newCredsHarness(t, generatedServer(tt.previous))
			text, isErr := h.call(t, "mcpserver_update", map[string]interface{}{"name": "test-server", "auth": tt.auth})
			require.False(t, isErr, text)
			as := h.server(t).Spec.Auth.AuthorizationServer
			if tt.want == nil {
				if as != nil {
					assert.Nil(t, as.ClientCredentialsSecretRef)
				}
				return
			}
			require.NotNil(t, as)
			assert.Equal(t, tt.want, as.ClientCredentialsSecretRef)
			assert.Equal(t, []string{"update MCPServer"}, h.writes)
		})
	}
}

// Moving a stored client to another authorization server needs the client
// again: muster sends it only where it was entered for.
func TestClientCredentials_MovedClient(t *testing.T) {
	const generated = "test-server-oauth-client-aaaaa"
	for name, as := range map[string]map[string]interface{}{
		"issuer":         {"issuer": "https://evil.example.com"},
		"token endpoint": {"issuer": "https://as.example.com", "authorizationEndpoint": "https://as.example.com/a", "tokenEndpoint": "https://evil.example.com/t"},
		"revocation":     {"issuer": "https://as.example.com", "revocationEndpoint": "https://evil.example.com/r"},
		"explicit ref":   {"issuer": "https://evil.example.com", "clientCredentialsSecretRef": map[string]interface{}{"name": generated}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newCredsHarness(t, generatedServer(generated))
			text, isErr := h.call(t, "mcpserver_update", map[string]interface{}{
				"name": "test-server", "auth": map[string]interface{}{"type": "oauth", "authorizationServer": as},
			})
			require.True(t, isErr, text)
			assert.Contains(t, text, "re-enter it with clientCredentials")
			assert.Empty(t, h.writes)
		})
	}
	t.Run("with the client again", func(t *testing.T) {
		h := newCredsHarness(t, generatedServer(generated))
		text, isErr := h.call(t, "mcpserver_update", map[string]interface{}{
			"name": "test-server", "auth": map[string]interface{}{"type": "oauth", "authorizationServer": map[string]interface{}{"issuer": "https://new.example.com"}},
			"clientCredentials": map[string]interface{}{"clientId": testClientID, "clientSecret": testClientSecret},
		})
		require.False(t, isErr, text)
		secrets := h.secrets(t)
		require.Len(t, secrets, 1)
		assert.Equal(t, api.AuthorizationServerDigest(&api.MCPServerAuthAuthorizationServer{Issuer: "https://new.example.com"}),
			secrets[0].Annotations[api.AuthorizationServerAnnotation])
		assert.Equal(t, secrets[0].Name, h.server(t).Spec.Auth.AuthorizationServer.ClientCredentialsSecretRef.Name)
	})
	t.Run("another Secret may move", func(t *testing.T) {
		h := newCredsHarness(t, generatedServer("gitops-client"))
		text, isErr := h.call(t, "mcpserver_update", map[string]interface{}{
			"name": "test-server", "auth": map[string]interface{}{"type": "oauth", "authorizationServer": map[string]interface{}{
				"issuer": "https://new.example.com", "clientCredentialsSecretRef": map[string]interface{}{"name": "gitops-client"},
			}},
		})
		require.False(t, isErr, text)
	})
}
