package agent

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// progressingAggregator answers a tool call after reporting progress
// `reports` times, one every `every`, through the client's notification
// handler, the way the aggregator's notifications/progress reaches it.
type progressingAggregator struct {
	MockMCPGoClient
	client  *Client
	reports int
	every   time.Duration
}

func (f *progressingAggregator) CallTool(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	f.lastCallToolRequest = req
	for i := 1; i <= f.reports; i++ {
		select {
		case <-time.After(f.every):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if req.Params.Meta == nil || req.Params.Meta.ProgressToken == nil {
			continue
		}
		total := float64(f.reports)
		f.client.queueNotification(progressNotification(req.Params.Meta.ProgressToken, float64(i), &total, "waiting for checks"))
	}
	return mcp.NewToolResultText("merged"), nil
}

func progressNotification(token mcp.ProgressToken, progress float64, total *float64, message string) mcp.JSONRPCNotification {
	fields := map[string]any{"progressToken": token, "progress": progress, "message": message}
	if total != nil {
		fields["total"] = *total
	}
	n := mcp.JSONRPCNotification{}
	n.Method = string(mcp.MethodNotificationProgress)
	n.Params.AdditionalFields = fields
	return n
}

func progressingClient(timeout time.Duration, reports int, every time.Duration) (*Client, *progressingAggregator) {
	c := NewClient("http://localhost:8090/mcp", NewDevNullLogger(), TransportStreamableHTTP)
	c.SetCallTimeout(timeout)
	aggregator := &progressingAggregator{client: c, reports: reports, every: every}
	c.client = aggregator
	return c, aggregator
}

// Every tool call sends a progressToken of its own; each progress
// notification for it reaches the progress handler and restarts the call
// timeout, so a tool that blocks for longer than the timeout completes.
func TestCallToolProgressKeepsTheCallAlive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, aggregator := progressingClient(time.Minute, 10, 50*time.Second)
		var updates []Progress
		c.SetProgressHandler(func(p Progress) { updates = append(updates, p) })
		start := time.Now()

		result, err := c.callToolDirect(t.Context(), "call_tool", nil)

		require.NoError(t, err)
		assert.Equal(t, "merged", result.Content[0].(mcp.TextContent).Text)
		assert.Equal(t, 500*time.Second, time.Since(start))
		require.NotNil(t, aggregator.lastCallToolRequest.Params.Meta)
		assert.Equal(t, "muster-cli-1", aggregator.lastCallToolRequest.Params.Meta.ProgressToken)
		require.Len(t, updates, 10)
		assert.Equal(t, float64(10), updates[9].Progress)
		require.NotNil(t, updates[9].Total)
		assert.Equal(t, float64(10), *updates[9].Total)
		assert.Equal(t, "waiting for checks", updates[9].Message)
	})
}

// A call with neither an answer nor progress within the call timeout is cut
// at the timeout, reported as a deadline.
func TestCallToolWithoutProgressIsCutAtTheTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _ := progressingClient(time.Minute, 1, 2*time.Minute)
		start := time.Now()

		_, err := c.callToolDirect(t.Context(), "call_tool", nil)

		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.EqualError(t, err, "tool call failed: no answer or progress within the call timeout: context deadline exceeded")
		assert.Equal(t, time.Minute, time.Since(start))
	})
}

// Progress does not keep a call going past MaxCallDuration.
func TestCallToolProgressStopsAtMaxCallDuration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c, _ := progressingClient(time.Minute, 1000, 30*time.Second)
		start := time.Now()

		_, err := c.callToolDirect(t.Context(), "call_tool", nil)

		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Equal(t, MaxCallDuration, time.Since(start))
	})
}

// Progress whose token belongs to no call in flight, and every other
// notification, go to the notification queue.
func TestProgressForNoCallIsQueued(t *testing.T) {
	c := NewClient("http://localhost:8090/mcp", NewDevNullLogger(), TransportStreamableHTTP)
	var updates []Progress
	c.SetProgressHandler(func(p Progress) { updates = append(updates, p) })

	c.queueNotification(progressNotification("muster-cli-9", 1, nil, ""))
	listChanged := mcp.JSONRPCNotification{}
	listChanged.Method = string(mcp.MethodNotificationToolsListChanged)
	c.queueNotification(listChanged)

	assert.Empty(t, updates)
	assert.Len(t, c.NotificationChan, 2)
}
