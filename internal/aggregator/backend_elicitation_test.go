package aggregator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

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
	completed   func(elicitationID string)
}

// OnElicitationComplete keeps the handler muster registers for the
// backend's notifications/elicitation/complete.
func (c *elicitingClient) OnElicitationComplete(handler func(string)) { c.completed = handler }

// complete sends the backend's notifications/elicitation/complete, as the
// client hands it over.
func (c *elicitingClient) complete(elicitationID string) {
	if c.completed != nil {
		c.completed(elicitationID)
	}
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

	t.Run("a 2026-07-28 retry that accepted waits for the backend's completion, then runs the call again", func(t *testing.T) {
		a, backend, call := newElicitingBackend(t, testCredentialFormURL, api.MCPServerURLElicitationRelay)
		synctest.Test(t, func(t *testing.T) {
			ctx := modernContext(t.Context(), urlElicitationCapabilities())
			first, err := a.urlElicitation()(call)(ctx, mcp.CallToolRequest{})
			require.NoError(t, err)
			go func() {
				time.Sleep(30 * time.Second)
				backend.complete(testElicitationID)
			}()
			start := time.Now()

			result, err := a.urlElicitation()(call)(ctx, acceptedRelayedRetry(first.RequestState))

			require.NoError(t, err)
			require.Equal(t, "agent created", mcpText(result))
			require.Equal(t, 2, backend.calls, "the call runs once more, after the wait: the person sees one prompt")
			require.Equal(t, 30*time.Second, time.Since(start), "the retry runs as soon as the backend completes the page")
		})
	})

	t.Run("a completion that arrived before the retry runs it at once", func(t *testing.T) {
		a, backend, call := newElicitingBackend(t, testCredentialFormURL, api.MCPServerURLElicitationRelay)
		synctest.Test(t, func(t *testing.T) {
			ctx := modernContext(t.Context(), urlElicitationCapabilities())
			first, err := a.urlElicitation()(call)(ctx, mcp.CallToolRequest{})
			require.NoError(t, err)
			backend.complete(testElicitationID)
			start := time.Now()

			result, err := a.urlElicitation()(call)(ctx, acceptedRelayedRetry(first.RequestState))

			require.NoError(t, err)
			require.Equal(t, "agent created", mcpText(result))
			require.Zero(t, time.Since(start))
		})
	})

	t.Run("a completion that never arrives ends the wait at the bound, and the answer says so", func(t *testing.T) {
		a, backend, call := newElicitingBackend(t, testCredentialFormURL, api.MCPServerURLElicitationRelay)
		backend.elicitTimes = 2
		synctest.Test(t, func(t *testing.T) {
			ctx := modernContext(t.Context(), urlElicitationCapabilities())
			first, err := a.urlElicitation()(call)(ctx, mcp.CallToolRequest{})
			require.NoError(t, err)
			start := time.Now()

			result, err := a.urlElicitation()(call)(ctx, acceptedRelayedRetry(first.RequestState))

			require.NoError(t, err)
			require.Equal(t, relayedRetryWait, time.Since(start))
			require.Equal(t, 2, backend.calls, "after the bound the call runs again: a page completed without a notification is not asked again")
			require.Equal(t, mcp.ResultTypeInputRequired, result.ResultType)
			message := result.InputRequests[relayedElicitationKey(0)].Elicitation.Message
			require.Contains(t, message, "waited 5 minutes for "+testElicitingServer)
			require.Contains(t, message, testElicitationText)
		})
	})

	t.Run("a retry for an elicitation this replica did not relay waits for nothing", func(t *testing.T) {
		a, backend, call := newElicitingBackend(t, testCredentialFormURL, api.MCPServerURLElicitationRelay)
		synctest.Test(t, func(t *testing.T) {
			ctx := modernContext(t.Context(), urlElicitationCapabilities())
			state := relayedRequestState{Relayed: testElicitingServer, ElicitationIDs: []string{testElicitationID}}.encode()
			start := time.Now()

			_, err := a.urlElicitation()(call)(ctx, acceptedRelayedRetry(state))

			require.NoError(t, err)
			require.Zero(t, time.Since(start), "another replica's elicitation never completes here")
			require.Equal(t, 1, backend.calls)
		})
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

// acceptedRelayedRetry is the 2026-07-28 retry of a client whose person
// agreed to open the relayed page, echoing requestState.
func acceptedRelayedRetry(requestState string) mcp.CallToolRequest {
	var retry mcp.CallToolRequest
	retry.Params.InputResponses = mcp.InputResponses{
		relayedElicitationKey(0): mcp.NewElicitationInputResponse(mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionAccept}}),
	}
	retry.Params.RequestState = requestState
	return retry
}

// The backend's completion of a relayed elicitation reaches the 2025-11-25
// transport session that received the -32042, with the same elicitationId,
// once; no other session, and no completion of an elicitation muster did
// not relay.
func TestBackendElicitation_CompletionReachesTheAskedSession(t *testing.T) {
	a, backend, call := newElicitingBackend(t, testCredentialFormURL, api.MCPServerURLElicitationRelay)
	a.mcpServer = mcpserver.NewMCPServer("muster", "test")
	asked := newLegacySession("transport-asked", urlElicitationCapabilities())
	other := newLegacySession("transport-other", urlElicitationCapabilities())
	require.NoError(t, a.mcpServer.RegisterSession(t.Context(), asked))
	require.NoError(t, a.mcpServer.RegisterSession(t.Context(), other))
	_, err := a.urlElicitation()(call)(a.mcpServer.WithContext(t.Context(), asked), mcp.CallToolRequest{})
	require.True(t, errors.As(err, new(mcp.URLElicitationRequiredError)), "err: %v", err)

	backend.complete("not-relayed")
	require.Empty(t, asked.ch, "a completion of an elicitation muster did not relay is not passed on")

	backend.complete(testElicitationID)
	backend.complete(testElicitationID)

	require.Len(t, asked.ch, 1, "an elicitation completes once")
	notification := <-asked.ch
	require.Equal(t, string(mcp.MethodNotificationElicitationComplete), notification.Method)
	require.Equal(t, testElicitationID, notification.Params.AdditionalFields["elicitationId"])
	require.Empty(t, other.ch, "only the session that was asked is told")
}

// A server may give the elicitations of several sessions the same id. Its
// completion on one session's own connection completes that session's only:
// another person who has not completed the page is not told it is done.
func TestBackendElicitation_SessionConnectionCompletesItsSessionOnly(t *testing.T) {
	a, backend, call := newElicitingBackend(t, testCredentialFormURL, api.MCPServerURLElicitationRelay)
	backend.elicitTimes = 2
	a.mcpServer = mcpserver.NewMCPServer("muster", "test")
	alice := newLegacySession("transport-alice", urlElicitationCapabilities())
	bob := newLegacySession("transport-bob", urlElicitationCapabilities())
	require.NoError(t, a.mcpServer.RegisterSession(t.Context(), alice))
	require.NoError(t, a.mcpServer.RegisterSession(t.Context(), bob))
	aliceCtx := a.mcpServer.WithContext(api.WithSessionID(t.Context(), "session-alice"), alice)
	bobCtx := a.mcpServer.WithContext(api.WithSessionID(t.Context(), "session-bob"), bob)
	for _, ctx := range []context.Context{aliceCtx, bobCtx} {
		_, err := a.urlElicitation()(call)(ctx, mcp.CallToolRequest{})
		require.True(t, errors.As(err, new(mcp.URLElicitationRequiredError)), "err: %v", err)
	}
	alicesConnection := &declaringClient{}
	a.declareURLElicitation(aliceCtx, testElicitingServer, alicesConnection)
	require.NotNil(t, alicesConnection.completed, "a session's own connection passes on its server's completions")

	alicesConnection.completed(testElicitationID)

	require.Len(t, alice.ch, 1)
	require.Empty(t, bob.ch, "bob's elicitation is not completed by alice's connection")
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
					require.Contains(t, mcpText(result), testElicitationText, "the server's own words reach the client")
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

// declaringClient records whether URL mode elicitation was declared on it,
// and keeps the handler of the server's elicitation completions.
type declaringClient struct {
	mockMCPClient
	declared  bool
	completed func(elicitationID string)
}

func (c *declaringClient) DeclareURLElicitation()                     { c.declared = true }
func (c *declaringClient) OnElicitationComplete(handler func(string)) { c.completed = handler }

func TestDeclareURLElicitation(t *testing.T) {
	for value, want := range map[string]bool{api.MCPServerURLElicitationRelay: true, "Drop": false, "": false} {
		t.Run(fmt.Sprintf("spec.urlElicitation=%q", value), func(t *testing.T) {
			api.RegisterMCPServerManager(&stubMCPServerManager{servers: map[string]api.MCPServerInfo{
				testElicitingServer: {Name: testElicitingServer, URLElicitation: value},
			}})
			t.Cleanup(func() { api.RegisterMCPServerManager(nil) })
			client := &declaringClient{}

			(&AggregatorServer{}).declareURLElicitation(t.Context(), testElicitingServer, client)

			require.Equal(t, want, client.declared)
		})
	}

	t.Run("an unknown server is not declared to", func(t *testing.T) {
		api.RegisterMCPServerManager(&stubMCPServerManager{})
		t.Cleanup(func() { api.RegisterMCPServerManager(nil) })
		client := &declaringClient{}

		(&AggregatorServer{}).declareURLElicitation(t.Context(), testElicitingServer, client)

		require.False(t, client.declared)
	})
}

// SECURITY: an organization's MCPServer cannot put itself on the allowlist.
// Its namespace refuses Relay (api.ValidateServerNamespace), and a refused
// definition that is still listed relays nothing and is declared nothing.
func TestBackendElicitation_TenantServerIsNeverRelayed(t *testing.T) {
	const tenantServer = "org-a/agents"
	api.RegisterMCPServerManager(&stubMCPServerManager{servers: map[string]api.MCPServerInfo{
		tenantServer: {Name: "agents", Namespace: "org-a", Key: tenantServer, URLElicitation: api.MCPServerURLElicitationRelay},
	}})
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })

	require.False(t, serverRelaysURLElicitation(t.Context(), tenantServer))
	client := &declaringClient{}
	(&AggregatorServer{}).declareURLElicitation(t.Context(), tenantServer, client)
	require.False(t, client.declared)

	buf := captureLog(t)
	a := &AggregatorServer{devPortalOrigin: "https://portal.example.com"}
	result, err := a.answerBackendElicitation(t.Context(), tenantServer, nil, mcp.URLElicitationRequiredError{
		Elicitations: []mcp.ElicitationParams{{Mode: mcp.ElicitationModeURL, URL: testCredentialFormURL, ElicitationID: testElicitationID}},
	})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.NotContains(t, mcpText(result), testCredentialFormURL)
	require.Equal(t, tenantServer, droppedLine(t, buf.String())["server"])
}

// A dropped elicitation's message reaches the client without any link the
// server put in it.
func TestDroppedElicitationResult_RemovesLinksFromTheMessage(t *testing.T) {
	result := droppedElicitationResult(testElicitingServer, "the page is not on the Dev Portal's origin", []mcp.ElicitationParams{{
		Message: "A model key is missing. Add it at https://attacker.example.com/form, //attacker.example.com/x or attacker.example.com/form now.",
		URL:     testForeignURL,
	}})

	text := mcpText(result)
	require.True(t, result.IsError)
	require.Contains(t, text, "A model key is missing. Add it at [link removed]")
	require.Contains(t, text, "now.")
	require.NotContains(t, text, "attacker.example.com")
}
