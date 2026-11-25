package aggregator

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
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
	"github.com/giantswarm/muster/v5/pkg/logging"
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
	ctx := context.Background()
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
			ctx := api.WithSignInIdentity(api.WithSessionID(context.Background(), "session-1"), bob)
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

// The gate is on by default: a call that reaches dispatch without passing
// call_tool asks like any other (a new call path cannot forget it). Only a
// call muster explicitly marks its own is not asked; Block still holds for
// it.
func TestGate_OnByDefault(t *testing.T) {
	rig := newGateRig(t)
	unmarked := api.WithSessionID(api.WithSignInIdentity(context.Background(), alice), "session-1")
	result, err := rig.server.CallToolInternal(unmarked, "x_github_merge_pull_request", map[string]any{})
	require.NoError(t, err)
	approvalIDOf(t, result)
	assert.Zero(t, rig.client.count("merge_pull_request"), "an unmarked call asks")

	own := api.WithoutApprovalGate(unmarked)
	_, err = rig.server.CallToolInternal(own, "x_github_merge_pull_request", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, 1, rig.client.count("merge_pull_request"))
	_, err = rig.server.CallToolInternal(own, "x_github_delete_repository", map[string]any{})
	require.EqualError(t, err, "tool not found: x_github_delete_repository")
}

// A registered server whose MCPServer object the manager no longer finds
// (deleted, deselected) is refused, Allow and Block tools alike, and listed
// as unavailable: its settings cannot be known (security M1-MU-S-01).
func TestGate_ServerObjectMissingFailsClosed(t *testing.T) {
	rig := newGateRig(t)
	manager := api.GetMCPServerManager().(*stubMCPServerManager)
	delete(manager.servers, "github")

	for _, tool := range []string{"x_github_get_issue", "x_github_merge_pull_request", "x_github_delete_repository"} {
		_, err := rig.server.CallToolInternal(rig.ctx, tool, map[string]any{})
		require.ErrorContains(t, err, "its MCPServer object cannot be found", tool)
	}
	assert.Zero(t, rig.client.count("get_issue")+rig.client.count("merge_pull_request")+rig.client.count("delete_repository"))

	tools := rig.server.applyApprovals(rig.ctx, []mcp.Tool{approvalSoloTool("github", "get_issue")})
	require.Len(t, tools, 1)
	reported, _ := api.ToolApprovalOf(tools[0])
	assert.Equal(t, api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceUnavailable}, reported)
}

// approvalSchemaManager is a manager whose served CRD may not know
// spec.approval.
type approvalSchemaManager struct {
	*stubMCPServerManager
	problem string
}

func (m approvalSchemaManager) ApprovalSchemaProblem(context.Context) string { return m.problem }

// A CRD that does not know spec.approval makes every server tool refused
// with the reason and reported unavailable, never Allow (M1-MU-C-02, S-04).
func TestGate_StaleCRDRefuses(t *testing.T) {
	rig := newGateRig(t)
	stub := api.GetMCPServerManager().(*stubMCPServerManager)
	api.RegisterMCPServerManager(approvalSchemaManager{stubMCPServerManager: stub, problem: "the MCPServer CRD in this cluster does not know spec.approval"})
	t.Cleanup(func() { api.RegisterMCPServerManager(stub) })

	_, err := rig.server.CallToolInternal(rig.ctx, "x_github_get_issue", map[string]any{})
	require.ErrorContains(t, err, "approval_unavailable: the MCPServer CRD in this cluster does not know spec.approval")
	assert.Zero(t, rig.client.count("get_issue"))

	tools := rig.server.applyApprovals(rig.ctx, []mcp.Tool{approvalSoloTool("github", "get_issue")})
	reported, _ := api.ToolApprovalOf(tools[0])
	assert.Equal(t, api.ApprovalSettingAsk, reported.Setting)
	assert.Contains(t, reported.Unavailable, "does not know spec.approval")
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

// A preset is expanded once, when the call asks; the person approves that
// expansion and it is what runs. A server that marks a tool read-only
// between the page and the retry makes the retry another call, which asks
// again (M1-MU-S-07).
func TestGate_PresetIsBoundToItsExpansion(t *testing.T) {
	rig := newGateRig(t)
	agent := api.WithSessionID(rig.ctx, api.ActorSessionPrefix+"session-1")
	args := map[string]any{"name": "github", "preset": "recommended"}
	result, err := rig.server.CallToolInternal(agent, "core_mcpserver_set_approval", args)
	require.NoError(t, err)
	id := approvalIDOf(t, result)
	request, err := rig.store.Get(context.Background(), id)
	require.NoError(t, err)
	assert.NotContains(t, request.Arguments, "preset", "the expansion is bound, not the preset's name")
	assert.Contains(t, request.Arguments, `"merge_pull_request":"Ask"`)
	require.NoError(t, rig.store.Decide(context.Background(), id, true, alice))

	info, ok := rig.server.registry.GetServerInfo("github")
	require.True(t, ok)
	readOnly := true
	info.mu.Lock()
	for i := range info.Tools {
		info.Tools[i].Annotations.ReadOnlyHint = &readOnly
	}
	info.mu.Unlock()

	again, err := rig.server.CallToolInternal(agent, "core_mcpserver_set_approval", args)
	require.NoError(t, err)
	assert.NotEqual(t, id, approvalIDOf(t, again), "another expansion is another call")
}

// On Governed, every way an agent pauses or removes a connector asks
// (M1-MU-S-03).
func TestGate_GovernedAsksBeforePausingOrRemoving(t *testing.T) {
	api.RegisterGovernance(api.Governance{Mode: api.GovernanceGoverned, Readable: true})
	t.Cleanup(func() { api.RegisterGovernance(api.Governance{}) })
	rig := newGateRig(t)
	for i, call := range []struct {
		tool string
		args map[string]any
	}{
		{"core_service_stop", map[string]any{"name": "github"}},
		{"core_mcpserver_delete", map[string]any{"name": "github"}},
		{"core_mcpserver_update", map[string]any{"name": "github", "suspended": true}},
		{"core_mcpserver_update", map[string]any{"name": "github", "autoStart": false}},
	} {
		agent := api.WithSessionID(rig.ctx, fmt.Sprintf("%ssession-%d", api.ActorSessionPrefix, i))
		result, err := rig.server.CallToolInternal(agent, call.tool, call.args)
		require.NoError(t, err, call.tool)
		id := approvalIDOf(t, result)
		request, err := rig.store.Get(context.Background(), id)
		require.NoError(t, err)
		assert.Equal(t, api.ApprovalSourceGovernance, request.Display.Source, call.tool)
	}
}

// Every approval event carries who in full, the caller kind and the approval
// id, so a request, its decision and its use join on one id; Block
// refusals are audit events too (M1-MU-S-10, M1-MU-C-16).
func TestGate_AuditCarriesTheApprovalID(t *testing.T) {
	var buf bytes.Buffer
	logging.InitForCLI(logging.LevelInfo, &buf)
	t.Cleanup(func() { logging.InitForCLI(logging.LevelInfo, io.Discard) })
	rig := newGateRig(t)
	result, err := rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", map[string]any{"pr": 1.0})
	require.NoError(t, err)
	id := approvalIDOf(t, result)
	require.NoError(t, rig.store.Decide(context.Background(), id, true, alice))
	_, err = rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", map[string]any{"pr": 1.0})
	require.NoError(t, err)
	_, _ = rig.server.CallToolInternal(rig.ctx, "x_github_delete_repository", map[string]any{})

	logs := buf.String()
	for _, action := range []string{"approval_requested", "approval_used"} {
		line := lineWith(logs, "[AUDIT] "+action)
		require.NotEmpty(t, line, action)
		assert.Contains(t, line, "approval="+id, action)
		assert.Contains(t, line, "subject=alice", action)
		assert.Contains(t, line, "issuer=https://dex.example.com", action)
		assert.Contains(t, line, `callerKind="person bearer"`, action)
	}
	assert.Contains(t, lineWith(logs, "[AUDIT] call_refused"), "blocked by the server's approval settings")
}

func lineWith(logs, needle string) string {
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

// A connector whose rule is Block hides and refuses every tool without its
// own entry -- new tools included -- like a tool's own Block, in listings,
// calls and workflows; a Block beats any floor (decision #74).
func TestGate_BlockRule(t *testing.T) {
	rig := newGateRig(t)
	rig.setSettings(t, &api.MCPServerApproval{Rule: api.ApprovalBlock, Tools: map[string]string{"get_issue": api.ApprovalAllow}})

	_, err := rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", map[string]any{})
	require.EqualError(t, err, "tool not found: x_github_merge_pull_request")
	_, err = rig.server.CallToolInternal(rig.ctx, "x_github_get_issue", map[string]any{})
	require.NoError(t, err, "a tool's own entry wins")

	tools := rig.server.applyApprovals(rig.ctx, []mcp.Tool{approvalSoloTool("github", "merge_pull_request"), approvalSoloTool("github", "get_issue")})
	require.Len(t, tools, 1)
	assert.Equal(t, "x_github_get_issue", tools[0].Name)
	assert.Zero(t, rig.client.count("merge_pull_request"))
}

func (rig *gateRig) setSettings(t *testing.T, settings *api.MCPServerApproval) {
	t.Helper()
	manager := api.GetMCPServerManager().(*stubMCPServerManager)
	info := manager.servers["github"]
	info.Approval = settings
	manager.servers["github"] = info
}

// A call that asks on a server the session must first sign in to is
// answered with the sign-in, before an approval is asked for or used: the
// person is not asked twice for one call (M1-MU-C-09).
func TestGate_SignInBeforeApproval(t *testing.T) {
	rig := newGateRig(t)
	require.NoError(t, rig.server.registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "hubspot", ToolPrefix: "hubspot"},
		URL:                "http://127.0.0.1:1/mcp",
		AuthInfo:           &AuthInfo{Issuer: "https://idp.example.com", Scope: "mcp:read"},
		AuthConfig:         &api.MCPServerAuth{Type: "oauth"},
	}))
	answer, err, needed := rig.server.signInFirst(rig.ctx, "hubspot", "create_contact", map[string]any{})
	require.True(t, needed)
	if err == nil {
		require.NotNil(t, answer)
	}
	_, _, needed = rig.server.signInFirst(rig.ctx, "github", "merge_pull_request", map[string]any{})
	assert.False(t, needed, "a server without per-session sign-in asks as before")
}
