package cli

import (
	"context"
	"strings"

	"github.com/giantswarm/muster/v5/internal/metatools"
	"github.com/giantswarm/muster/v5/internal/toolset"
)

// MCPToolInfo is a tool as list_tools reports it: the MCP tool plus the server
// it belongs to. The exposed name does not encode the server reliably -- a
// server named "gazelle-mcp-pro" with toolPrefix "pro" exposes x_pro_<tool> --
// so the attribution the aggregator recorded is carried alongside the tool.
type MCPToolInfo struct {
	MCPTool
	// Server is the MCPServer (or family) an aggregated tool belongs to, as
	// the aggregator names it. muster's own tools carry their kind instead:
	// "core" or "workflow".
	Server string
}

// ListMCPTools returns the caller's tool catalogue by paging through the
// list_tools meta-tool: the actual tools (core_*, x_*, workflow_*) with the
// server each belongs to, rather than the meta-tools the MCP native
// tools/list exposes.
func (e *ToolExecutor) ListMCPTools(ctx context.Context) ([]MCPToolInfo, error) {
	response, err := metatools.ListAllTools(ctx, e.client.CallTool)
	if err != nil {
		return nil, err
	}

	tools := make([]MCPToolInfo, len(response.Tools))
	for i, t := range response.Tools {
		tools[i] = newMCPToolInfo(t)
	}
	return tools, nil
}

// newMCPToolInfo projects a list_tools entry onto the CLI's tool listing.
func newMCPToolInfo(t metatools.ToolInfo) MCPToolInfo {
	return MCPToolInfo{
		MCPTool: MCPTool{Name: t.Name, Description: t.Text()},
		Server:  toolServer(t),
	}
}

// toolServer names the server a list_tools entry belongs to: the MCPServer
// the aggregator attributed it to, or the kind of one of muster's own tools
// ("core", "workflow"). An aggregator too old to report either is read the
// way it names its tools.
func toolServer(t metatools.ToolInfo) string {
	if t.Server != "" {
		return t.Server
	}
	if t.Kind != "" && t.Kind != string(toolset.OriginKindTool) {
		return t.Kind
	}
	return serverFromToolName(t.Name)
}

// serverFromToolName derives the server from an exposed tool name alone: the
// first name segment for core_* and workflow_*, the prefix for
// x_<prefix>_<tool> -- the server's name unless it configured a toolPrefix of
// its own -- and nothing for a name that carries no prefix.
func serverFromToolName(name string) string {
	rest := strings.TrimPrefix(name, "x_")
	if idx := strings.Index(rest, "_"); idx > 0 {
		return rest[:idx]
	}
	return ""
}
