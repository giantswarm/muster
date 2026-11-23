package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestCallTool_ParamHeaderDiscoveryFailure(t *testing.T) {
	_, url := newParamHeaderBackend(t, "pull_request_read")
	connection := connect(t, t.Context(), url)
	discoveryError := errors.New("discovery unavailable")
	observed := &observedToolClient{MCPClient: connection.client}
	observed.list = func(ctx context.Context, request mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
		if observed.lists.Load() == 1 {
			return nil, discoveryError
		}
		return observed.MCPClient.ListTools(ctx, request)
	}
	connection.client = observed

	_, err := connection.CallTool(WithProgressReporter(t.Context(), NewProgressReporter("caller-token", nil)), "pull_request_read", map[string]any{"owner": "giantswarm"})
	require.ErrorIs(t, err, discoveryError)
	require.Zero(t, observed.calls.Load())
	require.Empty(t, connection.progress.routes)

	result, err := connection.CallTool(t.Context(), "pull_request_read", map[string]any{"owner": "giantswarm"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.EqualValues(t, 2, observed.lists.Load())
	require.EqualValues(t, 1, observed.calls.Load())
}

func TestCallTool_ParamHeaderRetry(t *testing.T) {
	headerError := mcp.HeaderMismatchError{Header: "Mcp-Param-Owner", Reason: "missing header"}
	backendError := errors.New("backend unavailable")
	discoveryError := errors.New("discovery unavailable")
	testCases := []struct {
		name          string
		callError     error
		refreshError  error
		persistent    bool
		expectedError error
		expectedCalls int32
		expectedLists int32
	}{
		{name: "success", expectedCalls: 1, expectedLists: 1},
		{name: "header mismatch", callError: headerError, expectedCalls: 2, expectedLists: 2},
		{name: "pointer header mismatch", callError: &headerError, expectedCalls: 2, expectedLists: 2},
		{name: "wrapped header mismatch", callError: fmt.Errorf("transport: %w", headerError), expectedCalls: 2, expectedLists: 2},
		{name: "persistent mismatch", callError: headerError, persistent: true, expectedError: headerError, expectedCalls: 2, expectedLists: 2},
		{name: "other error", callError: backendError, expectedError: backendError, expectedCalls: 1, expectedLists: 1},
		{name: "refresh failure", callError: headerError, refreshError: discoveryError, expectedError: discoveryError, expectedCalls: 1, expectedLists: 2},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			observed := &observedToolClient{}
			observed.list = func(context.Context, mcp.ListToolsRequest) (*mcp.ListToolsResult, error) {
				if observed.lists.Load() > 1 && testCase.refreshError != nil {
					return nil, testCase.refreshError
				}
				return &mcp.ListToolsResult{}, nil
			}
			var requests []mcp.CallToolRequest
			var contexts []context.Context
			observed.call = func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				requests = append(requests, request)
				_, ok := ctx.Deadline()
				require.True(t, ok)
				contexts = append(contexts, ctx)
				if testCase.persistent || observed.calls.Load() == 1 {
					if testCase.callError != nil {
						return nil, testCase.callError
					}
				}
				return mcp.NewToolResultText("called"), nil
			}
			connection := &baseMCPClient{client: observed, connected: true, streamableHTTP: true, negotiatedProtocolVersion: mcp.ProtocolVersion20260728}
			ctx := WithProgressReporter(WithRoundTrip(WithCallerCapabilities(t.Context(), urlCapabilities()), mcp.MultiRoundTripParams{
				RequestState: "request-state",
				InputResponses: mcp.InputResponses{testInputKey: mcp.NewElicitationInputResponse(mcp.ElicitationResult{
					ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionAccept},
				})},
			}), NewProgressReporter("caller-token", nil))
			result, err := connection.callTool(ctx, "pull_request_read", map[string]any{"owner": "giantswarm"})
			if testCase.expectedError != nil {
				require.ErrorIs(t, err, testCase.expectedError)
			} else {
				require.NoError(t, err)
				require.False(t, result.IsError)
			}
			require.Equal(t, testCase.expectedCalls, observed.calls.Load())
			require.Equal(t, testCase.expectedLists, observed.lists.Load())
			require.Empty(t, connection.progress.routes)
			require.NotNil(t, requests[0].Params.Meta)
			require.NotEmpty(t, requests[0].Params.Meta.ProgressToken)
			if len(requests) > 1 {
				require.Equal(t, requests[0], requests[1])
				require.Same(t, contexts[0], contexts[1])
			}
		})
	}
}

func TestCallTool_ParamHeadersOnlyOnModernStreamableHTTP(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		http     bool
		protocol string
	}{
		{name: "stdio", protocol: mcp.ProtocolVersion20260728},
		{name: "legacy HTTP", http: true, protocol: mcp.LATEST_LEGACY_PROTOCOL_VERSION},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			headerError := mcp.HeaderMismatchError{Header: "Mcp-Param-Owner", Reason: "missing header"}
			observed := &observedToolClient{call: func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				return nil, headerError
			}}
			connection := &baseMCPClient{client: observed, connected: true, streamableHTTP: testCase.http, negotiatedProtocolVersion: testCase.protocol}
			_, err := connection.callTool(t.Context(), "pull_request_read", nil)
			require.ErrorIs(t, err, headerError)
			require.Zero(t, observed.lists.Load())
			require.EqualValues(t, 1, observed.calls.Load())
		})
	}
}
