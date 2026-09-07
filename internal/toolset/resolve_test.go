package toolset

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func serverTool(name, server string, readOnly bool) mcp.Tool {
	t := mcp.Tool{Name: name}
	if readOnly {
		t.Annotations.ReadOnlyHint = boolPtr(true)
	}
	SetToolOrigin(&t, ToolOrigin{Kind: OriginKindTool, Server: server})
	return t
}

func familyTool(name, family string, members []string, readOnly bool) mcp.Tool {
	t := mcp.Tool{Name: name}
	if readOnly {
		t.Annotations.ReadOnlyHint = boolPtr(true)
	}
	SetToolOrigin(&t, ToolOrigin{Kind: OriginKindTool, Server: family, Servers: members})
	return t
}

func workflowTool(name string, readOnly bool) mcp.Tool {
	t := mcp.Tool{Name: name}
	if readOnly {
		t.Annotations.ReadOnlyHint = boolPtr(true)
	}
	SetToolOrigin(&t, ToolOrigin{Kind: OriginKindWorkflow})
	return t
}

func coreTool(name string) mcp.Tool {
	t := mcp.Tool{Name: name}
	SetToolOrigin(&t, ToolOrigin{Kind: OriginKindCore})
	return t
}

// catalogue is the fixture every resolution test shares: a kubernetes server
// with a read-only and a destructive tool, a prometheus family with two
// members, a read-only and a read-write workflow, and two core tools.
func catalogue() []mcp.Tool {
	return []mcp.Tool{
		serverTool("x_k8s_get", "k8s", true),
		serverTool("x_k8s_delete", "k8s", false),
		familyTool("x_prometheus_query", "prometheus", []string{"prom-a", "prom-b"}, true),
		workflowTool("workflow_triage", true),
		workflowTool("workflow_rollout", false),
		coreTool("core_workflow_list"),
		coreTool("core_auth_login"),
	}
}

func resolve(t *testing.T, r *Registry, header string) Resolution {
	t.Helper()
	ts, err := ParseHeader(header)
	require.NoError(t, err)
	res, err := r.Resolve(ts, EntriesFromTools(catalogue()))
	require.NoError(t, err)
	return res
}

func TestEntriesFromTools_ClassifiesByOriginThenPrefix(t *testing.T) {
	entries := EntriesFromTools([]mcp.Tool{
		serverTool("x_k8s_get", "k8s", true),
		{Name: "workflow_untagged"},
		{Name: "core_untagged"},
		{Name: "x_plain_tool"},
	})
	assert.Equal(t, Entry{Name: "x_k8s_get", Kind: KindEntryTool, Server: "k8s", ReadOnly: true}, entries[0])
	assert.Equal(t, KindEntryWorkflow, entries[1].Kind)
	assert.Equal(t, KindEntryCore, entries[2].Kind)
	assert.Equal(t, KindEntryTool, entries[3].Kind)
}

func TestResolveWith_LabelRules(t *testing.T) {
	const group = "agent-platform.giantswarm.io/tool-group"
	labels := func(server string) map[string]string {
		switch server {
		case "k8s":
			return map[string]string{group: "infrastructure", "tier": ""}
		case "prom-b": // one family member carries the label
			return map[string]string{group: "infrastructure"}
		}
		return nil
	}
	r, err := NewRegistry(PresetsConfig{
		"infrastructure": {Include: []Rule{{Label: group + "=infrastructure"}}},
		"agent-platform": {Include: []Rule{{Label: group + "=agent-platform"}, {Pattern: "core_*"}}},
		"tiered":         {Include: []Rule{{Label: "tier"}}},
		"empty-tier":     {Include: []Rule{{Label: "tier="}}},
		"no-deletes":     {Include: []Rule{{Label: group}}, Exclude: []Rule{{Pattern: "*_delete"}}},
	})
	require.NoError(t, err)
	entries := EntriesFromTools(catalogue())

	res, err := r.ResolveWith(must(ParseHeader("preset:infrastructure")), entries, labels)
	require.NoError(t, err)
	assert.Equal(t, []string{"x_k8s_delete", "x_k8s_get", "x_prometheus_query"}, res.Names(), "value match; a family tool joins through a labelled member")

	res, _ = r.ResolveWith(must(ParseHeader("preset:agent-platform")), entries, labels)
	assert.Equal(t, []string{"core_auth_login", "core_workflow_list"}, res.Names(), "no server carries that value; core tools via the pattern")

	res, _ = r.ResolveWith(must(ParseHeader("preset:tiered")), entries, labels)
	assert.Equal(t, []string{"x_k8s_delete", "x_k8s_get"}, res.Names(), "presence match")
	res, _ = r.ResolveWith(must(ParseHeader("preset:empty-tier")), entries, labels)
	assert.Equal(t, []string{"x_k8s_delete", "x_k8s_get"}, res.Names(), "key= matches the empty value")

	res, _ = r.ResolveWith(must(ParseHeader("preset:no-deletes")), entries, labels)
	assert.Equal(t, []string{"x_k8s_get", "x_prometheus_query"}, res.Names(), "exclude applies after label includes")

	res, _ = r.ResolveWith(must(ParseHeader("preset:infrastructure")), entries, nil)
	assert.Empty(t, res.Names(), "without a label lookup label rules select nothing")
	assert.Equal(t, []string{"preset:infrastructure"}, res.Unmatched)
}

func TestNewRegistry_LabelRuleValidation(t *testing.T) {
	for _, bad := range []string{"=infra", "a b=c", "key=va lue", "-x"} {
		_, err := NewRegistry(PresetsConfig{"x": {Include: []Rule{{Label: bad}}}})
		require.Error(t, err, bad)
		assert.Contains(t, err.Error(), `include[0] label "`+bad+`" is invalid; expected <key>=<value> or <key>`)
	}
	for _, ok := range []string{"tier", "tier=", "tier=gold", "agent-platform.giantswarm.io/tool-group=infrastructure"} {
		_, err := NewRegistry(PresetsConfig{"x": {Include: []Rule{{Label: ok}}}})
		require.NoError(t, err, ok)
	}
	_, err := NewRegistry(PresetsConfig{"x": {Include: []Rule{{Label: "tier", Server: "k8s"}}}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must set exactly one of tool, pattern, server, workflow, readOnly, preset, label")
}

func must(ts Toolset, err error) Toolset {
	if err != nil {
		panic(err)
	}
	return ts
}

func TestResolve_InlineSelectors(t *testing.T) {
	r := BuiltIns()

	res := resolve(t, r, "tool:x_k8s_get")
	assert.Equal(t, []string{"x_k8s_get"}, res.Names())
	assert.Empty(t, res.Unmatched)
	assert.True(t, res.ContainsServer("k8s"))

	res = resolve(t, r, "server:k8s")
	assert.Equal(t, []string{"x_k8s_delete", "x_k8s_get"}, res.Names())

	// The family name selects the family surface; so does a member name.
	res = resolve(t, r, "server:prometheus")
	assert.Equal(t, []string{"x_prometheus_query"}, res.Names())
	assert.True(t, res.ContainsServer("prom-a"))
	assert.True(t, res.ContainsServer("prom-b"))
	res = resolve(t, r, "server:prom-b")
	assert.Equal(t, []string{"x_prometheus_query"}, res.Names())

	res = resolve(t, r, "workflow:triage")
	assert.Equal(t, []string{"workflow_triage"}, res.Names())
	assert.False(t, res.ContainsServer("k8s"))

	// Core tools only when named explicitly.
	res = resolve(t, r, "tool:core_workflow_list")
	assert.Equal(t, []string{"core_workflow_list"}, res.Names())
}

func TestResolve_UnmatchedSelectors(t *testing.T) {
	res := resolve(t, BuiltIns(), "tool:x_k8s_get, server:nope ,workflow:missing,tool:x_k8s_nope,preset:none")
	assert.Equal(t, []string{"x_k8s_get"}, res.Names())
	assert.Equal(t, []string{"server:nope", "workflow:missing", "tool:x_k8s_nope"}, res.Unmatched, "preset:none is empty by definition and not reported")
}

func TestResolve_BuiltInPresets(t *testing.T) {
	r := BuiltIns()

	assert.Equal(t, []string{"workflow_triage", "x_k8s_get", "x_prometheus_query"}, resolve(t, r, "preset:read-only").Names(),
		"read-only: annotated server tools plus the workflow with the derived hint; core tools carry no annotation")

	res := resolve(t, r, "preset:none")
	assert.Empty(t, res.Names())
	assert.Empty(t, res.Unmatched)

	assert.Len(t, resolve(t, r, "preset:full").Names(), len(catalogue()))

	// Inline selectors add to a preset.
	assert.Equal(t, []string{"workflow_rollout", "workflow_triage", "x_k8s_get", "x_prometheus_query"},
		resolve(t, r, "preset:read-only,workflow:rollout").Names())
}

func TestResolve_ConfiguredPresets(t *testing.T) {
	r, err := NewRegistry(PresetsConfig{
		"infra": {Include: []Rule{{Server: "k8s"}, {Server: "prometheus"}}, Exclude: []Rule{{Pattern: "*_delete"}}},
		"ops":   {Include: []Rule{{Preset: "infra"}, {Workflow: "rollout"}, {Tool: "core_workflow_list"}}},
		"safe":  {Include: []Rule{{Preset: "ops"}, {ReadOnly: boolPtr(true)}}, Exclude: []Rule{{Workflow: "rollout"}}},
		"core":  {Include: []Rule{{Pattern: "core_*"}}},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"x_k8s_get", "x_prometheus_query"}, resolve(t, r, "preset:infra").Names(), "exclude removes the destructive tool")
	assert.Equal(t, []string{"core_workflow_list", "workflow_rollout", "x_k8s_get", "x_prometheus_query"}, resolve(t, r, "preset:ops").Names(), "composition")
	assert.Equal(t, []string{"core_workflow_list", "workflow_triage", "x_k8s_get", "x_prometheus_query"}, resolve(t, r, "preset:safe").Names(), "composition plus readOnly, exclude applied last")
	assert.Equal(t, []string{"core_auth_login", "core_workflow_list"}, resolve(t, r, "preset:core").Names(), "core tools via pattern")
}

func TestResolve_UnknownPresetIsAnError(t *testing.T) {
	ts, _ := ParseHeader("preset:foo")
	_, err := BuiltIns().Resolve(ts, EntriesFromTools(catalogue()))
	require.Error(t, err)
	assert.Equal(t, `toolset [preset:foo] names unknown preset "foo"; known presets: read-only, none, full`, err.Error())
}

func TestFilter_PreservesCatalogueOrder(t *testing.T) {
	res := resolve(t, BuiltIns(), "tool:core_auth_login,tool:x_k8s_get")
	filtered := Filter(catalogue(), res)
	require.Len(t, filtered, 2)
	assert.Equal(t, "x_k8s_get", filtered[0].Name)
	assert.Equal(t, "core_auth_login", filtered[1].Name)
}

func TestSetToolOrigin_ClonesMeta(t *testing.T) {
	shared := &mcp.Meta{AdditionalFields: map[string]any{MetaKeyLabels: map[string]string{"a": "b"}}}
	downstream := mcp.Tool{Name: "get", Meta: shared}
	exposed := downstream
	SetToolOrigin(&exposed, ToolOrigin{Kind: OriginKindTool, Server: "k8s"})

	_, hasOrigin := shared.AdditionalFields[MetaKeyOrigin]
	assert.False(t, hasOrigin, "the downstream tool's meta must not be mutated")
	origin, ok := ToolOriginOf(exposed)
	require.True(t, ok)
	assert.Equal(t, "k8s", origin.Server)
	assert.Equal(t, map[string]string{"a": "b"}, exposed.Meta.AdditionalFields[MetaKeyLabels], "existing meta keys are kept")
}
