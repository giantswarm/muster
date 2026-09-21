package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/events"
	"github.com/giantswarm/muster/v5/internal/mcpserver"
	"github.com/giantswarm/muster/v5/internal/services"
)

// stubSecretHandler answers LoadClientCredentials with a fixed outcome, the
// way the Kubernetes-backed handler answers for a Secret that does or does
// not exist.
type stubSecretHandler struct {
	mu    sync.Mutex
	err   error
	loads int
}

func (s *stubSecretHandler) LoadClientCredentials(_ context.Context, _ *api.ClientCredentialsSecretRef, _ string) (*api.ClientCredentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loads++
	if s.err != nil {
		return nil, s.err
	}
	return &api.ClientCredentials{ClientID: "muster-token-exchange", ClientSecret: "not-logged"}, nil
}

func (s *stubSecretHandler) LoadSecretKey(context.Context, *api.ClientCredentialsSecretRef, string, string) ([]byte, error) {
	return nil, errors.New("not used")
}

func (s *stubSecretHandler) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *stubSecretHandler) loadCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loads
}

// tokenExchangeDefinition is a remote server reached with the caller's
// exchanged token, its client credentials in a Secret when withSecret is set.
func tokenExchangeDefinition(url string, withSecret bool) *api.MCPServer {
	def := &api.MCPServer{
		Name:      "remote-exchange",
		Namespace: "agent-platform",
		Type:      api.MCPServerTypeStreamableHTTP,
		URL:       url + "/mcp",
		Auth: &api.MCPServerAuth{
			Type: "oauth",
			TokenExchange: &api.TokenExchangeConfig{
				Enabled:          true,
				DexTokenEndpoint: "https://dex.remote.example.test/token",
				ConnectorID:      "remote-oidc",
			},
		},
	}
	if withSecret {
		def.Auth.TokenExchange.ClientCredentialsSecretRef = &api.ClientCredentialsSecretRef{Name: "remote-token-exchange-credentials"}
	}
	return def
}

func recordEvents(t *testing.T) *recordingEventManager {
	t.Helper()
	rec := &recordingEventManager{errors: map[string][]string{}}
	api.RegisterEventManager(rec)
	t.Cleanup(func() { api.RegisterEventManager(nil) })
	return rec
}

// TestStartTokenExchangeServerWithoutCredentialsSecretIsFailedAndRetried: a
// tokenExchange server whose credentials Secret does not exist fails every
// caller the same way, so its start ends in Failed with the Secret named,
// on the reconnect schedule -- GitOps delivers the Secret later -- and
// never in a state that reads healthy. Once the Secret exists the next
// attempt settles the server in Awaiting Session on its own.
func TestStartTokenExchangeServerWithoutCredentialsSecretIsFailedAndRetried(t *testing.T) {
	withBackoff(t, time.Second, time.Second)
	rec := recordEvents(t)
	secrets := &stubSecretHandler{}
	secrets.fail(errors.New(`secrets "remote-token-exchange-credentials" not found`))
	prev := api.GetSecretCredentialsHandler()
	api.RegisterSecretCredentialsHandler(secrets)
	t.Cleanup(func() { api.RegisterSecretCredentialsHandler(prev) })

	backend := startHTTPStatusServer(t, http.StatusUnauthorized)
	var hookRuns atomic.Int32
	svc, err := NewService(tokenExchangeDefinition(backend.URL, true), WithAuthRequiredHook(func(*api.MCPServer, *mcpserver.AuthRequiredError) {
		hookRuns.Add(1)
	}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	for attempt := 1; attempt <= 3; attempt++ {
		err = svc.Start(t.Context())
		require.Error(t, err)
		assert.False(t, api.IsAuthRequiredError(err), "a missing Secret is a failed start, not a server awaiting a caller")
		assert.Equal(t, services.StateFailed, svc.GetState(), "attempt %d: Failed, never unreachable -- the endpoint answered", attempt)
		assert.Equal(t, attempt, svc.GetConsecutiveFailures())
		require.NotNil(t, svc.GetNextRetryAfter(), "the Secret may appear later: retried with backoff")
	}
	assert.Equal(t, int32(0), hookRuns.Load(), "nothing is registered pending auth while no caller could connect")
	require.ErrorContains(t, svc.GetLastError(), "agent-platform/remote-token-exchange-credentials")

	data := svc.GetServiceData()
	assert.Equal(t, string(api.TokenExchangeFailureCredentials), data[api.ServiceDataFailureReason])
	assert.NotContains(t, data, api.ServiceDataLastTokenExchangeSucceededAt)

	failed := rec.texts(string(events.ReasonMCPServerFailed))
	require.NotEmpty(t, failed)
	assert.Contains(t, failed[0], "token exchange fails for every caller until the credentials Secret exists (credentials Secret unavailable, next retry in 1s at ")
	assert.Contains(t, failed[0], `secrets "remote-token-exchange-credentials" not found`)
	assert.Empty(t, rec.texts(string(events.ReasonMCPServerAwaitingSession)))

	// The Secret arrives; the orchestrator's retry is a plain Start.
	secrets.fail(nil)
	err = svc.Start(t.Context())
	require.True(t, api.IsAuthRequiredError(err), "%v", err)
	assert.Equal(t, services.StateAwaitingSession, svc.GetState())
	assert.Equal(t, 0, svc.GetConsecutiveFailures())
	assert.Nil(t, svc.GetNextRetryAfter())
	assert.Equal(t, int32(1), hookRuns.Load(), "registered pending auth once the server is usable")
	assert.NotContains(t, svc.GetServiceData(), api.ServiceDataFailureReason)
	assert.Len(t, rec.texts(string(events.ReasonMCPServerAwaitingSession)), 1)
	assert.GreaterOrEqual(t, secrets.loadCount(), 4, "every attempt verifies the Secret")
}

// TestStartTokenExchangeServerWithoutSecretHandler: with no handler to read
// Secrets (filesystem mode) a credentials reference cannot be honoured at
// exchange time either, so the start says so instead of reading healthy.
func TestStartTokenExchangeServerWithoutSecretHandler(t *testing.T) {
	withBackoff(t, time.Second, time.Second)
	prev := api.GetSecretCredentialsHandler()
	api.RegisterSecretCredentialsHandler(nil)
	t.Cleanup(func() { api.RegisterSecretCredentialsHandler(prev) })

	backend := startHTTPStatusServer(t, http.StatusUnauthorized)
	svc, err := NewService(tokenExchangeDefinition(backend.URL, true))
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	require.Error(t, svc.Start(t.Context()))
	assert.Equal(t, services.StateFailed, svc.GetState())
	assert.ErrorContains(t, svc.GetLastError(), "secret credentials handler not registered")
}

// TestRecordTokenExchangeOutcomeFollowsTheServersFailures: the aggregator
// reports every caller's exchange. A failure that is the server's puts it in
// Failed with the class as its reason; one caller's own failure changes
// nothing; the next success returns it to Awaiting Session and is remembered
// for the Ready condition; a start is an explicit reset.
func TestRecordTokenExchangeOutcomeFollowsTheServersFailures(t *testing.T) {
	rec := recordEvents(t)
	backend := startHTTPStatusServer(t, http.StatusUnauthorized)
	svc, err := NewService(tokenExchangeDefinition(backend.URL, false))
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	require.True(t, api.IsAuthRequiredError(svc.Start(t.Context())))
	require.Equal(t, services.StateAwaitingSession, svc.GetState())

	// One caller's expired token: the session's problem, not the server's.
	svc.RecordTokenExchangeOutcome(errors.New("token exchange failed: invalid_grant - subject token is expired"))
	assert.Equal(t, services.StateAwaitingSession, svc.GetState())
	assert.NotContains(t, svc.GetServiceData(), api.ServiceDataFailureReason)
	assert.Empty(t, rec.texts(string(events.ReasonMCPServerFailed)))

	// The client secret is wrong: every caller fails.
	svc.RecordTokenExchangeOutcome(fmt.Errorf("token exchange failed for remote-exchange: %w",
		errors.New("token exchange failed: invalid_client - Invalid client credentials.")))
	assert.Equal(t, services.StateFailed, svc.GetState())
	assert.Equal(t, services.HealthUnhealthy, svc.GetHealth())
	assert.ErrorContains(t, svc.GetLastError(), "invalid_client")
	assert.Equal(t, string(api.TokenExchangeFailureCredentials), svc.GetServiceData()[api.ServiceDataFailureReason])
	assert.Nil(t, svc.GetNextRetryAfter(), "no reconnect is scheduled: a probe would read Awaiting Session while the exchange is still broken")
	failed := rec.texts(string(events.ReasonMCPServerFailed))
	require.Len(t, failed, 1)
	assert.Contains(t, failed[0], "token exchange fails for every caller (TokenExchangeCredentials): ")
	assert.Contains(t, failed[0], "invalid_client")

	// The token endpoint stops answering: the class follows.
	svc.RecordTokenExchangeOutcome(fmt.Errorf("token exchange failed: %w",
		&url.Error{Op: "Post", URL: "https://dex.remote.example.test/token", Err: errors.New("dial tcp: connection refused")}))
	assert.Equal(t, services.StateFailed, svc.GetState())
	assert.Equal(t, string(api.TokenExchangeFailureEndpoint), svc.GetServiceData()[api.ServiceDataFailureReason])

	// A caller's exchange succeeds: the exchange works again.
	svc.RecordTokenExchangeOutcome(nil)
	assert.Equal(t, services.StateAwaitingSession, svc.GetState())
	data := svc.GetServiceData()
	assert.NotContains(t, data, api.ServiceDataFailureReason)
	last, ok := data[api.ServiceDataLastTokenExchangeSucceededAt].(time.Time)
	require.True(t, ok, "the time of the last successful exchange is published for the Ready condition")
	assert.WithinDuration(t, time.Now(), last, time.Minute)
	assert.Len(t, rec.texts(string(events.ReasonMCPServerAwaitingSession)), 2, "the start and the recovery each announce Awaiting Session")

	// A success while a session holds a live connection leaves Connected alone.
	svc.UpdateState(services.StateConnected, services.HealthHealthy, nil)
	svc.RecordTokenExchangeOutcome(nil)
	assert.Equal(t, services.StateConnected, svc.GetState())

	// Failed again, then an operator restarts the server: an explicit reset.
	svc.RecordTokenExchangeOutcome(errors.New("token exchange failed: invalid_request - Requested connector does not exist."))
	require.Equal(t, services.StateFailed, svc.GetState())
	assert.Equal(t, string(api.TokenExchangeFailureConnector), svc.GetServiceData()[api.ServiceDataFailureReason])
	require.True(t, api.IsAuthRequiredError(svc.Start(t.Context())))
	assert.Equal(t, services.StateAwaitingSession, svc.GetState())
	assert.NotContains(t, svc.GetServiceData(), api.ServiceDataFailureReason)
	assert.Contains(t, svc.GetServiceData(), api.ServiceDataLastTokenExchangeSucceededAt, "the last success survives a restart")
}

// TestReportedTokenExchangeReachesTheServiceThroughTheRegistry: the aggregator
// reaches services only through the registry adapter, which wraps them; a
// report that the wrapper does not forward is dropped silently (the first
// run of the oauth-sso-token-exchange-state scenario found exactly that).
func TestReportedTokenExchangeReachesTheServiceThroughTheRegistry(t *testing.T) {
	recordEvents(t)
	backend := startHTTPStatusServer(t, http.StatusUnauthorized)
	svc, err := NewService(tokenExchangeDefinition(backend.URL, false))
	require.NoError(t, err)
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	registry := services.NewRegistry()
	require.NoError(t, registry.Register(svc))
	services.NewRegistryAdapter(registry).Register()
	t.Cleanup(func() { api.RegisterServiceRegistry(nil) })

	require.True(t, api.IsAuthRequiredError(svc.Start(t.Context())))
	require.Equal(t, services.StateAwaitingSession, svc.GetState())

	api.ReportMCPServerTokenExchange(svc.GetName(), errors.New("token exchange failed: invalid_target - no trusted issuer configured for connector_id: no-such-connector"))
	assert.Equal(t, services.StateFailed, svc.GetState())
	assert.Equal(t, string(api.TokenExchangeFailureConnector), svc.GetServiceData()[api.ServiceDataFailureReason])

	api.ReportMCPServerTokenExchange(svc.GetName(), nil)
	assert.Equal(t, services.StateAwaitingSession, svc.GetState())
}

// TestStartSessionAuthServerAnswering401IsAwaitingSession: the 401 muster's
// token-less probe gets from a server that expects the caller's identity is
// the configured answer -- Awaiting Session, with the pending-auth hook run
// first, and never the Auth Required that names a sign-in through muster.
func TestStartSessionAuthServerAnswering401IsAwaitingSession(t *testing.T) {
	rec := recordEvents(t)
	backend := startHTTPStatusServer(t, http.StatusUnauthorized)

	for name, auth := range map[string]*api.MCPServerAuth{
		"forwardToken":  {Type: "oauth", ForwardToken: true},
		"tokenExchange": tokenExchangeDefinition(backend.URL, false).Auth,
	} {
		t.Run(name, func(t *testing.T) {
			var states []services.ServiceState
			var hookRan atomic.Bool
			def := &api.MCPServer{Name: "per-session-" + name, Type: api.MCPServerTypeStreamableHTTP, URL: backend.URL + "/mcp", Auth: auth}
			svc, err := NewService(def, WithAuthRequiredHook(func(*api.MCPServer, *mcpserver.AuthRequiredError) {
				hookRan.Store(true)
			}))
			require.NoError(t, err)
			t.Cleanup(func() { _ = svc.Stop(context.Background()) })
			svc.SetStateChangeCallback(func(_ string, _, newState services.ServiceState, _ services.HealthStatus, _ error) {
				states = append(states, newState)
			})

			err = svc.Start(t.Context())
			require.True(t, api.IsAuthRequiredError(err), "%v", err)
			assert.Equal(t, []services.ServiceState{services.StateStarting, services.StateAwaitingSession}, states)
			assert.True(t, hookRan.Load(), "the pending-auth registration hook must run")
			assert.False(t, svc.IsRunning())
			assert.False(t, api.IsDownState(svc.GetState()), "a start request must not restart a server that waits for a caller")
		})
	}
	assert.Empty(t, rec.texts(string(events.ReasonMCPServerAuthRequired)), "Auth Required is for a sign-in through muster only")
	assert.Len(t, rec.texts(string(events.ReasonMCPServerAwaitingSession)), 2)
}
