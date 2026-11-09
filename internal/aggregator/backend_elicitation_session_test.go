package aggregator

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpgoserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

const testSessionTool = "create_agent"

// newSessionInputBackend serves create_agent behind a bearer check, as a
// server a session signs in to. It asks a client that declared URL mode
// elicitation to open a page with an input_required result, and reports the
// answer of a retry. It records the capabilities each call declared.
func newSessionInputBackend(t *testing.T) (string, *atomic.Pointer[mcp.ClientCapabilities]) {
	t.Helper()
	declared := &atomic.Pointer[mcp.ClientCapabilities]{}
	mcpSrv := mcpgoserver.NewMCPServer("github", "1.0.0", mcpgoserver.WithToolCapabilities(true))
	mcpSrv.AddTool(mcp.NewTool(testSessionTool), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		capabilities := backendDeclared(ctx)
		declared.Store(&capabilities)
		if answer := mcpgoserver.ElicitationResponse(req.Params.InputResponses, testBackendInputKey); answer != nil {
			return mcp.NewToolResultText(string(answer.Action) + " " + req.Params.RequestState), nil
		}
		if capabilities.Elicitation == nil || capabilities.Elicitation.URL == nil {
			return mcp.NewToolResultText("not asked"), nil
		}
		return mcpgoserver.NewInputRequestBuilder(testBackendState).Elicit(testBackendInputKey, mcp.ElicitationParams{
			Mode: mcp.ElicitationModeURL, Message: "A model key is missing.", URL: testCredentialFormURL,
		}).ToolResult(), nil
	})
	backend, _ := serveGrantBackend(t, mcpSrv, subjectGrantToken)
	return backend.URL, declared
}

// newSessionInputFixture registers the backend as a server a session signs
// in to, and signs alice in to it.
func newSessionInputFixture(t *testing.T, backendURL string) *AggregatorServer {
	t.Helper()
	a := newTestAggregatorWithPool(t)
	registerSubjectGrantServer(t, a, backendURL, api.GrantScopeSubject)

	handler := newSubjectGrantMockHandler()
	handler.grant("alice", subjectGrantIssuerURL, subjectGrantToken)
	api.RegisterOAuthHandler(handler)
	t.Cleanup(func() { api.RegisterOAuthHandler(nil) })
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
			return a.CallToolInternal(ctx, "x_github_"+testSessionTool, map[string]any{})
		},
		"session-auth branch": func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return a.dispatchResolvedTool(ctx, "x_github_"+testSessionTool, "github", testSessionTool, map[string]any{}, "sess-2", "alice")
		},
	}
}

func TestBackendInput_SessionPaths(t *testing.T) {
	for path := range sessionDispatchPaths(nil) {
		t.Run(path, func(t *testing.T) {
			backendURL, declared := newSessionInputBackend(t)
			a := newSessionInputFixture(t, backendURL)
			call := a.urlElicitation()(sessionDispatchPaths(a)[path])

			first, err := call(sessionModernContext(urlElicitationCapabilities()), mcp.CallToolRequest{})
			require.NoError(t, err)
			require.True(t, first.NeedsInput(), "result: %+v", first)
			require.Equal(t, "[github] A model key is missing.", first.InputRequests[testBackendInputKey].Elicitation.Message)
			require.NotNil(t, declared.Load().Elicitation.URL)

			retry := mcp.CallToolRequest{Params: mcp.CallToolParams{MultiRoundTripParams: mcp.MultiRoundTripParams{
				InputResponses: accepted(), RequestState: first.RequestState,
			}}}
			retried, err := call(sessionModernContext(urlElicitationCapabilities()), retry)
			require.NoError(t, err)
			require.Equal(t, "accept "+testBackendState, mcpText(retried))

			result, err := call(sessionModernContext(mcp.ClientCapabilities{}), mcp.CallToolRequest{})
			require.NoError(t, err)
			require.Equal(t, "not asked", mcpText(result), "a client that declares nothing is not asked")
			require.Equal(t, mcp.ClientCapabilities{}, *declared.Load())
		})
	}
}
