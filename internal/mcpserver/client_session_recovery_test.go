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

// The standalone GET listener meets the 404 first: mcp-go clears the session
// id and the tool call then fails with whatever the server says about a
// request that carries no session (the Python SDK: 400 "Missing session ID").
func TestSessionRecovery_DetectsSessionClearedByListener(t *testing.T) {
	old := &sessionFakeClient{sessionID: "s1", callErrs: []error{errors.New("request failed with status 400: Missing session ID")}}
	h := newRecoveryHarness(old, &sessionFakeClient{sessionID: "s2"})
	old.mu.Lock()
	old.sessionID = "" // cleared by the listener's 404
	old.mu.Unlock()

	_, err := h.base.callTool(t.Context(), "echo", nil)

	require.NoError(t, err)
	assert.Equal(t, int32(1), h.reconnects.Load())
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

func TestSessionRecovery_ConcurrentCallersShareOneFailedHandshake(t *testing.T) {
	const callers = 16
	failing := make([]error, callers)
	refused := make([]error, callers)
	for i := range failing {
		failing[i] = errSessionGone
		refused[i] = errors.New("connection refused")
	}
	old := &sessionFakeClient{sessionID: "s1", callErrs: failing}
	// Every caller fails on the same generation: a caller that arrived after
	// the failed handshake would be the next operation, which does try again.
	var arrived sync.WaitGroup
	arrived.Add(callers)
	old.barrier = func() { arrived.Done(); arrived.Wait() }
	h := newRecoveryHarness(old, &sessionFakeClient{sessionID: "s2"})
	h.reconnectErrs = refused

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
// restarted (issue #999).
func TestStreamableHTTPClient_RecoversSessionAfterBackendRedeploy(t *testing.T) {
	newBackend := func() *server.StreamableHTTPServer {
		s := server.NewMCPServer("redeploy-test", "1.0.0")
		s.AddTool(mcp.NewTool("echo"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

	// Once recovered, the session is a normal one again.
	require.NoError(t, c.Ping(t.Context()))
	assert.Equal(t, uint64(1), c.sessionGeneration, "no further handshake while the session is valid")
}
