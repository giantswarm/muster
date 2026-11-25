package aggregator

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/approval"
)

var (
	alice = api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "alice"}
	bob   = api.SignInIdentity{Issuer: "https://dex.example.com", Subject: "bob"}
)

// countingMCPClient counts dispatches and keeps the last arguments.
type countingMCPClient struct {
	mockMCPClient
	mu    sync.Mutex
	calls map[string]int
	args  map[string]any
}

func (c *countingMCPClient) CallTool(ctx context.Context, name string, args map[string]any) (*mcp.CallToolResult, error) {
	c.mu.Lock()
	if c.calls == nil {
		c.calls = map[string]int{}
	}
	c.calls[name]++
	c.args = args
	c.mu.Unlock()
	return mcp.NewToolResultText("ran " + name), nil
}

func (c *countingMCPClient) count(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[name]
}

type gateRig struct {
	server *AggregatorServer
	client *countingMCPClient
	store  *approval.MemoryStore
	ctx    context.Context
}

// newGateRig registers github (merge asks, get_issue runs, delete is
// blocked) and makes approval available: an approval store, muster's
// browser sign-in and a public URL.
func newGateRig(t *testing.T) *gateRig {
	t.Helper()
	ctx := context.Background()
	server, err := NewAggregatorServer(ctx, AggregatorConfig{Host: "localhost", Port: 0}, nil)
	require.NoError(t, err)
	client := &countingMCPClient{mockMCPClient: mockMCPClient{tools: []mcp.Tool{
		{Name: "get_issue"}, {Name: "merge_pull_request"}, {Name: "delete_repository"},
	}}}
	require.NoError(t, server.RegisterServer(ctx, ServerRegistration{Name: "github"}, client))
	_ = server.registry.GetAllTools()
	registerApprovals(t, map[string]*api.MCPServerApproval{
		"github": {Rule: api.ApprovalAsk, Tools: map[string]string{"get_issue": api.ApprovalAllow, "delete_repository": api.ApprovalBlock}},
	})
	store := approval.NewMemoryStore()
	api.RegisterApprovalStore(store)
	api.RegisterBrowserIdentity(&stubBrowserIdentity{})
	t.Cleanup(func() {
		api.RegisterApprovalStore(nil)
		api.RegisterBrowserIdentity(nil)
	})
	server.approval.page = &approval.Page{PublicURL: "https://muster.example.com"}
	return &gateRig{server: server, client: client, store: store, ctx: personContext(alice, "session-1")}
}

func personContext(identity api.SignInIdentity, session string) context.Context {
	ctx := api.WithApprovalGate(context.Background())
	ctx = api.WithSessionID(ctx, session)
	ctx = api.WithSubject(ctx, identity.Subject)
	return api.WithSignInIdentity(ctx, identity)
}

// approvalIDOf returns the id in the approval link an answer carries.
func approvalIDOf(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	link := approvalLink(result)
	require.NotEmpty(t, link, "the call asks")
	require.True(t, result.IsError)
	parsed, err := url.Parse(link)
	require.NoError(t, err)
	assert.Equal(t, "muster.example.com", parsed.Host)
	return parsed.Path[len(approval.PathPrefix):]
}

func TestGate_AllowDispatchesBlockIsNotFound(t *testing.T) {
	rig := newGateRig(t)
	result, err := rig.server.CallToolInternal(rig.ctx, "x_github_get_issue", map[string]any{"id": 1.0})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, 1, rig.client.count("get_issue"))

	_, err = rig.server.CallToolInternal(rig.ctx, "x_github_delete_repository", map[string]any{})
	require.EqualError(t, err, "tool not found: x_github_delete_repository")
	assert.Zero(t, rig.client.count("delete_repository"))
}

func TestGate_AskApproveRunsOnce(t *testing.T) {
	rig := newGateRig(t)
	args := map[string]any{"pr": 42.0, "method": "squash"}

	result, err := rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", args)
	require.NoError(t, err)
	id := approvalIDOf(t, result)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "approval_required: Confirm Merge pull request in github.")
	assert.Zero(t, rig.client.count("merge_pull_request"), "asking runs nothing")

	again, err := rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", map[string]any{"method": "squash", "pr": 42.0})
	require.NoError(t, err)
	assert.Equal(t, id, approvalIDOf(t, again), "an identical call reuses the pending approval")

	request, err := rig.store.Get(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, `{"method":"squash","pr":42}`, request.Arguments)
	assert.Equal(t, api.ApprovalTarget{Kind: api.ApprovalTargetTool, Server: "github", Name: "merge_pull_request", Exposed: "x_github_merge_pull_request"}, request.Target)
	assert.Equal(t, alice, request.Identity)
	assert.Equal(t, "Merge pull request", request.Display.Title)
	assert.Equal(t, "connector", request.Display.Source)
	assert.NotEqual(t, request.ID, request.ElicitationID)

	require.NoError(t, rig.store.Decide(context.Background(), id, true, alice))
	result, err = rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", args)
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, 1, rig.client.count("merge_pull_request"))
	assert.Equal(t, map[string]any{"method": "squash", "pr": 42.0}, rig.client.args, "the approved arguments are dispatched")

	// Single use: the next call asks again, with a new link.
	result, err = rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", args)
	require.NoError(t, err)
	assert.NotEqual(t, id, approvalIDOf(t, result))
	assert.Equal(t, 1, rig.client.count("merge_pull_request"))
}

func TestGate_Decline(t *testing.T) {
	rig := newGateRig(t)
	result, err := rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", map[string]any{"pr": 1.0})
	require.NoError(t, err)
	require.NoError(t, rig.store.Decide(context.Background(), approvalIDOf(t, result), false, alice))

	result, err = rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", map[string]any{"pr": 1.0})
	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, result.Content[0].(mcp.TextContent).Text, "declined")
	assert.Zero(t, rig.client.count("merge_pull_request"))
}

// An approval runs only the call it was given for: other arguments, another
// person or Session, or a changed connector ask anew.
func TestGate_ApprovalIsBound(t *testing.T) {
	rig := newGateRig(t)
	result, err := rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", map[string]any{"pr": 1.0})
	require.NoError(t, err)
	id := approvalIDOf(t, result)
	require.NoError(t, rig.store.Decide(context.Background(), id, true, alice))

	for name, call := range map[string]func() (*mcp.CallToolResult, error){
		"other arguments": func() (*mcp.CallToolResult, error) {
			return rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", map[string]any{"pr": 2.0})
		},
		"another person": func() (*mcp.CallToolResult, error) {
			// The binding covers the person: bob on alice's Session id.
			ctx := api.WithSignInIdentity(api.WithSessionID(api.WithApprovalGate(context.Background()), "session-1"), bob)
			return rig.server.CallToolInternal(ctx, "x_github_merge_pull_request", map[string]any{"pr": 1.0})
		},
		"another session": func() (*mcp.CallToolResult, error) {
			return rig.server.CallToolInternal(personContext(alice, "session-2"), "x_github_merge_pull_request", map[string]any{"pr": 1.0})
		},
	} {
		result, err := call()
		require.NoError(t, err, name)
		assert.NotEqual(t, id, approvalIDOf(t, result), name)
	}
	assert.Zero(t, rig.client.count("merge_pull_request"))

	manager := api.GetMCPServerManager().(*stubMCPServerManager)
	info := manager.servers["github"]
	info.Generation = 7
	manager.servers["github"] = info
	result, err = rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", map[string]any{"pr": 1.0})
	require.NoError(t, err)
	assert.NotEqual(t, id, approvalIDOf(t, result), "a changed connector asks again")
	assert.Zero(t, rig.client.count("merge_pull_request"))
}

func TestGate_ConcurrentRetriesConsumeOnce(t *testing.T) {
	rig := newGateRig(t)
	result, err := rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", map[string]any{"pr": 1.0})
	require.NoError(t, err)
	require.NoError(t, rig.store.Decide(context.Background(), approvalIDOf(t, result), true, alice))

	var ran atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			result, err := rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", map[string]any{"pr": 1.0})
			if err == nil && !result.IsError {
				ran.Add(1)
			}
		})
	}
	wg.Wait()
	assert.Equal(t, int32(1), ran.Load())
	assert.Equal(t, 1, rig.client.count("merge_pull_request"))
}

// Without a prerequisite every call that asks is refused with the reason;
// allowed calls still run.
func TestGate_MissingPrerequisitesRefuse(t *testing.T) {
	cases := map[string]func(*gateRig) context.Context{
		"no shared store":       func(*gateRig) context.Context { api.RegisterApprovalStore(nil); return nil },
		"no browser sign-in":    func(*gateRig) context.Context { api.RegisterBrowserIdentity(nil); return nil },
		"no public URL":         func(rig *gateRig) context.Context { rig.server.approval.page = nil; return nil },
		"no person on the call": func(*gateRig) context.Context { return personContext(api.SignInIdentity{}, "session-1") },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			rig := newGateRig(t)
			ctx := rig.ctx
			if replaced := breakIt(rig); replaced != nil {
				ctx = replaced
			}
			_, err := rig.server.CallToolInternal(ctx, "x_github_merge_pull_request", map[string]any{"pr": 1.0})
			require.ErrorContains(t, err, "approval_unavailable: x_github_merge_pull_request asks before it runs (ask from connector)")
			assert.Zero(t, rig.client.count("merge_pull_request"))
			_, err = rig.server.CallToolInternal(ctx, "x_github_get_issue", map[string]any{})
			require.NoError(t, err)
		})
	}
}

type failingStore struct{ approval.MemoryStore }

func (*failingStore) Consume(context.Context, string) (*api.ApprovalRequest, error) {
	return nil, errors.New("valkey unavailable")
}

func TestGate_StoreOutageRefuses(t *testing.T) {
	rig := newGateRig(t)
	api.RegisterApprovalStore(&failingStore{})
	_, err := rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", map[string]any{"pr": 1.0})
	require.ErrorContains(t, err, "approval store cannot be reached")
	assert.Zero(t, rig.client.count("merge_pull_request"))
}

// muster's own calls (outside a client's call_tool) are not asked; Block
// still holds for them.
func TestGate_InternalCallsAreNotAsked(t *testing.T) {
	rig := newGateRig(t)
	ctx := api.WithSignInIdentity(context.Background(), alice)
	_, err := rig.server.CallToolInternal(ctx, "x_github_merge_pull_request", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, 1, rig.client.count("merge_pull_request"))
	_, err = rig.server.CallToolInternal(ctx, "x_github_delete_repository", map[string]any{})
	require.EqualError(t, err, "tool not found: x_github_delete_repository")
}

// muster's own configuration asks every caller; the floors ask agents only.
func TestGate_CoreToolsAndFloors(t *testing.T) {
	rig := newGateRig(t)
	result, err := rig.server.CallToolInternal(rig.ctx, "core_config_save", map[string]any{})
	require.NoError(t, err)
	approvalIDOf(t, result)

	agent := api.WithSessionID(rig.ctx, api.ActorSessionPrefix+"session-1")
	result, err = rig.server.CallToolInternal(agent, "core_mcpserver_set_approval", map[string]any{"name": "github", "preset": "ask-all"})
	require.NoError(t, err)
	id := approvalIDOf(t, result)
	request, err := rig.store.Get(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, api.CallerKindActor, request.CallerKind)
	assert.Equal(t, "builtin", request.Display.Source)
	assert.Contains(t, request.Display.Message, "An agent wants to run")
	assert.NotEmpty(t, request.Display.Preset, "a preset shows what it writes")
}

// The approval answer reaches a client that declared URL elicitation as an
// elicitation: input_required with only the random id as requestState, or
// -32042 with exactly one elicitation, its elicitationId distinct from the id.
func TestGate_ApprovalElicitation(t *testing.T) {
	rig := newGateRig(t)
	call := func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return rig.server.CallToolInternal(ctx, "x_github_merge_pull_request", map[string]any{"pr": 1.0})
	}

	t.Run("2026-07-28", func(t *testing.T) {
		capabilities := urlElicitationCapabilities()
		ctx := mcpserver.WithRequestProtocolInfo(rig.ctx, &mcpserver.RequestProtocolInfo{Modern: true, ProtocolVersion: mcp.ProtocolVersion20260728, ClientCapabilities: &capabilities})
		result, err := rig.server.urlElicitation()(call)(ctx, mcp.CallToolRequest{})
		require.NoError(t, err)
		require.Equal(t, mcp.ResultTypeInputRequired, result.ResultType)
		request := result.InputRequests[approvalInputKey]
		require.NotNil(t, request.Elicitation)
		assert.Equal(t, mcp.ElicitationModeURL, request.Elicitation.Mode)
		assert.Equal(t, "https://muster.example.com/approvals/"+result.RequestState, request.Elicitation.URL)
		assert.Equal(t, "Confirm Merge pull request in github.", request.Elicitation.Message)
		assert.NotEmpty(t, request.Elicitation.ElicitationID)
		assert.NotEqual(t, result.RequestState, request.Elicitation.ElicitationID)

		// Declining the prompt runs nothing; accepting goes on to the gate.
		var retry mcp.CallToolRequest
		retry.Params.InputResponses = mcp.InputResponses{approvalInputKey: mcp.NewElicitationInputResponse(mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionDecline}})}
		declined, err := rig.server.urlElicitation()(call)(ctx, retry)
		require.NoError(t, err)
		assert.True(t, declined.IsError)

		retry.Params.InputResponses = mcp.InputResponses{approvalInputKey: mcp.NewElicitationInputResponse(mcp.ElicitationResult{ElicitationResponse: mcp.ElicitationResponse{Action: mcp.ElicitationResponseActionAccept}})}
		retry.Params.RequestState = result.RequestState
		again, err := rig.server.urlElicitation()(call)(ctx, retry)
		require.NoError(t, err)
		assert.Equal(t, mcp.ResultTypeInputRequired, again.ResultType, "the client's accept is no approval")
		assert.Zero(t, rig.client.count("merge_pull_request"))
	})

	t.Run("2025-11-25", func(t *testing.T) {
		session := newLegacySession("transport-1", urlElicitationCapabilities())
		ctx := mcpserver.NewMCPServer("muster", "test").WithContext(rig.ctx, session)
		result, err := rig.server.urlElicitation()(call)(ctx, mcp.CallToolRequest{})
		require.Nil(t, result)
		var required mcp.URLElicitationRequiredError
		require.True(t, errors.As(err, &required))
		require.Len(t, required.Elicitations, 1)
		e := required.Elicitations[0]
		assert.Equal(t, mcp.ElicitationModeURL, e.Mode)
		assert.Contains(t, e.URL, "https://muster.example.com/approvals/")
		assert.NotEmpty(t, e.ElicitationID)
		assert.NotContains(t, e.URL, e.ElicitationID)
		require.NoError(t, e.Validate())
	})

	t.Run("without URL elicitation the text fallback", func(t *testing.T) {
		result, err := rig.server.urlElicitation()(call)(rig.ctx, mcp.CallToolRequest{})
		require.NoError(t, err)
		require.True(t, result.IsError)
		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(t, text, "approval_required: ")
		assert.Contains(t, text, "then run the tool again.")
	})
}

// A tool that asks carries why muster cannot ask here, so a client can say
// why its calls are refused; once approval is available it carries nothing.
func TestApplyApprovals_ReportsWhyAskingIsUnavailable(t *testing.T) {
	rig := newGateRig(t)
	tools := rig.server.applyApprovals(rig.ctx, []mcp.Tool{approvalSoloTool("github", "merge_pull_request"), approvalSoloTool("github", "get_issue")})
	for _, tool := range tools {
		approval, _ := api.ToolApprovalOf(tool)
		assert.Empty(t, approval.Unavailable, tool.Name)
	}

	api.RegisterApprovalStore(nil)
	tools = rig.server.applyApprovals(rig.ctx, []mcp.Tool{approvalSoloTool("github", "merge_pull_request"), approvalSoloTool("github", "get_issue")})
	merge, _ := api.ToolApprovalOf(tools[0])
	assert.Contains(t, merge.Unavailable, "not on Valkey")
	read, _ := api.ToolApprovalOf(tools[1])
	assert.Empty(t, read.Unavailable, "a tool that runs is unaffected")
}
