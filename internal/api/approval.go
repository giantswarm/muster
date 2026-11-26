package api

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// The values of an MCPServer's spec.approval, as they are written: rule and a
// tool entry take Allow, Ask or Block. They are case-sensitive.
const (
	ApprovalAllow = "Allow"
	ApprovalAsk   = "Ask"
	ApprovalBlock = "Block"
)

// The effective approval of a tool, as the meta-tools report it.
const (
	// ApprovalSettingAllow runs the call.
	ApprovalSettingAllow = "allow"
	// ApprovalSettingAsk asks a person to confirm the exact call first.
	ApprovalSettingAsk = "ask"
	// ApprovalSettingBlock hides the tool and refuses calls to it.
	ApprovalSettingBlock = "block"
)

// Where an effective approval comes from.
const (
	// ApprovalSourceTool is the tool's own entry in spec.approval.tools.
	ApprovalSourceTool = "tool"
	// ApprovalSourceConnector is the server's spec.approval.rule (or its
	// absence on a server that has spec.approval); a rule Block hides every
	// tool without its own entry.
	ApprovalSourceConnector = "connector"
	// ApprovalSourceUnreviewed is a tool of a server without spec.approval,
	// or a tool no server owns: Allow, until someone reviews it.
	ApprovalSourceUnreviewed = "unreviewed"
	// ApprovalSourceBuiltin is a tool muster always asks for, whoever calls
	// it: its own server configuration.
	ApprovalSourceBuiltin = "builtin"
	// ApprovalSourceUnavailable is a tool whose server's settings could not
	// be read; it is reported as Ask, never as Allow.
	ApprovalSourceUnavailable = "unavailable"
)

// The presets a person picks from when they review a server's tools. Each
// expands, once, into explicit settings (ExpandApprovalPreset); none is
// stored or evaluated later.
const (
	// ApprovalPresetRecommended: rule Ask, each tool annotated readOnlyHint
	// true Allow, every other tool Ask.
	ApprovalPresetRecommended = "recommended"
	// ApprovalPresetAllowAll: rule Allow, every tool Allow.
	ApprovalPresetAllowAll = "allow-all"
	// ApprovalPresetAskAll: rule Ask, every tool Ask.
	ApprovalPresetAskAll = "ask-all"
)

// MetaKeyApproval is the mcp.Tool._meta.AdditionalFields key under which the
// aggregator records a tool's effective approval for the caller of one
// listing. Like the origin key it is an in-process annotation the meta-tools
// project into their own response field; it is never sent as _meta.
const MetaKeyApproval = "muster.giantswarm.io/approval"

// MCPServerApproval is a server's spec.approval: the rule a tool without its
// own entry follows, and the tools' own entries, keyed by the server's own
// tool name. The values are kept as written, so a value the CRD would refuse
// (a definition read from a file, an object written before the CRD knew the
// field) still reaches resolution, which counts it as Ask.
type MCPServerApproval struct {
	Rule  string            `yaml:"rule,omitempty" json:"rule,omitempty"`
	Tools map[string]string `yaml:"tools,omitempty" json:"tools,omitempty"`
}

// ToolApproval is a tool's effective approval for one caller: the setting,
// where it comes from, and whether the tool is new (it has no entry of its
// own on a server that has approval settings, so it follows the rule).
type ToolApproval struct {
	Setting string `json:"setting"`
	Source  string `json:"source"`
	New     bool   `json:"new,omitempty"`
	// Unavailable says, for a tool that asks, why muster cannot ask here
	// (no shared store on Valkey, no browser sign-in, no public URL, no
	// person on the request): calls to it are refused until it is fixed.
	Unavailable string `json:"unavailable,omitempty"`
	// Entry is the agent entry that decided the agent setting ("default"
	// for the list's default); empty when no agent setting applies.
	Entry string `json:"entry,omitempty"`
	// Admin is the connector's own setting, reported beside Entry.
	Admin *ToolApproval `json:"admin,omitempty"`
	// Grant names the grant mark (entry@server) that lifted the
	// connector's Ask, for the audit; never reported.
	Grant string `json:"-"`
}

// builtinAskTools are muster's own server configuration tools, which always
// ask, for every caller.
var builtinAskTools = map[string]bool{
	"core_config_update_aggregator": true,
	"core_config_save":              true,
	"core_config_reload":            true,
}

// BuiltinApproval returns the built-in approval of the exposed tool name, if
// muster sets one itself.
func BuiltinApproval(exposedName string) (ToolApproval, bool) {
	if builtinAskTools[exposedName] {
		return ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceBuiltin}, true
	}
	return ToolApproval{}, false
}

// ResolveConnectorApproval returns the admin setting of the server's tool
// named tool (the server's own name): the tool's entry, else the server's
// rule, else Allow. Without approval settings the tool is Allow and not
// reviewed. A value other than Allow, Ask or Block counts as Ask; a rule
// Block blocks every tool without its own entry, new tools included.
func ResolveConnectorApproval(approval *MCPServerApproval, tool string) ToolApproval {
	if approval == nil {
		return ToolApproval{Setting: ApprovalSettingAllow, Source: ApprovalSourceUnreviewed}
	}
	if value, ok := approval.Tools[tool]; ok {
		return ToolApproval{Setting: settingOf(value), Source: ApprovalSourceTool}
	}
	setting := ApprovalSettingAllow
	if approval.Rule != "" {
		setting = settingOf(approval.Rule)
	}
	return ToolApproval{Setting: setting, Source: ApprovalSourceConnector, New: true}
}

// settingOf maps a written value to the reported setting; anything unknown
// counts as Ask (fail closed).
func settingOf(value string) string {
	switch value {
	case ApprovalAllow:
		return ApprovalSettingAllow
	case ApprovalBlock:
		return ApprovalSettingBlock
	default:
		return ApprovalSettingAsk
	}
}

// approvalRank orders settings by strictness; an unknown setting ranks as ask.
func approvalRank(setting string) int {
	switch setting {
	case ApprovalSettingAllow:
		return 0
	case ApprovalSettingBlock:
		return 2
	default:
		return 1
	}
}

// StrictestApproval returns the strictest of the approvals, Block over Ask
// over Allow; of equally strict ones, the first. No approval is the zero
// value.
func StrictestApproval(approvals ...ToolApproval) ToolApproval {
	var strictest ToolApproval
	for i, a := range approvals {
		if i == 0 || approvalRank(a.Setting) > approvalRank(strictest.Setting) {
			strictest = a
		}
	}
	return strictest
}

// ApprovalPresets returns the preset names ExpandApprovalPreset accepts.
func ApprovalPresets() []string {
	return []string{ApprovalPresetRecommended, ApprovalPresetAllowAll, ApprovalPresetAskAll}
}

// ExpandApprovalPreset expands a preset into explicit settings for the given
// tools, the server's own tools as it lists them now (their own names). The
// result depends on the annotations only at this moment: it is stored as it
// is, and a tool the server adds later follows its rule.
func ExpandApprovalPreset(preset string, tools []mcp.Tool) (*MCPServerApproval, error) {
	var rule string
	var settingFor func(mcp.Tool) string
	switch preset {
	case ApprovalPresetRecommended:
		rule = ApprovalAsk
		settingFor = func(t mcp.Tool) string {
			if t.Annotations.ReadOnlyHint != nil && *t.Annotations.ReadOnlyHint {
				return ApprovalAllow
			}
			return ApprovalAsk
		}
	case ApprovalPresetAllowAll:
		rule = ApprovalAllow
		settingFor = func(mcp.Tool) string { return ApprovalAllow }
	case ApprovalPresetAskAll:
		rule = ApprovalAsk
		settingFor = func(mcp.Tool) string { return ApprovalAsk }
	default:
		return nil, fmt.Errorf("unknown approval preset %q: use one of %s", preset, strings.Join(ApprovalPresets(), ", "))
	}
	approval := &MCPServerApproval{Rule: rule}
	if len(tools) > 0 {
		approval.Tools = make(map[string]string, len(tools))
		for _, t := range tools {
			approval.Tools[t.Name] = settingFor(t)
		}
	}
	return approval, nil
}

// SetToolApproval records the effective approval on tool. The tool's _meta is
// cloned before it is written to: exposed tools share the Meta pointer of the
// tools the registry keeps.
func SetToolApproval(tool *mcp.Tool, approval ToolApproval) {
	meta := &mcp.Meta{AdditionalFields: map[string]any{}}
	if tool.Meta != nil {
		meta.ProgressToken = tool.Meta.ProgressToken
		maps.Copy(meta.AdditionalFields, tool.Meta.AdditionalFields)
	}
	meta.AdditionalFields[MetaKeyApproval] = approval
	tool.Meta = meta
}

// ToolApprovalOf returns the effective approval recorded on tool, if any.
func ToolApprovalOf(tool mcp.Tool) (ToolApproval, bool) {
	if tool.Meta == nil || tool.Meta.AdditionalFields == nil {
		return ToolApproval{}, false
	}
	approval, ok := tool.Meta.AdditionalFields[MetaKeyApproval].(ToolApproval)
	return approval, ok
}

// MCPServerConnectorAdmin is the optional capability of the registered
// MCPServer manager that tells whether the caller is a connector admin of a
// server: whoever may update MCPServers in its namespace, the same check the
// write itself is subject to. The meta-tools report it so a client can offer
// the settings only to those who may change them.
type MCPServerConnectorAdmin interface {
	// IsConnectorAdmin reports whether the caller carried by ctx may update
	// the MCPServer with the given key (api.MCPServerKey).
	IsConnectorAdmin(ctx context.Context, serverKey string) (bool, error)
}

// MCPServerApprovalSchema is the optional capability of the registered
// MCPServer manager that tells whether approval settings can be trusted: the
// served MCPServer CRD knows spec.approval. A CRD that does not prunes the
// field on every write, so a connector's Ask and Block would silently run.
type MCPServerApprovalSchema interface {
	// ApprovalSchemaProblem returns "" when the CRD knows spec.approval, else
	// why muster cannot trust approval settings.
	ApprovalSchemaProblem(ctx context.Context) string
}

// ApprovalSchemaProblem returns why approval settings cannot be trusted, ""
// when they can (or without a manager offering the check).
func ApprovalSchemaProblem(ctx context.Context) string {
	if checker, ok := GetMCPServerManager().(MCPServerApprovalSchema); ok {
		return checker.ApprovalSchemaProblem(ctx)
	}
	return ""
}

// MCPServerLastObserved is the optional capability of an MCPServer manager
// that remembers the last definition of each server it read from its
// definition source (the reconciler's reads on every watch event and resync,
// and every live read since). A server the source reported deleted is
// forgotten.
type MCPServerLastObserved interface {
	// LastObservedMCPServer returns the last definition read of the server
	// key names and when it was read; ok is false for a server never read,
	// or one read as deleted since.
	LastObservedMCPServer(key string) (info *MCPServerInfo, observedAt time.Time, ok bool)
}

// LastObservedMCPServer returns the registered manager's last observed
// definition of a server; ok is false without a manager offering it.
func LastObservedMCPServer(key string) (*MCPServerInfo, time.Time, bool) {
	if observer, ok := GetMCPServerManager().(MCPServerLastObserved); ok {
		return observer.LastObservedMCPServer(key)
	}
	return nil, time.Time{}, false
}
