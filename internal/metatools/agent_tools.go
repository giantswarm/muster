package metatools

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/toolset"
	"github.com/giantswarm/muster/v5/pkg/logging"
)

// ArgAgentTools is the reporting-only argument of filter_tools and
// describe_tool: an agent's tool list with its approval settings, the
// list's default and its grants. The listing reports each tool's approval as
// that agent would see it (its settings, the grants given, floors and
// governance as for an agent caller), and the connectors behind each server
// tool. It never changes what runs: a call is decided by the settings bound
// to the caller's own Session.
const ArgAgentTools = "agentTools"

// schemaTypeString is the JSON Schema string type.
const schemaTypeString = "string"

// AgentToolsEcho says how many items of agentTools.tools a listing was
// evaluated with, and how many of them carry a setting.
type AgentToolsEcho struct {
	Entries  int `json:"entries"`
	Settings int `json:"settings"`
}

// ConnectorRef names the MCPServer behind a server tool, as agent-manager's
// connector-admin check and grant records need it.
type ConnectorRef struct {
	// Server is muster's server key.
	Server string `json:"server"`
	// Namespace and Name are the MCPServer's.
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
	// UID is the MCPServer's metadata.uid; empty in filesystem mode.
	UID string `json:"uid,omitempty"`
	// Tool is the server's own name of the tool, what a grant names.
	Tool string `json:"tool,omitempty"`
}

// agentToolsSchema is the JSON Schema of the agentTools argument.
var agentToolsSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"tools": map[string]any{
			"type": "array",
			"items": map[string]any{"oneOf": []any{
				map[string]any{"type": schemaTypeString},
				map[string]any{"type": "object", "minProperties": 1, "maxProperties": 1,
					"additionalProperties": map[string]any{"type": schemaTypeString, "enum": []string{api.ApprovalAllow, api.ApprovalAsk, api.ApprovalBlock}}},
			}},
		},
		"default": map[string]any{"type": schemaTypeString, "enum": []string{api.ApprovalAllow, api.ApprovalAsk, api.ApprovalBlock}},
		"grants": map[string]any{
			"type": "array",
			"items": map[string]any{"type": "object", "properties": map[string]any{
				"entry": map[string]any{"type": schemaTypeString}, "server": map[string]any{"type": schemaTypeString}, "uid": map[string]any{"type": schemaTypeString},
				"tools": map[string]any{"type": "array", "items": map[string]any{"type": schemaTypeString}},
			}},
		},
	},
	"required": []string{"tools"},
}

// parseAgentTools reads the agentTools argument: the settings to report with
// and the list's selectors, which narrow the listing like the toolset
// argument.
func parseAgentTools(raw any) (*api.AgentApproval, []string, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("%s must be an object {tools, default, grants}", ArgAgentTools)
	}
	for key := range obj {
		if key != "tools" && key != "default" && key != "grants" {
			return nil, nil, fmt.Errorf("%s does not take %q: use tools, default and grants", ArgAgentTools, key)
		}
	}
	list, ok := obj["tools"].([]any)
	if !ok || len(list) == 0 {
		return nil, nil, fmt.Errorf("%s.tools must be a non-empty array of selectors or {selector: Allow|Ask|Block} objects", ArgAgentTools)
	}
	settings := &api.AgentApproval{Reporting: true}
	selectors := make([]string, 0, len(list))
	for i, item := range list {
		switch v := item.(type) {
		case string:
			selectors = append(selectors, v)
		case map[string]any:
			if len(v) != 1 {
				return nil, nil, fmt.Errorf("%s.tools[%d] must have exactly one selector", ArgAgentTools, i)
			}
			for selector, value := range v {
				setting, isString := value.(string)
				if !isString {
					return nil, nil, fmt.Errorf("%s.tools[%d] %s: setting must be Allow, Ask or Block", ArgAgentTools, i, selector)
				}
				selectors = append(selectors, selector)
				settings.Entries = append(settings.Entries, api.AgentApprovalEntry{Entry: selector, Setting: setting})
			}
		default:
			return nil, nil, fmt.Errorf("%s.tools[%d] must be a selector or a {selector: setting} object", ArgAgentTools, i)
		}
	}
	if raw, present := obj["default"]; present && raw != nil {
		value, isString := raw.(string)
		if !isString {
			return nil, nil, fmt.Errorf("%s.default must be Allow, Ask or Block", ArgAgentTools)
		}
		settings.Default = value
	}
	if raw, present := obj["grants"]; present && raw != nil {
		grants, isList := raw.([]any)
		if !isList {
			return nil, nil, fmt.Errorf("%s.grants must be an array of {entry, server, uid}", ArgAgentTools)
		}
		for i, item := range grants {
			g, isObj := item.(map[string]any)
			if !isObj {
				return nil, nil, fmt.Errorf("%s.grants[%d] must be {entry, server, uid}", ArgAgentTools, i)
			}
			entry, _ := g["entry"].(string)
			server, _ := g["server"].(string)
			uid, _ := g["uid"].(string)
			var tools []string
			if raw, present := g["tools"]; present && raw != nil {
				list, err := toStringList(raw)
				if err != nil {
					return nil, nil, fmt.Errorf("%s.grants[%d].tools must be an array of tool names", ArgAgentTools, i)
				}
				tools = list
			}
			settings.Grants = append(settings.Grants, api.AgentApprovalGrant{Entry: entry, Server: server, UID: uid, Tools: tools})
		}
	}
	if err := settings.Validate(); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", ArgAgentTools, err)
	}
	return settings, selectors, nil
}

// refuseAgentCaller refuses agentTools to an agent caller: it would list
// what the agent's own binding hides from it.
func refuseAgentCaller(ctx context.Context) error {
	if api.IsAgentCaller(api.CallerKind(ctx, api.GetSessionIDFromContext(ctx))) {
		return fmt.Errorf("%s is for people and services that review an agent's tool list; an agent's own listing is evaluated with the settings bound to its Session", ArgAgentTools)
	}
	return nil
}

// connectorLookup returns, for one request, the connectors behind a server
// tool: its server, or every member of a family tool. The MCPServers are
// listed once, on first use. nil without an MCPServer manager.
func connectorLookup(ctx context.Context) func(tool mcp.Tool) []ConnectorRef {
	manager := api.GetMCPServerManager()
	if manager == nil {
		return nil
	}
	list := sync.OnceValue(func() map[string]ConnectorRef {
		refs := map[string]ConnectorRef{}
		definitions, err := manager.ListMCPServers(ctx)
		if err != nil {
			logging.Warn("metatools", "connectors not reported: listing MCPServers failed: %v", err)
			return refs
		}
		for _, d := range definitions {
			refs[d.ServerKey()] = ConnectorRef{Server: d.ServerKey(), Namespace: d.Namespace, Name: d.Name, UID: d.UID}
		}
		return refs
	})
	return func(tool mcp.Tool) []ConnectorRef {
		servers := connectorServers(tool)
		if len(servers) == 0 {
			return nil
		}
		refs := list()
		out := make([]ConnectorRef, 0, len(servers))
		original := ""
		if origin, ok := toolset.ToolOriginOf(tool); ok {
			original = origin.Tool
		}
		for _, server := range servers {
			ref, ok := refs[server]
			if !ok {
				ref = ConnectorRef{Server: server}
			}
			ref.Tool = original
			out = append(out, ref)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Server < out[j].Server })
		return out
	}
}
