package mcpserver

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// progressRecorder collects what a ProgressReporter sends to the caller.
type progressRecorder struct {
	mu   sync.Mutex
	sent []map[string]any
}

func (r *progressRecorder) send(params map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, params)
	return nil
}

func (r *progressRecorder) progress() []float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	var values []float64
	for _, params := range r.sent {
		values = append(values, params["progress"].(float64))
	}
	return values
}

// progressBackend is a streamable-http MCP server whose tool "work" reports
// progress 1..steps (total = steps) under the request's progressToken and
// records the tokens it was sent. It reports and returns at once, so every
// test also proves the transport delivers the last in-flight notification.
type progressBackend struct {
	url string

	mu   sync.Mutex
	seen []any
}

func newProgressBackend(t *testing.T) *progressBackend {
	t.Helper()
	b := &progressBackend{}

	srv := server.NewMCPServer("progress-backend", "test")
	srv.AddTool(mcp.NewTool("work", mcp.WithNumber("steps")), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var token mcp.ProgressToken
		if req.Params.Meta != nil {
			token = req.Params.Meta.ProgressToken
		}
		b.mu.Lock()
		b.seen = append(b.seen, token)
		b.mu.Unlock()

		steps := int(req.GetFloat("steps", 3))
		for i := 1; token != nil && i <= steps; i++ {
			err := server.ServerFromContext(ctx).SendNotificationToClient(ctx, string(mcp.MethodNotificationProgress), map[string]any{
				"progressToken": token,
				"progress":      float64(i),
				"total":         float64(steps),
				"message":       "step",
			})
			if err != nil {
				return nil, err
			}
		}
		return mcp.NewToolResultText("done"), nil
	})

	ts := httptest.NewServer(server.NewStreamableHTTPServer(srv))
	t.Cleanup(ts.Close)
	b.url = ts.URL
	return b
}

func (b *progressBackend) tokens() []any {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]any(nil), b.seen...)
}

func connectedClient(t *testing.T, url string) *StreamableHTTPClient {
	t.Helper()
	c := NewStreamableHTTPClientWithHeaders(url, nil)
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, c.Initialize(ctx))
	return c
}

// TestCallToolRelaysDownstreamProgressUnderTheCallersToken proves a call
// whose context carries a reporter sends the backend a token of muster's own
// and relays the backend's progress under the caller's token.
func TestCallToolRelaysDownstreamProgressUnderTheCallersToken(t *testing.T) {
	backend := newProgressBackend(t)
	c := connectedClient(t, backend.url)

	rec := &progressRecorder{}
	ctx := WithProgressReporter(t.Context(), NewProgressReporter("caller-token", rec.send))
	_, err := c.CallTool(ctx, "work", map[string]any{"steps": 3})
	require.NoError(t, err)

	tokens := backend.tokens()
	require.Len(t, tokens, 1)
	assert.NotNil(t, tokens[0], "the backend got no progressToken")
	assert.NotEqual(t, "caller-token", tokens[0], "the caller's token reached the backend untranslated")

	require.Equal(t, []float64{1, 2, 3}, rec.progress())
	for _, params := range rec.sent {
		assert.Equal(t, "caller-token", params["progressToken"])
		assert.Equal(t, float64(3), params["total"])
		assert.Equal(t, "step", params["message"])
	}
}

// TestCallToolWithoutReporterSendsNoProgressToken proves a caller that asked
// for no progress gets the plain request it got before.
func TestCallToolWithoutReporterSendsNoProgressToken(t *testing.T) {
	backend := newProgressBackend(t)
	c := connectedClient(t, backend.url)

	_, err := c.CallTool(t.Context(), "work", map[string]any{"steps": 3})
	require.NoError(t, err)
	assert.Equal(t, []any{nil}, backend.tokens())
}

// TestPooledClientRoutesProgressToEachCaller proves one downstream
// connection serving several callers at once hands each its own progress.
func TestPooledClientRoutesProgressToEachCaller(t *testing.T) {
	backend := newProgressBackend(t)
	c := connectedClient(t, backend.url)

	steps := map[string]int{"a": 2, "b": 5, "c": 3}
	recorders := map[string]*progressRecorder{}
	var wg sync.WaitGroup
	for caller, n := range steps {
		rec := &progressRecorder{}
		recorders[caller] = rec
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := WithProgressReporter(t.Context(), NewProgressReporter(caller, rec.send))
			_, err := c.CallTool(ctx, "work", map[string]any{"steps": n})
			assert.NoError(t, err)
		}()
	}
	wg.Wait()

	for caller, n := range steps {
		rec := recorders[caller]
		require.Len(t, rec.sent, n, "caller %s", caller)
		for i, params := range rec.sent {
			assert.Equal(t, caller, params["progressToken"])
			assert.Equal(t, float64(i+1), params["progress"])
			assert.Equal(t, float64(n), params["total"])
		}
	}
}

func progressNotification(token any, progress float64) mcp.JSONRPCNotification {
	return mcp.JSONRPCNotification{
		JSONRPC: mcp.JSONRPC_VERSION,
		Notification: mcp.Notification{
			Method: string(mcp.MethodNotificationProgress),
			Params: mcp.NotificationParams{AdditionalFields: map[string]any{
				"progressToken": token,
				"progress":      progress,
			}},
		},
	}
}

// TestProgressRouterDropsUnknownTokens proves progress for a call no longer
// in flight, or under a token muster never sent, reaches no caller.
func TestProgressRouterDropsUnknownTokens(t *testing.T) {
	var router progressRouter
	rec := &progressRecorder{}
	token, done := router.open(NewProgressReporter("caller", rec.send))

	router.route(progressNotification(token, 1))
	router.route(progressNotification("made-up", 2))
	router.route(progressNotification(42.0, 3))
	done()
	router.route(progressNotification(token, 4))

	assert.Equal(t, []float64{1}, rec.progress())
}

// TestProgressReporterKeepsProgressIncreasing proves a caller whose call
// reaches several downstream tools never sees its progress go back.
func TestProgressReporterKeepsProgressIncreasing(t *testing.T) {
	rec := &progressRecorder{}
	reporter := NewProgressReporter("caller", rec.send)
	for _, progress := range []float64{1, 2, 1, 2, 3} {
		reporter.report(t.Context(), progress, nil, "")
	}
	assert.Equal(t, []float64{1, 2, 3}, rec.progress())
	assert.NotContains(t, rec.sent[0], "total")
	assert.NotContains(t, rec.sent[0], "message")
}

// TestNonProgressNotificationsReachTheHandler proves the capability
// notifications the aggregator listens for still arrive.
func TestNonProgressNotificationsReachTheHandler(t *testing.T) {
	var b baseMCPClient
	var got []string
	b.onNotification(func(n mcp.JSONRPCNotification) { got = append(got, n.Method) })

	b.dispatchNotification(mcp.JSONRPCNotification{Notification: mcp.Notification{Method: "notifications/tools/list_changed"}})
	b.dispatchNotification(progressNotification("unknown", 1))

	assert.Equal(t, []string{"notifications/tools/list_changed"}, got)
}
