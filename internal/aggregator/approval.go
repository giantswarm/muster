package aggregator

import (
	"context"
	"sync"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/clock"
	"github.com/giantswarm/muster/v5/internal/toolset"
	"github.com/giantswarm/muster/v5/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// Approval settings (spec.approval of an MCPServer) are read from the
// definition source through the registered MCPServer manager, live, like
// spec.suspended (suspended.go): the aggregator keeps no copy, so a change
// reaches the next listing and the next call of every session without a
// restart of the server. Without a manager (servers registered directly, as
// in tests) a server has no settings: its tools are Allow, not reviewed. A
// registered server the manager does not find (its object deleted, its
// labels or namespace no longer served, while the registry entry outlives
// it) is refused, never Allow: its settings cannot be known. While the
// served CRD does not know spec.approval every server tool is refused
// (api.ApprovalSchemaProblem).
//
// A blocked tool is absent from the caller's catalogue (so from list_tools,
// filter_tools and describe_tool) and a call to it is refused as not found,
// at dispatch (approval_gate.go), so a workflow step cannot reach it either.

// connectorApprovals lists the approval settings of every MCPServer once,
// keyed by server key. managed is false without a manager: no server has
// settings then. ok is false when the listing failed: no tool's setting is
// known then.
func connectorApprovals(ctx context.Context) (approvals map[string]*api.MCPServerApproval, uids map[string]string, managed, ok bool) {
	manager := api.GetMCPServerManager()
	if manager == nil {
		return nil, nil, false, true
	}
	definitions, err := manager.ListMCPServers(ctx)
	if err != nil {
		logging.Warn("Aggregator", "approval settings unavailable: listing MCPServers failed: %v", err)
		return nil, nil, true, false
	}
	approvals = make(map[string]*api.MCPServerApproval, len(definitions))
	uids = make(map[string]string, len(definitions))
	for _, definition := range definitions {
		approvals[definition.ServerKey()] = definition.Approval
		uids[definition.ServerKey()] = definition.UID
	}
	return approvals, uids, true, true
}

// applyApprovals records on each tool of the caller's catalogue its
// effective approval and returns the catalogue without the tools a server
// blocks. A family tool resolves per member: the members that block it are
// dropped from it (its instance argument no longer offers them), it is
// dropped when all of them do, and it reports the strictest setting of the
// members left. muster's own configuration tools report their built-in Ask;
// for an agent caller the floors and, on Governed, the governed default are
// reported too; every other core tool is Allow, not reviewed. A workflow
// reports its strictest step.
//
// When the settings cannot be read (the listing failed, the server's object
// is missing, the CRD does not know spec.approval), every affected server
// tool is kept and reported as Ask from source "unavailable" (fail closed in
// what is reported; a call is refused at dispatch).
func (a *AggregatorServer) applyApprovals(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
	out := a.resolveApprovals(ctx, tools)
	a.checkSafeLists(ctx)
	reason := a.approvalUnavailable(ctx)
	if reason == "" && anyAsks(out) {
		// A store that is registered but cannot be reached refuses every
		// call that asks; the listing says so too (lab M1-3).
		if !a.approvalStoreReachable(ctx) {
			reason = "muster's approval store cannot be reached"
		}
	}
	if reason != "" {
		for i := range out {
			if approval, ok := api.ToolApprovalOf(out[i]); ok && approval.Setting == api.ApprovalSettingAsk {
				approval.Unavailable = reason
				api.SetToolApproval(&out[i], approval)
			}
		}
	}
	return out
}

// storePingInterval is how long a store check of a listing holds: listings
// are frequent, and one command each would add to every listing's cost.
const storePingInterval = 10 * time.Second

// storeReachability remembers the last check of the approval store.
type storeReachability struct {
	mu        sync.Mutex
	checkedAt time.Time
	reachable bool
}

// approvalStoreReachable checks the registered store at most once per
// storePingInterval.
func (a *AggregatorServer) approvalStoreReachable(ctx context.Context) bool {
	r := a.approval.reach
	if r == nil {
		return api.GetApprovalStore().Ping(ctx) == nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := clock.Now()
	if !r.checkedAt.IsZero() && now.Sub(r.checkedAt) < storePingInterval {
		return r.reachable
	}
	r.checkedAt, r.reachable = now, api.GetApprovalStore().Ping(ctx) == nil
	return r.reachable
}

// anyAsks reports whether a tool of the listing asks.
func anyAsks(tools []mcp.Tool) bool {
	for _, tool := range tools {
		if approval, ok := api.ToolApprovalOf(tool); ok && approval.Setting == api.ApprovalSettingAsk {
			return true
		}
	}
	return false
}

// resolveApprovals is applyApprovals without the availability of asking.
func (a *AggregatorServer) resolveApprovals(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
	l := &approvalListing{readable: true, a: a}
	l.unavailable = api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceUnavailable}
	if problem := api.ApprovalSchemaProblem(ctx); problem != "" {
		l.readable = false
		l.unavailable.Unavailable = problem
		l.listed = true
	}
	// What a call would do for this caller: an agent caller gets the floors
	// and, on Governed, the governed default reported too (M1-MU-C-08).
	// A listing reporting for an agent's settings (filter_tools agentTools)
	// reports them the same way.
	l.agent = isAgentCallerOrReport(ctx)
	l.governance = api.GetGovernance()
	// What an agent's own entries block is listed when reporting for its
	// settings, so an editor can show the Block and lift it; a connector's
	// Block stays hidden from everyone.
	keepAgentBlocks := api.ReportingAsAgent(ctx)
	hidden := func(approval api.ToolApproval) bool {
		return approval.Setting == api.ApprovalSettingBlock && (!keepAgentBlocks || approval.Source != api.ApprovalSourceAgent)
	}

	out := tools[:0:0]
	for _, tool := range tools {
		if builtin, ok := api.BuiltinApproval(tool.Name); ok {
			reported := withAgentCoreSetting(ctx, builtin, tool.Name)
			if hidden(reported) {
				continue
			}
			api.SetToolApproval(&tool, reported)
			out = append(out, tool)
			continue
		}
		switch toolset.KindOf(tool) {
		case toolset.KindEntryWorkflow:
			if reported, ok := a.workflowReport(ctx, l, tool.Name); ok {
				if hidden(reported) {
					continue
				}
				api.SetToolApproval(&tool, reported)
			}
			out = append(out, tool)
			continue
		case toolset.KindEntryCore:
			reported := l.core(ctx, tool.Name)
			if hidden(reported) {
				continue
			}
			api.SetToolApproval(&tool, reported)
			out = append(out, tool)
			continue
		}

		l.list(ctx)
		servers, original, ok := a.approvalTarget(tool)
		if !l.readable || !ok {
			api.SetToolApproval(&tool, l.unavailable)
			out = append(out, tool)
			continue
		}

		var kept []string
		var settings []api.ToolApproval
		missing := false
		for _, server := range servers {
			setting, found := l.server(ctx, server, original, &tool)
			if !found {
				missing = true
				break
			}
			if hidden(setting) {
				continue
			}
			kept = append(kept, server)
			settings = append(settings, setting)
		}
		if missing {
			api.SetToolApproval(&tool, l.unavailable)
			out = append(out, tool)
			continue
		}
		if len(kept) == 0 {
			continue
		}
		if len(kept) < len(servers) {
			narrowFamilyMembers(&tool, kept)
		}
		api.SetToolApproval(&tool, api.StrictestApproval(settings...))
		out = append(out, tool)
	}
	return out
}

// approvalListing resolves the approvals of one listing for its caller,
// reading the connectors' settings once.
type approvalListing struct {
	a           *AggregatorServer
	approvals   map[string]*api.MCPServerApproval
	uids        map[string]string
	managed     bool
	readable    bool
	listed      bool
	unavailable api.ToolApproval
	agent       bool
	governance  api.Governance
}

func (l *approvalListing) list(ctx context.Context) {
	if !l.listed {
		l.approvals, l.uids, l.managed, l.readable = connectorApprovals(ctx)
		l.listed = true
	}
}

// server resolves a server's own tool for the caller; found is false for a
// server the manager does not know. tool is the listed tool, nil when the
// listing resolves a workflow step.
func (l *approvalListing) server(ctx context.Context, server, original string, tool *mcp.Tool) (api.ToolApproval, bool) {
	written, found := l.approvals[server]
	if l.managed && !found {
		return api.ToolApproval{}, false
	}
	setting := api.ResolveConnectorApproval(written, original)
	if setting.Setting == api.ApprovalSettingBlock {
		return setting, true
	}
	exposed := ""
	if tool != nil {
		exposed = tool.Name
	}
	if l.a != nil {
		setting = l.a.withAgentSetting(ctx, setting, server, original, exposed, l.uids[server], tool)
	}
	if setting.Setting == api.ApprovalSettingBlock || !l.agent {
		return setting, true
	}
	entry, applies, refused := l.governance.ServerEntry(server, original)
	switch {
	case refused:
		// Refused to agents when called (a floor of a role no governed
		// server holds): reported as blocked by governance, so it leaves
		// an agent's listing like a Block; the call names the reason.
		return api.ToolApproval{Setting: api.ApprovalSettingBlock, Source: api.ApprovalSourceGovernance}, true
	case applies:
		return api.StrictestApproval(setting, entry), true
	}
	return setting, true
}

// core resolves one of muster's core tools for the caller.
func (l *approvalListing) core(ctx context.Context, name string) api.ToolApproval {
	effective := withAgentCoreSetting(ctx, api.ToolApproval{Setting: api.ApprovalSettingAllow, Source: api.ApprovalSourceUnreviewed}, name)
	if effective.Setting == api.ApprovalSettingBlock {
		return effective
	}
	if l.agent {
		if entry, ok := l.governance.CoreEntry(coreName(name)); ok {
			return api.StrictestApproval(entry, effective)
		}
	}
	return effective
}

// approvalTarget returns the servers a server tool runs on (one, or a
// family's members) and the servers' own name of it, the key of their
// approval settings: from the origin the registry recorded, else (a family
// tool that fell back to per-server names) from the registry's name mapping.
func (a *AggregatorServer) approvalTarget(tool mcp.Tool) (servers []string, original string, ok bool) {
	if origin, found := toolset.ToolOriginOf(tool); found && origin.Tool != "" {
		if len(origin.Servers) > 0 {
			return origin.Servers, origin.Tool, true
		}
		return []string{origin.Server}, origin.Tool, true
	}
	server, original, err := a.registry.ResolveToolName(tool.Name)
	if err != nil {
		return nil, "", false
	}
	return []string{server}, original, true
}

// narrowFamilyMembers advertises a family tool on the given members only, as
// a toolset selecting it on some members does.
func narrowFamilyMembers(tool *mcp.Tool, members []string) {
	origin, ok := toolset.ToolOriginOf(*tool)
	if !ok || origin.InstanceArg == "" {
		return
	}
	toolset.SetFamilyMembers(tool, origin.InstanceArg, members)
	origin.Servers = members
	toolset.SetToolOrigin(tool, origin)
}
