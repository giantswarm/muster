package api

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveConnectorApproval(t *testing.T) {
	tests := []struct {
		name     string
		approval *MCPServerApproval
		tool     string
		want     ToolApproval
	}{
		{
			name: "no approval is allow, not reviewed",
			tool: "get",
			want: ToolApproval{Setting: ApprovalSettingAllow, Source: ApprovalSourceUnreviewed},
		},
		{
			name:     "an empty approval is reviewed: the tool is new and allowed",
			approval: &MCPServerApproval{},
			tool:     "get",
			want:     ToolApproval{Setting: ApprovalSettingAllow, Source: ApprovalSourceConnector, New: true},
		},
		{
			name:     "rule allow applies to a tool without an entry",
			approval: &MCPServerApproval{Rule: ApprovalAllow},
			tool:     "get",
			want:     ToolApproval{Setting: ApprovalSettingAllow, Source: ApprovalSourceConnector, New: true},
		},
		{
			name:     "rule ask applies to a tool without an entry",
			approval: &MCPServerApproval{Rule: ApprovalAsk, Tools: map[string]string{"other": ApprovalAllow}},
			tool:     "get",
			want:     ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceConnector, New: true},
		},
		{
			name:     "a tool entry wins over the rule (allow under ask)",
			approval: &MCPServerApproval{Rule: ApprovalAsk, Tools: map[string]string{"get": ApprovalAllow}},
			tool:     "get",
			want:     ToolApproval{Setting: ApprovalSettingAllow, Source: ApprovalSourceTool},
		},
		{
			name:     "a tool entry wins over the rule (ask under allow)",
			approval: &MCPServerApproval{Rule: ApprovalAllow, Tools: map[string]string{"merge": ApprovalAsk}},
			tool:     "merge",
			want:     ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceTool},
		},
		{
			name:     "block on a tool",
			approval: &MCPServerApproval{Rule: ApprovalAllow, Tools: map[string]string{"delete": ApprovalBlock}},
			tool:     "delete",
			want:     ToolApproval{Setting: ApprovalSettingBlock, Source: ApprovalSourceTool},
		},
		{
			name:     "an unknown tool value counts as ask",
			approval: &MCPServerApproval{Rule: ApprovalAllow, Tools: map[string]string{"get": "Deny"}},
			tool:     "get",
			want:     ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceTool},
		},
		{
			name:     "values are case-sensitive: allow in lower case counts as ask",
			approval: &MCPServerApproval{Tools: map[string]string{"get": "allow"}},
			tool:     "get",
			want:     ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceTool},
		},
		{
			name:     "an unknown rule counts as ask",
			approval: &MCPServerApproval{Rule: "Deny"},
			tool:     "get",
			want:     ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceConnector, New: true},
		},
		{
			name:     "a Block rule blocks every tool without its own entry",
			approval: &MCPServerApproval{Rule: ApprovalBlock, Tools: map[string]string{"get": ApprovalAllow}},
			tool:     "new_tool",
			want:     ToolApproval{Setting: ApprovalSettingBlock, Source: ApprovalSourceConnector, New: true},
		},
		{
			name:     "a tool's own entry wins over a Block rule",
			approval: &MCPServerApproval{Rule: ApprovalBlock, Tools: map[string]string{"get": ApprovalAllow}},
			tool:     "get",
			want:     ToolApproval{Setting: ApprovalSettingAllow, Source: ApprovalSourceTool},
		},
		{
			name:     "tool names match exactly",
			approval: &MCPServerApproval{Rule: ApprovalAllow, Tools: map[string]string{"Delete": ApprovalBlock}},
			tool:     "delete",
			want:     ToolApproval{Setting: ApprovalSettingAllow, Source: ApprovalSourceConnector, New: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ResolveConnectorApproval(tt.approval, tt.tool))
		})
	}
}

func TestStrictestApproval(t *testing.T) {
	allow := ToolApproval{Setting: ApprovalSettingAllow, Source: ApprovalSourceTool}
	ask := ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceConnector, New: true}
	block := ToolApproval{Setting: ApprovalSettingBlock, Source: ApprovalSourceTool}

	assert.Equal(t, block, StrictestApproval(allow, block, ask))
	assert.Equal(t, ask, StrictestApproval(allow, ask))
	assert.Equal(t, allow, StrictestApproval(allow))
	assert.Equal(t, ask, StrictestApproval(ask, ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceTool}),
		"a tie keeps the first")
	assert.Equal(t, ToolApproval{}, StrictestApproval(), "no approval at all is the zero value")
}

func TestBuiltinApproval(t *testing.T) {
	for _, name := range []string{"core_config_update_aggregator", "core_config_save", "core_config_reload"} {
		got, ok := BuiltinApproval(name)
		require.True(t, ok, name)
		assert.Equal(t, ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceBuiltin}, got, name)
	}
	for _, name := range []string{"core_config_get", "core_mcpserver_list", "x_github_config_save", "config_save"} {
		_, ok := BuiltinApproval(name)
		assert.False(t, ok, name)
	}
}

func TestExpandApprovalPreset(t *testing.T) {
	yes, no := true, false
	tools := []mcp.Tool{
		{Name: "get_issue", Annotations: mcp.ToolAnnotation{ReadOnlyHint: &yes}},
		{Name: "merge_pull_request", Annotations: mcp.ToolAnnotation{ReadOnlyHint: &no}},
		{Name: "unannotated"},
	}

	t.Run("recommended asks for every tool not annotated read-only", func(t *testing.T) {
		got, err := ExpandApprovalPreset(ApprovalPresetRecommended, tools)
		require.NoError(t, err)
		assert.Equal(t, &MCPServerApproval{Rule: ApprovalAsk, Tools: map[string]string{
			"get_issue":          ApprovalAllow,
			"merge_pull_request": ApprovalAsk,
			"unannotated":        ApprovalAsk,
		}}, got)
	})
	t.Run("allow-all", func(t *testing.T) {
		got, err := ExpandApprovalPreset(ApprovalPresetAllowAll, tools)
		require.NoError(t, err)
		assert.Equal(t, &MCPServerApproval{Rule: ApprovalAllow, Tools: map[string]string{
			"get_issue": ApprovalAllow, "merge_pull_request": ApprovalAllow, "unannotated": ApprovalAllow,
		}}, got)
	})
	t.Run("ask-all", func(t *testing.T) {
		got, err := ExpandApprovalPreset(ApprovalPresetAskAll, tools)
		require.NoError(t, err)
		assert.Equal(t, &MCPServerApproval{Rule: ApprovalAsk, Tools: map[string]string{
			"get_issue": ApprovalAsk, "merge_pull_request": ApprovalAsk, "unannotated": ApprovalAsk,
		}}, got)
	})
	t.Run("a server without tools gets the rule alone", func(t *testing.T) {
		got, err := ExpandApprovalPreset(ApprovalPresetRecommended, nil)
		require.NoError(t, err)
		assert.Equal(t, &MCPServerApproval{Rule: ApprovalAsk}, got)
	})
	t.Run("an unknown preset lists the three", func(t *testing.T) {
		_, err := ExpandApprovalPreset("Recommended", tools)
		require.Error(t, err)
		for _, p := range ApprovalPresets() {
			assert.Contains(t, err.Error(), p)
		}
	})
	t.Run("the expansion is explicit and never re-evaluated", func(t *testing.T) {
		got, err := ExpandApprovalPreset(ApprovalPresetRecommended, tools)
		require.NoError(t, err)
		// The server flips its hint later: what was stored still decides.
		yes2 := true
		tools[1].Annotations.ReadOnlyHint = &yes2
		assert.Equal(t, ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceTool},
			ResolveConnectorApproval(got, "merge_pull_request"))
		// A tool the server adds later follows the rule and is new.
		assert.Equal(t, ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceConnector, New: true},
			ResolveConnectorApproval(got, "close_issue"))
	})
}

func TestToolApprovalMeta(t *testing.T) {
	tool := mcp.Tool{Name: "x_github_get_issue", Meta: &mcp.Meta{AdditionalFields: map[string]any{"keep": 1}}}
	shared := tool.Meta
	_, ok := ToolApprovalOf(tool)
	require.False(t, ok)

	want := ToolApproval{Setting: ApprovalSettingAsk, Source: ApprovalSourceTool}
	SetToolApproval(&tool, want)
	got, ok := ToolApprovalOf(tool)
	require.True(t, ok)
	assert.Equal(t, want, got)
	assert.Equal(t, 1, tool.Meta.AdditionalFields["keep"])
	_, leaked := shared.AdditionalFields[MetaKeyApproval]
	assert.False(t, leaked, "the shared _meta of the registry's tool must not be written to")
}
