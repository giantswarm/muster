package toolset

import (
	"maps"

	"github.com/mark3labs/mcp-go/mcp"
)

// MetaKeyOrigin is the mcp.Tool._meta.AdditionalFields key under which the
// aggregator records where an exposed tool comes from (ToolOrigin). Like the
// discovery labels key it is an in-process annotation: the meta-tools project
// it into their own response fields (server, kind) and it is never sent to a
// client as _meta.
const MetaKeyOrigin = "muster.giantswarm.io/origin"

// MetaKeyLabels mirrors api.MetaKeyLabels for tests in this package; the
// aggregator stashes discovery labels under it.
const MetaKeyLabels = "muster.giantswarm.io/labels"

// OriginKind classifies an exposed tool by what serves it.
type OriginKind string

const (
	// OriginKindTool is a tool served by a registered MCP server
	// (x_<server>_<tool> or x_<family>_<tool>).
	OriginKindTool OriginKind = "tool"
	// OriginKindWorkflow is a workflow execution tool (workflow_<name>).
	OriginKindWorkflow OriginKind = "workflow"
	// OriginKindCore is one of muster's own core_* tools.
	OriginKindCore OriginKind = "core"
)

// ToolOrigin records where an exposed tool comes from. It is stashed on the
// mcp.Tool by the aggregator when the exposed catalogue is assembled and read
// by the meta-tools (server / kind in list_tools, filter_tools, describe_tool)
// and by toolset resolution (server:<name> selectors).
type ToolOrigin struct {
	// Kind classifies the tool: tool, workflow or core.
	Kind OriginKind
	// Server is the owning MCPServer for a server tool. For a family-grouped
	// tool it is the family name, which is what the exposed name carries.
	// Empty for workflows and core tools.
	Server string
	// Servers lists the member servers providing a family-grouped tool, in
	// sorted order. Empty for solo tools, workflows and core tools.
	Servers []string
}

// SetToolOrigin records origin on tool. The tool's _meta is cloned before it
// is written to, because exposed tools are shallow copies of the downstream
// tools the registry keeps and share their Meta pointer.
func SetToolOrigin(tool *mcp.Tool, origin ToolOrigin) {
	meta := &mcp.Meta{AdditionalFields: map[string]any{}}
	if tool.Meta != nil {
		meta.ProgressToken = tool.Meta.ProgressToken
		maps.Copy(meta.AdditionalFields, tool.Meta.AdditionalFields)
	}
	meta.AdditionalFields[MetaKeyOrigin] = origin
	tool.Meta = meta
}

// ToolOriginOf returns the origin recorded on tool, if any.
func ToolOriginOf(tool mcp.Tool) (ToolOrigin, bool) {
	if tool.Meta == nil || tool.Meta.AdditionalFields == nil {
		return ToolOrigin{}, false
	}
	origin, ok := tool.Meta.AdditionalFields[MetaKeyOrigin].(ToolOrigin)
	return origin, ok
}
