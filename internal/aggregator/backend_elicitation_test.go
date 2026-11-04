package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

const (
	testDevPortalURL      = "https://portal.example.com/"
	testCredentialFormURL = "https://portal.example.com/agents/credentials/new?model=claude"
	testForeignURL        = "https://attacker.example.com/agents/credentials/new"
	testElicitingServer   = "agents"
	testElicitingTool     = "create_agent"
	testElicitationID     = "backend-elicitation-1"
	testElicitationText   = "This agent needs a model key."
)

// elicitingClient is a backend that answers its first calls with a URL
// elicitation, the way mcp-go's client hands over a -32042 answer, and every
// call after them with a text result.
type elicitingClient struct {
	mockMCPClient
	url         string
	elicitTimes int
	calls       int
}

func (c *elicitingClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	if _, err := c.mockMCPClient.CallTool(ctx, name, args); err != nil {
		return nil, err
	}
	c.calls++
	if c.calls <= c.elicitTimes {
		return nil, fmt.Errorf("failed to call tool: %w", mcp.URLElicitationRequiredError{
			Elicitations: []mcp.ElicitationParams{{
				Mode:          mcp.ElicitationModeURL,
				Message:       testElicitationText,
				URL:           c.url,
				ElicitationID: testElicitationID,
			}},
		})
	}
	return mcp.NewToolResultText("agent created"), nil
}

// newElicitingBackend registers a backend that elicits url once, with
// spec.urlElicitation set to urlElicitation in its definition, and returns
// the aggregator and a handler that calls the backend's tool the way call_tool
// does.
func newElicitingBackend(t *testing.T, url, urlElicitation string) (*AggregatorServer, *elicitingClient, mcpserver.ToolHandlerFunc) {
	t.Helper()
	a, err := NewAggregatorServer(t.Context(), AggregatorConfig{Host: "localhost", Port: 0, DevPortalURL: testDevPortalURL}, nil)
	require.NoError(t, err)
	backend := &elicitingClient{mockMCPClient: mockMCPClient{tools: []mcp.Tool{{Name: testElicitingTool}}}, url: url, elicitTimes: 1}
	require.NoError(t, a.RegisterServer(t.Context(), ServerRegistration{Name: testElicitingServer}, backend))

	api.RegisterMCPServerManager(&stubMCPServerManager{servers: map[string]api.MCPServerInfo{
		testElicitingServer: {Name: testElicitingServer, Type: "streamable-http", URL: "http://agents:8080/mcp", URLElicitation: urlElicitation},
	}})
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })

	exposed := a.registry.ExposedToolName(testElicitingServer, testElicitingTool)
	call := func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return a.CallToolInternal(ctx, exposed, map[string]any{})
	}
	return a, backend, call
}

func legacyContext(t *testing.T, capabilities mcp.ClientCapabilities) context.Context {
	return mcpserver.NewMCPServer("muster", "test").WithContext(t.Context(), newLegacySession("transport-1", capabilities))
}

func TestBackendElicitation_Relayed(t *testing.T) {
	t.Run("a 2026-07-28 request is answered with an input_required URL elicitation", func(t *testing.T) {
		a, _, call := newElicitingBackend(t, testCredentialFormURL, api.MCPServerURLElicitationRelay)

		result, err := a.urlElicitation()(call)(modernContext(t.Context(), urlElicitationCapabilities()), mcp.CallToolRequest{})

		require.NoError(t, err)
		require.Equal(t, mcp.ResultTypeInputRequired, result.ResultType)
		request, ok := result.InputRequests[relayedElicitationKey(0)]
		require.True(t, ok, "inputRequests: %v", result.InputRequests)
		require.NotNil(t, request.Elicitation)
		require.Equal(t, mcp.ElicitationModeURL, request.Elicitation.Mode)
		require.Equal(t, testCredentialFormURL, request.Elicitation.URL)
		require.Equal(t, testElicitationText, request.Elicitation.Message)
		require.Equal(t, testElicitationID, request.Elicitation.ElicitationID)
		require.NoError(t, request.Elicitation.Validate())
		require.Empty(t, result.Content, "the link must not reach the model as tool output")
		require.NotContains(t, result.InputRequests, signInInputKey, "a relayed elicitation is not a sign-in")
	})

	t.Run("a 2025-11-25 session is answered with URLElicitationRequiredError", func(t *testing.T) {
		a, _, call := newElicitingBackend(t, testCredentialFormURL, api.MCPServerURLElicitationRelay)

		result, err := a.urlElicitation()(call)(legacyContext(t, urlElicitationCapabilities()), mcp.CallToolRequest{})

		require.Nil(t, result)
		var required mcp.URLElicitationRequiredError
		require.True(t, errors.As(err, &required), "err: %v", err)
		require.Len(t, required.Elicitations, 1)
		require.Equal(t, mcp.ElicitationModeURL, required.Elicitations[0].Mode)
		require.Equal(t, testCredentialFormURL, required.Elicitations[0].URL)
		require.Equal(t, testElicitationID, required.Elicitations[0].ElicitationID)
		require.NoError(t, required.Elicitations[0].Validate())
	})

	t.Run("a client without elicitation.url gets the link as text", func(t *testing.T) {
		a, _, call := newElicitingBackend(t, testCredentialFormURL, api.MCPServerURLElicitationRelay)
		formOnly := mcp.ClientCapabilities{Elicitation: &mcp.ElicitationCapability{Form: &struct{}{}}}

		result, err := a.urlElicitation()(call)(legacyContext(t, formOnly), mcp.CallToolRequest{})

		require.NoError(t, err)
		require.True(t, result.IsError)
		require.Contains(t, mcpText(result), testCredentialFormURL)
		require.Contains(t, mcpText(result), testElicitationText)
	})

	t.Run("a 2026-07-28 retry that accepted runs the call again", func(t *testing.T) {
		a, backend, call := newElicitingBackend(t, testCredentialFormURL, api.MCPServerURLElicitationRelay)
		ctx := modernContext(t.Context(), urlElicitationCapabilities())
		first, err := a.urlElicitation()(call)(ctx, mcp.CallToolRequest{})
		require.NoError(t, err)

		var retry mcp.CallToolRequest
		retry.Params.InputResponses = mcp.InputResponses{
			relayedElicitationKey(0): mcp.NewElicitationInputResponse(mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionAccept}}),
		}
		retry.Params.RequestState = first.RequestState
		result, err := a.urlElicitation()(call)(ctx, retry)

		require.NoError(t, err)
		require.Equal(t, "agent created", mcpText(result))
		require.Equal(t, 2, backend.calls)
	})

	t.Run("a 2026-07-28 retry that declined or cancelled runs nothing", func(t *testing.T) {
		for _, action := range []mcp.ElicitationResponseAction{mcp.ElicitationResponseActionDecline, mcp.ElicitationResponseActionCancel} {
			t.Run(string(action), func(t *testing.T) {
				a, backend, call := newElicitingBackend(t, testCredentialFormURL, api.MCPServerURLElicitationRelay)
				var retry mcp.CallToolRequest
				retry.Params.InputResponses = mcp.InputResponses{
					relayedElicitationKey(0): mcp.NewElicitationInputResponse(mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{Action: action}}),
				}
				retry.Params.RequestState = relayedRequestState{Relayed: testElicitingServer}.encode()

				result, err := a.urlElicitation()(call)(modernContext(t.Context(), urlElicitationCapabilities()), retry)

				require.NoError(t, err)
				require.True(t, result.IsError)
				require.Contains(t, mcpText(result), testElicitingServer)
				require.Zero(t, backend.calls, "a declined elicitation runs nothing")
			})
		}
	})
}

// droppedLine returns the parsed log line of a dropped backend elicitation.
func droppedLine(t *testing.T, logged string) map[string]any {
	t.Helper()
	for _, raw := range strings.Split(strings.TrimRight(logged, "\n"), "\n") {
		if strings.Contains(raw, "Dropped a backend's URL elicitation") {
			return parseLogLine(t, raw)
		}
	}
	require.Failf(t, "no drop logged", "log:\n%s", logged)
	return nil
}

func TestBackendElicitation_Dropped(t *testing.T) {
	// SECURITY: no backend may send people to a page of its choosing. A
	// backend whose MCPServer does not opt in, and a page off the Dev
	// Portal's origin, are dropped: the client gets a tool error without the
	// URL, in every protocol shape.
	cases := map[string]struct {
		url            string
		urlElicitation string
		wantOrigin     string
	}{
		"a backend that is not allowlisted":           {url: testCredentialFormURL, urlElicitation: "", wantOrigin: "https://portal.example.com"},
		"a backend that set Drop":                     {url: testCredentialFormURL, urlElicitation: "Drop", wantOrigin: "https://portal.example.com"},
		"an allowlisted backend to another origin":    {url: testForeignURL, urlElicitation: api.MCPServerURLElicitationRelay, wantOrigin: "https://attacker.example.com"},
		"an allowlisted backend to a look-alike host": {url: "https://portal.example.com.attacker.example.com/", urlElicitation: api.MCPServerURLElicitationRelay, wantOrigin: "https://portal.example.com.attacker.example.com"},
		"an allowlisted backend to plain http":        {url: "http://portal.example.com/agents/credentials/new", urlElicitation: api.MCPServerURLElicitationRelay, wantOrigin: "http://portal.example.com"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			for shape, ctxOf := range map[string]func(*testing.T) context.Context{
				"2026-07-28": func(t *testing.T) context.Context {
					return modernContext(t.Context(), urlElicitationCapabilities())
				},
				"2025-11-25": func(t *testing.T) context.Context { return legacyContext(t, urlElicitationCapabilities()) },
			} {
				t.Run(shape, func(t *testing.T) {
					buf := captureLog(t)
					a, _, call := newElicitingBackend(t, tc.url, tc.urlElicitation)

					result, err := a.urlElicitation()(call)(ctxOf(t), mcp.CallToolRequest{})

					require.NoError(t, err, "a dropped elicitation is never -32042")
					require.NotNil(t, result)
					require.NotEqual(t, mcp.ResultTypeInputRequired, result.ResultType)
					require.Empty(t, result.InputRequests)
					require.True(t, result.IsError)
					require.Contains(t, mcpText(result), testElicitingServer)
					raw, marshalErr := json.Marshal(result)
					require.NoError(t, marshalErr)
					require.NotContains(t, string(raw), tc.url, "the URL must not reach the client")

					line := droppedLine(t, buf.String())
					require.Equal(t, testElicitingServer, line["server"])
					require.Equal(t, tc.wantOrigin, line["origin"])
					require.NotEmpty(t, line["reason"])
					require.NotContains(t, buf.String(), "credentials/new", "only the origin is logged")
				})
			}
		})
	}
}

func TestBackendElicitation_NoDevPortalRelaysNothing(t *testing.T) {
	buf := captureLog(t)
	a, _, call := newElicitingBackend(t, testCredentialFormURL, api.MCPServerURLElicitationRelay)
	a.devPortalOrigin = ""

	result, err := a.urlElicitation()(call)(modernContext(t.Context(), urlElicitationCapabilities()), mcp.CallToolRequest{})

	require.NoError(t, err)
	require.Empty(t, result.InputRequests)
	require.NotContains(t, mcpText(result), testCredentialFormURL)
	require.Equal(t, testElicitingServer, droppedLine(t, buf.String())["server"])
}

func TestBackendElicitation_OtherErrorsPassThrough(t *testing.T) {
	a := &AggregatorServer{devPortalOrigin: "https://portal.example.com"}
	backendErr := errors.New("backend refused")

	result, err := a.answerBackendElicitation(t.Context(), testElicitingServer, nil, backendErr)

	require.Nil(t, result)
	require.Same(t, backendErr, err)
}

func TestBackendElicitation_OnTheWire(t *testing.T) {
	a, _, call := newElicitingBackend(t, testCredentialFormURL, api.MCPServerURLElicitationRelay)
	srv := mcpserver.NewMCPServer("muster", "test", mcpserver.WithToolHandlerMiddleware(a.urlElicitation()))
	srv.AddTool(mcp.NewTool("call_tool"), call)

	t.Run("2025-11-25", func(t *testing.T) {
		ctx := srv.WithContext(t.Context(), newLegacySession("transport-1", urlElicitationCapabilities()))

		reply := srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"call_tool"}}`))

		raw, err := json.Marshal(reply)
		require.NoError(t, err)
		var decoded struct {
			Error struct {
				Code int `json:"code"`
				Data struct {
					Elicitations []mcp.ElicitationParams `json:"elicitations"`
				} `json:"data"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal(raw, &decoded), string(raw))
		require.Equal(t, mcp.URL_ELICITATION_REQUIRED, decoded.Error.Code, string(raw))
		require.Len(t, decoded.Error.Data.Elicitations, 1)
		require.Equal(t, testCredentialFormURL, decoded.Error.Data.Elicitations[0].URL)
	})
}

// declaringClient records whether URL mode elicitation was declared on it.
type declaringClient struct {
	mockMCPClient
	declared bool
}

func (c *declaringClient) DeclareURLElicitation() { c.declared = true }

func TestDeclareURLElicitation(t *testing.T) {
	for value, want := range map[string]bool{api.MCPServerURLElicitationRelay: true, "Drop": false, "": false} {
		t.Run(fmt.Sprintf("spec.urlElicitation=%q", value), func(t *testing.T) {
			api.RegisterMCPServerManager(&stubMCPServerManager{servers: map[string]api.MCPServerInfo{
				testElicitingServer: {Name: testElicitingServer, URLElicitation: value},
			}})
			t.Cleanup(func() { api.RegisterMCPServerManager(nil) })
			client := &declaringClient{}

			declareURLElicitation(t.Context(), testElicitingServer, client)

			require.Equal(t, want, client.declared)
		})
	}

	t.Run("an unknown server is not declared to", func(t *testing.T) {
		api.RegisterMCPServerManager(&stubMCPServerManager{})
		t.Cleanup(func() { api.RegisterMCPServerManager(nil) })
		client := &declaringClient{}

		declareURLElicitation(t.Context(), testElicitingServer, client)

		require.False(t, client.declared)
	})
}
