package api

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// An agent's approval settings: the entries of its tool list that carry
// Allow, Ask or Block, the list's default, and the grants a connector admin
// recorded in agent-manager (an Allow over that connector's Ask). muster takes
// them from a Session binding sent by a sender Kubernetes allows
// (credprovider), and, for reporting only, from filter_tools' agentTools
// argument. They combine with the connector's settings (ApplyAgentSetting):
// the stricter holds, except a granted Allow over a connector's Ask; floors
// and governance apply on top and are never lifted.

// Where an effective approval comes from, when an agent's settings decided it.
const (
	// ApprovalSourceAgent is an agent entry (or the list's default) that is
	// stricter than the connector's setting.
	ApprovalSourceAgent = "agent"
	// ApprovalSourceAgentException is an agent's granted Allow over the
	// connector's Ask.
	ApprovalSourceAgentException = "agent-exception"
)

// AgentDefaultEntry names the list's default where an entry is named.
const AgentDefaultEntry = "default"

// Bounds on the approval settings a binding or a reporting call carries.
const (
	MaxAgentApprovalEntries = 128
	MaxAgentApprovalGrants  = 256
)

// agentSelectorPattern is the toolset grammar an entry is written in.
var agentSelectorPattern = regexp.MustCompile(`^(preset|server|workflow|tool):[^\s,]+$`)

// AgentApprovalEntry is one entry of an agent's tool list with its setting.
type AgentApprovalEntry struct {
	Entry   string `json:"entry"`
	Setting string `json:"setting"`
}

// AgentApprovalGrant marks an entry's Allow as granted on one connector: the
// MCPServer's server key and its UID (a recreated server has another), for
// the tools (the server's own names) it covered when the connector admin
// granted it. A tool that starts asking later is not covered: its admin
// never saw it lifted.
type AgentApprovalGrant struct {
	Entry  string   `json:"entry"`
	Server string   `json:"server"`
	UID    string   `json:"uid"`
	Tools  []string `json:"tools,omitempty"`
}

// AgentApproval is an agent's approval settings as one request sees them.
type AgentApproval struct {
	// Agent is the agent, <namespace>/<name>, where known.
	Agent   string               `json:"agent,omitempty"`
	Entries []AgentApprovalEntry `json:"entries,omitempty"`
	Default string               `json:"default,omitempty"`
	Grants  []AgentApprovalGrant `json:"grants,omitempty"`
	// Reporting marks settings passed to a listing to report what an
	// agent would see; they never decide a call.
	Reporting bool `json:"-"`
}

// Validate checks the settings' shape: selectors in the toolset grammar, each
// once, values Allow, Ask or Block, grants naming an Allow entry (or an
// allowing default) and a server.
func (a *AgentApproval) Validate() error {
	if a == nil {
		return nil
	}
	if len(a.Entries) > MaxAgentApprovalEntries {
		return fmt.Errorf("approval.entries has %d entries, more than %d", len(a.Entries), MaxAgentApprovalEntries)
	}
	if len(a.Grants) > MaxAgentApprovalGrants {
		return fmt.Errorf("approval.grants has %d grants, more than %d", len(a.Grants), MaxAgentApprovalGrants)
	}
	settings := make(map[string]string, len(a.Entries))
	for _, e := range a.Entries {
		if !agentSelectorPattern.MatchString(e.Entry) {
			return fmt.Errorf("approval entry %q is not a selector (preset:, server:, workflow: or tool:)", e.Entry)
		}
		if !validWrittenSetting(e.Setting) {
			return fmt.Errorf("approval entry %q has setting %q: use Allow, Ask or Block", e.Entry, e.Setting)
		}
		if _, dup := settings[e.Entry]; dup {
			return fmt.Errorf("approval entry %q appears twice", e.Entry)
		}
		settings[e.Entry] = e.Setting
	}
	if a.Default != "" && !validWrittenSetting(a.Default) {
		return fmt.Errorf("approval default %q: use Allow, Ask or Block", a.Default)
	}
	for _, g := range a.Grants {
		// A grant without a UID (a server read in filesystem mode) is kept
		// but never matches (Granted): it lifts nothing.
		if g.Server == "" {
			return fmt.Errorf("approval grant for %q needs a server", g.Entry)
		}
		setting := settings[g.Entry]
		if g.Entry == AgentDefaultEntry {
			setting = a.Default
		}
		if setting != ApprovalAllow {
			return fmt.Errorf("approval grant for %q names no entry with Allow", g.Entry)
		}
	}
	return nil
}

func validWrittenSetting(value string) bool {
	return value == ApprovalAllow || value == ApprovalAsk || value == ApprovalBlock
}

// AgentTarget is what an agent entry is matched against: a server's tool
// (one member of a family), a workflow, or a core tool.
type AgentTarget struct {
	// Kind is "tool", "workflow" or "core".
	Kind string
	// Server is the member server's key; Family the family's name, if any.
	Server, Family string
	// Original is the server's own name of the tool.
	Original string
	// Exposed is the tool's exposed name (x_..., core_..., workflow_...).
	Exposed string
	// Aliases are other names the tool is exposed or was called under (a
	// family member's per-server name, the name the call used); a tool:
	// entry naming any of them selects the tool.
	Aliases []string
	// Workflow is the workflow's name.
	Workflow string
	// Tool is the exposed tool, for preset rules; nil when unknown.
	Tool *mcp.Tool
	// ResolveTool maps an exposed name to the server and the server's own
	// name, so a tool: entry selects the dispatch target whatever alias it
	// names. nil compares names only.
	ResolveTool func(name string) (server, original string, ok bool)
}

// Target kinds of AgentTarget.
const (
	AgentTargetTool     = "tool"
	AgentTargetWorkflow = "workflow"
	AgentTargetCore     = "core"
)

// entryRank orders entry kinds by specificity.
func entryRank(kind string) int {
	switch kind {
	case "tool":
		return 4
	case "workflow":
		return 3
	case "server":
		return 2
	case "preset":
		return 1
	}
	return 0
}

// Decide returns the agent setting for the target and the entry that decided
// it: the most specific entry selecting it (tool: over workflow: over server:
// over preset:), the strictest of equally specific ones, else the default.
// Both are empty when nothing applies.
func (a *AgentApproval) Decide(ctx context.Context, t AgentTarget) (setting, entry string) {
	if a == nil {
		return "", ""
	}
	best := -1
	for _, e := range a.Entries {
		kind, name, _ := strings.Cut(e.Entry, ":")
		if !t.selectedBy(ctx, kind, name) {
			continue
		}
		rank := entryRank(kind)
		switch {
		case rank > best:
			best, setting, entry = rank, e.Setting, e.Entry
		case rank == best && approvalRank(settingOf(e.Setting)) > approvalRank(settingOf(setting)):
			setting, entry = e.Setting, e.Entry
		}
	}
	if best >= 0 {
		return setting, entry
	}
	if a.Default != "" {
		return a.Default, AgentDefaultEntry
	}
	return "", ""
}

// Key identifies a grant mark with what it covers: entry, server, UID and
// the tools, in any order.
func (g AgentApprovalGrant) Key() string {
	tools := slices.Clone(g.Tools)
	slices.Sort(tools)
	return strings.Join([]string{g.Entry, g.Server, g.UID, strings.Join(tools, ",")}, "\x00")
}

// Granted returns the grant that lifts the entry's Allow for the server's
// own tool original on the server with the given UID, if any: a grant names
// the tools it covers, so one without tools lifts nothing.
func (a *AgentApproval) Granted(entry, server, uid, original string) (AgentApprovalGrant, bool) {
	if a == nil || uid == "" || original == "" {
		return AgentApprovalGrant{}, false
	}
	for _, g := range a.Grants {
		if g.Entry == entry && g.Server == server && g.UID == uid && slices.Contains(g.Tools, original) {
			return g, true
		}
	}
	return AgentApprovalGrant{}, false
}

// selectedBy reports whether the selector kind:name selects the target.
func (t AgentTarget) selectedBy(ctx context.Context, kind, name string) bool {
	switch kind {
	case "tool":
		return t.toolNamed(name)
	case "workflow":
		return t.Kind == AgentTargetWorkflow && name == t.Workflow
	case "server":
		return t.Kind == AgentTargetTool && (name == t.Server || (t.Family != "" && name == t.Family))
	case "preset":
		return PresetSelects(ctx, name, t)
	}
	return false
}

// toolNamed reports whether a tool: entry names the target.
func (t AgentTarget) toolNamed(name string) bool {
	switch t.Kind {
	case AgentTargetWorkflow:
		return WorkflowNameOf(name) == t.Workflow && WorkflowNameOf(name) != ""
	case AgentTargetCore:
		return "core_"+strings.TrimPrefix(name, "core_") == "core_"+strings.TrimPrefix(t.Exposed, "core_")
	}
	if name != "" && (name == t.Exposed || slices.Contains(t.Aliases, name)) {
		return true
	}
	if t.ResolveTool == nil {
		return false
	}
	server, original, ok := t.ResolveTool(name)
	return ok && original == t.Original && (server == t.Server || (t.Family != "" && server == t.Family))
}

// WorkflowNameOf returns the workflow an exposed name runs (workflow_X,
// core_workflow_X), "" for any other name.
func WorkflowNameOf(name string) string {
	rest, ok := strings.CutPrefix(strings.TrimPrefix(name, "core_"), "workflow_")
	if !ok {
		return ""
	}
	return rest
}

// ApplyAgentSetting combines the admin setting with an agent setting (as
// written, "" for none): an admin Block holds; an agent Block blocks; a
// granted Allow over an admin Ask of a connector (its tool entry or rule)
// allows; otherwise the stricter holds, ties keeping the admin's source.
// entry is reported with the admin setting beside it.
func ApplyAgentSetting(admin ToolApproval, setting, entry string, granted bool) ToolApproval {
	if setting == "" {
		// Where an agent's settings apply, the connector's own setting is
		// reported even when no entry decides.
		explained := admin
		explained.Admin = &ToolApproval{Setting: admin.Setting, Source: admin.Source}
		return explained
	}
	adminOnly := admin
	adminOnly.Unavailable = ""
	explained := func(a ToolApproval) ToolApproval {
		a.Entry = entry
		a.Admin = &ToolApproval{Setting: adminOnly.Setting, Source: adminOnly.Source}
		return a
	}
	if admin.Setting == ApprovalSettingBlock {
		return explained(admin)
	}
	agent := settingOf(setting)
	switch {
	case agent == ApprovalSettingBlock:
		return explained(ToolApproval{Setting: ApprovalSettingBlock, Source: ApprovalSourceAgent, New: admin.New})
	case agent == ApprovalSettingAllow && granted && admin.Setting == ApprovalSettingAsk &&
		(admin.Source == ApprovalSourceTool || admin.Source == ApprovalSourceConnector):
		return explained(ToolApproval{Setting: ApprovalSettingAllow, Source: ApprovalSourceAgentException, New: admin.New})
	case approvalRank(agent) > approvalRank(admin.Setting):
		return explained(ToolApproval{Setting: agent, Source: ApprovalSourceAgent, New: admin.New, Unavailable: admin.Unavailable})
	}
	return explained(admin)
}

// KeepExplanation returns final, the approval after floors and governance,
// with the agent explanation of decided (the approval before them): the
// deciding entry and the connector's own setting stay reported when a floor
// or the governed default is what decides. A grant mark is kept only
// when it is still what lets the call run.
func KeepExplanation(final, decided ToolApproval) ToolApproval {
	final.Entry, final.Admin = decided.Entry, decided.Admin
	if final.Source == ApprovalSourceAgentException {
		final.Grant = decided.Grant
	} else {
		final.Grant = ""
	}
	return final
}

type agentApprovalKey struct{}

// WithAgentApproval returns ctx carrying an agent's approval settings.
func WithAgentApproval(ctx context.Context, a *AgentApproval) context.Context {
	if a == nil {
		return ctx
	}
	return context.WithValue(ctx, agentApprovalKey{}, a)
}

// AgentApprovalFromContext returns the agent's approval settings the request
// carries, nil without.
func AgentApprovalFromContext(ctx context.Context) *AgentApproval {
	a, _ := ctx.Value(agentApprovalKey{}).(*AgentApproval)
	return a
}

// WithoutReportingApproval returns ctx without settings that were passed for
// reporting only, so they never decide a call.
func WithoutReportingApproval(ctx context.Context) context.Context {
	if a := AgentApprovalFromContext(ctx); a != nil && a.Reporting {
		return context.WithValue(ctx, agentApprovalKey{}, (*AgentApproval)(nil))
	}
	return ctx
}

// ReportingAsAgent reports whether a listing reports for an agent's settings
// passed to it (filter_tools agentTools): floors and governance are then
// reported as for an agent caller.
func ReportingAsAgent(ctx context.Context) bool {
	a := AgentApprovalFromContext(ctx)
	return a != nil && a.Reporting
}

// AgentOfContext returns the bound agent (<namespace>/<name>) of a request,
// "" when it carries none or only reporting settings.
func AgentOfContext(ctx context.Context) string {
	a := AgentApprovalFromContext(ctx)
	if a == nil || a.Reporting {
		return ""
	}
	return a.Agent
}

// PresetSelector reports whether a toolset preset selects a target.
type PresetSelector func(ctx context.Context, preset string, t AgentTarget) bool

var presetSelector PresetSelector

// RegisterPresetSelector registers how preset: entries are resolved (the
// meta-tools adapter, which holds the presets).
func RegisterPresetSelector(s PresetSelector) { presetSelector = s }

// PresetSelects resolves a preset: entry; without a registered selector a
// preset selects nothing.
func PresetSelects(ctx context.Context, preset string, t AgentTarget) bool {
	if presetSelector == nil {
		return false
	}
	return presetSelector(ctx, preset, t)
}

// MaxAgentReasonRunes bounds the reason an agent gives with a call.
const MaxAgentReasonRunes = 500

type agentReasonKey struct{}

// WithAgentReason returns ctx carrying the reason the caller gave with a
// call (call_tool's reason argument).
func WithAgentReason(ctx context.Context, reason string) context.Context {
	if reason == "" {
		return ctx
	}
	return context.WithValue(ctx, agentReasonKey{}, reason)
}

// AgentReasonFromContext returns the reason the caller gave with the call.
func AgentReasonFromContext(ctx context.Context) string {
	reason, _ := ctx.Value(agentReasonKey{}).(string)
	return reason
}

// ToolApprovalHeader carries an agent's approval settings on the requests its
// runtime sends through its own RemoteMCPServer (the agent chart renders it
// beside X-Muster-Toolset): "<selector>=<Setting>,...[,default=<Setting>]".
// It carries no grants, so it can only make tools stricter than their
// connector's settings, and it applies only where no Session binding with an
// approval sender's settings does (Sessions not started through
// agent-manager).
const ToolApprovalHeader = "X-Muster-Tool-Approval"

// ParseToolApprovalHeader parses a ToolApprovalHeader value. A value that
// cannot be parsed is an error; the caller then applies a default Ask (fail
// closed).
func ParseToolApprovalHeader(value string) (*AgentApproval, error) {
	settings := &AgentApproval{}
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		i := strings.LastIndex(part, "=")
		if i <= 0 {
			return nil, fmt.Errorf("%s entry %q is not <selector>=<Setting>", ToolApprovalHeader, part)
		}
		selector, setting := part[:i], part[i+1:]
		if selector == AgentDefaultEntry {
			if settings.Default != "" {
				return nil, fmt.Errorf("%s names default twice", ToolApprovalHeader)
			}
			settings.Default = setting
			continue
		}
		settings.Entries = append(settings.Entries, AgentApprovalEntry{Entry: selector, Setting: setting})
	}
	if err := settings.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", ToolApprovalHeader, err)
	}
	return settings, nil
}

// ContextWithToolApprovalHeader applies a request's ToolApprovalHeader to
// ctx unless ctx already carries settings (a Session binding's). An empty
// value applies nothing; one that cannot be parsed makes every tool ask.
func ContextWithToolApprovalHeader(ctx context.Context, value string) context.Context {
	if strings.TrimSpace(value) == "" || AgentApprovalFromContext(ctx) != nil {
		return ctx
	}
	settings, err := ParseToolApprovalHeader(value)
	if err != nil {
		settings = &AgentApproval{Default: ApprovalAsk}
	}
	return WithAgentApproval(ctx, settings)
}
