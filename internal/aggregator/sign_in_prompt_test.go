package aggregator

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// TestURLElicitation_MarksAgentRequestsForTheSignInPrompt pins which requests
// filter_tools may answer with a sign-in: an agent's whose client shows URL
// elicitations, never a person's own client nor a client without them.
func TestURLElicitation_MarksAgentRequestsForTheSignInPrompt(t *testing.T) {
	marked := func(ctx context.Context) bool {
		var seen bool
		probe := func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			seen = api.SignInPrompt(ctx)
			return mcp.NewToolResultText("ok"), nil
		}
		_, err := (&AggregatorServer{}).urlElicitation()(probe)(ctx, mcp.CallToolRequest{})
		require.NoError(t, err)
		return seen
	}
	modern := func(sessionID string, capabilities mcp.ClientCapabilities) context.Context {
		return mcpserver.WithRequestProtocolInfo(api.WithSessionID(t.Context(), sessionID), &mcpserver.RequestProtocolInfo{
			Modern: true, ProtocolVersion: mcp.ProtocolVersion20260728, ClientCapabilities: &capabilities,
		})
	}

	require.True(t, marked(modern(api.ActorSessionPrefix+"abc", urlElicitationCapabilities())), "an agent's session with URL elicitation")
	require.False(t, marked(modern("family-1", urlElicitationCapabilities())), "a person's own client")
	require.False(t, marked(modern(api.ActorSessionPrefix+"abc", mcp.ClientCapabilities{})), "an agent whose client shows no links")
}
