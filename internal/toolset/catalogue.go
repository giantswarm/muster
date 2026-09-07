package toolset

import (
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// Kind classifies a catalogue entry the way the meta-tools report it.
type Kind string

const (
	KindEntryTool     Kind = "tool"
	KindEntryWorkflow Kind = "workflow"
	KindEntryCore     Kind = "core"
)

// Entry is what toolset resolution knows about one exposed tool.
type Entry struct {
	// Name is the exposed tool name (x_<server>_<tool>, workflow_<name>, core_*).
	Name string
	// Kind is tool, workflow or core.
	Kind Kind
	// Server is the owning MCPServer (the family name for a family tool).
	// Empty for workflows and core tools.
	Server string
	// Servers lists the member servers of a family tool.
	Servers []string
	// ReadOnly is the tool's readOnlyHint annotation (for a workflow the
	// derived hint the aggregator computed from its step tools).
	ReadOnly bool
	// Labels are the labels of the owning MCPServer resource (#1168). Nil
	// when unknown.
	Labels map[string]string
}

// ServerLabels resolves an MCPServer name to its resource labels. It is
// consulted for server tools when non-nil; see #1168.
type ServerLabels func(server string) map[string]string

// EntriesFromTools projects exposed tools to resolution entries. Kind and
// server come from the origin the aggregator stashed on the tool; tools
// without one are classified by name prefix, which is how the aggregator
// itself tells core and workflow tools apart.
func EntriesFromTools(tools []mcp.Tool, labels ServerLabels) []Entry {
	entries := make([]Entry, 0, len(tools))
	for _, t := range tools {
		e := Entry{Name: t.Name, Kind: KindOf(t)}
		if origin, ok := ToolOriginOf(t); ok {
			e.Server = origin.Server
			e.Servers = origin.Servers
		}
		if t.Annotations.ReadOnlyHint != nil && *t.Annotations.ReadOnlyHint {
			e.ReadOnly = true
		}
		if labels != nil && e.Kind == KindEntryTool && e.Server != "" {
			e.Labels = labels(e.Server)
		}
		entries = append(entries, e)
	}
	return entries
}

// KindOf classifies an exposed tool: from its origin when the aggregator
// recorded one, else by the name prefixes the aggregator uses.
func KindOf(t mcp.Tool) Kind {
	if origin, ok := ToolOriginOf(t); ok {
		switch origin.Kind {
		case OriginKindWorkflow:
			return KindEntryWorkflow
		case OriginKindCore:
			return KindEntryCore
		case OriginKindTool:
			return KindEntryTool
		}
	}
	switch {
	case strings.HasPrefix(t.Name, "workflow_"):
		return KindEntryWorkflow
	case strings.HasPrefix(t.Name, "core_"):
		return KindEntryCore
	}
	return KindEntryTool
}

// Filter returns the tools whose names the resolution selected, in input order.
func Filter(tools []mcp.Tool, res Resolution) []mcp.Tool {
	out := make([]mcp.Tool, 0, len(res.Selected))
	for _, t := range tools {
		if _, ok := res.Selected[t.Name]; ok {
			out = append(out, t)
		}
	}
	return out
}
