package mcpserver

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

const testFormURL = "https://portal.example.com/agents/credentials/new"

const testInputKey = "model_key"

// declaredCapabilities returns what the calling client declared: on the
// request from 2026-07-28, at the session's handshake before.
func declaredCapabilities(ctx context.Context) mcp.ClientCapabilities {
	if info := server.RequestProtocolInfoFromContext(ctx); info != nil && info.Modern && info.ClientCapabilities != nil {
		return *info.ClientCapabilities
	}
	if session, ok := server.ClientSessionFromContext(ctx).(server.SessionWithClientInfo); ok {
		return session.GetClientCapabilities()
	}
	return mcp.ClientCapabilities{}
}

func declaresURLElicitation(capabilities mcp.ClientCapabilities) bool {
	return capabilities.Elicitation != nil && capabilities.Elicitation.URL != nil
}

// elicitingBackend serves a tool "create" that, like a backend following the
// protocol, asks the person to open a page with an input_required result only
// of a client that declared URL mode elicitation, and answers any other with
// text. It records the capabilities each call declared.
type elicitingBackend struct {
	url      string
	declared atomic.Pointer[mcp.ClientCapabilities]
	srv      *server.MCPServer
	ts       *httptest.Server
}

func newElicitingBackend(t *testing.T, versions ...string) *elicitingBackend {
	t.Helper()
	b := &elicitingBackend{srv: server.NewMCPServer("eliciting-backend", "test", server.WithToolCapabilities(true))}
	b.srv.AddTool(mcp.NewTool("create"), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		declared := declaredCapabilities(ctx)
		b.declared.Store(&declared)
		if answer := server.ElicitationResponse(req.Params.InputResponses, testInputKey); answer != nil {
			return mcp.NewToolResultText("created after " + string(answer.Action) + " with state " + req.Params.RequestState), nil
		}
		if !declaresURLElicitation(declared) {
			return mcp.NewToolResultText("A model key is missing: add one in the Dev Portal."), nil
		}
		return server.NewInputRequestBuilder("backend-state").Elicit(testInputKey, mcp.ElicitationParams{
			Mode: mcp.ElicitationModeURL, Message: "A model key is missing.", URL: testFormURL,
		}).ToolResult(), nil
	})
	ts := httptest.NewServer(server.NewStreamableHTTPServer(b.srv, server.WithStreamableHTTPProtocolVersions(versions...)))
	t.Cleanup(ts.Close)
	b.url, b.ts = ts.URL, ts
	return b
}

func urlCapabilities() mcp.ClientCapabilities {
	return mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapability{URL: &struct{}{}}}
}

func connect(t *testing.T, ctx context.Context, url string) *StreamableHTTPClient {
	t.Helper()
	c := NewStreamableHTTPClientWithHeaders(url, nil)
	t.Cleanup(func() { _ = c.Close() })
	require.NoError(t, c.Initialize(ctx))
	return c
}

func TestBackendProtocol_ModernBackend(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	backend := newElicitingBackend(t)
	c := connect(t, ctx, backend.url)
	require.Equal(t, mcp.ProtocolVersion20260728, c.NegotiatedProtocolVersion())

	t.Run("the call declares the caller's capabilities and the input request comes back unchanged", func(t *testing.T) {
		result, err := c.CallTool(WithCallerCapabilities(ctx, urlCapabilities()), "create", nil)

		require.NoError(t, err)
		require.True(t, result.NeedsInput())
		require.Equal(t, "backend-state", result.RequestState)
		require.Equal(t, testFormURL, result.InputRequests[testInputKey].Elicitation.URL)
		require.True(t, declaresURLElicitation(*backend.declared.Load()))
	})

	t.Run("a caller without URL elicitation is not declared to have it", func(t *testing.T) {
		formOnly := mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapability{Form: &struct{}{}}}

		result, err := c.CallTool(WithCallerCapabilities(ctx, formOnly), "create", nil)

		require.NoError(t, err)
		require.False(t, result.NeedsInput())
		declared := backend.declared.Load()
		require.False(t, declaresURLElicitation(*declared))
		require.NotNil(t, declared.Elicitation.Form, "what the caller does support is forwarded")
	})

	t.Run("sampling and roots are not declared", func(t *testing.T) {
		everything := urlCapabilities()
		everything.Sampling = &mcp.SamplingCapability{}
		everything.Roots = &struct {
			ListChanged bool `json:"listChanged,omitempty"`
		}{ListChanged: true}

		_, err := c.CallTool(WithCallerCapabilities(ctx, everything), "create", nil)

		require.NoError(t, err)
		require.Equal(t, mcp.ClientCapabilities{Elicitation: urlCapabilities().Elicitation}, *backend.declared.Load())
	})

	t.Run("a call without a caller declares nothing", func(t *testing.T) {
		result, err := c.CallTool(ctx, "create", nil)

		require.NoError(t, err)
		require.False(t, result.NeedsInput())
		require.Equal(t, mcp.ClientCapabilities{}, *backend.declared.Load())
	})

	t.Run("a retry carries the inputResponses and the requestState", func(t *testing.T) {
		retry := WithRoundTrip(WithCallerCapabilities(ctx, urlCapabilities()), mcp.MultiRoundTripParams{
			InputResponses: mcp.InputResponses{testInputKey: mcp.NewElicitationInputResponse(mcp.ElicitationResult{
				ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionAccept},
			})},
			RequestState: "backend-state",
		})

		result, err := c.CallTool(retry, "create", nil)

		require.NoError(t, err)
		require.Equal(t, "created after accept with state backend-state", result.Content[0].(mcp.TextContent).Text)
	})
}

func TestBackendProtocol_LegacyBackend(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	backend := newElicitingBackend(t, mcp.LATEST_LEGACY_PROTOCOL_VERSION)
	c := connect(t, ctx, backend.url)

	require.Equal(t, mcp.LATEST_LEGACY_PROTOCOL_VERSION, c.NegotiatedProtocolVersion(), "a backend that lists only 2025-11-25 is spoken to on it")

	result, err := c.CallTool(WithCallerCapabilities(ctx, urlCapabilities()), "create", nil)

	require.NoError(t, err)
	require.Equal(t, "A model key is missing: add one in the Dev Portal.", result.Content[0].(mcp.TextContent).Text)
	require.Equal(t, mcp.ClientCapabilities{}, *backend.declared.Load(), "its handshake declared nothing, whoever calls")
}

// TestBackendProtocol_ListChanged pins that a backend's tools/list_changed
// reaches the notification handler in both eras: on a subscriptions/listen
// stream from 2026-07-28, on the standalone GET stream before.
func TestBackendProtocol_ListChanged(t *testing.T) {
	for name, versions := range map[string][]string{
		"2026-07-28": nil,
		"2025-11-25": {mcp.LATEST_LEGACY_PROTOCOL_VERSION},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			backend := newElicitingBackend(t, versions...)
			c := NewStreamableHTTPClientWithHeaders(backend.url, nil)
			t.Cleanup(func() { _ = c.Close() })
			changed := make(chan struct{}, 8)
			listening := make(chan struct{}, 1)
			c.OnNotification(func(n mcp.JSONRPCNotification) {
				switch n.Method {
				case string(mcp.MethodNotificationToolsListChanged):
					changed <- struct{}{}
				case mcp.MethodNotificationSubscriptionsAcknowledged:
					listening <- struct{}{}
				}
			})
			require.NoError(t, c.Initialize(ctx))
			if versions == nil {
				<-listening
			}

			require.Eventually(t, func() bool {
				backend.srv.AddTool(mcp.NewTool("another"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
					return mcp.NewToolResultText(""), nil
				})
				select {
				case <-changed:
					return true
				default:
					return false
				}
			}, 5*time.Second, 50*time.Millisecond)
		})
	}
}

// A 2026-07-28 backend whose list-changes stream drops -- another process
// took over behind the address -- tells nobody what it changed meanwhile: the
// reopened stream's acknowledgement tells the OnSessionRecovered handler, so
// the caller re-reads what the backend serves.
func TestBackendProtocol_ReopenedListChangesStreamResyncs(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	backend := newElicitingBackend(t)
	c := NewStreamableHTTPClientWithHeaders(backend.url, nil)
	t.Cleanup(func() { _ = c.Close() })
	listening := make(chan struct{}, 4)
	c.OnNotification(func(n mcp.JSONRPCNotification) {
		if n.Method == mcp.MethodNotificationSubscriptionsAcknowledged {
			listening <- struct{}{}
		}
	})
	recovered := make(chan struct{}, 1)
	c.OnSessionRecovered(func() { recovered <- struct{}{} })
	require.NoError(t, c.Initialize(ctx))
	<-listening
	require.Empty(t, recovered, "the first stream is no resync")

	backend.ts.CloseClientConnections()

	select {
	case <-recovered:
	case <-ctx.Done():
		t.Fatal("the reopened stream did not resync the client")
	}
}
