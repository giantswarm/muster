package aggregator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

func TestCompleteApprovalElicitations(t *testing.T) {
	srv := mcpserver.NewMCPServer("muster", "test")
	asked := newLegacySession("transport-asked", urlElicitationCapabilities())
	other := newLegacySession("transport-other", urlElicitationCapabilities())
	require.NoError(t, srv.RegisterSession(t.Context(), asked))
	require.NoError(t, srv.RegisterSession(t.Context(), other))
	a := &AggregatorServer{mcpServer: srv}
	expires := time.Now().Add(time.Minute)
	a.approvalElicitations.add("req-1", asked.id, "el-1", expires)
	a.approvalElicitations.add("req-1", asked.id, "el-1", expires)
	a.approvalElicitations.add("req-2", other.id, "el-2", expires)

	a.completeApprovalElicitations(&api.ApprovalRequest{ID: "req-1", ElicitationID: "el-1"})

	require.Len(t, asked.ch, 1, "a session asked twice is told once")
	notification := <-asked.ch
	require.Equal(t, string(mcp.MethodNotificationElicitationComplete), notification.Method)
	require.Equal(t, map[string]any{"elicitationId": "el-1"}, notification.Params.AdditionalFields,
		"only the elicitationId: no decision, no approval id")
	require.Empty(t, other.ch, "only the session that was asked is told, and only about its own request")

	a.completeApprovalElicitations(&api.ApprovalRequest{ID: "req-1"})
	require.Empty(t, asked.ch, "an elicitation completes once")
}

func TestApprovalElicitationsIgnoreWhatCannotComplete(t *testing.T) {
	var s approvalElicitations
	s.add("req-1", "", "el-1", time.Now().Add(time.Minute))
	s.add("req-1", "t", "el-1", time.Now().Add(-time.Second))
	require.Empty(t, s.take("req-1"), "no transport session, or an expired request, completes nothing")
}

// The -32042 answer to a gated call records the asked transport session, so
// the decision of that request completes the elicitation it carries, for that
// session only.
func TestGate_ApprovalDecisionCompletesTheAskedSession(t *testing.T) {
	rig := newGateRig(t)
	srv := mcpserver.NewMCPServer("muster", "test")
	rig.server.mcpServer = srv
	asked := newLegacySession("transport-1", urlElicitationCapabilities())
	other := newLegacySession("transport-2", urlElicitationCapabilities())
	require.NoError(t, srv.RegisterSession(t.Context(), asked))
	require.NoError(t, srv.RegisterSession(t.Context(), other))
	call := func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return rig.server.CallToolInternal(ctx, "x_github_merge_pull_request", map[string]any{"pr": 1.0})
	}

	_, err := rig.server.urlElicitation()(call)(srv.WithContext(rig.ctx, asked), mcp.CallToolRequest{})
	var required mcp.URLElicitationRequiredError
	require.True(t, errors.As(err, &required))
	elicitation := required.Elicitations[0]
	id := elicitation.URL[strings.LastIndex(elicitation.URL, "/")+1:]
	request, err := rig.store.Get(t.Context(), id)
	require.NoError(t, err)

	rig.server.completeApprovalElicitations(request)

	require.Len(t, asked.ch, 1)
	notification := <-asked.ch
	require.Equal(t, string(mcp.MethodNotificationElicitationComplete), notification.Method)
	require.Equal(t, elicitation.ElicitationID, notification.Params.AdditionalFields["elicitationId"])
	require.Empty(t, other.ch)
}
