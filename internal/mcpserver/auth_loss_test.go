package mcpserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingHandler collects onAuthLost invocations for assertions.
type recordingHandler struct {
	mu      sync.Mutex
	reasons []string
	fired   chan string
}

func newRecordingHandler() *recordingHandler {
	return &recordingHandler{fired: make(chan string, 8)}
}

func (r *recordingHandler) handle(reason string) {
	r.mu.Lock()
	r.reasons = append(r.reasons, reason)
	r.mu.Unlock()
	r.fired <- reason
}

func (r *recordingHandler) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.reasons)
}

func (r *recordingHandler) waitFired(t *testing.T, timeout time.Duration) string {
	t.Helper()
	select {
	case reason := <-r.fired:
		return reason
	case <-time.After(timeout):
		t.Fatalf("onAuthLost did not fire within %v", timeout)
		return ""
	}
}

func TestAuthLossDetector_FiresOnceAtThreshold(t *testing.T) {
	handler := newRecordingHandler()
	detector := &authLossDetector{onAuthLost: handler.handle}

	for i := 0; i < authLossThreshold-1; i++ {
		detector.noteFailure("almost")
	}
	select {
	case <-handler.fired:
		t.Fatal("fired below threshold")
	case <-time.After(50 * time.Millisecond):
	}

	detector.noteFailure("over the line")
	assert.Equal(t, "over the line", handler.waitFired(t, time.Second))

	// Further failures never re-fire: the connection is expected to be retired.
	for i := 0; i < 2*authLossThreshold; i++ {
		detector.noteFailure("extra")
	}
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, handler.count())
}

func TestAuthLossDetector_SuccessResetsCount(t *testing.T) {
	handler := newRecordingHandler()
	detector := &authLossDetector{onAuthLost: handler.handle}

	// Interleaved successes keep the consecutive count below the threshold.
	for i := 0; i < 3*authLossThreshold; i++ {
		detector.noteFailure("blip")
		if (i+1)%(authLossThreshold-1) == 0 {
			detector.noteSuccess()
		}
	}
	select {
	case <-handler.fired:
		t.Fatal("fired despite interleaved successes")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestAuthLossDetector_NilSafe(t *testing.T) {
	var detector *authLossDetector
	detector.noteFailure("nil")
	detector.noteSuccess()
}

// staticTokenStore serves a fixed token until dropToken is set, then reports
// transport.ErrNoToken — the shape of a backing store that lost its data.
type staticTokenStore struct {
	dropToken atomic.Bool
	token     transport.Token
}

func (s *staticTokenStore) GetToken(_ context.Context) (*transport.Token, error) {
	if s.dropToken.Load() {
		return nil, transport.ErrNoToken
	}
	token := s.token
	return &token, nil
}

func (s *staticTokenStore) SaveToken(_ context.Context, _ *transport.Token) error { return nil }

func TestAuthObservingTokenStore_ReportsMisses(t *testing.T) {
	handler := newRecordingHandler()
	store := &staticTokenStore{token: transport.Token{AccessToken: "tok", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour)}}
	observing := &authObservingTokenStore{
		TokenStore: store,
		detector:   &authLossDetector{onAuthLost: handler.handle},
	}

	// Successful reads do not fire and do not reset anything relevant here.
	_, err := observing.GetToken(context.Background())
	require.NoError(t, err)

	store.dropToken.Store(true)
	for i := 0; i < authLossThreshold; i++ {
		_, err := observing.GetToken(context.Background())
		require.ErrorIs(t, err, transport.ErrNoToken)
	}
	handler.waitFired(t, time.Second)
}

func TestAuthObservingTransport_CountsOnly401(t *testing.T) {
	handler := newRecordingHandler()
	detector := &authLossDetector{onAuthLost: handler.handle}

	status := &atomic.Int64{}
	status.Store(http.StatusOK)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(int(status.Load()))
	}))
	defer ts.Close()

	client := &http.Client{Transport: &authObservingTransport{next: http.DefaultTransport, detector: detector}}
	get := func() {
		resp, err := client.Get(ts.URL)
		require.NoError(t, err)
		_ = resp.Body.Close()
	}

	// 401s below the threshold, then a success: the count resets.
	status.Store(http.StatusUnauthorized)
	for i := 0; i < authLossThreshold-1; i++ {
		get()
	}
	status.Store(http.StatusOK)
	get()
	status.Store(http.StatusUnauthorized)
	for i := 0; i < authLossThreshold-1; i++ {
		get()
	}
	select {
	case <-handler.fired:
		t.Fatal("fired despite reset")
	case <-time.After(50 * time.Millisecond):
	}

	get()
	handler.waitFired(t, time.Second)
}

// TestDynamicAuthClient_AuthLossStopsListener reproduces the incident shape
// end to end: a session-scoped OAuth connection is established, then the
// server starts rejecting the (still held) token with 401. mcp-go's
// continuous listener hits the 401s; the auth-loss handler must fire so the
// connection can be retired, and closing the client must stop the retry loop.
func TestDynamicAuthClient_AuthLossStopsListener(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-second listener retry timing")
	}

	revoked := &atomic.Bool{}
	requests := &atomic.Int64{}

	mcpHandler := server.NewStreamableHTTPServer(newTestMCPServer(), server.WithStateful(true))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if revoked.Load() {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token", error_description="Token validation failed"`)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_token","error_description":"Token validation failed"}`))
			return
		}
		mcpHandler.ServeHTTP(w, r)
	}))
	defer ts.Close()

	store := &staticTokenStore{token: transport.Token{AccessToken: "tok", TokenType: "Bearer", ExpiresAt: time.Now().Add(time.Hour)}}
	handler := newRecordingHandler()
	client := NewDynamicAuthClient(ts.URL, store, "scope", "client-id", "").
		WithAuthLossHandler(handler.handle)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, client.Initialize(ctx))

	// The grant disappears server-side. The continuous listener retries once
	// per second, so the threshold is crossed within a few seconds even with
	// no tool call in flight.
	revoked.Store(true)
	handler.waitFired(t, 15*time.Second)

	// The production handler evicts the pooled client, which closes it.
	require.NoError(t, client.Close())

	// Give any in-flight request time to land, then verify the retry loop is
	// gone: no further requests reach the server.
	time.Sleep(time.Second)
	before := requests.Load()
	time.Sleep(2500 * time.Millisecond)
	assert.Equal(t, before, requests.Load(), "listener kept retrying after close")
}
