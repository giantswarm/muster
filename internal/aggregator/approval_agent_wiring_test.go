package aggregator

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/credprovider"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// The Session binding's settings reach every request of the actor through
// the actor bearer handler: a binding an approval sender wrote carries the
// agent, its entries and grants into the request's context, and a blocked
// tool is refused; a binding without the sender carries none
// (M2-S3-C-01).
func TestActorBearerHandlerCarriesTheAgentsSettings(t *testing.T) {
	rig := agentRig(t)
	store := credprovider.NewMemoryStore()
	actor := "spiffe://substrate-actor.local/actor/kagent/session-a"
	var seen *api.AgentApproval
	var callErr error
	mcpHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCtx := httpRequestContext(r.Context(), r)
		seen = api.AgentApprovalFromContext(reqCtx)
		_, callErr = rig.server.CallToolInternal(reqCtx, "x_github_get_issue", map[string]any{})
		w.WriteHeader(http.StatusOK)
	})
	h := actorBearerHandler(testActorBearer(t, store), mcpHandler, http.NotFoundHandler(), nil, nil)
	call := func(header string) {
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.Header.Set("Authorization", "Bearer "+fakeActorJWT(testActorIssuer, "actor/kagent/session-a"))
		if header != "" {
			req.Header.Set(api.ToolApprovalHeader, header)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
	}
	settings := &api.AgentApproval{Entries: []api.AgentApprovalEntry{{Entry: "tool:x_github_get_issue", Setting: api.ApprovalBlock}}, Grants: []api.AgentApprovalGrant{mergeGrant}}

	require.NoError(t, store.Put(context.Background(), actor, credprovider.Binding{Subject: "alice", Session: "kagent/s1",
		Providers: []string{credprovider.MusterProvider}, ApprovalSender: true, Sender: "am", Agent: "kagent/alert-review", Approval: settings}, time.Hour))
	call("")
	require.NotNil(t, seen)
	assert.Equal(t, "kagent/alert-review", seen.Agent)
	assert.Equal(t, settings.Entries, seen.Entries)
	assert.Equal(t, settings.Grants, seen.Grants)
	require.EqualError(t, callErr, "tool not found: x_github_get_issue")

	call("tool:x_github_get_issue=Ask")
	assert.Equal(t, settings.Entries, seen.Entries, "a binding's settings win over the header")

	require.NoError(t, store.Delete(context.Background(), actor, "alice"))
	require.NoError(t, store.Put(context.Background(), actor, credprovider.Binding{Subject: "alice", Session: "kagent/s1",
		Providers: []string{credprovider.MusterProvider}, Agent: "kagent/alert-review", Approval: settings}, time.Hour))
	call("")
	assert.Nil(t, seen, "a binding without the sender carries no settings")
	require.NoError(t, callErr)
}

// X-Muster-Tool-Approval carries an agent's settings on requests no binding
// with a sender covers: they tighten; a value that cannot be parsed makes
// everything ask (fail closed); a person's request without it is unchanged.
func TestToolApprovalHeader(t *testing.T) {
	rig := agentRig(t)
	request := func(value string) context.Context {
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		if value != "" {
			req.Header.Set(api.ToolApprovalHeader, value)
		}
		return httpRequestContext(rig.ctx, req)
	}

	ctx := request("tool:x_github_get_issue=Block,server:github=Ask")
	_, err := rig.server.CallToolInternal(ctx, "x_github_get_issue", map[string]any{})
	require.EqualError(t, err, "tool not found: x_github_get_issue")

	ctx = request("tool:x_github_merge_pull_request=Allow")
	result, err := rig.server.CallToolInternal(ctx, "x_github_merge_pull_request", map[string]any{})
	require.NoError(t, err)
	approvalIDOf(t, result) // no grant from a header: the connector's Ask holds

	ctx = request("not a setting")
	result, err = rig.server.CallToolInternal(ctx, "x_github_get_issue", map[string]any{})
	require.NoError(t, err)
	approvalIDOf(t, result) // unparseable: default Ask

	result, err = rig.server.CallToolInternal(request(""), "x_github_get_issue", map[string]any{})
	require.NoError(t, err)
	assert.False(t, result.IsError)
}

// Regressions for the mutants that survived (M2-S3-C-11).
func TestAgentSettings_MutantsKilled(t *testing.T) {
	t.Run("equally specific entries: the stricter wins in either order", func(t *testing.T) {
		rig := agentRig(t)
		for _, entries := range [][]api.AgentApprovalEntry{
			{{Entry: "server:github", Setting: api.ApprovalAllow}, {Entry: "server:github-alias", Setting: api.ApprovalAsk}},
			{{Entry: "server:github", Setting: api.ApprovalAsk}, {Entry: "server:github-alias", Setting: api.ApprovalAllow}},
		} {
			target := api.AgentTarget{Kind: api.AgentTargetTool, Server: "github", Family: "github-alias", Original: "get_issue", Exposed: "x_github_get_issue"}
			setting, _ := (&api.AgentApproval{Entries: entries}).Decide(context.Background(), target)
			assert.Equal(t, api.ApprovalAsk, setting, "%v", entries)
		}
		_ = rig
	})
	t.Run("reporting settings never decide a call", func(t *testing.T) {
		rig := agentRig(t)
		ctx := api.WithAgentApproval(rig.ctx, &api.AgentApproval{Reporting: true, Entries: []api.AgentApprovalEntry{{Entry: "tool:x_github_get_issue", Setting: api.ApprovalBlock}}})
		result, err := rig.server.CallToolInternal(ctx, "x_github_get_issue", map[string]any{})
		require.NoError(t, err)
		assert.False(t, result.IsError)
		assert.Equal(t, 1, rig.client.count("get_issue"))
	})
	t.Run("a core tool the agent blocks is refused at the call", func(t *testing.T) {
		rig := agentRig(t)
		ctx := boundAgent(rig, &api.AgentApproval{Entries: []api.AgentApprovalEntry{{Entry: "tool:core_service_list", Setting: api.ApprovalBlock}}})
		_, _, err := rig.server.gateCoreTool(ctx, "core_service_list", map[string]any{})
		require.EqualError(t, err, "tool not found: core_service_list")
	})
	t.Run("an approval is bound to the agent", func(t *testing.T) {
		rig := agentRig(t)
		first := boundAgent(rig, &api.AgentApproval{})
		result, err := rig.server.CallToolInternal(first, "x_github_merge_pull_request", map[string]any{"pr": 3.0})
		require.NoError(t, err)
		id := approvalIDOf(t, result)
		require.NoError(t, rig.store.Decide(context.Background(), id, true, alice))
		other := api.WithAgentApproval(agentContext(rig, "alert-review-1"), &api.AgentApproval{Agent: "kagent/release-bot"})
		result, err = rig.server.CallToolInternal(other, "x_github_merge_pull_request", map[string]any{"pr": 3.0})
		require.NoError(t, err)
		assert.NotEqual(t, id, approvalIDOf(t, result), "another agent's call asks anew")
		assert.Zero(t, rig.client.count("merge_pull_request"))
	})
	t.Run("an agent's Ask on a workflow asks once up front", func(t *testing.T) {
		rig := newWorkflowRig(t)
		ctx := boundAgent(rig.gateRig, &api.AgentApproval{Entries: []api.AgentApprovalEntry{{Entry: "workflow:readonly", Setting: api.ApprovalAsk}}})
		result, err := rig.server.CallToolInternal(ctx, "workflow_readonly", map[string]any{})
		require.NoError(t, err)
		approvalIDOf(t, result)
		assert.Empty(t, rig.workflows.ran, "a workflow whose steps all run asks for the agent's own Ask")

		blocked := boundAgent(rig.gateRig, &api.AgentApproval{Entries: []api.AgentApprovalEntry{{Entry: "workflow:readonly", Setting: api.ApprovalBlock}}})
		_, err = rig.server.CallToolInternal(blocked, "workflow_readonly", map[string]any{})
		require.EqualError(t, err, "tool not found: workflow_readonly")
	})
}

// A tool: entry names the tool whatever name the call used: a family
// member's per-server name and the grouped name select the same tool
// (M2-S3-C-12).
func TestAgentSettings_EntriesMatchCanonicalNames(t *testing.T) {
	rig := agentRig(t)
	names := rig.server.registry.ToolNames("github", "get_issue")
	require.NotEmpty(t, names)
	target := rig.server.serverAgentTarget("github", "get_issue", "some_alias_name", nil)
	assert.Equal(t, names[0], target.Exposed, "the canonical name, not the alias")
	assert.Contains(t, target.Aliases, "some_alias_name")
	setting, _ := (&api.AgentApproval{Entries: []api.AgentApprovalEntry{{Entry: "tool:" + names[0], Setting: api.ApprovalBlock}}}).Decide(context.Background(), target)
	assert.Equal(t, api.ApprovalBlock, setting)
	setting, _ = (&api.AgentApproval{Entries: []api.AgentApprovalEntry{{Entry: "tool:some_alias_name", Setting: api.ApprovalBlock}}}).Decide(context.Background(), target)
	assert.Equal(t, api.ApprovalBlock, setting)
	_ = mcp.Tool{}
}

// A call a grant lets run is audited with the grant mark that lifted the
// connector's Ask (M2-S3-S-10).
func TestAgentSettings_GrantedCallIsAudited(t *testing.T) {
	var buf bytes.Buffer
	logging.InitForCLI(logging.LevelInfo, &buf)
	t.Cleanup(func() { logging.InitForCLI(logging.LevelInfo, io.Discard) })
	rig := agentRig(t)
	ctx := boundAgent(rig, &api.AgentApproval{Entries: []api.AgentApprovalEntry{{Entry: mergeGrant.Entry, Setting: api.ApprovalAllow}}, Grants: []api.AgentApprovalGrant{mergeGrant}})
	result, err := rig.server.CallToolInternal(ctx, "x_github_merge_pull_request", map[string]any{})
	require.NoError(t, err)
	require.False(t, result.IsError)
	line := lineWith(buf.String(), "[AUDIT] call_allowed_by_exception")
	require.NotEmpty(t, line)
	assert.Contains(t, line, "grant=tool:x_github_merge_pull_request@github")
	assert.Contains(t, line, "agent=kagent/alert-review")
}
