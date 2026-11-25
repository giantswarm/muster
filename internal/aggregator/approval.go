package aggregator

import (
	"context"

	"github.com/giantswarm/muster/v5/internal/api"
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
func connectorApprovals(ctx context.Context) (approvals map[string]*api.MCPServerApproval, managed, ok bool) {
	manager := api.GetMCPServerManager()
	if manager == nil {
		return nil, false, true
	}
	definitions, err := manager.ListMCPServers(ctx)
	if err != nil {
		logging.Warn("Aggregator", "approval settings unavailable: listing MCPServers failed: %v", err)
		return nil, true, false
	}
	approvals = make(map[string]*api.MCPServerApproval, len(definitions))
	for _, definition := range definitions {
		approvals[definition.ServerKey()] = definition.Approval
	}
	return approvals, true, true
}

// applyApprovals records on each tool of the caller's catalogue its
// effective approval and returns the catalogue without the tools a server
// blocks. A family tool resolves per member: the members that block it are
// dropped from it (its instance argument no longer offers them), it is
// dropped when all of them do, and it reports the strictest setting of the
// members left. muster's own configuration tools report their built-in Ask,
// every other core tool Allow, not reviewed.
//
// When the settings cannot be read (the listing failed, the server's object
// is missing, the CRD does not know spec.approval), every affected server
// tool is kept and reported as Ask from source "unavailable" (fail closed in
// what is reported; a call is refused at dispatch).
func (a *AggregatorServer) applyApprovals(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
	out := a.resolveApprovals(ctx, tools)
	if reason := a.approvalUnavailable(ctx); reason != "" {
		for i := range out {
			if approval, ok := api.ToolApprovalOf(out[i]); ok && approval.Setting == api.ApprovalSettingAsk {
				approval.Unavailable = reason
				api.SetToolApproval(&out[i], approval)
			}
		}
	}
	return out
}

// resolveApprovals is applyApprovals without the availability of asking.
func (a *AggregatorServer) resolveApprovals(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
	var (
		approvals map[string]*api.MCPServerApproval
		managed   bool
		readable  = true
		listed    bool
	)
	unavailable := api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceUnavailable}
	if problem := api.ApprovalSchemaProblem(ctx); problem != "" {
		readable = false
		unavailable.Unavailable = problem
		listed = true
	}

	out := tools[:0:0]
	for _, tool := range tools {
		if builtin, ok := api.BuiltinApproval(tool.Name); ok {
			api.SetToolApproval(&tool, builtin)
			out = append(out, tool)
			continue
		}
		switch toolset.KindOf(tool) {
		case toolset.KindEntryWorkflow:
			out = append(out, tool)
			continue
		case toolset.KindEntryCore:
			api.SetToolApproval(&tool, api.ToolApproval{Setting: api.ApprovalSettingAllow, Source: api.ApprovalSourceUnreviewed})
			out = append(out, tool)
			continue
		}

		if !listed {
			approvals, managed, readable = connectorApprovals(ctx)
			listed = true
		}
		servers, original, ok := a.approvalTarget(tool)
		if !readable || !ok {
			api.SetToolApproval(&tool, unavailable)
			out = append(out, tool)
			continue
		}

		var kept []string
		var settings []api.ToolApproval
		missing := false
		for _, server := range servers {
			written, found := approvals[server]
			if managed && !found {
				missing = true
				break
			}
			setting := api.ResolveConnectorApproval(written, original)
			if setting.Setting == api.ApprovalSettingBlock {
				continue
			}
			kept = append(kept, server)
			settings = append(settings, setting)
		}
		if missing {
			api.SetToolApproval(&tool, unavailable)
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
