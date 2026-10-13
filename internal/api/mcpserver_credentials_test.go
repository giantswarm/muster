package api

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestValidateNoInlineCredential is the check the reconciler and the core
// tools run where muster loads a server; the CRD's CEL rule is the same check
// at admission (TestCredentialCELEnvtest).
func TestValidateNoInlineCredential(t *testing.T) {
	const remote = string(MCPServerTypeStreamableHTTP)
	tests := []struct {
		name           string
		serverType     string
		url            string
		headers        map[string]string
		env            map[string]string
		kubernetesMode bool
		refused        string
	}{
		{name: "plain server", serverType: remote, url: "https://mcp.example.com/mcp", headers: map[string]string{"X-MCP-Toolsets": "repos"}, kubernetesMode: true},
		{name: "Authorization", serverType: remote, url: "https://mcp.example.com/mcp", headers: map[string]string{"Authorization": "Bearer t"}, kubernetesMode: true, refused: "Authorization"},
		{name: "lower-case authorization", serverType: remote, url: "https://mcp.example.com/mcp", headers: map[string]string{"authorization": "Bearer t"}, kubernetesMode: true, refused: "Authorization"},
		{name: "Proxy-Authorization", serverType: remote, url: "https://mcp.example.com/mcp", headers: map[string]string{"PROXY-AUTHORIZATION": "Basic x"}, kubernetesMode: true, refused: "Proxy-Authorization"},
		{name: "Cookie", serverType: remote, url: "https://mcp.example.com/mcp", headers: map[string]string{"Cookie": "s=1"}, kubernetesMode: true, refused: "Cookie"},
		{name: "X-API-Key", serverType: remote, url: "https://mcp.example.com/mcp", headers: map[string]string{"x-api-key": "k"}, kubernetesMode: true, refused: "X-API-Key"},
		{name: "empty Authorization value", serverType: remote, url: "https://mcp.example.com/mcp", headers: map[string]string{"Authorization": ""}, kubernetesMode: true},
		{name: "user and password in url", serverType: remote, url: "https://user:secret@mcp.example.com/mcp", kubernetesMode: true, refused: "user name or password"},
		{name: "user in url", serverType: remote, url: "https://user@mcp.example.com/mcp", kubernetesMode: true, refused: "user name or password"},
		{name: "@ in the path", serverType: remote, url: "https://mcp.example.com/users/@me", kubernetesMode: true},
		{name: "env on a remote server", serverType: remote, url: "https://mcp.example.com/mcp", env: map[string]string{"TOKEN": "t"}, kubernetesMode: true, refused: "env is only allowed"},
		{name: "env on a remote server in filesystem mode", serverType: remote, url: "https://mcp.example.com/mcp", env: map[string]string{"TOKEN": "t"}, refused: "env is only allowed"},
		{name: "env on a stdio server", serverType: string(MCPServerTypeStdio), env: map[string]string{"TOKEN": "t"}},
		{name: "Authorization in filesystem mode", serverType: remote, url: "https://user:p@mcp.example.com/mcp", headers: map[string]string{"Authorization": "Bearer t"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNoInlineCredential(tt.serverType, tt.url, tt.headers, tt.env, tt.kubernetesMode)
			if tt.refused == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, ErrCredentialInDefinition)
			require.ErrorContains(t, err, tt.refused)
		})
	}
}

func TestValidateHeadersFrom(t *testing.T) {
	valid := MCPServerHeaderFrom{Name: "Authorization", ValueFrom: MCPServerValueSource{Type: ValueSourceTypeSecret, Name: "token", Key: "value"}}
	require.NoError(t, ValidateHeadersFrom(string(MCPServerTypeStreamableHTTP), []MCPServerHeaderFrom{valid}))
	require.NoError(t, ValidateHeadersFrom(string(MCPServerTypeStdio), nil))

	require.ErrorContains(t, ValidateHeadersFrom(string(MCPServerTypeStdio), []MCPServerHeaderFrom{valid}), "only allowed when type")

	noKey := valid
	noKey.ValueFrom.Key = ""
	require.ErrorContains(t, ValidateHeadersFrom(string(MCPServerTypeSSE), []MCPServerHeaderFrom{noKey}), "valueFrom.key")

	configMap := valid
	configMap.ValueFrom.Type = "ConfigMap"
	require.ErrorContains(t, ValidateHeadersFrom(string(MCPServerTypeSSE), []MCPServerHeaderFrom{configMap}), `must be "Secret"`)
}

// TestValidateSecretNamespaces: a server outside muster's namespace cannot
// make muster read a Secret outside its own namespace, through either
// clientCredentialsSecretRef; a server in muster's namespace keeps today's
// references.
func TestValidateSecretNamespaces(t *testing.T) {
	ref := func(namespace string) *ClientCredentialsSecretRef {
		return &ClientCredentialsSecretRef{Name: "client", Namespace: namespace}
	}
	exchange := func(namespace string) *MCPServerAuth {
		return &MCPServerAuth{TokenExchange: &TokenExchangeConfig{Enabled: true, ClientCredentialsSecretRef: ref(namespace)}}
	}
	pin := func(namespace string) *MCPServerAuth {
		return &MCPServerAuth{Type: MCPServerAuthTypeOAuth, AuthorizationServer: &MCPServerAuthAuthorizationServer{Issuer: "https://github.com/login/oauth", ClientCredentialsSecretRef: ref(namespace)}}
	}
	tests := []struct {
		name            string
		serverNamespace string
		auth            *MCPServerAuth
		refused         string
	}{
		{name: "no auth", serverNamespace: "org-acme"},
		{name: "server in muster's namespace reads another", serverNamespace: "muster", auth: exchange("platform")},
		{name: "filesystem mode", serverNamespace: "", auth: exchange("platform")},
		{name: "tenant exchange without namespace", serverNamespace: "org-acme", auth: exchange("")},
		{name: "tenant exchange in its own namespace", serverNamespace: "org-acme", auth: exchange("org-acme")},
		{name: "tenant exchange in muster's namespace", serverNamespace: "org-acme", auth: exchange("muster"), refused: "auth.tokenExchange.clientCredentialsSecretRef"},
		{name: "tenant pin in its own namespace", serverNamespace: "org-acme", auth: pin("org-acme")},
		{name: "tenant pin in muster's namespace", serverNamespace: "org-acme", auth: pin("muster"), refused: "auth.authorizationServer.clientCredentialsSecretRef"},
		{name: "tenant pin in another tenant's namespace", serverNamespace: "org-acme", auth: pin("org-other"), refused: `"org-other"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSecretNamespaces(tt.serverNamespace, "muster", tt.auth)
			if tt.refused == "" {
				require.NoError(t, err)
				return
			}
			require.True(t, errors.Is(err, ErrSecretOutsideNamespace), "got %v", err)
			require.ErrorContains(t, err, tt.refused)
		})
	}
}
