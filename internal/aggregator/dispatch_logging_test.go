package aggregator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// The dispatch line is what an audit counts: the subject, the server and the
// tool of every call that ran, and nothing the call carried. A Dex subject is
// long and opaque, so the tests use one and expect it truncated like every
// identity in the log.
const (
	dispatchTestSubject   = "CiQwOGE4Njg0Yi1kYjg4LTRiNzMtOTBhOS0zY2QxNjYxZjU0NjYSBWxvY2Fs"
	dispatchTestSubjectID = "CiQwOGE4..."
	dispatchArgSentinel   = "arg-sentinel"
	dispatchResSentinel   = "result-sentinel"
)

// sentinelClient answers every known tool with a result whose text must
// never reach the log.
type sentinelClient struct {
	mockMCPClient
	err error
}

func (c *sentinelClient) CallTool(ctx context.Context, name string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	if _, err := c.mockMCPClient.CallTool(ctx, name, args); err != nil {
		return nil, err
	}
	if c.err != nil {
		return nil, c.err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(dispatchResSentinel)}}, nil
}

// dispatchLines returns the parsed dispatch lines of a captured log.
func dispatchLines(t *testing.T, logged string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(strings.TrimRight(logged, "\n"), "\n") {
		if strings.Contains(raw, DispatchLogMessage) {
			lines = append(lines, parseLogLine(t, raw))
		}
	}
	return lines
}

// theDispatchLine returns the one dispatch line of a captured log, with its
// subsystem, subject and outcome checked.
func theDispatchLine(t *testing.T, logged, wantOutcome string) map[string]any {
	t.Helper()
	lines := dispatchLines(t, logged)
	require.Len(t, lines, 1, "one dispatch line per call:\n%s", logged)
	line := lines[0]
	require.Equal(t, LogSubsystem, line["subsystem"])
	require.Equal(t, dispatchTestSubjectID, line["subject"], "the subject, truncated like every identity in the log")
	require.Equal(t, wantOutcome, line["outcome"])
	require.Contains(t, line, "duration_s")
	_, hasErr := line["error"]
	require.False(t, hasErr, "the error stays on the boundary line: %v", line)
	return line
}

func newDispatchTestServer(t *testing.T, backend MCPClient) (*AggregatorServer, string) {
	t.Helper()
	server, err := NewAggregatorServer(t.Context(), AggregatorConfig{Host: "localhost", Port: 0}, nil)
	require.NoError(t, err)
	require.NoError(t, server.RegisterServer(t.Context(), ServerRegistration{Name: "kubernetes"}, backend))
	return server, server.registry.ExposedToolName("kubernetes", "list_pods")
}

func TestDispatchLine_BackendCall(t *testing.T) {
	buf := captureLog(t)
	server, exposed := newDispatchTestServer(t, &sentinelClient{mockMCPClient: mockMCPClient{tools: []mcp.Tool{{Name: "list_pods"}}}})

	_, err := server.CallToolInternal(api.WithSubject(t.Context(), dispatchTestSubject), exposed, map[string]any{"namespace": dispatchArgSentinel})
	require.NoError(t, err)

	line := theDispatchLine(t, buf.String(), outcomeOK)
	require.Equal(t, "kubernetes", line["server"])
	require.Equal(t, exposed, line["tool"], "the aggregator-exposed name, the one list_tools shows")
	require.NotContains(t, buf.String(), dispatchArgSentinel, "arguments never reach the log")
	require.NotContains(t, buf.String(), dispatchResSentinel, "results never reach the log")
}

func TestDispatchLine_BackendError(t *testing.T) {
	buf := captureLog(t)
	server, exposed := newDispatchTestServer(t, &sentinelClient{
		mockMCPClient: mockMCPClient{tools: []mcp.Tool{{Name: "list_pods"}}},
		err:           errors.New("backend refused " + dispatchArgSentinel),
	})

	_, err := server.CallToolInternal(api.WithSubject(t.Context(), dispatchTestSubject), exposed, nil)
	require.Error(t, err)

	line := theDispatchLine(t, buf.String(), outcomeError)
	require.Equal(t, "kubernetes", line["server"])
	require.Equal(t, exposed, line["tool"])
}

// A core tool routed through call_tool reaches no backend: its line names
// the subject and the tool and no server.
func TestDispatchLine_CoreToolThroughCallTool(t *testing.T) {
	buf := captureLog(t)
	server, _ := newDispatchTestServer(t, &mockMCPClient{})

	_, err := server.CallToolInternal(api.WithSubject(t.Context(), dispatchTestSubject), "core_config_get", map[string]any{"key": dispatchArgSentinel})
	require.Error(t, err, "no config handler is registered in this test binary")

	line := theDispatchLine(t, buf.String(), outcomeError)
	require.Equal(t, "core_config_get", line["tool"])
	require.NotContains(t, line, "server", "a core tool reaches no backend: %v", line)
	require.NotContains(t, buf.String(), dispatchArgSentinel, "arguments never reach the log")
}

// A core tool a client calls directly never reaches CallToolInternal; its
// handler writes the line. The meta-tools themselves get none.
func TestDispatchLine_CoreToolCalledDirectly(t *testing.T) {
	ctx := api.WithSubject(t.Context(), dispatchTestSubject)
	a := &AggregatorServer{}
	provider := failingToolProvider{err: errors.New("spec rejected")}

	t.Run("core tool", func(t *testing.T) {
		buf := captureLog(t)
		req := mcp.CallToolRequest{}
		req.Params.Name = "core_mcpserver_create"
		req.Params.Arguments = map[string]any{"name": dispatchArgSentinel}

		result, err := a.createMetaToolHandler(provider, "core_mcpserver_create")(ctx, req)
		require.NoError(t, err)
		require.True(t, result.IsError)

		line := theDispatchLine(t, buf.String(), outcomeErrorResult)
		require.Equal(t, "core_mcpserver_create", line["tool"])
		require.NotContains(t, line, "server")
		require.NotContains(t, buf.String(), dispatchArgSentinel, "arguments never reach the log")
	})

	t.Run("meta-tool", func(t *testing.T) {
		buf := captureLog(t)
		req := mcp.CallToolRequest{}
		req.Params.Name = "call_tool"

		_, err := a.createMetaToolHandler(provider, "call_tool")(ctx, req)
		require.NoError(t, err)
		require.Empty(t, dispatchLines(t, buf.String()), "call_tool writes no line of its own; the tool it runs does")
	})
}
