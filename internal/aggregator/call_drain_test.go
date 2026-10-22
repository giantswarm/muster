package aggregator

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/mcpserver"
)

// drainFixture is a caller reaching a progress-reporting backend tool
// "x_backend_wait" through a muster server built with the production option
// chain and the in-flight call tracker. The backend tool reports progress,
// signals started, and answers "green" once release closes, or "timeout"
// (its cancellation verdict) when its call is cancelled first.
type drainFixture struct {
	calls   *inFlightCalls
	caller  *client.Client
	started chan struct{}
	release chan struct{}
}

func newDrainFixture(t *testing.T) *drainFixture {
	t.Helper()
	f := &drainFixture{
		calls:   newInFlightCalls(),
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}

	backend := server.NewMCPServer("backend", "test")
	backend.AddTool(mcp.NewTool("wait"), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		err := server.ServerFromContext(ctx).SendNotificationToClient(ctx, string(mcp.MethodNotificationProgress), map[string]any{
			"progressToken": req.Params.Meta.ProgressToken,
			"progress":      float64(1),
		})
		if err != nil {
			return nil, err
		}
		f.started <- struct{}{}
		select {
		case <-f.release:
			return mcp.NewToolResultText("green"), nil
		case <-ctx.Done():
			return mcp.NewToolResultText("timeout"), nil
		}
	})
	backendTS := httptest.NewServer(server.NewStreamableHTTPServer(backend))
	t.Cleanup(backendTS.Close)

	downstream := mcpserver.NewStreamableHTTPClientWithHeaders(backendTS.URL, nil)
	t.Cleanup(func() { _ = downstream.Close() })
	require.NoError(t, downstream.Initialize(t.Context()))

	opts := append(mcpServerOptions(), server.WithToolHandlerMiddleware(f.calls.middleware()))
	muster := server.NewMCPServer("muster-aggregator-test", "test", opts...)
	muster.AddTool(mcp.NewTool("x_backend_wait"), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return downstream.CallTool(ctx, "wait", nil)
	})
	musterTS := httptest.NewServer(server.NewStreamableHTTPServer(muster))
	t.Cleanup(musterTS.Close)

	caller, err := client.NewStreamableHttpClient(musterTS.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = caller.Close() })
	require.NoError(t, caller.Start(t.Context()))
	_, err = caller.Initialize(t.Context(), mcp.InitializeRequest{})
	require.NoError(t, err)
	f.caller = caller
	return f
}

// call starts a call of x_backend_wait and returns its answer's channel.
func (f *drainFixture) call(t *testing.T) <-chan *mcp.CallToolResult {
	t.Helper()
	answer := make(chan *mcp.CallToolResult, 1)
	go func() {
		result, err := f.caller.CallTool(t.Context(), mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "x_backend_wait"}})
		assert.NoError(t, err)
		answer <- result
	}()
	return answer
}

func (f *drainFixture) stopping() bool {
	f.calls.mu.Lock()
	defer f.calls.mu.Unlock()
	return f.calls.stopping
}

func textOf(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
	return result.Content[0].(mcp.TextContent).Text
}

func assertInterrupted(t *testing.T, result *mcp.CallToolResult) {
	t.Helper()
	assert.True(t, result.IsError)
	assert.Equal(t, interruptedText, textOf(t, result))
	require.NotNil(t, result.Meta)
	assert.Equal(t, map[string]any{"reason": errReplicaStopping.Error(), "retryable": true},
		result.Meta.AdditionalFields[interruptedMetaKey])
}

// TestDrainInterruptsAProgressKeptCallPastTheGrace proves a progress-kept
// call still in flight when the grace period ends answers interrupted, not
// with the backend's cancellation verdict, and that drain returns once it
// answered.
func TestDrainInterruptsAProgressKeptCallPastTheGrace(t *testing.T) {
	f := newDrainFixture(t)
	answer := f.call(t)
	<-f.started

	f.calls.drain(t.Context(), 0)

	assertInterrupted(t, <-answer)
}

// TestDrainLetsACallFinishWithinTheGrace proves a call that answers within
// the grace period keeps its answer.
func TestDrainLetsACallFinishWithinTheGrace(t *testing.T) {
	f := newDrainFixture(t)
	answer := f.call(t)
	<-f.started

	drained := make(chan struct{})
	go func() {
		f.calls.drain(t.Context(), time.Hour)
		close(drained)
	}()
	require.Eventually(t, f.stopping, 5*time.Second, time.Millisecond)
	close(f.release)

	result := <-answer
	assert.False(t, result.IsError)
	assert.Equal(t, "green", textOf(t, result))
	<-drained
}

// TestDrainRefusesANewCall proves a call arriving once the drain started is
// answered interrupted at once, without reaching the backend.
func TestDrainRefusesANewCall(t *testing.T) {
	f := newDrainFixture(t)
	f.calls.drain(t.Context(), time.Hour)

	assertInterrupted(t, <-f.call(t))
	assert.Empty(t, f.started)
}
