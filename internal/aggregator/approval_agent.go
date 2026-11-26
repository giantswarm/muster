package aggregator

import (
	"context"
	"slices"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/v5/internal/api"
)

// An agent's approval settings (api.AgentApproval) come with a request of a
// Session an approval sender bound (actor_bearer.go), or, for reporting only,
// with filter_tools' agentTools argument. They are applied right after the
// connector's own setting and before the floors and governance entries, so a
// granted Allow can lift a connector's Ask but never a floor's, a governed
// one's or an unreadable setting's (decision #79).

// serverAgentTarget describes a server's tool, as one member serves it, for
// matching agent entries: exposed is the name the listing shows or the call
// used, tool the exposed tool when the caller has it (a listing); otherwise
// the server's own definition stands in for its annotations.
func (a *AggregatorServer) serverAgentTarget(server, original, exposed string, tool *mcp.Tool) api.AgentTarget {
	target := api.AgentTarget{Kind: api.AgentTargetTool, Server: server, Original: original, Exposed: exposed, Tool: tool}
	if a.registry == nil {
		return target
	}
	// The canonical names, recorded nowhere: a tool: or preset: entry is
	// matched against the name the catalogue lists, whatever alias the call
	// used (M2-S3-C-12).
	names := a.registry.ToolNames(server, original)
	if tool == nil && len(names) > 0 {
		target.Exposed = names[0]
	}
	for _, name := range append(names, exposed) {
		if name != "" && name != target.Exposed && !slices.Contains(target.Aliases, name) {
			target.Aliases = append(target.Aliases, name)
		}
	}
	target.Family = a.registry.GroupedFamily(server)
	target.ResolveTool = func(name string) (string, string, bool) {
		srv, orig, err := a.registry.ResolveToolName(name)
		return srv, orig, err == nil
	}
	if target.Tool == nil {
		definition := mcp.Tool{Name: original}
		if info, ok := a.registry.GetServerInfo(server); ok && info != nil {
			if own, found := info.Tool(original); found {
				definition = own
			}
		}
		definition.Name = target.Exposed
		target.Tool = &definition
	}
	return target
}

// withAgentSetting applies the request's agent settings to the admin setting
// of a server's tool. uid is the member MCPServer's UID, which a grant must
// name.
func (a *AggregatorServer) withAgentSetting(ctx context.Context, admin api.ToolApproval, server, original, exposed, uid string, tool *mcp.Tool) api.ToolApproval {
	settings := api.AgentApprovalFromContext(ctx)
	if settings == nil {
		return admin
	}
	setting, entry := settings.Decide(ctx, a.serverAgentTarget(server, original, exposed, tool))
	var grant api.AgentApprovalGrant
	granted := false
	if setting == api.ApprovalAllow {
		grant, granted = settings.Granted(entry, server, uid, original)
	}
	effective := api.ApplyAgentSetting(admin, setting, entry, granted)
	if effective.Source == api.ApprovalSourceAgentException {
		effective.Grant = grant.Entry + "@" + grant.Server
	}
	return effective
}

// withAgentCoreSetting applies the request's agent settings to a core tool:
// they can only make it stricter (no grant reaches a core tool).
func withAgentCoreSetting(ctx context.Context, admin api.ToolApproval, name string) api.ToolApproval {
	settings := api.AgentApprovalFromContext(ctx)
	if settings == nil {
		return admin
	}
	setting, entry := settings.Decide(ctx, api.AgentTarget{Kind: api.AgentTargetCore, Exposed: name, Tool: &mcp.Tool{Name: name}})
	return api.ApplyAgentSetting(admin, setting, entry, false)
}

// withAgentWorkflowSetting applies the request's agent settings to a
// workflow run itself (its steps resolve on their own).
func withAgentWorkflowSetting(ctx context.Context, admin api.ToolApproval, workflow string) api.ToolApproval {
	settings := api.AgentApprovalFromContext(ctx)
	if settings == nil {
		return admin
	}
	exposed := "workflow_" + workflow
	setting, entry := settings.Decide(ctx, api.AgentTarget{Kind: api.AgentTargetWorkflow, Workflow: workflow, Exposed: exposed, Tool: &mcp.Tool{Name: exposed}})
	return api.ApplyAgentSetting(admin, setting, entry, false)
}

// isAgentCallerOrReport reports whether the floors and governance entries
// apply: an agent caller, or a listing reporting for an agent's settings.
func isAgentCallerOrReport(ctx context.Context) bool {
	return api.IsAgentCaller(api.CallerKind(ctx, getSessionIDFromContext(ctx))) || api.ReportingAsAgent(ctx)
}
