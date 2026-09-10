package metatools

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/toolset"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ro(t mcp.Tool, readOnly bool) mcp.Tool {
	if readOnly {
		yes := true
		t.Annotations.ReadOnlyHint = &yes
	} else {
		no, yes := false, true
		t.Annotations.ReadOnlyHint = &no
		t.Annotations.DestructiveHint = &yes
	}
	return t
}

func tagged(name string, origin toolset.ToolOrigin) mcp.Tool {
	t := mcp.Tool{Name: name, Description: name + " description", InputSchema: mcp.ToolInputSchema{Type: "object"}}
	toolset.SetToolOrigin(&t, origin)
	return t
}

// toolsetFixture is a catalogue with a kubernetes server (read-only get,
// destructive delete), a workflow with the derived read-only hint, and a core
// tool; plus resources and prompts on two servers.
func toolsetFixture() *mockMetaToolsHandler {
	return &mockMetaToolsHandler{
		tools: []mcp.Tool{
			ro(tagged("x_k8s_get", toolset.ToolOrigin{Kind: toolset.OriginKindTool, Server: "k8s"}), true),
			ro(tagged("x_k8s_delete", toolset.ToolOrigin{Kind: toolset.OriginKindTool, Server: "k8s"}), false),
			tagged("x_gh_issues", toolset.ToolOrigin{Kind: toolset.OriginKindTool, Server: "gh"}),
			ro(tagged("workflow_triage", toolset.ToolOrigin{Kind: toolset.OriginKindWorkflow}), true),
			tagged("core_workflow_list", toolset.ToolOrigin{Kind: toolset.OriginKindCore}),
		},
		resources: []api.ResourceOrigin{
			{Resource: mcp.Resource{URI: "k8s://pods", Name: "pods"}, Server: "k8s"},
			{Resource: mcp.Resource{URI: "gh://repos", Name: "repos"}, Server: "gh"},
		},
		prompts: []api.PromptOrigin{
			{Prompt: mcp.Prompt{Name: "x_k8s_debug"}, Server: "k8s"},
			{Prompt: mcp.Prompt{Name: "x_gh_review"}, Server: "gh"},
		},
		callToolResult: &mcp.CallToolResult{Content: []mcp.Content{mcp.TextContent{Type: "text", Text: "ok"}}},
		getResourceResult: &mcp.ReadResourceResult{Contents: []mcp.ResourceContents{
			mcp.TextResourceContents{URI: "k8s://pods", Text: "pods"},
		}},
		getPromptResult: &mcp.GetPromptResult{Messages: []mcp.PromptMessage{{Role: mcp.RoleUser, Content: mcp.TextContent{Type: "text", Text: "hi"}}}},
	}
}

// withHeader returns a context as the transport would build it for a request
// carrying (or, with present=false, not carrying) X-Muster-Toolset.
func withHeader(value string, present bool) context.Context {
	req, _ := http.NewRequest(http.MethodPost, "/mcp", nil)
	if present {
		req.Header.Set(toolset.HeaderName, value)
	}
	return toolset.HTTPContextFunc(context.Background(), req)
}

func decode(t *testing.T, result *api.CallToolResult) map[string]any {
	t.Helper()
	require.NotNil(t, result)
	require.False(t, result.IsError, "expected success, got %v", result.Content)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(string)), &out))
	return out
}

func toolNames(t *testing.T, resp map[string]any) []string {
	t.Helper()
	raw, _ := resp["tools"].([]any)
	names := make([]string, 0, len(raw))
	for _, item := range raw {
		names = append(names, item.(map[string]any)["name"].(string))
	}
	return names
}

func errorText(t *testing.T, result *api.CallToolResult) string {
	t.Helper()
	require.NotNil(t, result)
	require.True(t, result.IsError, "expected an error result, got %v", result.Content)
	return result.Content[0].(string)
}

func TestToolset_NoHeaderIsUnscoped(t *testing.T) {
	defer registerMockHandler(toolsetFixture())()
	p := NewProvider()

	result, err := p.ExecuteTool(withHeader("", false), "list_tools", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"x_k8s_get", "x_k8s_delete", "x_gh_issues", "workflow_triage", "core_workflow_list"}, toolNames(t, decode(t, result)))

	result, err = p.ExecuteTool(withHeader("", false), "call_tool", map[string]any{"name": "x_k8s_delete"})
	require.NoError(t, err)
	assert.False(t, result.IsError)
}

func TestToolset_HeaderFiltersEveryReader(t *testing.T) {
	defer registerMockHandler(toolsetFixture())()
	p := NewProvider()
	ctx := withHeader("tool:x_k8s_get, workflow:triage", true)

	result, err := p.ExecuteTool(ctx, "list_tools", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"x_k8s_get", "workflow_triage"}, toolNames(t, decode(t, result)))

	result, err = p.ExecuteTool(ctx, "filter_tools", map[string]any{"limit": float64(10)})
	require.NoError(t, err)
	resp := decode(t, result)
	assert.Equal(t, []string{"x_k8s_get", "workflow_triage"}, toolNames(t, resp))
	assert.EqualValues(t, 2, resp["total_tools"], "total_tools is the size of the caller's (scoped) catalogue")

	result, err = p.ExecuteTool(ctx, "list_core_tools", nil)
	require.NoError(t, err)
	assert.Empty(t, toolNames(t, decode(t, result)), "core tools are hidden unless the toolset includes them")

	result, err = p.ExecuteTool(ctx, "describe_tool", map[string]any{"name": "x_k8s_get"})
	require.NoError(t, err)
	detail := decode(t, result)
	assert.Equal(t, "k8s", detail["server"])
	assert.Equal(t, "tool", detail["kind"])
	assert.Equal(t, map[string]any{"readOnlyHint": true}, detail["annotations"])

	result, err = p.ExecuteTool(ctx, "describe_tool", map[string]any{"name": "x_k8s_delete"})
	require.NoError(t, err)
	assert.Equal(t, `tool "x_k8s_delete" is outside the toolset [tool:x_k8s_get,workflow:triage]`, errorText(t, result))

	result, err = p.ExecuteTool(ctx, "describe_tool", map[string]any{"name": "x_unknown"})
	require.NoError(t, err)
	assert.Equal(t, "Tool not found: x_unknown", errorText(t, result), "an unknown tool is still simply not found")
}

func TestToolset_CallToolRefusesOutsideAndAllowsInside(t *testing.T) {
	defer registerMockHandler(toolsetFixture())()
	p := NewProvider()
	ctx := withHeader("preset:read-only", true)

	result, err := p.ExecuteTool(ctx, "call_tool", map[string]any{"name": "x_k8s_get"})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	result, err = p.ExecuteTool(ctx, "call_tool", map[string]any{"name": "workflow_triage"})
	require.NoError(t, err)
	assert.False(t, result.IsError, "a workflow with the derived read-only hint is inside read-only")

	result, err = p.ExecuteTool(ctx, "call_tool", map[string]any{"name": "x_k8s_delete"})
	require.NoError(t, err)
	assert.Equal(t, `tool "x_k8s_delete" is outside the toolset [preset:read-only]`, errorText(t, result))

	result, err = p.ExecuteTool(ctx, "call_tool", map[string]any{"name": "core_workflow_list"})
	require.NoError(t, err)
	assert.Contains(t, errorText(t, result), "is outside the toolset")
}

func TestToolset_HeaderErrorsOnEveryMetaTool(t *testing.T) {
	defer registerMockHandler(toolsetFixture())()
	p := NewProvider()

	cases := map[string]string{
		"":                    `toolset [] is empty; use "preset:none"`,
		"preset:foo":          `toolset [preset:foo] names unknown preset "foo"; known presets: read-only, none, full`,
		"toolset:shared":      `selector "toolset:shared" is reserved for shared toolsets`,
		"label:tier=infra":    `selector "label:tier=infra" is allowed inside presets only`,
		"preset:none,garbage": `selector "garbage" is malformed`,
	}
	for header, want := range cases {
		for _, tool := range MetaToolNames {
			args := map[string]any{"name": "x_k8s_get", "uri": "k8s://pods"}
			result, err := p.ExecuteTool(withHeader(header, true), tool, args)
			require.NoError(t, err, tool)
			assert.Contains(t, errorText(t, result), want, "%s with header %q", tool, header)
		}
	}
}

func TestToolset_SameSessionDifferentHeaders(t *testing.T) {
	defer registerMockHandler(toolsetFixture())()
	p := NewProvider()

	a, _ := p.ExecuteTool(withHeader("server:k8s", true), "list_tools", nil)
	b, _ := p.ExecuteTool(withHeader("server:gh", true), "list_tools", nil)
	c, _ := p.ExecuteTool(withHeader("", false), "list_tools", nil)
	assert.Equal(t, []string{"x_k8s_get", "x_k8s_delete"}, toolNames(t, decode(t, a)))
	assert.Equal(t, []string{"x_gh_issues"}, toolNames(t, decode(t, b)))
	assert.Len(t, toolNames(t, decode(t, c)), 5)
}

func TestToolset_FilterToolsArgument(t *testing.T) {
	defer registerMockHandler(toolsetFixture())()
	p := NewProviderWithPresets(must(toolset.NewRegistry(toolset.PresetsConfig{
		"infrastructure": {Description: "infra", Include: []toolset.Rule{{Server: "k8s"}}},
	})))

	// Unscoped request, argument given: resolution, unmatched, presets.
	result, err := p.ExecuteTool(withHeader("", false), "filter_tools", map[string]any{
		"toolset": []any{"preset:infrastructure", "server:nope", "tool:core_workflow_list"},
	})
	require.NoError(t, err)
	resp := decode(t, result)
	assert.Equal(t, []string{"x_k8s_get", "x_k8s_delete", "core_workflow_list"}, toolNames(t, resp))
	assert.Equal(t, []any{"preset:infrastructure", "server:nope", "tool:core_workflow_list"}, resp["toolset"])
	assert.Equal(t, []any{"server:nope"}, resp["toolset_unmatched"])
	presets, _ := resp["presets"].([]any)
	require.Len(t, presets, 4)
	assert.Equal(t, map[string]any{"name": "read-only", "description": presets[0].(map[string]any)["description"], "built_in": true}, presets[0])
	assert.Equal(t, "infrastructure", presets[3].(map[string]any)["name"])
	assert.Equal(t, false, presets[3].(map[string]any)["built_in"])

	// include_presets alone lists presets without resolving anything.
	result, err = p.ExecuteTool(withHeader("", false), "filter_tools", map[string]any{"include_presets": true, "limit": float64(10)})
	require.NoError(t, err)
	resp = decode(t, result)
	assert.Len(t, toolNames(t, resp), 5)
	assert.Len(t, resp["presets"], 4)
	assert.Nil(t, resp["toolset"])

	// Header and argument: the argument resolves within the header's toolset.
	result, err = p.ExecuteTool(withHeader("preset:read-only", true), "filter_tools", map[string]any{
		"toolset": []any{"server:k8s"},
	})
	require.NoError(t, err)
	resp = decode(t, result)
	assert.Equal(t, []string{"x_k8s_get"}, toolNames(t, resp), "server:k8s cannot widen read-only to the destructive tool")

	// preset:none resolves to a structured empty answer, not the legacy text.
	result, err = p.ExecuteTool(withHeader("", false), "filter_tools", map[string]any{"toolset": []any{"preset:none"}})
	require.NoError(t, err)
	resp = decode(t, result)
	assert.Empty(t, toolNames(t, resp))
	assert.Nil(t, resp["toolset_unmatched"])

	// Argument errors use the header's texts.
	for _, tc := range []struct{ arg, want any }{
		{[]any{}, `toolset [] is empty`},
		{[]any{"preset:foo"}, `names unknown preset "foo"; known presets: read-only, none, full, infrastructure`},
		{[]any{"label:a=b"}, `is allowed inside presets only`},
		{"preset:none", `toolset must be an array of selector strings`},
	} {
		result, err = p.ExecuteTool(withHeader("", false), "filter_tools", map[string]any{"toolset": tc.arg})
		require.NoError(t, err)
		assert.Contains(t, errorText(t, result), tc.want)
	}
}

func TestToolset_FilterToolsEchoesRequestToolset(t *testing.T) {
	defer registerMockHandler(toolsetFixture())()
	p := NewProvider()

	// Header only: the response names the header's toolset, selectors as
	// declared (trimmed), with the unmatched ones; presets stay opt-in.
	result, err := p.ExecuteTool(withHeader("preset:read-only, server:nope", true), "filter_tools", map[string]any{"limit": float64(10)})
	require.NoError(t, err)
	resp := decode(t, result)
	assert.Equal(t, []any{"preset:read-only", "server:nope"}, resp["toolset"])
	assert.Equal(t, []any{"server:nope"}, resp["toolset_unmatched"])
	assert.Nil(t, resp["presets"], "a request-declared toolset alone does not list the presets")
	assert.Equal(t, []string{"x_k8s_get", "workflow_triage"}, toolNames(t, resp))

	// Header and include_presets: the echo and the presets together.
	result, err = p.ExecuteTool(withHeader("preset:read-only", true), "filter_tools", map[string]any{"include_presets": true})
	require.NoError(t, err)
	resp = decode(t, result)
	assert.Equal(t, []any{"preset:read-only"}, resp["toolset"])
	assert.Len(t, resp["presets"], 3)

	// Header and argument: the argument is what the tools were resolved
	// within, so it is the one echoed.
	result, err = p.ExecuteTool(withHeader("preset:read-only", true), "filter_tools", map[string]any{"toolset": []any{"server:k8s"}})
	require.NoError(t, err)
	resp = decode(t, result)
	assert.Equal(t, []any{"server:k8s"}, resp["toolset"])
	assert.Equal(t, []string{"x_k8s_get"}, toolNames(t, resp))

	// No toolset anywhere: nothing to echo.
	result, err = p.ExecuteTool(withHeader("", false), "filter_tools", map[string]any{"limit": float64(10)})
	require.NoError(t, err)
	resp = decode(t, result)
	_, present := resp["toolset"]
	assert.False(t, present, "an unscoped request carries no toolset")
	assert.Nil(t, resp["toolset_unmatched"])

	// list_core_tools shares the engine and echoes the header the same way.
	result, err = p.ExecuteTool(withHeader("tool:core_workflow_list", true), "list_core_tools", nil)
	require.NoError(t, err)
	resp = decode(t, result)
	assert.Equal(t, []any{"tool:core_workflow_list"}, resp["toolset"])
	assert.Equal(t, []string{"core_workflow_list"}, toolNames(t, resp))
}

func TestToolset_ResourcesAndPromptsFollowTheServers(t *testing.T) {
	defer registerMockHandler(toolsetFixture())()
	p := NewProvider()
	ctx := withHeader("server:k8s", true)

	result, err := p.ExecuteTool(ctx, "list_resources", nil)
	require.NoError(t, err)
	text := result.Content[0].(string)
	assert.Contains(t, text, "k8s://pods")
	assert.NotContains(t, text, "gh://repos")

	result, err = p.ExecuteTool(ctx, "get_resource", map[string]any{"uri": "gh://repos"})
	require.NoError(t, err)
	assert.Equal(t, `resource "gh://repos" is outside the toolset [server:k8s]`, errorText(t, result))

	result, err = p.ExecuteTool(ctx, "get_resource", map[string]any{"uri": "k8s://pods"})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	result, err = p.ExecuteTool(ctx, "filter_prompts", map[string]any{})
	require.NoError(t, err)
	text = result.Content[0].(string)
	assert.Contains(t, text, "x_k8s_debug")
	assert.NotContains(t, text, "x_gh_review")

	result, err = p.ExecuteTool(ctx, "get_prompt", map[string]any{"name": "x_gh_review"})
	require.NoError(t, err)
	assert.Equal(t, `prompt "x_gh_review" is outside the toolset [server:k8s]`, errorText(t, result))

	result, err = p.ExecuteTool(ctx, "describe_prompt", map[string]any{"name": "x_gh_review"})
	require.NoError(t, err)
	assert.Contains(t, errorText(t, result), "is outside the toolset")
}

func TestToolInfo_CarriesOriginAndAnnotations(t *testing.T) {
	defer registerMockHandler(toolsetFixture())()
	p := NewProvider()

	result, err := p.ExecuteTool(withHeader("", false), "list_tools", nil)
	require.NoError(t, err)
	tools := decode(t, result)["tools"].([]any)
	first := tools[0].(map[string]any)
	assert.Equal(t, "k8s", first["server"])
	assert.Equal(t, "tool", first["kind"])
	assert.Equal(t, map[string]any{"readOnlyHint": true}, first["annotations"])
	deleteTool := tools[1].(map[string]any)
	assert.Equal(t, map[string]any{"readOnlyHint": false, "destructiveHint": true}, deleteTool["annotations"])
	plain := tools[2].(map[string]any)
	assert.Nil(t, plain["annotations"], "a tool without annotations has none")
	wf := tools[3].(map[string]any)
	assert.Equal(t, "workflow", wf["kind"])
	assert.Nil(t, wf["server"])
	core := tools[4].(map[string]any)
	assert.Equal(t, "core", core["kind"])

	result, err = p.ExecuteTool(withHeader("", false), "filter_tools", map[string]any{"pattern": "workflow_*"})
	require.NoError(t, err)
	item := decode(t, result)["tools"].([]any)[0].(map[string]any)
	assert.Equal(t, "workflow", item["kind"])
	assert.Equal(t, map[string]any{"readOnlyHint": true}, item["annotations"])
}

func must(r *toolset.Registry, err error) *toolset.Registry {
	if err != nil {
		panic(err)
	}
	return r
}
