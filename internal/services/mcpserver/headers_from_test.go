package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// headerSecret answers LoadSecretKey with one value and records the
// namespace of each read.
type headerSecret struct {
	mu         sync.Mutex
	value      string
	namespaces []string
}

func (s *headerSecret) LoadClientCredentials(context.Context, *api.ClientCredentialsSecretRef, string) (*api.ClientCredentials, error) {
	return nil, errors.New("not used")
}

func (s *headerSecret) LoadSecretKey(_ context.Context, ref *api.ClientCredentialsSecretRef, _ string, defaultNamespace string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	namespace := ref.Namespace
	if namespace == "" {
		namespace = defaultNamespace
	}
	s.namespaces = append(s.namespaces, namespace)
	return []byte(s.value), nil
}

// TestStartSendsHeadersFromAndKeepsTheValueOutOfServiceData: the value of a
// spec.headersFrom entry is read from its Secret in the server's namespace
// when muster connects and sent on the wire; the service's definition and
// its service data, which core_service_* return, carry the reference only.
func TestStartSendsHeadersFromAndKeepsTheValueOutOfServiceData(t *testing.T) {
	const secretValue = "Bearer never-in-service-data"
	secrets := &headerSecret{value: secretValue}
	prev := api.GetSecretCredentialsHandler()
	api.RegisterSecretCredentialsHandler(secrets)
	t.Cleanup(func() { api.RegisterSecretCredentialsHandler(prev) })

	var mu sync.Mutex
	var received []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received = append(received, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(backend.Close)

	def := &api.MCPServer{
		Name:      "github",
		Namespace: "org-acme",
		Type:      api.MCPServerTypeStreamableHTTP,
		URL:       backend.URL + "/mcp",
		Headers:   map[string]string{"X-MCP-Toolsets": "repos"},
		HeadersFrom: []api.MCPServerHeaderFrom{{
			Name:      "Authorization",
			ValueFrom: api.MCPServerValueSource{Type: api.ValueSourceTypeSecret, Name: "github-token", Key: "token"},
		}},
	}
	svc, err := NewService(def)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	_ = svc.Start(t.Context())

	mu.Lock()
	require.NotEmpty(t, received, "the connect reached the backend")
	assert.Equal(t, secretValue, received[0], "the Secret's value is the Authorization header on the wire")
	mu.Unlock()
	assert.Equal(t, []string{"org-acme"}, secrets.namespaces, "read in the server's own namespace")

	data, err := json.Marshal(svc.GetServiceData())
	require.NoError(t, err)
	assert.NotContains(t, string(data), "never-in-service-data")
	assert.Contains(t, string(data), "github-token", "the reference stays visible")

	definition, err := json.Marshal(svc.GetConfiguration())
	require.NoError(t, err)
	assert.NotContains(t, string(definition), "never-in-service-data")
}
