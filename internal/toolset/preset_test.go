package toolset

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/yaml.v3"
)

func boolPtr(b bool) *bool { return &b }

func TestNewRegistry_BuiltInsAlwaysPresent(t *testing.T) {
	r := BuiltIns()
	assert.Equal(t, []string{"read-only", "none", "full"}, r.Names())
	infos := r.List()
	require.Len(t, infos, 3)
	for _, info := range infos {
		assert.True(t, info.BuiltIn, info.Name)
		assert.NotEmpty(t, info.Description, info.Name)
	}
}

func TestNewRegistry_ConfiguredPresetsListedAfterBuiltInsSorted(t *testing.T) {
	r, err := NewRegistry(PresetsConfig{
		"infrastructure": {Description: "infra", Include: []Rule{{Server: "mcp-kubernetes"}}},
		"agent-platform": {Description: "ap", Include: []Rule{{Server: "agent-manager"}, {Pattern: "core_*"}}},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"read-only", "none", "full", "agent-platform", "infrastructure"}, r.Names())
	infos := r.List()
	assert.Equal(t, Info{Name: "agent-platform", Description: "ap", BuiltIn: false}, infos[3])
}

func TestNewRegistry_ValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		cfg  PresetsConfig
		want string
	}{
		{"redefines built-in", PresetsConfig{"read-only": {Include: []Rule{{Pattern: "*"}}}}, `preset "read-only" is built in and cannot be redefined`},
		{"redefines none", PresetsConfig{"none": {}}, `preset "none" is built in`},
		{"bad name", PresetsConfig{"has space": {Include: []Rule{{Pattern: "*"}}}}, `preset name "has space" is invalid`},
		{"empty rule", PresetsConfig{"x": {Include: []Rule{{}}}}, `preset "x" include[0] must set exactly one of tool, pattern, server, workflow, readOnly, preset`},
		{"two keys", PresetsConfig{"x": {Include: []Rule{{Tool: "a", Server: "b"}}}}, `include[0] must set exactly one of`},
		{"bad pattern", PresetsConfig{"x": {Include: []Rule{{Pattern: "[a"}}}}, `include[0] pattern "[a" is invalid`},
		{"readOnly false", PresetsConfig{"x": {Include: []Rule{{ReadOnly: boolPtr(false)}}}}, `readOnly: false selects nothing`},
		{"preset in exclude", PresetsConfig{"x": {Include: []Rule{{Pattern: "*"}}, Exclude: []Rule{{Preset: "none"}}}}, `exclude[0] cannot compose a preset in exclude`},
		{"unknown composed preset", PresetsConfig{"x": {Include: []Rule{{Preset: "nope"}}}}, `preset "x" includes unknown preset "nope"; known presets: read-only, none, full, x`},
		{"cycle", PresetsConfig{"a": {Include: []Rule{{Preset: "b"}}}, "b": {Include: []Rule{{Preset: "a"}}}}, `preset composition cycles: a -> b -> a`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewRegistry(tc.cfg)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestRegistry_CheckNamesUnknownPreset(t *testing.T) {
	r, err := NewRegistry(PresetsConfig{"infrastructure": {Include: []Rule{{Server: "k8s"}}}})
	require.NoError(t, err)

	ts, err := ParseHeader("preset:read-only,preset:foo")
	require.NoError(t, err)
	err = r.Check(ts)
	require.Error(t, err)
	assert.Equal(t, `toolset [preset:read-only,preset:foo] names unknown preset "foo"; known presets: read-only, none, full, infrastructure`, err.Error())

	ok, _ := ParseHeader("preset:read-only,preset:infrastructure,preset:none,preset:full")
	assert.NoError(t, r.Check(ok))
}

func TestPresetsConfig_YAMLShape(t *testing.T) {
	src := `
infrastructure:
  description: Every infrastructure server
  include:
    - server: mcp-kubernetes
    - pattern: x_mcp-prometheus_*
    - workflow: incident-triage
    - tool: core_workflow_list
    - readOnly: true
    - preset: none
  exclude:
    - tool: x_mcp-kubernetes_delete
`
	var cfg PresetsConfig
	require.NoError(t, yaml.Unmarshal([]byte(src), &cfg))
	p := cfg["infrastructure"]
	assert.Equal(t, "Every infrastructure server", p.Description)
	require.Len(t, p.Include, 6)
	assert.Equal(t, "server: mcp-kubernetes", p.Include[0].String())
	assert.Equal(t, "pattern: x_mcp-prometheus_*", p.Include[1].String())
	assert.Equal(t, "workflow: incident-triage", p.Include[2].String())
	assert.Equal(t, "tool: core_workflow_list", p.Include[3].String())
	assert.Equal(t, "readOnly: true", p.Include[4].String())
	assert.Equal(t, "preset: none", p.Include[5].String())
	require.Len(t, p.Exclude, 1)
	_, err := NewRegistry(cfg)
	require.NoError(t, err)
}
