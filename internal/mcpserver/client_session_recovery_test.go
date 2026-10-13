package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sessionFakeClient stands in for the mcp-go client behind a baseMCPClient.
// Methods the recovery path does not touch are left to the embedded nil
// interface and panic if called.
type sessionFakeClient struct {
	client.MCPClient

	mu        sync.Mutex
	sessionID string
	// callErrs are returned by successive CallTool calls; a nil entry and any
	// call past the end succeed.
	callErrs []error
	calls    int
	closed   int
	// barrier, when set, runs at the start of every CallTool so a test can
	// hold concurrent callers inside the operation until all have arrived.
	barrier func()
}

func (f *sessionFakeClient) GetSessionId() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessionID
}

func (f *sessionFakeClient) CallTool(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if f.barrier != nil {
		f.barrier()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.calls
	f.calls++
	if i < len(f.callErrs) && f.callErrs[i] != nil {
		return nil, f.callErrs[i]
	}
	return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent("ok")}}, nil
}

func (f *sessionFakeClient) Ping(ctx context.Context) error {
	_, err := f.CallTool(ctx, mcp.CallToolRequest{})
	return err
}

func (f *sessionFakeClient) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}

func (f *sessionFakeClient) OnNotification(func(mcp.JSONRPCNotification)) {}

func (f *sessionFakeClient) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// recoveryHarness wires a baseMCPClient to a fake whose reconnect swaps in
// the next fake from the queue, the way a transport's connectLocked replaces
// the mcp-go client.
type recoveryHarness struct {
	base       *baseMCPClient
	first      *sessionFakeClient
	next       []*sessionFakeClient
	reconnects atomic.Int32
	// reconnectErrs are returned by successive reconnect attempts.
	reconnectErrs []error
}

func newRecoveryHarness(first *sessionFakeClient, next ...*sessionFakeClient) *recoveryHarness {
	h := &recoveryHarness{first: first, next: next}
	h.base = &baseMCPClient{client: first, connected: true, hadSession: first.sessionID != ""}
	h.base.reconnect = func(context.Context) error {
		n := int(h.reconnects.Add(1))
		if n-1 < len(h.reconnectErrs) && h.reconnectErrs[n-1] != nil {
			return h.reconnectErrs[n-1]
		}
		if len(h.next) == 0 {
			panic("harness ran out of replacement clients")
		}
		fresh := h.next[0]
		h.next = h.next[1:]
		h.base.client = fresh
		h.base.connected = true
		h.base.hadSession = fresh.sessionID != ""
		return nil
	}
	return h
}

var errSessionGone = fmt.Errorf("failed to send request: %w", transport.ErrSessionTerminated)

// The holder of the client learns of every recovered session, on a goroutine
// of its own, so it can re-list what the new process serves (issue #1440); a
// handshake that failed is no recovery.
func TestSessionRecovery_TellsTheObserverAfterTheHandshake(t *testing.T) {
	old := &sessionFakeClient{sessionID: "s1", callErrs: []error{errSessionGone}}
	h := newRecoveryHarness(old, &sessionFakeClient{sessionID: "s2"})
	recovered := make(chan struct{}, 1)
	h.base.OnSessionRecovered(func() { recovered <- struct{}{} })

	_, err := h.base.callTool(t.Context(), "echo", nil)

	require.NoError(t, err)
	select {
	case <-recovered:
	case <-t.Context().Done():
		t.Fatal("the observer was not told of the recovered session")
	}

	failing := &sessionFakeClient{sessionID: "s3", callErrs: []error{errSessionGone}}
	h = newRecoveryHarness(failing)
	h.reconnectErrs = []error{errors.New("connection refused")}
	told := atomic.Int32{}
	h.base.OnSessionRecovered(func() { told.Add(1) })

	_, err = h.base.callTool(t.Context(), "echo", nil)

	require.Error(t, err)
	assert.Equal(t, int32(0), told.Load(), "a failed handshake recovers nothing")
}

// ErrSessionTerminated proves the backend did not run the call: it answered
// 404 for the unknown session, or the transport knew the session had ended
// and did not send it. That call is sent once on the new session.
func TestSessionRecovery_ReinitializesOnceAfterSessionTerminated(t *testing.T) {
	old := &sessionFakeClient{sessionID: "s1", callErrs: []error{errSessionGone}}
	fresh := &sessionFakeClient{sessionID: "s2"}
	h := newRecoveryHarness(old, fresh)

	res, err := h.base.callTool(t.Context(), "echo", nil)

	require.NoError(t, err)
	assert.NotNil(t, res)
	assert.Equal(t, int32(1), h.reconnects.Load())
	assert.Equal(t, 1, old.closed, "the dead client is closed before the handshake")
	assert.Equal(t, 1, fresh.callCount(), "the failed call is retried exactly once on the new session")
	assert.Equal(t, uint64(1), h.base.sessionGeneration)
}

// ranThenLost is a tool call that reached the backend and ran there, while
// the standalone GET listener met the 404 of the restarted process and
// cleared the session id; the call's own answer was lost.
func ranThenLost(f *sessionFakeClient) {
	f.barrier = func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.sessionID = ""
	}
	f.callErrs = []error{errors.New("transport error: unexpected EOF")}
}

// A call that may have run on the backend is never sent again after the
// session is recovered, a write as little as a read: the caller gets the
// failure, marked ErrNotRepeated, and decides. The session itself is
// re-established, so the next call goes to the new one.
func TestSessionRecovery_CallThatMayHaveRunIsNotRepeated(t *testing.T) {
	for _, tool := range []string{"create_incident", "list_incidents"} {
		t.Run(tool, func(t *testing.T) {
			old := &sessionFakeClient{sessionID: "s1"}
			ranThenLost(old)
			fresh := &sessionFakeClient{sessionID: "s2"}
			h := newRecoveryHarness(old, fresh)

			_, err := h.base.callTool(t.Context(), tool, nil)

			require.ErrorIs(t, err, ErrNotRepeated)
			assert.ErrorContains(t, err, "unexpected EOF", "the call's own failure is kept")
			assert.Equal(t, 1, old.callCount(), "the call was sent once")
			assert.Equal(t, 0, fresh.callCount(), "and not again on the new session")
			assert.Equal(t, int32(1), h.reconnects.Load(), "the session is re-established")

			_, err = h.base.callTool(t.Context(), tool, nil)
			require.NoError(t, err, "the next call runs on the new session")
			assert.Equal(t, 1, fresh.callCount())
		})
	}
}

func TestSessionRecovery_LeavesOtherErrorsAlone(t *testing.T) {
	boom := errors.New("tool exploded")
	old := &sessionFakeClient{sessionID: "s1", callErrs: []error{boom}}
	h := newRecoveryHarness(old, &sessionFakeClient{sessionID: "s2"})

	_, err := h.base.callTool(t.Context(), "echo", nil)

	require.ErrorIs(t, err, boom)
	assert.Equal(t, int32(0), h.reconnects.Load())
	assert.Equal(t, 1, old.callCount())
	assert.Equal(t, 0, old.closed)
}

// A stateless server never issued a session, so a missing session id means
// nothing and a failing call is not a reason to reconnect.
func TestSessionRecovery_NoReconnectForStatelessServer(t *testing.T) {
	old := &sessionFakeClient{sessionID: "", callErrs: []error{errors.New("boom")}}
	h := newRecoveryHarness(old, &sessionFakeClient{})

	_, err := h.base.callTool(t.Context(), "echo", nil)

	require.Error(t, err)
	assert.Equal(t, int32(0), h.reconnects.Load())
}

func TestSessionRecovery_FailedHandshakeIsRetriedByTheNextOperation(t *testing.T) {
	old := &sessionFakeClient{sessionID: "s1", callErrs: []error{errSessionGone}}
	fresh := &sessionFakeClient{sessionID: "s2"}
	h := newRecoveryHarness(old, fresh)
	h.reconnectErrs = []error{errors.New("connection refused")}

	_, err := h.base.callTool(t.Context(), "echo", nil)
	require.ErrorIs(t, err, transport.ErrSessionTerminated, "the original failure is reported when the handshake fails")
	assert.ErrorContains(t, err, "connection refused", "together with why the handshake failed")
	assert.Equal(t, int32(1), h.reconnects.Load())
	assert.False(t, h.base.connected)
	assert.True(t, h.base.reconnectPending)

	res, err := h.base.callTool(t.Context(), "echo", nil)
	require.NoError(t, err)
	assert.NotNil(t, res)
	assert.Equal(t, int32(2), h.reconnects.Load())
	assert.False(t, h.base.reconnectPending)
	assert.Equal(t, 1, fresh.callCount())
}

// A backend that rejects the handshake with 401 must be reported as auth
// required, not as a lost session: the aggregator's 401 checks decide on the
// error whether to refresh a token or ask the user to sign in again.
func TestSessionRecovery_FailedHandshakeKeepsAuthRequiredError(t *testing.T) {
	old := &sessionFakeClient{sessionID: "s1", callErrs: []error{errSessionGone}}
	h := newRecoveryHarness(old, &sessionFakeClient{sessionID: "s2"})
	h.reconnectErrs = []error{&AuthRequiredError{
		URL: "http://backend",
		Err: fmt.Errorf("server returned 401 Unauthorized: %w", transport.ErrUnauthorized),
	}}

	_, err := h.base.callTool(t.Context(), "echo", nil)

	require.Error(t, err)
	var authErr *AuthRequiredError
	assert.ErrorAs(t, err, &authErr)
	assert.ErrorIs(t, err, transport.ErrUnauthorized)
	assert.ErrorIs(t, err, transport.ErrSessionTerminated)
}

// The handshake runs with the write lock held, so it is bounded even when
// the caller's context carries no deadline; otherwise a hung backend would
// block every other operation on the client, Close included.
func TestSessionRecovery_HandshakeHasDeadline(t *testing.T) {
	old := &sessionFakeClient{sessionID: "s1", callErrs: []error{errSessionGone}}
	h := newRecoveryHarness(old, &sessionFakeClient{sessionID: "s2"})
	inner := h.base.reconnect
	var hasDeadline bool
	h.base.reconnect = func(ctx context.Context) error {
		_, hasDeadline = ctx.Deadline()
		return inner(ctx)
	}

	_, err := h.base.callTool(t.Context(), "echo", nil)

	require.NoError(t, err)
	assert.True(t, hasDeadline)
}

// A server configured with a timeout longer than the default connected under
// that budget the first time; its recovery handshake gets the same budget.
func TestSessionRecovery_HandshakeDeadlineFollowsConfiguredTimeout(t *testing.T) {
	old := &sessionFakeClient{sessionID: "s1", callErrs: []error{errSessionGone}}
	h := newRecoveryHarness(old, &sessionFakeClient{sessionID: "s2"})
	h.base.timeout = 90 * time.Second
	inner := h.base.reconnect
	var remaining time.Duration
	h.base.reconnect = func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		remaining = time.Until(deadline)
		return inner(ctx)
	}

	_, err := h.base.callTool(t.Context(), "echo", nil)

	require.NoError(t, err)
	assert.Greater(t, remaining, DefaultTimeout, "the configured timeout, not the default, bounds the handshake")
}

// Every caller queued behind the one failed handshake gets its cause, not
// only the caller that ran it: the aggregator's 401 checks run on each
// caller's error.
func TestSessionRecovery_ConcurrentCallersShareOneFailedHandshake(t *testing.T) {
	const callers = 16
	failing := make([]error, callers)
	rejected := make([]error, callers)
	for i := range failing {
		failing[i] = errSessionGone
		rejected[i] = &AuthRequiredError{
			URL: "http://backend",
			Err: fmt.Errorf("server returned 401 Unauthorized: %w", transport.ErrUnauthorized),
		}
	}
	old := &sessionFakeClient{sessionID: "s1", callErrs: failing}
	// Every caller fails on the same generation: a caller that arrived after
	// the failed handshake would be the next operation, which does try again.
	var arrived sync.WaitGroup
	arrived.Add(callers)
	old.barrier = func() { arrived.Done(); arrived.Wait() }
	h := newRecoveryHarness(old, &sessionFakeClient{sessionID: "s2"})
	h.reconnectErrs = rejected

	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.base.callTool(t.Context(), "echo", nil)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.ErrorIs(t, err, transport.ErrSessionTerminated)
		var authErr *AuthRequiredError
		require.ErrorAs(t, err, &authErr, "every caller learns why the shared handshake failed")
		require.ErrorIs(t, err, transport.ErrUnauthorized)
	}
	assert.Equal(t, int32(1), h.reconnects.Load(), "callers queued behind a failed handshake do not each repeat it")
	assert.Equal(t, uint64(1), h.base.sessionGeneration)
	assert.True(t, h.base.reconnectPending, "the next operation tries again")
}

func TestSessionRecovery_ConcurrentCallersShareOneHandshake(t *testing.T) {
	const callers = 16
	failing := make([]error, callers)
	for i := range failing {
		failing[i] = errSessionGone
	}
	old := &sessionFakeClient{sessionID: "s1", callErrs: failing}
	fresh := &sessionFakeClient{sessionID: "s2"}
	h := newRecoveryHarness(old, fresh)

	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := h.base.callTool(t.Context(), "echo", nil)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	assert.Equal(t, int32(1), h.reconnects.Load(), "one handshake serves every caller that failed on the dead session")
	assert.Equal(t, uint64(1), h.base.sessionGeneration)
}

func TestSessionRecovery_NoReconnectAfterClose(t *testing.T) {
	old := &sessionFakeClient{sessionID: "s1"}
	h := newRecoveryHarness(old, &sessionFakeClient{sessionID: "s2"})

	require.NoError(t, h.base.closeClient())
	_, err := h.base.callTool(t.Context(), "echo", nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "client not connected")
	assert.Equal(t, int32(0), h.reconnects.Load())
}

// The health probe goes through the same path, so a periodic ping heals a
// lost session as well.
func TestSessionRecovery_PingReinitializes(t *testing.T) {
	old := &sessionFakeClient{sessionID: "s1", callErrs: []error{errSessionGone}}
	h := newRecoveryHarness(old, &sessionFakeClient{sessionID: "s2"})

	require.NoError(t, h.base.ping(t.Context()))
	assert.Equal(t, int32(1), h.reconnects.Load())
}

// TestStreamableHTTPClient_RecoversSessionAfterBackendRedeploy runs the real
// mcp-go stack: a stateful streamable-http server is replaced by a fresh
// process that does not know the client's session, as a redeployed pod would
// be. Before the fix every call after the swap failed until the service was
// restarted (issue #999). The redeployed process refuses the call with 404,
// so the call runs there once, after the new handshake, and never twice.
func TestStreamableHTTPClient_RecoversSessionAfterBackendRedeploy(t *testing.T) {
	var runs atomic.Int32
	newBackend := func() *server.StreamableHTTPServer {
		s := server.NewMCPServer("redeploy-test", "1.0.0")
		s.AddTool(mcp.NewTool("echo"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			runs.Add(1)
			return mcp.NewToolResultText("alive"), nil
		})
		return server.NewStreamableHTTPServer(s, server.WithStateful(true))
	}

	var backend atomic.Pointer[server.StreamableHTTPServer]
	backend.Store(newBackend())
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backend.Load().ServeHTTP(w, r)
	}))
	defer ts.Close()

	c := NewStreamableHTTPClientWithHeaders(ts.URL, nil)
	require.NoError(t, c.Initialize(t.Context()))
	defer func() { _ = c.Close() }()
	require.True(t, c.hadSession, "a stateful server issues a session id at initialize")

	res, err := c.CallTool(t.Context(), "echo", nil)
	require.NoError(t, err)
	require.Equal(t, "alive", res.Content[0].(mcp.TextContent).Text)

	// "Redeploy": the new process has an empty session table.
	backend.Store(newBackend())

	res, err = c.CallTool(t.Context(), "echo", nil)
	require.NoError(t, err, "the call after the redeploy re-initializes and succeeds")
	require.Equal(t, "alive", res.Content[0].(mcp.TextContent).Text)
	assert.Equal(t, uint64(1), c.sessionGeneration)
	assert.Equal(t, int32(2), runs.Load(), "each of the two calls ran exactly once")

	// Once recovered, the session is a normal one again.
	require.NoError(t, c.Ping(t.Context()))
	assert.Equal(t, uint64(1), c.sessionGeneration, "no further handshake while the session is valid")
}
