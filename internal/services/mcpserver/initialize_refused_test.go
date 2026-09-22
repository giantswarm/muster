package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/events"
	"github.com/giantswarm/muster/v5/internal/mcpserver"
	"github.com/giantswarm/muster/v5/internal/services"
)

// rolloutBackend is a backend mid-rollout: it answers 404 on every request
// until served is set, then it is a real MCP endpoint that accepts an
// anonymous initialize -- the shape of the previous pod still holding the
// route while the new one comes up (issue #1295).
type rolloutBackend struct {
	*httptest.Server
	served atomic.Bool
}

func startRolloutBackend(t *testing.T) *rolloutBackend {
	t.Helper()
	mcp := server.NewStreamableHTTPServer(server.NewMCPServer("rolled-out-backend", "1.0.0"), server.WithStateful(true))
	backend := &rolloutBackend{}
	backend.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !backend.served.Load() {
			http.Error(w, "route not found", http.StatusNotFound)
			return
		}
		mcp.ServeHTTP(w, r)
	}))
	t.Cleanup(backend.Close)
	return backend
}

// TestStartRecoversFromARefusedInitialize is the regression test for issue
// #1295: an MCPServer registered while its backend answers 404 reads Failed
// with a retry scheduled -- not Failed for good -- and the retry the
// orchestrator runs once the path answers settles it without a manual step:
// in Awaiting Session for a forwardToken server (the anonymous probe is
// discarded, sessions connect with their own token), in Connected otherwise.
func TestStartRecoversFromARefusedInitialize(t *testing.T) {
	for name, tc := range map[string]struct {
		auth          *api.MCPServerAuth
		recovered     services.ServiceState
		recoveryEvent events.EventReason
	}{
		"forwardToken": {
			auth:          &api.MCPServerAuth{Type: "oauth", ForwardToken: true},
			recovered:     services.StateAwaitingSession,
			recoveryEvent: events.ReasonMCPServerRecoveryAwaitingAuth,
		},
		"no auth": {
			recovered:     services.StateConnected,
			recoveryEvent: events.ReasonMCPServerRecoverySucceeded,
		},
	} {
		t.Run(name, func(t *testing.T) {
			withBackoff(t, time.Second, 3*time.Second)
			rec := &recordingEventManager{errors: map[string][]string{}}
			api.RegisterEventManager(rec)
			t.Cleanup(func() { api.RegisterEventManager(nil) })

			backend := startRolloutBackend(t)
			var hookRuns atomic.Int32
			svc, err := NewService(&api.MCPServer{
				Name: "behind-rollout", Type: api.MCPServerTypeStreamableHTTP, URL: backend.URL + "/mcp", Timeout: 5, Auth: tc.auth,
			}, WithAuthRequiredHook(func(*api.MCPServer, *mcpserver.AuthRequiredError) { hookRuns.Add(1) }))
			require.NoError(t, err)
			t.Cleanup(func() { _ = svc.Stop(context.Background()) })

			// Registration while the previous pod answers 404: Failed, but on the schedule.
			err = svc.Start(t.Context())
			require.Error(t, err)
			var refused *mcpserver.InitializeRefusedError
			require.ErrorAs(t, err, &refused, "the refusal is typed through every wrap")
			assert.False(t, api.IsAuthRequiredError(err), "a 404 is not a 401")
			assert.Equal(t, services.StateFailed, svc.GetState())
			assert.Equal(t, 1, svc.GetConsecutiveFailures())
			assert.Equal(t, http.StatusNotFound, svc.GetLastFailureHTTPStatus(), "the status mcp-go drops reaches the CR")
			next, scheduled := svc.GetServiceData()[api.ServiceDataNextRetryAfter].(time.Time)
			require.True(t, scheduled, "a path not served yet is retried")
			assert.True(t, next.After(time.Now()))
			assert.Equal(t, int32(0), hookRuns.Load(), "not registered pending auth while the endpoint does not answer")

			failed := rec.texts(string(events.ReasonMCPServerFailed))
			require.Len(t, failed, 1)
			assert.Contains(t, failed[0], "connection failure 1 of 3 before unreachable (endpoint answered HTTP 404, next retry in 1s at ")
			assert.Contains(t, failed[0], "endpoint answered the initialize POST with HTTP 404")

			// A second attempt against the same 404 keeps counting; nothing settles.
			require.Error(t, svc.Restart(t.Context()))
			assert.Equal(t, services.StateFailed, svc.GetState())
			assert.Equal(t, 2, svc.GetConsecutiveFailures())

			// The new pod takes the route; the orchestrator's retry is a Restart.
			backend.served.Store(true)
			err = svc.Restart(t.Context())
			if tc.recovered == services.StateAwaitingSession {
				require.True(t, api.IsAuthRequiredError(err), "the recovery of a forwardToken server ends in Awaiting Session: %v", err)
				assert.Equal(t, int32(1), hookRuns.Load(), "registered pending auth once the endpoint answers")
				assert.Nil(t, svc.GetMCPClient(), "the anonymous probe's client is discarded")
			} else {
				require.NoError(t, err)
				assert.NotNil(t, svc.GetMCPClient())
			}
			assert.Equal(t, tc.recovered, svc.GetState())
			assert.Equal(t, 0, svc.GetConsecutiveFailures(), "reaching the endpoint clears the schedule")
			assert.Equal(t, 0, svc.GetLastFailureHTTPStatus())
			_, scheduled = svc.GetServiceData()[api.ServiceDataNextRetryAfter]
			assert.False(t, scheduled)
			assert.Len(t, rec.texts(string(tc.recoveryEvent)), 1, "the recovery is reported as such")
			assert.Len(t, rec.texts(string(events.ReasonMCPServerFailed)), 2, "no further failure event")
		})
	}
}

// TestForwardTokenStateFollowsTheSessions pins the state rules of a server
// whose callers bring their own token: the anonymous probe decides between
// Failed (the endpoint does not answer the initialize) and Auth Required (it
// does: a 401, or a 200 that is discarded), and from there the sessions
// decide -- Connected after the first one connects (the aggregator's
// notifyMCPServerConnected through api.UpdateMCPServerState), and no health
// probe of the service touches it, since it holds no client of its own.
func TestForwardTokenStateFollowsTheSessions(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="backend"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(backend.Close)

	def := &api.MCPServer{
		Name: "per-session", Type: api.MCPServerTypeStreamableHTTP, URL: backend.URL + "/mcp", Timeout: 5,
		Auth: &api.MCPServerAuth{Type: "oauth", ForwardToken: true},
	}
	var hookRuns atomic.Int32
	svc, err := NewService(def, WithAuthRequiredHook(func(*api.MCPServer, *mcpserver.AuthRequiredError) { hookRuns.Add(1) }))
	require.NoError(t, err)

	registry := services.NewRegistry()
	require.NoError(t, registry.Register(svc))
	services.NewRegistryAdapter(registry).Register()
	t.Cleanup(func() { api.RegisterServiceRegistry(nil) })

	var states []services.ServiceState
	svc.SetStateChangeCallback(func(_ string, _, newState services.ServiceState, _ services.HealthStatus, _ error) {
		states = append(states, newState)
	})

	// The anonymous probe meets the 401: Awaiting Session, registered pending auth.
	err = svc.Start(t.Context())
	require.True(t, api.IsAuthRequiredError(err), "%v", err)
	assert.Equal(t, services.StateAwaitingSession, svc.GetState())
	assert.Equal(t, int32(1), hookRuns.Load())
	assert.Equal(t, 0, svc.GetConsecutiveFailures())

	// The first session connects with its own token: the aggregator syncs Connected.
	require.NoError(t, api.UpdateMCPServerState(def.Name, api.StateConnected, api.HealthHealthy, nil))
	assert.Equal(t, services.StateConnected, svc.GetState())
	assert.True(t, svc.IsRunning())

	// The service holds no client for the sessions' connections: its health
	// probe has nothing to check and leaves the state where the sessions put it.
	health, err := svc.CheckHealth(t.Context())
	require.NoError(t, err)
	assert.Equal(t, services.HealthHealthy, health)
	assert.Equal(t, services.StateConnected, svc.GetState())
	assert.Equal(t, 0, healthCheckFailures(svc))

	// The last session's grant is lost: the aggregator syncs Awaiting Session back.
	require.NoError(t, api.UpdateMCPServerState(def.Name, api.StateAwaitingSession, api.HealthUnknown, nil))
	assert.Equal(t, services.StateAwaitingSession, svc.GetState())

	assert.Equal(t, []services.ServiceState{services.StateStarting, services.StateAwaitingSession, services.StateConnected, services.StateAwaitingSession}, states)
	assert.NotContains(t, states, services.StateFailed, "a forwardToken server whose endpoint answers is never Failed")
}

// TestInitializeRefusedIsRetriedAndNamesItsStatus pins the classification: the
// typed refusal is transient however deep it is wrapped and reports its status,
// while a bare 4xx in an error's text -- a request other than the initialize --
// stays what it was.
func TestInitializeRefusedIsRetriedAndNamesItsStatus(t *testing.T) {
	svc, err := NewService(&api.MCPServer{Name: "typed", Type: api.MCPServerTypeStreamableHTTP, URL: "http://example.com/mcp", Timeout: 30})
	require.NoError(t, err)

	refused := &mcpserver.InitializeRefusedError{URL: "http://example.com/mcp", StatusCode: http.StatusNotFound, Err: transport.ErrLegacySSEServer}
	wrapped := fmt.Errorf("failed to start MCP server: %w", fmt.Errorf("failed to initialize streamable-http MCP client: %w", refused))
	assert.True(t, svc.isTransientConnectivityError(refused))
	assert.True(t, svc.isTransientConnectivityError(wrapped))
	assert.Equal(t, http.StatusNotFound, httpStatusFromError(wrapped))
	assert.ErrorIs(t, wrapped, transport.ErrLegacySSEServer)

	withoutStatus := &mcpserver.InitializeRefusedError{URL: "http://example.com/mcp", Err: transport.ErrLegacySSEServer}
	assert.True(t, svc.isTransientConnectivityError(withoutStatus), "retried even when the transport recorded no status")
	assert.Equal(t, 0, httpStatusFromError(withoutStatus))
	assert.Equal(t, "endpoint refused the initialize POST with a 4xx", withoutStatus.Error())

	assert.False(t, svc.isTransientConnectivityError(errors.New("request failed with status 404: Not Found")),
		"a 404 to another request is not the initialize being refused")
	assert.True(t, svc.isTransientConnectivityError(fmt.Errorf("transport error: %w", transport.ErrLegacySSEServer)),
		"the transport's bare sentinel, from a client that does not type it, is retried as well")
	assert.Equal(t, 0, httpStatusFromError(transport.ErrLegacySSEServer), "the sentinel carries no status")
}

// TestInitializeRefusedRetryLaterHonoursRetryAfter pins issue #1303 at the
// service level: a 429 or 503 the endpoint answered the initialize with is
// retried, its status reaches the CR status, and the Retry-After it carried is
// the reconnect wait -- the delay the server named, not muster's doubling
// backoff -- while a refusal without a Retry-After falls back to the backoff.
func TestInitializeRefusedRetryLaterHonoursRetryAfter(t *testing.T) {
	svc, err := NewService(&api.MCPServer{Name: "rate-limited", Type: api.MCPServerTypeStreamableHTTP, URL: "http://example.com/mcp", Timeout: 30})
	require.NoError(t, err)

	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			refused := &mcpserver.InitializeRefusedError{URL: "http://example.com/mcp", StatusCode: status, RetryAfter: 1500 * time.Millisecond, Err: transport.ErrLegacySSEServer}
			assert.True(t, svc.isTransientConnectivityError(refused), "a retry-later status is transient")
			assert.Equal(t, status, httpStatusFromError(refused), "the status reaches the CR status")
			assert.Equal(t, 1500*time.Millisecond, retryAfterFromError(refused), "the Retry-After is read off the typed error")
		})
	}

	// No Retry-After: the reconnect falls back to muster's backoff.
	assert.Equal(t, time.Duration(0), retryAfterFromError(&mcpserver.InitializeRefusedError{StatusCode: http.StatusTooManyRequests, Err: transport.ErrLegacySSEServer}))
	assert.Equal(t, time.Duration(0), retryAfterFromError(errors.New("request failed with status 503: Service Unavailable")),
		"a status in an error's text carries no typed Retry-After")

	// The message names the status and, for a 429, the Retry-After; it never
	// blames a legacy SSE server for a status that has its own meaning.
	rateLimited := &mcpserver.InitializeRefusedError{StatusCode: http.StatusTooManyRequests, RetryAfter: time.Second, Err: transport.ErrLegacySSEServer}
	assert.Equal(t, "endpoint answered the initialize POST with HTTP 429 Too Many Requests; retry after 1s", rateLimited.Error())
	assert.NotContains(t, rateLimited.Error(), "legacy SSE server")

	legacy := &mcpserver.InitializeRefusedError{StatusCode: http.StatusMethodNotAllowed, Err: transport.ErrLegacySSEServer}
	assert.Contains(t, legacy.Error(), "likely a legacy SSE server", "405 is the one status that means legacy SSE")
}

// TestRetryLaterReconnectSchedule pins that a Retry-After puts the next
// reconnect exactly that far out, while its absence falls back to the
// exponential backoff even after several failures.
func TestRetryLaterReconnectSchedule(t *testing.T) {
	withBackoff(t, 30*time.Second, 2*time.Minute)
	svc, err := NewService(&api.MCPServer{Name: "rate-limited", Type: api.MCPServerTypeStreamableHTTP, URL: "http://example.com/mcp", Timeout: 30})
	require.NoError(t, err)

	// The server named a 5s Retry-After: the wait is exactly that, not the
	// 30s initial backoff, however many failures have accrued.
	svc.failureMutex.Lock()
	svc.consecutiveFailures = 3
	svc.calculateNextRetryTimeLocked(5 * time.Second)
	retryBackoff := svc.retryBackoff
	svc.failureMutex.Unlock()
	assert.Equal(t, 5*time.Second, retryBackoff, "the Retry-After the server named is the wait")

	// No Retry-After: the wait is the exponential backoff for the failure
	// count (second failure: 30s doubled to 60s).
	svc.failureMutex.Lock()
	svc.consecutiveFailures = 2
	svc.calculateNextRetryTimeLocked(0)
	fallback := svc.retryBackoff
	svc.failureMutex.Unlock()
	assert.Equal(t, 60*time.Second, fallback, "without a Retry-After the exponential backoff applies")
}
