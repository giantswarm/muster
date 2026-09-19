package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// slowToolBridge wires a bridge to a mock aggregator whose call_tool answers
// after delay (or when the request's context ends), the way a slow tool
// behind the aggregator does.
func slowToolBridge(t *testing.T, delay time.Duration) (*MockMCPGoClient, *Client, func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error)) {
	t.Helper()

	wrappedResult, err := json.Marshal(map[string]any{
		"isError": false,
		"content": []any{map[string]any{"type": "text", "text": "answered late"}},
	})
	require.NoError(t, err)

	mock := &MockMCPGoClient{
		callToolResponses: map[string]*mcp.CallToolResult{
			"call_tool": mcp.NewToolResultText(string(wrappedResult)),
		},
		callToolDelay: delay,
	}
	client := NewClient("http://localhost:8090/mcp", NewDevNullLogger(), TransportStreamableHTTP)
	client.client = mock

	server := &MCPServer{client: client, logger: NewDevNullLogger()}
	return mock, client, server.forwardToServerMetaTool("call_tool")
}

func callToolRequest(args map[string]any) mcp.CallToolRequest {
	req := mcp.CallToolRequest{}
	req.Params.Name = "call_tool"
	req.Params.Arguments = args
	return req
}

func slowToolArgs() map[string]any {
	return map[string]any{
		"name":      "x_slow_read",
		"arguments": map[string]any{"scope": "all"},
	}
}

func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, result.Content)
	text, ok := mcp.AsTextContent(result.Content[0])
	require.True(t, ok)
	return text.Text
}

// The bridge gives a tool call the client's call timeout, five minutes by
// default -- the aggregator's largest tool timeout -- not the 30 s the
// handshake and the listings run under.
func TestCallToolBridgeDefaultTimeoutOutlastsThirtySeconds(t *testing.T) {
	mock, client, handler := slowToolBridge(t, 0)
	require.Equal(t, DefaultCallTimeout, client.CallTimeout())

	before := time.Now()
	result, err := handler(context.Background(), callToolRequest(slowToolArgs()))
	require.NoError(t, err)
	assert.False(t, result.IsError)

	remaining := mock.lastCallToolDeadline.Sub(before)
	assert.Greater(t, remaining, 30*time.Second, "a tool answering after 30 s must fit the call's deadline")
	assert.InDelta(t, DefaultCallTimeout, remaining, float64(5*time.Second))

	args, ok := mock.lastCallToolRequest.Params.Arguments.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "x_slow_read", args["name"])
	assert.NotContains(t, args, "timeout")
}

// A tool that answers within the call timeout completes; one that does not is
// cut at the call timeout, reported as a tool error, never a hang.
func TestCallToolBridgeSlowToolAgainstCallTimeout(t *testing.T) {
	t.Run("completes within the call timeout", func(t *testing.T) {
		_, client, handler := slowToolBridge(t, 200*time.Millisecond)
		client.SetCallTimeout(2 * time.Second)

		result, err := handler(context.Background(), callToolRequest(slowToolArgs()))
		require.NoError(t, err)
		assert.False(t, result.IsError)
		assert.Contains(t, resultText(t, result), "answered late")
	})

	t.Run("cut at the call timeout", func(t *testing.T) {
		_, client, handler := slowToolBridge(t, 5*time.Second)
		client.SetCallTimeout(50 * time.Millisecond)

		started := time.Now()
		result, err := handler(context.Background(), callToolRequest(slowToolArgs()))
		require.NoError(t, err)
		assert.True(t, result.IsError)
		assert.Contains(t, resultText(t, result), "context deadline exceeded")
		assert.Less(t, time.Since(started), time.Second)
	})
}

// call_tool's timeout argument bounds that one call in place of the client's
// call timeout and is consumed by the bridge, never forwarded.
func TestCallToolBridgeTimeoutArgument(t *testing.T) {
	t.Run("bounds the call and is not forwarded", func(t *testing.T) {
		mock, _, handler := slowToolBridge(t, 0)

		args := slowToolArgs()
		args["timeout"] = float64(1)
		before := time.Now()
		result, err := handler(context.Background(), callToolRequest(args))
		require.NoError(t, err)
		assert.False(t, result.IsError)

		assert.InDelta(t, time.Second, mock.lastCallToolDeadline.Sub(before), float64(100*time.Millisecond))

		forwarded, ok := mock.lastCallToolRequest.Params.Arguments.(map[string]any)
		require.True(t, ok)
		assert.NotContains(t, forwarded, "timeout")
		assert.Equal(t, "x_slow_read", forwarded["name"])
	})

	t.Run("cuts a slower tool", func(t *testing.T) {
		_, client, handler := slowToolBridge(t, 5*time.Second)
		client.SetCallTimeout(time.Minute)

		args := slowToolArgs()
		args["timeout"] = 0.05
		started := time.Now()
		result, err := handler(context.Background(), callToolRequest(args))
		require.NoError(t, err)
		assert.True(t, result.IsError)
		assert.Contains(t, resultText(t, result), "context deadline exceeded")
		assert.Less(t, time.Since(started), time.Second)
	})

	t.Run("refuses anything but a positive number", func(t *testing.T) {
		for _, bad := range []any{"10", float64(0), float64(-3), true} {
			mock, _, handler := slowToolBridge(t, 0)
			args := slowToolArgs()
			args["timeout"] = bad

			result, err := handler(context.Background(), callToolRequest(args))
			require.NoError(t, err)
			assert.True(t, result.IsError, "timeout=%v", bad)
			assert.Contains(t, resultText(t, result), "timeout must be a positive number of seconds")
			assert.Empty(t, mock.lastCallToolRequest.Params.Name, "nothing is forwarded for timeout=%v", bad)
		}
	})
}

// The bridge advertises the timeout argument on call_tool and on no other
// meta-tool, whose definitions stay the aggregator's own.
func TestCallToolBridgeAdvertisesTimeoutArgument(t *testing.T) {
	client := NewClient("http://localhost:8090/mcp", NewDevNullLogger(), TransportStreamableHTTP)
	client.client = &MockMCPGoClient{}
	server, err := NewMCPServer(client, NewDevNullLogger(), false)
	require.NoError(t, err)

	tools := server.mcpServer.ListTools()
	callTool, ok := tools["call_tool"]
	require.True(t, ok)
	timeoutSchema, ok := callTool.Tool.InputSchema.Properties["timeout"].(map[string]any)
	require.True(t, ok, "call_tool advertises timeout")
	assert.EqualValues(t, "number", timeoutSchema["type"])
	assert.NotContains(t, callTool.Tool.InputSchema.Required, "timeout")

	for name, tool := range tools {
		if name == "call_tool" {
			continue
		}
		assert.NotContains(t, tool.Tool.InputSchema.Properties, "timeout", "%s carries no bridge argument", name)
	}
}

// The REPL runs a command under the client's call timeout, so `call` waits as
// long for a tool as the bridge does.
func TestREPLCommandContextFollowsCallTimeout(t *testing.T) {
	client := NewClient("http://localhost:8090/mcp", NewDevNullLogger(), TransportStreamableHTTP)
	client.SetCallTimeout(90 * time.Second)
	repl := NewREPL(client, NewDevNullLogger())

	probe := &deadlineProbe{}
	repl.commandRegistry.Register("probe", probe)

	before := time.Now()
	require.NoError(t, repl.executeCommand("probe"))
	require.True(t, probe.hasDeadline)
	assert.InDelta(t, 90*time.Second, probe.deadline.Sub(before), float64(5*time.Second))
}

// deadlineProbe is a REPL command that records the deadline it runs under.
type deadlineProbe struct {
	deadline    time.Time
	hasDeadline bool
}

func (p *deadlineProbe) Execute(ctx context.Context, _ []string) error {
	p.deadline, p.hasDeadline = ctx.Deadline()
	return nil
}
func (p *deadlineProbe) Usage() string               { return "probe" }
func (p *deadlineProbe) Description() string         { return "records its deadline" }
func (p *deadlineProbe) Completions(string) []string { return nil }
func (p *deadlineProbe) Aliases() []string           { return nil }
