package aggregator

import (
	"context"
	"fmt"
	"log/slog"

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
// in tests) or for a server the manager does not know (a registry entry that
// outlived its object), a server has no settings: its tools are Allow, not
// reviewed.
//
// What this slice enforces is Block only: a blocked tool is absent from the
// caller's catalogue (so from list_tools, filter_tools and describe_tool) and
// a call to it is refused as not found, here at dispatch, so a workflow step
// cannot reach it either. Allow and Ask are reported, not enforced.

// connectorApprovals lists the approval settings of every MCPServer once,
// keyed by server key. ok is false when the listing failed: no tool's
// setting is known then.
func connectorApprovals(ctx context.Context) (approvals map[string]*api.MCPServerApproval, ok bool) {
	manager := api.GetMCPServerManager()
	if manager == nil {
		return nil, true
	}
	definitions, err := manager.ListMCPServers(ctx)
	if err != nil {
		logging.Warn("Aggregator", "approval settings unavailable: listing MCPServers failed: %v", err)
		return nil, false
	}
	approvals = make(map[string]*api.MCPServerApproval, len(definitions))
	for _, definition := range definitions {
		approvals[definition.ServerKey()] = definition.Approval
	}
	return approvals, true
}

// connectorApproval reads the approval settings of one server.
func connectorApproval(ctx context.Context, serverKey string) (*api.MCPServerApproval, error) {
	manager := api.GetMCPServerManager()
	if manager == nil {
		return nil, nil
	}
	info, err := manager.GetMCPServer(ctx, serverKey)
	if err != nil {
		if api.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if info == nil {
		return nil, nil
	}
	return info.Approval, nil
}

// refuseBlocked refuses a call to a tool its server blocks, as a tool that
// does not exist, before anything reaches the server. A call whose server's
// settings cannot be read is refused too: muster cannot tell that the tool is
// not blocked.
func refuseBlocked(ctx context.Context, toolName, serverName, originalName string) error {
	approval, err := connectorApproval(ctx, serverName)
	if err != nil {
		return fmt.Errorf("tool %s cannot be called: the approval settings of server %s could not be read: %w", toolName, serverName, err)
	}
	if api.ResolveConnectorApproval(approval, originalName).Setting != api.ApprovalSettingBlock {
		return nil
	}
	logging.InfoWithAttrs("Aggregator", "call refused: the tool is blocked by its server's approval settings",
		slog.String("tool", toolName), slog.String("server", serverName), slog.String("original", originalName))
	return fmt.Errorf("tool not found: %s", toolName)
}

// applyApprovals records on each tool of the caller's catalogue its
// effective approval and returns the catalogue without the tools a server
// blocks. A family tool resolves per member: the members that block it are
// dropped from it (its instance argument no longer offers them), it is
// dropped when all of them do, and it reports the strictest setting of the
// members left. muster's own configuration tools report their built-in Ask,
// every other core tool Allow, not reviewed. Workflows carry no approval yet.
//
// When the settings cannot be read, every server tool is kept and reported
// as Ask from source "unavailable" (fail closed in what is reported; a call
// is still checked at dispatch).
func (a *AggregatorServer) applyApprovals(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
	var (
		approvals map[string]*api.MCPServerApproval
		readable  = true
		listed    bool
	)
	unavailable := api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceUnavailable}

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
			approvals, readable = connectorApprovals(ctx)
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
		for _, server := range servers {
			setting := api.ResolveConnectorApproval(approvals[server], original)
			if setting.Setting == api.ApprovalSettingBlock {
				continue
			}
			kept = append(kept, server)
			settings = append(settings, setting)
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
