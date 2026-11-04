package aggregator

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpgoserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

const backendFallbackText = "A model key is missing: add one in the Dev Portal."

// newSessionElicitingBackend serves create_agent behind a bearer check, as a
// server a session signs in to. Like a backend that follows the protocol, it
// answers with a URL elicitation of url only to a client that declared URL
// mode elicitation, and with its text answer to any other.
func newSessionElicitingBackend(t *testing.T, url string) string {
	backendURL, _ := newCompletingSessionBackend(t, url)
	return backendURL
}

// newCompletingSessionBackend is newSessionElicitingBackend that also
// returns a function which completes the elicitation as the backend does
// once the person completed the page: notifications/elicitation/complete,
// out of band, to each MCP session it elicited. A call after it is answered
// with agentCreatedText.
func newCompletingSessionBackend(t *testing.T, url string) (string, func()) {
	t.Helper()
	var (
		mu        sync.Mutex
		elicited  []string
		completed bool
	)
	mcpSrv := mcpgoserver.NewMCPServer("github", "1.0.0", mcpgoserver.WithToolCapabilities(true))
	mcpSrv.AddTool(mcp.NewTool(testElicitingTool), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		mu.Lock()
		defer mu.Unlock()
		if completed {
			return mcp.NewToolResultText(agentCreatedText), nil
		}
		if session, ok := mcpgoserver.ClientSessionFromContext(ctx).(mcpgoserver.SessionWithClientInfo); ok {
			if declared := session.GetClientCapabilities(); declared.Elicitation != nil && declared.Elicitation.URL != nil {
				elicited = append(elicited, session.SessionID())
				return nil, mcp.URLElicitationRequiredError{Elicitations: []mcp.ElicitationParams{{
					Mode: mcp.ElicitationModeURL, Message: testElicitationText, URL: url, ElicitationID: testElicitationID,
				}}}
			}
		}
		return mcp.NewToolResultText(backendFallbackText), nil
	})
	backend, _ := serveGrantBackend(t, mcpSrv, subjectGrantToken)
	complete := func() {
		mu.Lock()
		defer mu.Unlock()
		completed = true
		for _, sessionID := range elicited {
			require.NoError(t, mcpSrv.SendNotificationToSpecificClient(sessionID, string(mcp.MethodNotificationElicitationComplete), map[string]any{"elicitationId": testElicitationID}))
		}
	}
	return backend.URL, complete
}

const agentCreatedText = "agent created"

// newSessionElicitationFixture registers the backend as a server a session
// signs in to, with spec.urlElicitation set to urlElicitation, and signs
// alice in to it.
func newSessionElicitationFixture(t *testing.T, url, urlElicitation string) *AggregatorServer {
	t.Helper()
	return newSessionElicitationFixtureOn(t, newSessionElicitingBackend(t, url), urlElicitation)
}

// newSessionElicitationFixtureOn is newSessionElicitationFixture on a
// backend already served.
func newSessionElicitationFixtureOn(t *testing.T, backendURL, urlElicitation string) *AggregatorServer {
	t.Helper()
	a := newTestAggregatorWithPool(t)
	a.devPortalOrigin = "https://portal.example.com"
	registerSubjectGrantServer(t, a, backendURL, api.GrantScopeSubject)

	handler := newSubjectGrantMockHandler()
	handler.grant("alice", subjectGrantIssuerURL, subjectGrantToken)
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })

	api.RegisterMCPServerManager(&stubMCPServerManager{servers: map[string]api.MCPServerInfo{
		"github": {Name: "github", Type: "streamable-http", URLElicitation: urlElicitation},
	}})
	t.Cleanup(func() { api.RegisterMCPServerManager(nil) })
	return a
}

func sessionModernContext(capabilities mcp.ClientCapabilities) context.Context {
	return mcpgoserver.WithRequestProtocolInfo(sessionContext("sess-2", "alice"), &mcpgoserver.RequestProtocolInfo{
		Modern:             true,
		ProtocolVersion:    mcp.ProtocolVersion20260728,
		ClientCapabilities: &capabilities,
	})
}

// The two dispatch paths of a server a session signs in to: the
// session-cache branch of CallToolInternal, and dispatchResolvedTool's
// session-auth branch. Both reach the backend through
// callToolWithTokenExchangeRetry on a per-session client.
func sessionDispatchPaths(a *AggregatorServer) map[string]mcpgoserver.ToolHandlerFunc {
	return map[string]mcpgoserver.ToolHandlerFunc{
		"session-cache branch": func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return a.CallToolInternal(ctx, "x_github_"+testElicitingTool, map[string]any{})
		},
		"session-auth branch": func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return a.dispatchResolvedTool(ctx, "x_github_"+testElicitingTool, "github", testElicitingTool, map[string]any{}, "sess-2", "alice")
		},
	}
}

func TestBackendElicitation_SessionPaths(t *testing.T) {
	t.Run("a server with Relay is declared URL mode elicitation and its elicitation is relayed", func(t *testing.T) {
		for path := range sessionDispatchPaths(nil) {
			t.Run(path, func(t *testing.T) {
				a := newSessionElicitationFixture(t, testCredentialFormURL, api.MCPServerURLElicitationRelay)
				call := sessionDispatchPaths(a)[path]
				_, _, resolveErr := a.registry.ResolveToolName("x_github_" + testElicitingTool)
				require.Error(t, resolveErr, "precondition: CallToolInternal takes the session-cache branch")

				result, err := a.urlElicitation()(call)(sessionModernContext(urlElicitationCapabilities()), mcp.CallToolRequest{})

				require.NoError(t, err)
				require.Equal(t, mcp.ResultTypeInputRequired, result.ResultType, "result: %+v", result)
				require.Equal(t, testCredentialFormURL, result.InputRequests[relayedElicitationKey(0)].Elicitation.URL)

				legacy := mcpgoserver.NewMCPServer("muster", "test").WithContext(sessionContext("sess-2", "alice"), newLegacySession("transport-1", urlElicitationCapabilities()))
				_, err = a.urlElicitation()(call)(legacy, mcp.CallToolRequest{})
				var required mcp.URLElicitationRequiredError
				require.True(t, errors.As(err, &required), "err: %v", err)
				require.Equal(t, testCredentialFormURL, required.Elicitations[0].URL)
			})
		}
	})

	t.Run("a server without Relay is not declared it and answers with its text", func(t *testing.T) {
		for path := range sessionDispatchPaths(nil) {
			t.Run(path, func(t *testing.T) {
				a := newSessionElicitationFixture(t, testCredentialFormURL, "")
				call := sessionDispatchPaths(a)[path]

				result, err := a.urlElicitation()(call)(sessionModernContext(urlElicitationCapabilities()), mcp.CallToolRequest{})

				require.NoError(t, err)
				require.Empty(t, result.InputRequests)
				require.Equal(t, backendFallbackText, mcpText(result))
			})
		}
	})

	t.Run("a server with Relay to another origin is dropped and logged", func(t *testing.T) {
		for path := range sessionDispatchPaths(nil) {
			t.Run(path, func(t *testing.T) {
				buf := captureLog(t)
				a := newSessionElicitationFixture(t, testForeignURL, api.MCPServerURLElicitationRelay)
				call := sessionDispatchPaths(a)[path]

				result, err := a.urlElicitation()(call)(sessionModernContext(urlElicitationCapabilities()), mcp.CallToolRequest{})

				require.NoError(t, err)
				require.Empty(t, result.InputRequests)
				require.True(t, result.IsError)
				require.Contains(t, mcpText(result), testElicitationText)
				require.NotContains(t, mcpText(result), testForeignURL)
				line := droppedLine(t, buf.String())
				require.Equal(t, "github", line["server"])
				require.Equal(t, "https://attacker.example.com", line["origin"])
			})
		}
	})
}

// The backend's completion arrives on the per-session connection muster
// relayed the elicitation from: it reaches the 2025-11-25 transport session
// that received the -32042, and it ends the wait of a 2026-07-28 retry that
// accepted, which then runs the call once.
func TestBackendElicitation_SessionPathsPassOnTheCompletion(t *testing.T) {
	for path := range sessionDispatchPaths(nil) {
		t.Run(path+", 2025-11-25", func(t *testing.T) {
			backendURL, complete := newCompletingSessionBackend(t, testCredentialFormURL)
			a := newSessionElicitationFixtureOn(t, backendURL, api.MCPServerURLElicitationRelay)
			a.mcpServer = mcpgoserver.NewMCPServer("muster", "test")
			asked := newLegacySession("transport-asked", urlElicitationCapabilities())
			require.NoError(t, a.mcpServer.RegisterSession(t.Context(), asked))
			call := sessionDispatchPaths(a)[path]
			_, err := a.urlElicitation()(call)(a.mcpServer.WithContext(sessionContext("sess-2", "alice"), asked), mcp.CallToolRequest{})
			require.True(t, errors.As(err, new(mcp.URLElicitationRequiredError)), "err: %v", err)

			complete()

			select {
			case notification := <-asked.ch:
				require.Equal(t, string(mcp.MethodNotificationElicitationComplete), notification.Method)
				require.Equal(t, testElicitationID, notification.Params.AdditionalFields["elicitationId"])
			case <-time.After(5 * time.Second):
				t.Fatal("the backend's completion did not reach the session that received the elicitation")
			}
		})

		t.Run(path+", 2026-07-28", func(t *testing.T) {
			backendURL, complete := newCompletingSessionBackend(t, testCredentialFormURL)
			a := newSessionElicitationFixtureOn(t, backendURL, api.MCPServerURLElicitationRelay)
			call := sessionDispatchPaths(a)[path]
			ctx := sessionModernContext(urlElicitationCapabilities())
			first, err := a.urlElicitation()(call)(ctx, mcp.CallToolRequest{})
			require.NoError(t, err)
			require.Equal(t, mcp.ResultTypeInputRequired, first.ResultType)

			type answer struct {
				result *mcp.CallToolResult
				err    error
			}
			answered := make(chan answer, 1)
			go func() {
				result, err := a.urlElicitation()(call)(ctx, acceptedRelayedRetry(first.RequestState))
				answered <- answer{result, err}
			}()
			// A retry that does not wait answers at once with the backend's
			// second elicitation. The window can only let a broken wait pass
			// on a slow runner, never fail a working one; the synctest
			// subtests of TestBackendElicitation_Relayed pin the wait exactly.
			select {
			case got := <-answered:
				t.Fatalf("the retry ran before the backend completed the page: %+v, %v", got.result, got.err)
			case <-time.After(200 * time.Millisecond):
			}
			complete()

			select {
			case got := <-answered:
				require.NoError(t, got.err)
				require.Equal(t, agentCreatedText, mcpText(got.result))
			case <-time.After(5 * time.Second):
				t.Fatal("the backend's completion did not end the retry's wait")
			}
		})
	}
}
