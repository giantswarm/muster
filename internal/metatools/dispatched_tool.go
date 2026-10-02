package metatools

import (
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/v5/pkg/observability"
)

// MetaKeyDispatchedTool is the _meta key under which a call_tool result
// carries the DispatchedTool, on the call_tool result itself and, in the agent
// client, on the unwrapped result.
const MetaKeyDispatchedTool = "muster.giantswarm.io/tool"

// DispatchedTool identifies the tool a call_tool call ran, so a consumer can
// attribute the result without remembering the call's arguments.
type DispatchedTool struct {
	// Name is the tool call_tool was asked to run, as muster exposes it.
	Name string `json:"name"`
	// Server is the MCPServer the call was dispatched to; empty for muster's
	// own core and workflow tools.
	Server string `json:"server,omitempty"`
	// ServerTool is the backend-native name the server knows the tool by.
	ServerTool string `json:"serverTool,omitempty"`
}

func newDispatchedTool(name string, record *observability.DispatchRecord) DispatchedTool {
	tool := DispatchedTool{Name: name}
	if call, ok := record.Dispatched(); ok {
		tool.Server, tool.ServerTool = call.Server, call.Tool
	}
	return tool
}

// DispatchedToolFromMeta returns the DispatchedTool a result's _meta carries
// under MetaKeyDispatchedTool, whether set in process or decoded from the wire.
func DispatchedToolFromMeta(meta *mcp.Meta) (DispatchedTool, bool) {
	if meta == nil {
		return DispatchedTool{}, false
	}
	switch v := meta.AdditionalFields[MetaKeyDispatchedTool].(type) {
	case DispatchedTool:
		return v, true
	case nil:
		return DispatchedTool{}, false
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return DispatchedTool{}, false
		}
		var tool DispatchedTool
		if err := json.Unmarshal(raw, &tool); err != nil || tool.Name == "" {
			return DispatchedTool{}, false
		}
		return tool, true
	}
}
