package mcpserver

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// secretKeyReader answers LoadMCPServerSecretKey from a map keyed by
// "namespace/name/key" and records the namespace each read went to.
type secretKeyReader struct {
	values     map[string]string
	namespaces []string
	owners     []api.SecretOwner
	fields     []string
}

func (s *secretKeyReader) LoadClientCredentialsForConfig(context.Context, *api.ClientCredentialsSecretRef, string) (*api.ClientCredentials, error) {
	return nil, fmt.Errorf("not used")
}

func (s *secretKeyReader) LoadMCPServerClientCredentials(context.Context, api.SecretOwner, string, *api.ClientCredentialsSecretRef) (*api.ClientCredentials, error) {
	return nil, fmt.Errorf("not used")
}

func (s *secretKeyReader) LoadMCPServerSecretKey(_ context.Context, owner api.SecretOwner, field string, ref *api.ClientCredentialsSecretRef, key string) ([]byte, error) {
	namespace := ref.Namespace
	if namespace == "" {
		namespace = owner.Namespace
	}
	s.namespaces = append(s.namespaces, namespace)
	s.owners = append(s.owners, owner)
	s.fields = append(s.fields, field)
	value, ok := s.values[namespace+"/"+ref.Name+"/"+key]
	if !ok {
		return nil, fmt.Errorf("secret %s/%s missing required key %q", namespace, ref.Name, key)
	}
	return []byte(value), nil
}

func registerSecretKeyReader(t *testing.T, reader api.SecretCredentialsHandler) {
	t.Helper()
	prev := api.GetSecretCredentialsHandler()
	api.RegisterSecretCredentialsHandler(reader)
	t.Cleanup(func() { api.RegisterSecretCredentialsHandler(prev) })
}

// githubOwner is the MCPServer org-acme/github the headers are resolved for.
var githubOwner = api.SecretOwner{Name: "github", Namespace: "org-acme", UID: "uid-github"}

func bearerFrom(secret string) api.MCPServerHeaderFrom {
	return api.MCPServerHeaderFrom{
		Name:      "Authorization",
		ValueFrom: api.MCPServerValueSource{Type: api.ValueSourceTypeSecret, Name: secret, Key: "token"},
	}
}

// TestResolveHeadersReadsTheServersOwnNamespace: each value is read from the
// Secret in the server's namespace and wins over a spec.headers entry of the
// same name in any case; the input map is not changed.
func TestResolveHeadersReadsTheServersOwnNamespace(t *testing.T) {
	reader := &secretKeyReader{values: map[string]string{"org-acme/github-token/token": "Bearer s3cr3t"}}
	registerSecretKeyReader(t, reader)

	static := map[string]string{"authorization": "", "X-MCP-Toolsets": "repos"}
	got, err := ResolveHeaders(t.Context(), githubOwner, static, []api.MCPServerHeaderFrom{bearerFrom("github-token")})
	require.NoError(t, err)

	assert.Equal(t, map[string]string{"Authorization": "Bearer s3cr3t", "X-MCP-Toolsets": "repos"}, got)
	assert.Equal(t, []string{"org-acme"}, reader.namespaces)
	assert.Equal(t, []api.SecretOwner{githubOwner}, reader.owners, "the Secret is read for the MCPServer, so its binding is checked")
	assert.Equal(t, []string{api.SecretFieldHeadersFrom}, reader.fields)
	assert.Equal(t, map[string]string{"authorization": "", "X-MCP-Toolsets": "repos"}, static, "the definition's map is not changed")
}

func TestResolveHeadersFailsOnAMissingKey(t *testing.T) {
	registerSecretKeyReader(t, &secretKeyReader{})

	_, err := ResolveHeaders(t.Context(), githubOwner, nil, []api.MCPServerHeaderFrom{bearerFrom("github-token")})
	require.ErrorContains(t, err, "Authorization")
	require.ErrorContains(t, err, "org-acme/github-token")
	var headerSecretErr *HeaderSecretError
	require.ErrorAs(t, err, &headerSecretErr, "typed, so the service retries it")
}

// TestResolveHeadersSendsNothingOfARefusedSecret: one refused Secret fails
// the whole set, so no header is sent, not even those that were readable.
func TestResolveHeadersSendsNothingOfARefusedSecret(t *testing.T) {
	registerSecretKeyReader(t, &refusingReader{})

	got, err := ResolveHeaders(t.Context(), githubOwner, map[string]string{"X-MCP-Toolsets": "repos"}, []api.MCPServerHeaderFrom{bearerFrom("github-token")})
	require.ErrorIs(t, err, api.ErrSecretProtected)
	assert.Nil(t, got)
}

// refusingReader refuses every Secret as one muster keeps for itself.
type refusingReader struct{ secretKeyReader }

func (refusingReader) LoadMCPServerSecretKey(context.Context, api.SecretOwner, string, *api.ClientCredentialsSecretRef, string) ([]byte, error) {
	return nil, fmt.Errorf("refused: %w", api.ErrSecretProtected)
}

// TestResolveHeadersTrimsOneTrailingNewline: a Secret written with echo or
// from an editor's file ends in a newline, which is not part of the value.
func TestResolveHeadersTrimsOneTrailingNewline(t *testing.T) {
	for stored, want := range map[string]string{
		"Bearer s3cr3t\n":   "Bearer s3cr3t",
		"Bearer s3cr3t\r\n": "Bearer s3cr3t",
		"Bearer s3cr3t\n\n": "Bearer s3cr3t\n",
		"Bearer s3cr3t":     "Bearer s3cr3t",
		"Bearer s3cr3t\r":   "Bearer s3cr3t\r",
	} {
		registerSecretKeyReader(t, &secretKeyReader{values: map[string]string{"org-acme/github-token/token": stored}})
		got, err := ResolveHeaders(t.Context(), githubOwner, nil, []api.MCPServerHeaderFrom{bearerFrom("github-token")})
		require.NoError(t, err)
		assert.Equal(t, want, got["Authorization"], "stored %q", stored)
	}
}

// TestResolveHeadersWithoutHeadersFromNeedsNoSecretReader: filesystem mode
// registers no Secret reader; a server without headersFrom connects as before.
func TestResolveHeadersWithoutHeadersFromNeedsNoSecretReader(t *testing.T) {
	registerSecretKeyReader(t, nil)

	static := map[string]string{"X-MCP-Toolsets": "repos"}
	got, err := ResolveHeaders(t.Context(), api.SecretOwner{}, static, nil)
	require.NoError(t, err)
	assert.Equal(t, static, got)

	_, err = ResolveHeaders(t.Context(), api.SecretOwner{}, static, []api.MCPServerHeaderFrom{bearerFrom("github-token")})
	require.ErrorContains(t, err, "Kubernetes mode")
}

// TestHeadersFromSurvivesTheConversionRoundTrip: a dropped reference fails
// nowhere until the server answers 401, so the plumbing through create, read
// and update is asserted directly.
func TestHeadersFromSurvivesTheConversionRoundTrip(t *testing.T) {
	adapter := &Adapter{namespace: "muster"}
	headersFrom := []api.MCPServerHeaderFrom{bearerFrom("github-token")}

	crd := adapter.convertRequestToCRD(&api.MCPServerCreateRequest{
		Name:        "github",
		Type:        string(api.MCPServerTypeStreamableHTTP),
		URL:         "https://api.githubcopilot.com/mcp/",
		HeadersFrom: headersFrom,
	})
	require.Equal(t, []musterv1alpha1.MCPServerHeaderFrom{{
		Name:      "Authorization",
		ValueFrom: musterv1alpha1.MCPServerValueSource{Type: "Secret", Name: "github-token", Key: "token"},
	}}, crd.Spec.HeadersFrom)

	info := convertCRDToInfo(crd)
	assert.Equal(t, headersFrom, info.HeadersFrom)
	assert.Equal(t, headersFrom, info.ToMCPServer().HeadersFrom)
}

// TestValidateMCPServerRefusesACredentialInTheDefinition is the check
// core_mcpserver_create, update and validate run before the CR is written.
func TestValidateMCPServerRefusesACredentialInTheDefinition(t *testing.T) {
	remote := func(mutate func(*musterv1alpha1.MCPServer)) *musterv1alpha1.MCPServer {
		server := &musterv1alpha1.MCPServer{}
		server.Name = "github"
		server.Namespace = "muster"
		server.Spec.Type = string(api.MCPServerTypeStreamableHTTP)
		server.Spec.URL = "https://api.githubcopilot.com/mcp/"
		mutate(server)
		return server
	}
	kubernetes := &Adapter{namespace: "muster", client: &stubMusterClient{}}
	filesystem := &Adapter{namespace: "muster", client: &stubMusterClient{filesystem: true}}

	tests := []struct {
		name    string
		adapter *Adapter
		server  *musterv1alpha1.MCPServer
		refused string
	}{
		{name: "headersFrom", adapter: kubernetes, server: remote(func(s *musterv1alpha1.MCPServer) {
			s.Spec.Headers = map[string]string{"X-MCP-Toolsets": "repos"}
			s.Spec.HeadersFrom = convertAPIHeadersFromToCRD([]api.MCPServerHeaderFrom{bearerFrom("github-token")})
		})},
		{name: "Authorization header", adapter: kubernetes, server: remote(func(s *musterv1alpha1.MCPServer) {
			s.Spec.Headers = map[string]string{"authorization": "Bearer t"}
		}), refused: "header Authorization holds a credential"},
		{name: "user info in url", adapter: kubernetes, server: remote(func(s *musterv1alpha1.MCPServer) {
			s.Spec.URL = "https://user:pass@api.githubcopilot.com/mcp/"
		}), refused: "user name or password"},
		{name: "env on a remote server", adapter: filesystem, server: remote(func(s *musterv1alpha1.MCPServer) {
			s.Spec.Env = map[string]string{"GITHUB_TOKEN": "t"}
		}), refused: "env is only allowed"},
		{name: "headersFrom on stdio", adapter: filesystem, server: remote(func(s *musterv1alpha1.MCPServer) {
			s.Spec.Type = string(api.MCPServerTypeStdio)
			s.Spec.Command = "github-mcp"
			s.Spec.HeadersFrom = convertAPIHeadersFromToCRD([]api.MCPServerHeaderFrom{bearerFrom("github-token")})
		}), refused: "headersFrom is only allowed"},
		{name: "tenant pin reads muster's namespace", adapter: kubernetes, server: remote(func(s *musterv1alpha1.MCPServer) {
			s.Namespace = "org-acme"
			s.Spec.Auth = &musterv1alpha1.MCPServerAuth{
				Type: api.MCPServerAuthTypeOAuth,
				AuthorizationServer: &musterv1alpha1.MCPServerAuthAuthorizationServer{
					Issuer:                     "https://github.com/login/oauth",
					ClientCredentialsSecretRef: &musterv1alpha1.ClientCredentialsSecretRef{Name: "github-app", Namespace: "muster"},
				},
			}
		}), refused: "auth.authorizationServer.clientCredentialsSecretRef"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.adapter.validateMCPServer(tt.server)
			if tt.refused == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.refused)
		})
	}
}
