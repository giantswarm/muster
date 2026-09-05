package mcpserver

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/events"
	"github.com/giantswarm/muster/internal/mcpserver"
	"github.com/giantswarm/muster/internal/services"
)

// withBackoff pins InitialBackoff and MaxBackoff for one test and restores
// them afterwards. Both are package variables read from the environment at
// start-up, so a test cannot rely on their defaults.
func withBackoff(t *testing.T, initial, maximum time.Duration) {
	t.Helper()
	prevInitial, prevMax := InitialBackoff, MaxBackoff
	InitialBackoff, MaxBackoff = initial, maximum
	t.Cleanup(func() { InitialBackoff, MaxBackoff = prevInitial, prevMax })
}

// recordingEventManager keeps the Error text of every event so a test can
// assert on what an operator reads in `kubectl get events` / `muster events`.
type recordingEventManager struct {
	mu     sync.Mutex
	errors map[string][]string
}

func (r *recordingEventManager) CreateEventWithData(_ context.Context, _ api.ObjectReference, reason string, data api.EventData) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors[reason] = append(r.errors[reason], data.Error)
	return nil
}

func (r *recordingEventManager) DefaultNamespace() string { return "default" }

func (r *recordingEventManager) QueryEvents(_ context.Context, _ api.EventQueryOptions) (*api.EventQueryResult, error) {
	return &api.EventQueryResult{}, nil
}

func (r *recordingEventManager) WatchEvents(_ context.Context, _ api.EventQueryOptions) (<-chan api.EventResult, error) {
	ch := make(chan api.EventResult)
	close(ch)
	return ch, nil
}

func (r *recordingEventManager) IsKubernetesMode() bool { return false }

func (r *recordingEventManager) texts(reason string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.errors[reason]...)
}

// TestBackoffScheduleIsCapped pins the production schedule: 30s, 60s, 120s and
// then 120s for every further failure. The former 30 minute cap let the fifth
// failure wait 8 minutes and the seventh 30, hiding an upstream recovery for
// as long (issue #1163).
func TestBackoffScheduleIsCapped(t *testing.T) {
	withBackoff(t, 30*time.Second, 2*time.Minute)

	svc, err := NewService(&api.MCPServer{Name: "capped", Type: api.MCPServerTypeStreamableHTTP, URL: "http://example.com/mcp"})
	require.NoError(t, err)

	want := map[int]time.Duration{
		1:   30 * time.Second,
		2:   60 * time.Second,
		3:   120 * time.Second,
		4:   120 * time.Second,
		5:   120 * time.Second,
		7:   120 * time.Second,
		100: 120 * time.Second,
	}
	for failures, expected := range want {
		svc.failureMutex.Lock()
		svc.consecutiveFailures = failures
		svc.calculateNextRetryTimeLocked()
		got := svc.retryBackoff
		svc.failureMutex.Unlock()
		assert.Equal(t, expected, got, "backoff after %d failures", failures)
	}
}

// TestBackoffCapBelowInitialWins: an operator who sets the cap below the
// initial backoff gets the cap on every retry rather than a schedule that
// starts above its own maximum.
func TestBackoffCapBelowInitialWins(t *testing.T) {
	withBackoff(t, 30*time.Second, 10*time.Second)

	svc, err := NewService(&api.MCPServer{Name: "low-cap", Type: api.MCPServerTypeStreamableHTTP, URL: "http://example.com/mcp"})
	require.NoError(t, err)

	for _, failures := range []int{1, 2, 5} {
		svc.failureMutex.Lock()
		svc.consecutiveFailures = failures
		svc.calculateNextRetryTimeLocked()
		got := svc.retryBackoff
		svc.failureMutex.Unlock()
		assert.Equal(t, 10*time.Second, got, "backoff after %d failures", failures)
	}
}

// TestMaxBackoffDefaultAndOverride pins the default cap and its environment
// override, the knob an operator turns without a new release.
func TestMaxBackoffDefaultAndOverride(t *testing.T) {
	t.Setenv("MUSTER_MCPSERVER_MAX_BACKOFF", "")
	assert.Equal(t, 2*time.Minute, durationFromEnv("MUSTER_MCPSERVER_MAX_BACKOFF", 2*time.Minute))

	t.Setenv("MUSTER_MCPSERVER_MAX_BACKOFF", "5m")
	assert.Equal(t, 5*time.Minute, durationFromEnv("MUSTER_MCPSERVER_MAX_BACKOFF", 2*time.Minute))

	t.Setenv("MUSTER_MCPSERVER_MAX_BACKOFF", "not-a-duration")
	assert.Equal(t, 2*time.Minute, durationFromEnv("MUSTER_MCPSERVER_MAX_BACKOFF", 2*time.Minute),
		"an unparsable override falls back to the default")
}

func TestHTTPStatusFromError(t *testing.T) {
	cases := map[string]struct {
		err  string
		want int
	}{
		"streamable-http initialize through a gateway": {
			err:  "failed to initialize streamable-http MCP client: failed to start StreamableHTTP transport: request failed with status 504: Gateway Timeout",
			want: 504,
		},
		"sse endpoint": {
			err:  "failed to initialize sse MCP client: unexpected status code: 502",
			want: 502,
		},
		"401 for a machine identity": {
			err:  "authentication required: server returned 401 Unauthorized: unauthorized",
			want: 401,
		},
		"status code with colon": {
			err:  "request failed with status code: 503",
			want: 503,
		},
		"connection refused carries no status": {
			err:  "failed to initialize streamable-http MCP client: dial tcp 127.0.0.1:1: connect: connection refused",
			want: 0,
		},
		"timeout carries no status": {
			err:  "failed to initialize streamable-http MCP client: context deadline exceeded",
			want: 0,
		},
		"a port number is not a status": {
			err:  "dial tcp 10.0.0.1:504: i/o timeout",
			want: 0,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, httpStatusFromError(assertErr(tc.err)))
		})
	}
	assert.Equal(t, 0, httpStatusFromError(nil))
}

type stringErr string

func (e stringErr) Error() string { return string(e) }

func assertErr(s string) error { return stringErr(s) }

// TestStartRecordsUpstreamStatusAndSchedule drives a remote server against an
// endpoint that answers 504, the incident shape: a gateway in front of a
// healthy server timing out. The service must record the HTTP status, publish
// it with the schedule through GetServiceData, and put both into every
// MCPServerFailed event -- with the exact capped wait, so "next retry in 3s"
// on the third and fourth failure is the proof the cap is in force.
func TestStartRecordsUpstreamStatusAndSchedule(t *testing.T) {
	withBackoff(t, time.Second, 3*time.Second)

	rec := &recordingEventManager{errors: map[string][]string{}}
	api.RegisterEventManager(rec)
	defer api.RegisterEventManager(nil)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Gateway Timeout", http.StatusGatewayTimeout)
	}))
	defer upstream.Close()

	svc, err := NewService(&api.MCPServer{Name: "behind-gateway", Type: api.MCPServerTypeStreamableHTTP, URL: upstream.URL + "/mcp", Timeout: 5})
	require.NoError(t, err)

	for attempt := 1; attempt <= 4; attempt++ {
		err := svc.Start(t.Context())
		require.Error(t, err, "attempt %d", attempt)
		require.Contains(t, err.Error(), "status 504")
	}

	assert.Equal(t, services.StateUnreachable, svc.GetState())
	assert.Equal(t, 4, svc.GetConsecutiveFailures())
	assert.Equal(t, http.StatusGatewayTimeout, svc.GetLastFailureHTTPStatus())

	data := svc.GetServiceData()
	assert.Equal(t, 4, data[api.ServiceDataConsecutiveFailures])
	assert.Equal(t, http.StatusGatewayTimeout, data[api.ServiceDataLastFailureHTTPStatus])
	next, ok := data[api.ServiceDataNextRetryAfter].(time.Time)
	require.True(t, ok, "nextRetryAfter must be published as time.Time")
	last, ok := data[api.ServiceDataLastAttempt].(time.Time)
	require.True(t, ok)
	assert.InDelta(t, (3 * time.Second).Seconds(), next.Sub(last).Seconds(), 0.5,
		"the fourth failure waits the cap, not 8x the initial backoff")

	failed := rec.texts(string(events.ReasonMCPServerFailed))
	require.Len(t, failed, 4, "one MCPServerFailed event per attempt: %v", failed)
	assert.Contains(t, failed[0], "connection failure 1 of 3 before unreachable (endpoint answered HTTP 504, next retry in 1s at ")
	assert.Contains(t, failed[1], "connection failure 2 of 3 before unreachable (endpoint answered HTTP 504, next retry in 2s at ")
	assert.Contains(t, failed[2], "server unreachable after 3 consecutive failures (endpoint answered HTTP 504, next retry in 3s at ")
	assert.Contains(t, failed[3], "server unreachable after 4 consecutive failures (endpoint answered HTTP 504, next retry in 3s at ")
	for _, text := range failed {
		assert.True(t, strings.HasSuffix(strings.SplitN(text, "): ", 2)[0], "Z"), "schedule names an RFC 3339 UTC time: %q", text)
		assert.Contains(t, text, "status 504", "the raw error still follows the schedule")
	}
}

// TestStartRefusedConnectionRecordsNoHTTPStatus is the other side of the
// distinction the status and events have to make: nothing listening yields no
// HTTP status and the event says so.
func TestStartRefusedConnectionRecordsNoHTTPStatus(t *testing.T) {
	withBackoff(t, time.Second, 3*time.Second)

	rec := &recordingEventManager{errors: map[string][]string{}}
	api.RegisterEventManager(rec)
	defer api.RegisterEventManager(nil)

	// Reserve a port and close it so the connection is refused deterministically.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	url := "http://" + l.Addr().String() + "/mcp"
	require.NoError(t, l.Close())

	svc, err := NewService(&api.MCPServer{Name: "gone", Type: api.MCPServerTypeStreamableHTTP, URL: url, Timeout: 5})
	require.NoError(t, err)
	require.Error(t, svc.Start(t.Context()))

	assert.Equal(t, 0, svc.GetLastFailureHTTPStatus())
	_, published := svc.GetServiceData()[api.ServiceDataLastFailureHTTPStatus]
	assert.False(t, published, "no HTTP status key when the failure carried none")

	failed := rec.texts(string(events.ReasonMCPServerFailed))
	require.Len(t, failed, 1)
	assert.Contains(t, failed[0], "connection failure 1 of 3 before unreachable (no HTTP response, next retry in 1s at ")
	assert.Contains(t, failed[0], "connection refused")
}

// TestAuthRequiredClearsSchedule: a 401 after an outage proves the endpoint is
// back. The server lands in Auth Required as before, and the schedule from the
// outage does not linger next to it.
func TestAuthRequiredClearsSchedule(t *testing.T) {
	withBackoff(t, time.Second, 3*time.Second)

	var outage sync.Mutex
	remaining := 4
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		outage.Lock()
		defer outage.Unlock()
		if remaining > 0 {
			remaining--
			http.Error(w, "Gateway Timeout", http.StatusGatewayTimeout)
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="test"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	svc, err := NewService(&api.MCPServer{Name: "behind-gateway-oauth", Type: api.MCPServerTypeStreamableHTTP, URL: upstream.URL + "/mcp", Timeout: 5})
	require.NoError(t, err)

	for attempt := 1; attempt <= 4; attempt++ {
		require.Error(t, svc.Start(t.Context()))
	}
	require.Equal(t, services.StateUnreachable, svc.GetState())
	require.Equal(t, http.StatusGatewayTimeout, svc.GetLastFailureHTTPStatus())

	err = svc.Start(t.Context())
	var authErr *mcpserver.AuthRequiredError
	require.ErrorAs(t, err, &authErr)
	assert.Equal(t, services.StateAuthRequired, svc.GetState())
	assert.Equal(t, 0, svc.GetConsecutiveFailures())
	assert.Nil(t, svc.GetNextRetryAfter())
	assert.Equal(t, 0, svc.GetLastFailureHTTPStatus())
}
