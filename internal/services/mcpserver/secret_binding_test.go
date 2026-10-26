package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/services"
)

// errNotBound is how the Secret reader refuses a Secret not bound to the
// MCPServer that names it (secretBinding.mode enforce).
var errNotBound = fmt.Errorf("MCPServer agent-platform/x may not read Secret agent-platform/x: %w", api.ErrSecretNotBound)

// TestTokenExchangeSecretIsReadForTheMCPServer: the client Secret of
// spec.auth.tokenExchange is read for the MCPServer, and a refused Secret
// fails the server with the refusal and never reaches the token endpoint: no
// probe posts credentials.
func TestTokenExchangeSecretIsReadForTheMCPServer(t *testing.T) {
	prober := registerProber(t)
	secrets := &stubSecretHandler{}
	prevSecrets := api.GetSecretCredentialsHandler()
	api.RegisterSecretCredentialsHandler(secrets)
	t.Cleanup(func() { api.RegisterSecretCredentialsHandler(prevSecrets) })

	backend := startHTTPStatusServer(t, http.StatusUnauthorized)
	def := tokenExchangeDefinition(backend.URL, true)
	def.UID = "uid-remote-exchange"
	svc, err := NewService(def)
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	require.True(t, api.IsAuthRequiredError(svc.Start(t.Context())))
	secrets.mu.Lock()
	assert.Equal(t, []api.SecretOwner{{Name: "remote-exchange", Namespace: "agent-platform", UID: "uid-remote-exchange"}}, secrets.owners)
	assert.Equal(t, []string{api.SecretFieldTokenExchange}, secrets.fields)
	secrets.mu.Unlock()

	// A probe is due, and the Secret is refused: nothing is posted.
	svc.RecordTokenExchangeOutcome(errEndpointTimeout)
	secrets.fail(errNotBound)
	err = svc.Start(t.Context())
	require.ErrorIs(t, err, api.ErrSecretNotBound)
	assert.Equal(t, services.StateFailed, svc.GetState())
	assert.ErrorIs(t, svc.GetLastError(), api.ErrSecretNotBound, "the refusal is the server's last error")
	assert.Empty(t, prober.probes(), "a refused Secret's credentials never reach the token endpoint")
}

// TestHeadersFromSecretIsReadForTheMCPServer: spec.headersFrom is read for
// the MCPServer, and a refused Secret fails the connect before any request
// reaches the backend.
func TestHeadersFromSecretIsReadForTheMCPServer(t *testing.T) {
	secrets := &headerSecret{value: "Bearer s3cr3t", err: errNotBound}
	prev := api.GetSecretCredentialsHandler()
	api.RegisterSecretCredentialsHandler(secrets)
	t.Cleanup(func() { api.RegisterSecretCredentialsHandler(prev) })

	var mu sync.Mutex
	requests := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(backend.Close)

	svc, err := NewService(&api.MCPServer{
		Name:      "org-acme/github",
		Namespace: "org-acme",
		UID:       "uid-github",
		Type:      api.MCPServerTypeStreamableHTTP,
		URL:       backend.URL + "/mcp",
		HeadersFrom: []api.MCPServerHeaderFrom{{
			Name:      "Authorization",
			ValueFrom: api.MCPServerValueSource{Type: api.ValueSourceTypeSecret, Name: "github-token", Key: "token"},
		}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	err = svc.Start(t.Context())
	require.ErrorIs(t, err, api.ErrSecretNotBound)

	secrets.mu.Lock()
	assert.Equal(t, []api.SecretOwner{{Name: "github", Namespace: "org-acme", UID: "uid-github"}}, secrets.owners,
		"the owner is the MCPServer object, not its key")
	secrets.mu.Unlock()
	mu.Lock()
	assert.Zero(t, requests, "nothing is sent to the backend")
	mu.Unlock()
}

// TestRefusedPinSecretIsTheServersStatus: a refused client Secret of
// spec.auth.authorizationServer makes the server Failed with the refusal,
// retried with backoff, instead of Auth Required with nothing in the status;
// once the Secret is bound the server reads Auth Required.
func TestRefusedPinSecretIsTheServersStatus(t *testing.T) {
	secrets := &stubSecretHandler{}
	secrets.fail(errNotBound)
	prev := api.GetSecretCredentialsHandler()
	api.RegisterSecretCredentialsHandler(secrets)
	t.Cleanup(func() { api.RegisterSecretCredentialsHandler(prev) })

	backend := startHTTPStatusServer(t, http.StatusUnauthorized)
	svc, err := NewService(&api.MCPServer{
		Name:      "github",
		Namespace: "agent-platform",
		UID:       "uid-github",
		Type:      api.MCPServerTypeStreamableHTTP,
		URL:       backend.URL + "/mcp",
		Auth: &api.MCPServerAuth{Type: "oauth", AuthorizationServer: &api.MCPServerAuthAuthorizationServer{
			Issuer:                     "https://github.com/login/oauth",
			ClientCredentialsSecretRef: &api.ClientCredentialsSecretRef{Name: "github-oauth-client"},
		}},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	err = svc.Start(t.Context())
	require.ErrorIs(t, err, api.ErrSecretNotBound)
	assert.False(t, api.IsAuthRequiredError(err), "a refusal is a failed start, not a server waiting for a sign-in")
	assert.Equal(t, services.StateFailed, svc.GetState())
	assert.ErrorIs(t, svc.GetLastError(), api.ErrSecretNotBound)
	assert.NotNil(t, svc.GetNextRetryAfter(), "binding the Secret needs no restart")
	secrets.mu.Lock()
	assert.Equal(t, []string{api.SecretFieldAuthorizationServer}, secrets.fields)
	assert.Equal(t, api.SecretOwner{Name: "github", Namespace: "agent-platform", UID: "uid-github"}, secrets.owners[0])
	secrets.mu.Unlock()

	secrets.fail(nil)
	require.True(t, api.IsAuthRequiredError(svc.Start(t.Context())))
	assert.Equal(t, services.StateAuthRequired, svc.GetState())
}
