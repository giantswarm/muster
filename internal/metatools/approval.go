package metatools

import (
	"context"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/toolset"
	"github.com/giantswarm/muster/v5/pkg/logging"

	"github.com/mark3labs/mcp-go/mcp"
)

// approvalOf returns the effective approval the aggregator recorded on the
// tool for the caller; nil for a tool that carries none (a workflow).
func approvalOf(tool mcp.Tool) *api.ToolApproval {
	approval, ok := api.ToolApprovalOf(tool)
	if !ok {
		return nil
	}
	return &approval
}

// connectorServers returns the MCPServers whose approval settings apply to a
// server tool: its server, or the members of a family tool. Nil for
// workflows and core tools.
func connectorServers(tool mcp.Tool) []string {
	if toolset.KindOf(tool) != toolset.KindEntryTool {
		return nil
	}
	origin, ok := toolset.ToolOriginOf(tool)
	if !ok {
		return nil
	}
	if len(origin.Servers) > 0 {
		return origin.Servers
	}
	if origin.Server == "" {
		return nil
	}
	return []string{origin.Server}
}

// connectorAdminLookup returns, for one request, whether the caller is a
// connector admin of a server tool: of its server, or of every member of a
// family tool. Each server is reviewed at most once per request. nil when
// the MCPServer manager cannot tell (then no tool reports it); a tool that
// is not a server tool reports nothing either. A review that fails counts
// as not a connector admin.
func connectorAdminLookup(ctx context.Context) func(tool mcp.Tool) *bool {
	reviewer, ok := api.GetMCPServerManager().(api.MCPServerConnectorAdmin)
	if !ok {
		return nil
	}
	memo := map[string]bool{}
	isAdmin := func(server string) bool {
		if admin, done := memo[server]; done {
			return admin
		}
		admin, err := reviewer.IsConnectorAdmin(ctx, server)
		if err != nil {
			logging.Debug("metatools", "connector admin of %s unknown, reported as not: %v", server, err)
			admin = false
		}
		memo[server] = admin
		return admin
	}
	return func(tool mcp.Tool) *bool {
		servers := connectorServers(tool)
		if len(servers) == 0 {
			return nil
		}
		admin := true
		for _, server := range servers {
			admin = admin && isAdmin(server)
		}
		return &admin
	}
}

// governanceMode is the mode a client is shown: governed when the governed
// entries apply, whatever the reason.
func governanceMode() string {
	if api.GetGovernance().Governed() {
		return api.GovernanceGoverned
	}
	return api.GovernanceTrusted
}
