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
	res, err := r.Resolve(ts, EntriesFromTools(catalogue(), nil))
	require.NoError(t, err)
	return res
}

func TestEntriesFromTools_ClassifiesByOriginThenPrefix(t *testing.T) {
	entries := EntriesFromTools([]mcp.Tool{
		serverTool("x_k8s_get", "k8s", true),
		{Name: "workflow_untagged"},
		{Name: "core_untagged"},
		{Name: "x_plain_tool"},
	}, nil)
	assert.Equal(t, Entry{Name: "x_k8s_get", Kind: KindEntryTool, Server: "k8s", ReadOnly: true}, entries[0])
	assert.Equal(t, KindEntryWorkflow, entries[1].Kind)
	assert.Equal(t, KindEntryCore, entries[2].Kind)
	assert.Equal(t, KindEntryTool, entries[3].Kind)
}

func TestEntriesFromTools_ServerLabels(t *testing.T) {
	labels := func(server string) map[string]string {
		if server == "k8s" {
			return map[string]string{"tier": "infrastructure"}
		}
		return nil
	}
	entries := EntriesFromTools(catalogue(), labels)
	assert.Equal(t, map[string]string{"tier": "infrastructure"}, entries[0].Labels)
	assert.Nil(t, entries[2].Labels, "family tool has no label for the family name")
	assert.Nil(t, entries[3].Labels, "workflows carry no server labels")
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
	_, err := BuiltIns().Resolve(ts, EntriesFromTools(catalogue(), nil))
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
