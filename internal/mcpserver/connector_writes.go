package mcpserver

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"

	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"

	"github.com/giantswarm/muster/v5/internal/api"
)

// The connector write tools, split so each can be governed on its own:
// core_mcpserver_create (everything; a preset or approval settings
// required), core_mcpserver_update (description, timeout, suspended,
// restart and other fields that change no target), core_mcpserver_connect
// (where the server is and how muster reaches it), core_mcpserver_set_approval
// (approval settings or a preset) and core_mcpserver_delete. Every write is
// made as the caller, so whoever may update MCPServers in the namespace (the
// connector admins) may use them. The connector manager takes them over
// unchanged.

// connectFields are the arguments of core_mcpserver_connect: a connector
// pointed elsewhere or reached another way would be sent the person's
// forwarded token, so they are a floor for agents.
var connectFields = []string{"type", "toolPrefix", "family", "command", "args", "url", "env", "headers",
	"headersFrom", "requestMeta", "meta", "auth", "clientCredentials", "clearClientCredentials"}

// updateFields are the arguments of core_mcpserver_update besides name.
var updateFields = []string{"description", "displayName", "tags", "autoStart", "timeout", "progress",
	"suspended", "restartRequestedAt"}

// refuseOtherFields refuses arguments a write tool does not take, naming the
// tool that does.
func refuseOtherFields(tool string, args map[string]any, allowed []string, elsewhere string) *api.CallToolResult {
	var other []string
	for key := range args {
		if key != "name" && !slices.Contains(allowed, key) {
			other = append(other, key)
		}
	}
	if len(other) == 0 {
		return nil
	}
	slices.Sort(other)
	refused, _ := simpleError(fmt.Sprintf("%s does not take %s: use %s", tool, strings.Join(other, ", "), elsewhere))
	return refused
}

func connectorWriteTools() []api.ToolMetadata {
	args := mcpServerArgs(false)
	var connect, update []api.ArgMetadata
	for _, arg := range args {
		switch {
		case arg.Name == "name":
			connect, update = append(connect, arg), append(update, arg)
		case slices.Contains(connectFields, arg.Name):
			connect = append(connect, arg)
		case slices.Contains(updateFields, arg.Name):
			update = append(update, arg)
		}
	}
	connect = append(connect,
		clientCredentialsArg("Write-only: replaces the client of auth.authorizationServer with a new Secret owned by the server, written as you"),
		api.ArgMetadata{Name: "clearClientCredentials", Type: api.ArgTypeBoolean, Required: false, Description: "Drops the client stored with clientCredentials and keeps auth.authorizationServer; not with clientCredentials or clientCredentialsSecretRef"},
	)
	update = append(update,
		api.ArgMetadata{Name: "suspended", Type: api.ArgTypeBoolean, Required: false, Description: "Desired lifecycle state: true stops the server's service and keeps it stopped; false resumes it; omitted keeps the current value"},
		api.ArgMetadata{Name: "restartRequestedAt", Type: api.ArgTypeString, Required: false, Description: "RFC 3339 timestamp requesting a one-shot restart; processed once by the reconciler"},
	)
	return []api.ToolMetadata{
		{
			Name:        "mcpserver_update",
			Annotations: api.WriteAnnotations(true, true),
			Description: "Update an MCP server's description, display name, tags, autoStart, timeout, progress, suspended or restart.\n\nWhere the server is and how muster reaches it change with core_mcpserver_connect; its approval settings with core_mcpserver_set_approval.",
			Args:        update,
		},
		{
			Name:        "mcpserver_connect",
			Annotations: api.WriteAnnotations(true, true),
			Description: "Change where an MCP server is and how muster reaches it.\n\nFields: type, URL, command, args, env, headers, headersFrom, requestMeta, auth, clientCredentials, family and toolPrefix. auth replaces the server's auth, but an authorizationServer without clientCredentialsSecretRef keeps a client stored with clientCredentials; clearClientCredentials drops it. Changing its issuer or an endpoint while keeping that client is refused: send clientCredentials again.",
			Args:        connect,
		},
		{
			Name:        "mcpserver_set_approval",
			Annotations: api.WriteAnnotations(false, true),
			Description: "Set an MCP server's approval settings: per tool Allow, Ask or Block, and a rule for the rest.\n\nAllow runs, Ask asks a person first, Block hides the tool. Pass approval, or a preset that muster expands, once, into explicit settings from the tools the server lists now: recommended (read-only tools Allow, the rest Ask, rule Ask), allow-all or ask-all.",
			Args: []api.ArgMetadata{
				{Name: "name", Type: api.ArgTypeString, Required: true, Description: "Name of the MCP server"},
				presetArg(),
				approvalArg(),
				{Name: "expectedGeneration", Type: api.ArgTypeInteger, Required: false, Description: "The generation core_mcpserver_get returned; the write is refused when the server changed since, so a concurrent change is not overwritten"},
			},
		},
	}
}

func presetArg() api.ArgMetadata {
	return api.ArgMetadata{Name: "preset", Type: api.ArgTypeString, Required: false,
		Description: "One of " + strings.Join(api.ApprovalPresets(), ", ") + "; not with approval"}
}

func approvalArg() api.ArgMetadata {
	return api.ArgMetadata{Name: "approval", Type: api.ArgTypeObject, Required: false,
		Description: "Explicit settings {rule: Allow|Ask|Block, tools: {<tool>: Allow|Ask|Block}}; rule covers tools without an entry, new ones too; not with preset",
		Schema: map[string]interface{}{
			api.SchemaKeyType: string(api.ArgTypeObject),
			"properties": map[string]interface{}{
				"rule":  map[string]interface{}{api.SchemaKeyType: "string", "enum": []string{api.ApprovalAllow, api.ApprovalAsk, api.ApprovalBlock}},
				"tools": map[string]interface{}{api.SchemaKeyType: "object", api.SchemaKeyAdditionalProperties: map[string]interface{}{api.SchemaKeyType: "string", "enum": []string{api.ApprovalAllow, api.ApprovalAsk, api.ApprovalBlock}}},
			},
			api.SchemaKeyAdditionalProperties: false,
		}}
}

// approvalFromArgs reads preset or approval from a write's arguments and
// removes both. required refuses a write with neither; tools are what a
// preset expands from.
func approvalFromArgs(args map[string]any, required bool, tools func() []mcp.Tool) (*musterv1alpha1.MCPServerApproval, *api.CallToolResult) {
	preset, hasPreset := args["preset"]
	raw, hasApproval := args["approval"]
	delete(args, "preset")
	delete(args, "approval")
	refuse := func(msg string) (*musterv1alpha1.MCPServerApproval, *api.CallToolResult) {
		refused, _ := simpleError(msg)
		return nil, refused
	}
	switch {
	case hasPreset && hasApproval:
		return refuse("pass either preset or approval, not both")
	case hasPreset:
		name, _ := preset.(string)
		expanded, err := api.ExpandApprovalPreset(name, tools())
		if err != nil {
			return refuse(err.Error())
		}
		return &musterv1alpha1.MCPServerApproval{Rule: expanded.Rule, Tools: expanded.Tools}, nil
	case hasApproval:
		object, ok := raw.(map[string]any)
		if !ok {
			return refuse("approval must be an object {rule, tools}")
		}
		approval := &musterv1alpha1.MCPServerApproval{}
		for key, value := range object {
			switch key {
			case "rule":
				rule, _ := value.(string)
				if rule != api.ApprovalAllow && rule != api.ApprovalAsk && rule != api.ApprovalBlock {
					return refuse("approval.rule must be Allow, Ask or Block")
				}
				approval.Rule = rule
			case "tools":
				entries, ok := value.(map[string]any)
				if !ok {
					return refuse("approval.tools must map tool names to Allow, Ask or Block")
				}
				approval.Tools = make(map[string]string, len(entries))
				for tool, setting := range entries {
					s, _ := setting.(string)
					if s != api.ApprovalAllow && s != api.ApprovalAsk && s != api.ApprovalBlock {
						return refuse(fmt.Sprintf("approval.tools.%s must be Allow, Ask or Block", tool))
					}
					approval.Tools[tool] = s
				}
			default:
				return refuse(fmt.Sprintf("approval takes rule and tools, not %s", key))
			}
		}
		return approval, nil
	case required:
		return refuse("core_mcpserver_create needs the server's approval settings: pass preset (" + strings.Join(api.ApprovalPresets(), ", ") + ") or approval {rule, tools}")
	}
	return nil, nil
}

// expectedGeneration reads the optional precondition of a settings write.
func expectedGeneration(args map[string]any) (int64, bool, *api.CallToolResult) {
	raw, ok := args["expectedGeneration"]
	delete(args, "expectedGeneration")
	if !ok || raw == nil {
		return 0, false, nil
	}
	switch v := raw.(type) {
	case float64:
		if v == float64(int64(v)) {
			return int64(v), true, nil
		}
	case int:
		return int64(v), true, nil
	case int64:
		return v, true, nil
	}
	refused, _ := simpleError("expectedGeneration must be the integer generation core_mcpserver_get returned")
	return 0, false, refused
}

// handleMCPServerSetApproval writes a server's spec.approval as the caller.
// A preset expands from the tools the server lists now; what is written is
// explicit and never evaluated again.
func (a *Adapter) handleMCPServerSetApproval(ctx context.Context, args map[string]any) (*api.CallToolResult, error) {
	args = maps.Clone(args)
	name, _ := args["name"].(string)
	if name == "" {
		return simpleError("name is required")
	}
	if refused := refuseOtherFields("core_mcpserver_set_approval", args, []string{"preset", "approval", "expectedGeneration"}, "core_mcpserver_update or core_mcpserver_connect"); refused != nil {
		return refused, nil
	}
	expected, hasExpected, refused := expectedGeneration(args)
	if refused != nil {
		return refused, nil
	}
	approval, refused := approvalFromArgs(args, true, func() []mcp.Tool {
		tools, _ := api.ServerTools(ctx, name)
		return tools
	})
	if refused != nil {
		return refused, nil
	}
	if refused := refuseTenantKey(name, "update"); refused != nil {
		return refused, nil
	}
	if problem := a.ApprovalSchemaProblem(ctx); problem != "" {
		return simpleError("core_mcpserver_set_approval cannot store approval settings: " + problem)
	}
	writer, errResult := a.mutationWriter(ctx)
	if errResult != nil {
		return errResult, nil
	}
	var old *musterv1alpha1.MCPServerApproval
	var changed *api.CallToolResult
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		existing, err := a.client.GetMCPServer(ctx, name, a.namespace)
		if err != nil {
			return err
		}
		if hasExpected && existing.Generation != expected {
			changed, _ = simpleError(fmt.Sprintf("conflict: MCP server '%s' changed since it was read (generation %d now, expectedGeneration %d): read it again with core_mcpserver_get and apply the change to what it holds now", name, existing.Generation, expected))
			return errUpdateRefused
		}
		old = existing.Spec.Approval
		existing.Spec.Approval = approval
		return writer.UpdateMCPServer(ctx, existing)
	})
	if changed != nil {
		return changed, nil
	}
	if err != nil {
		if errors.IsNotFound(err) {
			return api.HandleErrorWithPrefix(api.NewMCPServerNotFoundError(name), "Failed to set approval"), nil
		}
		if msg := a.describeWriteAuthError(err, "update", name); msg != "" {
			return simpleError(msg)
		}
		return simpleError(fmt.Sprintf("Failed to set approval of MCP server '%s': %v", name, err))
	}
	if refused := a.verifyApprovalStored(ctx, name, approval); refused != nil {
		return refused, nil
	}
	auditApprovalChange(ctx, "core_mcpserver_set_approval", name, old, approval)
	return &api.CallToolResult{
		Content: []interface{}{map[string]interface{}{
			"name":     name,
			"approval": convertCRDApprovalToAPI(approval),
			"message":  fmt.Sprintf("Approval settings of MCP server '%s' set", name),
		}},
	}, nil
}

// auditApprovalChange records a change of a connector's approval settings,
// old to new as structured values, with who made it in full.
func auditApprovalChange(ctx context.Context, tool, server string, old, updated *musterv1alpha1.MCPServerApproval) {
	api.AuditApproval(ctx, "approval_settings_change",
		slog.String("tool", tool),
		slog.String("server", server),
		slog.Any("old", approvalValue(old)),
		slog.Any("new", approvalValue(updated)))
}

// auditConnectorWrite records a connector write other than its settings:
// which fields the call set. Values are left out: headers, env, auth and
// client credentials can carry secrets; the URL, type, command and prefix
// are recorded, as where a connector points is what its floors protect.
func auditConnectorWrite(ctx context.Context, tool, server string, args map[string]any) {
	fields := make([]string, 0, len(args))
	for key := range args {
		if key != "name" {
			fields = append(fields, key)
		}
	}
	slices.Sort(fields)
	attrs := []slog.Attr{slog.String("tool", tool), slog.String("server", server), slog.Any("fields", fields)}
	for _, key := range []string{"url", "type", "command", "toolPrefix", "suspended", "autoStart"} {
		if value, ok := args[key]; ok {
			attrs = append(attrs, slog.Any(key, value))
		}
	}
	api.AuditApproval(ctx, "connector_write", attrs...)
}

// audited runs a connector write and records it when it succeeded.
func audited(ctx context.Context, tool string, args map[string]any, run func() (*api.CallToolResult, error)) (*api.CallToolResult, error) {
	result, err := run()
	if err != nil || result == nil || result.IsError {
		return result, err
	}
	name, _ := args["name"].(string)
	auditConnectorWrite(ctx, tool, name, args)
	return result, nil
}

// approvalValue is settings as the audit records them.
func approvalValue(approval *musterv1alpha1.MCPServerApproval) any {
	if approval == nil {
		return nil
	}
	return map[string]any{"rule": approval.Rule, "tools": approval.Tools}
}

// noTools is the tool list of a server not created yet.
func noTools() []mcp.Tool { return nil }
