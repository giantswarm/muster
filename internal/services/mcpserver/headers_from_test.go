package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/mcpserver"
	"github.com/giantswarm/muster/v5/internal/services"
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

// missingHeaderSecret answers every LoadSecretKey as a Secret that does not
// exist yet.
type missingHeaderSecret struct{}

func (missingHeaderSecret) LoadClientCredentials(context.Context, *api.ClientCredentialsSecretRef, string) (*api.ClientCredentials, error) {
	return nil, errors.New("not used")
}

func (missingHeaderSecret) LoadSecretKey(_ context.Context, ref *api.ClientCredentialsSecretRef, _ string, defaultNamespace string) ([]byte, error) {
	return nil, fmt.Errorf("secret %s/%s not found", defaultNamespace, ref.Name)
}

// TestStartRetriesAMissingHeadersFromSecret: Flux applies an MCPServer and
// its Secret in any order, so a headersFrom Secret that does not exist yet
// puts the server on the reconnect schedule. It stays Failed, never reads
// unreachable, and the orchestrator retries it once nextRetryAfter is due. A
// Secret or header whose name contains "certificate" is retried too: the
// name is in the error text, and it is not a TLS misconfiguration.
func TestStartRetriesAMissingHeadersFromSecret(t *testing.T) {
	prev := api.GetSecretCredentialsHandler()
	api.RegisterSecretCredentialsHandler(missingHeaderSecret{})
	t.Cleanup(func() { api.RegisterSecretCredentialsHandler(prev) })

	cases := []struct {
		name, header, secret string
	}{
		{name: "plain names", header: "Authorization", secret: "github-token"},
		{name: "a Secret named like a certificate", header: "Authorization", secret: "tls-certificate-token"},
		{name: "a header named like a certificate", header: "X-Client-Certificate", secret: "github-token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, err := NewService(&api.MCPServer{
				Name:      "github",
				Namespace: "org-acme",
				Type:      api.MCPServerTypeStreamableHTTP,
				URL:       "http://127.0.0.1:1/mcp",
				HeadersFrom: []api.MCPServerHeaderFrom{{
					Name:      tc.header,
					ValueFrom: api.MCPServerValueSource{Type: api.ValueSourceTypeSecret, Name: tc.secret, Key: "token"},
				}},
			})
			require.NoError(t, err)

			for attempt := 1; attempt <= UnreachableThreshold+1; attempt++ {
				err := svc.Start(t.Context())
				var headerSecretErr *mcpserver.HeaderSecretError
				require.ErrorAs(t, err, &headerSecretErr, "attempt %d", attempt)
			}

			assert.Equal(t, services.StateFailed, svc.GetState(), "a missing Secret is not the endpoint's fault")
			_, ok := svc.GetServiceData()[api.ServiceDataNextRetryAfter].(time.Time)
			assert.True(t, ok, "nextRetryAfter is set, so the orchestrator retries the server")
		})
	}
}

// TestTypedErrorsKeepTheirRetryAnswer pins the order of the classification:
// a typed error is judged by its type before the certificate and TLS patterns
// read its text. A missing token exchange Secret named like a certificate is
// retried, and so is an unanswered token endpoint whose cause is a
// certificate error; only an untyped certificate error fails at once.
func TestTypedErrorsKeepTheirRetryAnswer(t *testing.T) {
	svc, err := NewService(&api.MCPServer{Name: "typed", Type: api.MCPServerTypeStreamableHTTP, URL: "http://example.com/mcp"})
	require.NoError(t, err)
	certificateErr := errors.New("x509: certificate signed by unknown authority")

	missingSecret := api.NewTokenExchangeCredentialsError(&api.ClientCredentialsSecretRef{Name: "tls-certificate-token"}, "muster", errors.New(`secrets "tls-certificate-token" not found`))
	assert.Contains(t, missingSecret.Error(), "certificate", "the case is only meaningful if the text reads like a certificate error")
	assert.True(t, svc.isTransientConnectivityError(fmt.Errorf("failed to start MCP server: %w", missingSecret)),
		"a missing token exchange Secret is retried whatever its name")

	unanswered := &api.TokenEndpointUnavailableError{Endpoint: "https://dex.example.com/token", Err: certificateErr}
	assert.True(t, svc.isTransientConnectivityError(unanswered), "an unanswered token endpoint is retried whatever its cause")

	assert.False(t, svc.isTransientConnectivityError(certificateErr), "an untyped certificate error fails at once")
}
