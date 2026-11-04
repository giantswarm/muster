package aggregator

import (
	"context"
	"errors"
	"testing"

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
	t.Helper()
	mcpSrv := mcpgoserver.NewMCPServer("github", "1.0.0", mcpgoserver.WithToolCapabilities(true))
	mcpSrv.AddTool(mcp.NewTool(testElicitingTool), func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if session, ok := mcpgoserver.ClientSessionFromContext(ctx).(mcpgoserver.SessionWithClientInfo); ok {
			if declared := session.GetClientCapabilities(); declared.Elicitation != nil && declared.Elicitation.URL != nil {
				return nil, mcp.URLElicitationRequiredError{Elicitations: []mcp.ElicitationParams{{
					Mode: mcp.ElicitationModeURL, Message: testElicitationText, URL: url, ElicitationID: testElicitationID,
				}}}
			}
		}
		return mcp.NewToolResultText(backendFallbackText), nil
	})
	backend, _ := serveGrantBackend(t, mcpSrv, subjectGrantToken)
	return backend.URL
}

// newSessionElicitationFixture registers the backend as a server a session
// signs in to, with spec.urlElicitation set to urlElicitation, and signs
// alice in to it.
func newSessionElicitationFixture(t *testing.T, url, urlElicitation string) *AggregatorServer {
	t.Helper()
	a := newTestAggregatorWithPool(t)
	a.devPortalOrigin = "https://portal.example.com"
	registerSubjectGrantServer(t, a, newSessionElicitingBackend(t, url), api.GrantScopeSubject)

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
