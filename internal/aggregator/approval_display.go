package aggregator

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/approval"
)

// What the approval page shows is worked out here, from the target's own
// definition when the call asks: the tool's MCP title or its name made
// readable, the connector, whether it changes data, and each argument
// labelled by the input schema's title or its key made readable (never the
// schema's description, which a connector writes in prose). Nothing comes
// from words an agent chose.

// toolDisplay describes a call of a server's tool.
func (a *AggregatorServer) toolDisplay(ctx context.Context, exposed string, info *api.MCPServerInfo, original string, args map[string]any) api.ApprovalDisplay {
	tool, found := a.catalogueTool(ctx, exposed)
	connector := ""
	if info != nil {
		connector = info.DisplayName
		if connector == "" {
			connector = info.Name
		}
	}
	title := readable(original)
	changesData := true
	if found {
		title = toolTitle(tool, original)
		changesData = tool.Annotations.ReadOnlyHint == nil || !*tool.Annotations.ReadOnlyHint
	}
	return api.ApprovalDisplay{
		Message:     approvalMessage(ctx, title, connector),
		Title:       title,
		Connector:   connector,
		ChangesData: changesData,
		Arguments:   labelledArguments(tool.InputSchema, args),
	}
}

// coreDisplay describes a call of one of muster's core tools. An edit of a
// connector shows the fields it changes, old to new, read from the connector
// as it is now; a preset shows the settings it expands to.
func (a *AggregatorServer) coreDisplay(ctx context.Context, name string, args map[string]any) api.ApprovalDisplay {
	tool, found := a.catalogueTool(ctx, name)
	title := readable(strings.TrimPrefix(name, "core_"))
	changesData := true
	if found {
		title = toolTitle(tool, name)
		changesData = tool.Annotations.ReadOnlyHint == nil || !*tool.Annotations.ReadOnlyHint
	}
	display := api.ApprovalDisplay{
		Message:     approvalMessage(ctx, title, "muster"),
		Title:       title,
		Connector:   "muster",
		ChangesData: changesData,
		Arguments:   labelledArguments(tool.InputSchema, args),
	}
	server, _ := args["name"].(string)
	switch name {
	case "core_mcpserver_update", "core_mcpserver_connect", "core_mcpserver_set_approval":
		display.Changes = connectorChanges(ctx, server, args)
	}
	if preset, _ := args["preset"].(string); preset != "" {
		display.Preset = a.presetDisplay(ctx, server, preset, name == "core_mcpserver_set_approval")
	}
	return display
}

// connectorChanges lists the arguments that change a connector's current
// field values. A connector muster cannot read shows no changes: the page
// shows the full arguments.
func connectorChanges(ctx context.Context, server string, args map[string]any) []api.ApprovalChange {
	info, err := connectorInfo(ctx, server)
	if err != nil || info == nil {
		return nil
	}
	raw, err := json.Marshal(info)
	if err != nil {
		return nil
	}
	var current map[string]any
	if json.Unmarshal(raw, &current) != nil {
		return nil
	}
	var changes []api.ApprovalChange
	for _, key := range sortedKeys(args) {
		if key == "name" || key == "preset" {
			continue
		}
		oldValue, newValue := displayValue(current[key]), displayValue(args[key])
		if current[key] == nil {
			oldValue = "(not set)"
		}
		if oldValue != newValue {
			changes = append(changes, api.ApprovalChange{Field: readable(key), Old: oldValue, New: newValue})
		}
	}
	return changes
}

// presetDisplay lists the settings a preset writes: per tool the server
// lists now, then the rule new tools follow. A server not connected yet
// lists no tools; the rule alone is shown.
func (a *AggregatorServer) presetDisplay(ctx context.Context, server, preset string, existing bool) []api.ApprovalArgument {
	var tools []mcp.Tool
	if existing {
		tools, _ = a.ServerTools(ctx, server)
	}
	expanded, err := api.ExpandApprovalPreset(preset, tools)
	if err != nil {
		return []api.ApprovalArgument{{Key: "preset", Label: "Preset", Value: preset + " (unknown: the call will fail)"}}
	}
	var shown []api.ApprovalArgument
	for _, name := range sortedKeys(expanded.Tools) {
		shown = append(shown, api.ApprovalArgument{Key: name, Label: name, Value: expanded.Tools[name]})
	}
	return append(shown, api.ApprovalArgument{Key: "rule", Label: "New tools (rule)", Value: expanded.Rule})
}

// catalogueTool finds a tool in the caller's catalogue by its exposed name.
func (a *AggregatorServer) catalogueTool(ctx context.Context, exposed string) (mcp.Tool, bool) {
	for _, tool := range a.ListToolsForContext(ctx) {
		if tool.Name == exposed {
			return tool, true
		}
	}
	return mcp.Tool{}, false
}

// toolTitle is a tool's MCP title, else its annotation title, else its name
// made readable.
func toolTitle(tool mcp.Tool, name string) string {
	switch {
	case tool.Title != "":
		return tool.Title
	case tool.Annotations.Title != "":
		return tool.Annotations.Title
	default:
		return readable(name)
	}
}

// approvalMessage is the plain-words summary the elicitation and the page
// lead with. muster does not know which agent calls (slice 3 binds it), so
// an agent caller is "An agent".
func approvalMessage(ctx context.Context, title, connector string) string {
	where := ""
	if connector != "" {
		where = " in " + connector
	}
	if api.IsAgentCaller(api.CallerKind(ctx, getSessionIDFromContext(ctx))) {
		return fmt.Sprintf("An agent wants to run %s%s.", title, where)
	}
	return fmt.Sprintf("Confirm %s%s.", title, where)
}

// labelledArguments labels each argument by the input schema's title or its
// key made readable; values are shown in full.
func labelledArguments(schema mcp.ToolInputSchema, args map[string]any) []api.ApprovalArgument {
	shown := make([]api.ApprovalArgument, 0, len(args))
	for _, key := range sortedKeys(args) {
		label := readable(key)
		if property, ok := schema.Properties[key].(map[string]any); ok {
			if title, ok := property["title"].(string); ok && title != "" {
				label = title
			}
		}
		shown = append(shown, api.ApprovalArgument{Key: key, Label: label, Value: displayValue(args[key])})
	}
	return shown
}

// displayValue writes a value as the page shows it: a string as it is,
// anything else in canonical JSON.
func displayValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	canonical, _, err := approval.Canonical(map[string]any{"v": value})
	if err != nil {
		return fmt.Sprint(value)
	}
	return strings.TrimSuffix(strings.TrimPrefix(canonical, `{"v":`), "}")
}

// readable turns a name such as merge_pull_request into "Merge pull request".
func readable(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool { return r == '_' || r == '-' || r == '.' })
	if len(words) == 0 {
		return name
	}
	text := strings.ToLower(strings.Join(words, " "))
	runes := []rune(text)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// ServerTools lists a server's own tools: the shared listing, else the
// calling session's cached capabilities for a server it signs in to.
func (a *AggregatorServer) ServerTools(ctx context.Context, server string) ([]mcp.Tool, bool) {
	info, ok := a.registry.GetServerInfo(server)
	if !ok || info == nil {
		return nil, false
	}
	info.mu.RLock()
	tools := slices.Clone(info.Tools)
	info.mu.RUnlock()
	if len(tools) > 0 || a.capabilityStore == nil {
		return tools, len(tools) > 0
	}
	sessionID := getSessionIDFromContext(ctx)
	if sessionID == "" {
		return nil, false
	}
	cached, err := a.capabilityStore.Get(ctx, sessionID, server)
	if err != nil || cached == nil {
		return nil, false
	}
	return slices.Clone(cached.Tools), len(cached.Tools) > 0
}
