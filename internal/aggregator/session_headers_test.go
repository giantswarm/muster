package aggregator

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// sessionHeaderSecrets answers LoadMCPServerSecretKey by key and records the namespace
// of each read.
type sessionHeaderSecrets struct {
	values     map[string]string
	namespaces []string
	owners     []api.SecretOwner
}

func (s *sessionHeaderSecrets) LoadClientCredentialsForConfig(context.Context, *api.ClientCredentialsSecretRef, string) (*api.ClientCredentials, error) {
	return nil, errors.New("not used")
}

func (s *sessionHeaderSecrets) LoadMCPServerClientCredentials(context.Context, api.SecretOwner, string, *api.ClientCredentialsSecretRef) (*api.ClientCredentials, error) {
	return nil, errors.New("not used")
}

func (s *sessionHeaderSecrets) LoadMCPServerSecretKey(_ context.Context, owner api.SecretOwner, _ string, _ *api.ClientCredentialsSecretRef, key string) ([]byte, error) {
	s.namespaces = append(s.namespaces, owner.Namespace)
	s.owners = append(s.owners, owner)
	value, ok := s.values[key]
	if !ok {
		return nil, errors.New("secret not found")
	}
	return []byte(value), nil
}

// TestSessionHeadersReadHeadersFromAtTheConnect: a session-scoped client
// sends the values of spec.headersFrom, read from the server's namespace when
// it connects, next to spec.headers; an Authorization entry stays out, as the
// session's own credential is its Authorization header, and its Secret is not
// read, so a missing one does not fail the connect. The registry entry keeps
// the references only.
func TestSessionHeadersReadHeadersFromAtTheConnect(t *testing.T) {
	secrets := &sessionHeaderSecrets{values: map[string]string{"api-key": "k3y"}}
	prev := api.GetSecretCredentialsHandler()
	api.RegisterSecretCredentialsHandler(secrets)
	t.Cleanup(func() { api.RegisterSecretCredentialsHandler(prev) })

	info := &ServerInfo{
		Name:      "jira",
		Namespace: "org-acme",
		Headers:   map[string]string{"x-mcp-toolsets": "issues"},
		HeadersFrom: []api.MCPServerHeaderFrom{
			{Name: "X-API-Key", ValueFrom: api.MCPServerValueSource{Type: api.ValueSourceTypeSecret, Name: "jira", Key: "api-key"}},
			{Name: "authorization", ValueFrom: api.MCPServerValueSource{Type: api.ValueSourceTypeSecret, Name: "missing", Key: "token"}},
		},
	}

	headers, err := sessionHeaders(t.Context(), info)
	require.NoError(t, err)

	assert.Equal(t, map[string]string{"X-Mcp-Toolsets": "issues", "X-Api-Key": "k3y"}, headers)
	assert.Equal(t, []string{"org-acme"}, secrets.namespaces, "only the X-API-Key Secret is read")
	assert.Equal(t, map[string]string{"x-mcp-toolsets": "issues"}, info.Headers, "the registry entry is not changed")
}
