package metatools

import (
	"context"
	"testing"

	"github.com/giantswarm/muster/v5/internal/toolset"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withBound returns the context of a bound Session's request: the toolset of
// its Session binding, and the header as the transport reads it.
func withBound(bound []string, header string, present bool) context.Context {
	return toolset.ContextWithBound(withHeader(header, present), bound)
}

func TestBoundToolset_WithoutHeaderBoundsEveryReader(t *testing.T) {
	defer registerMockHandler(toolsetFixture())()
	p := NewProvider()
	ctx := withBound([]string{"preset:read-only"}, "", false)

	result, err := p.ExecuteTool(ctx, "list_tools", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"x_k8s_get", "workflow_triage"}, toolNames(t, decode(t, result)))

	result, err = p.ExecuteTool(ctx, "filter_tools", map[string]any{"limit": float64(10)})
	require.NoError(t, err)
	resp := decode(t, result)
	assert.Equal(t, []string{"x_k8s_get", "workflow_triage"}, toolNames(t, resp))
	assert.Equal(t, []any{"preset:read-only"}, resp["toolset"])
	assert.Equal(t, []any{"preset:read-only"}, resp["toolset_bound"])

	result, err = p.ExecuteTool(ctx, "describe_tool", map[string]any{"name": "x_k8s_delete"})
	require.NoError(t, err)
	assert.Equal(t, `tool "x_k8s_delete" is outside the toolset [preset:read-only] bound to the session`, errorText(t, result))

	result, err = p.ExecuteTool(ctx, "call_tool", map[string]any{"name": "x_k8s_delete"})
	require.NoError(t, err)
	assert.Equal(t, `tool "x_k8s_delete" is outside the toolset [preset:read-only] bound to the session`, errorText(t, result))

	result, err = p.ExecuteTool(ctx, "call_tool", map[string]any{"name": "x_k8s_get"})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	result, err = p.ExecuteTool(ctx, "get_resource", map[string]any{"uri": "gh://repos"})
	require.NoError(t, err)
	assert.Contains(t, errorText(t, result), "bound to the session")
}

func TestBoundToolset_HeaderNarrowsAndNeverWidens(t *testing.T) {
	defer registerMockHandler(toolsetFixture())()
	p := NewProvider()

	// A narrower header resolves to the intersection.
	ctx := withBound([]string{"server:k8s"}, "preset:read-only", true)
	result, err := p.ExecuteTool(ctx, "list_tools", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"x_k8s_get"}, toolNames(t, decode(t, result)))
	result, err = p.ExecuteTool(ctx, "call_tool", map[string]any{"name": "x_k8s_delete"})
	require.NoError(t, err)
	assert.Equal(t, `tool "x_k8s_delete" is outside the toolset [preset:read-only]`, errorText(t, result),
		"inside the binding, outside the header: the header refuses it")

	// A wider header gets the bound toolset and no error.
	ctx = withBound([]string{"preset:read-only"}, "preset:full", true)
	result, err = p.ExecuteTool(ctx, "filter_tools", map[string]any{"limit": float64(10)})
	require.NoError(t, err)
	resp := decode(t, result)
	assert.Equal(t, []string{"x_k8s_get", "workflow_triage"}, toolNames(t, resp))
	assert.Equal(t, []any{"preset:full"}, resp["toolset"])
	assert.Equal(t, []any{"preset:read-only"}, resp["toolset_bound"])
	result, err = p.ExecuteTool(ctx, "call_tool", map[string]any{"name": "x_k8s_delete"})
	require.NoError(t, err)
	assert.Equal(t, `tool "x_k8s_delete" is outside the toolset [preset:read-only] bound to the session`, errorText(t, result))
	result, err = p.ExecuteTool(ctx, "call_tool", map[string]any{"name": "x_gh_issues"})
	require.NoError(t, err)
	assert.Contains(t, errorText(t, result), "bound to the session", "a header naming a tool outside the binding cannot reach it")

	// The filter_tools argument narrows within both.
	result, err = p.ExecuteTool(ctx, "filter_tools", map[string]any{"toolset": []any{"server:k8s", "server:gh"}})
	require.NoError(t, err)
	assert.Equal(t, []string{"x_k8s_get"}, toolNames(t, decode(t, result)))
}

func TestBoundToolset_NoneResolvesNothing(t *testing.T) {
	defer registerMockHandler(toolsetFixture())()
	p := NewProvider()
	ctx := withBound([]string{"preset:none"}, "preset:full", true)

	result, err := p.ExecuteTool(ctx, "list_tools", nil)
	require.NoError(t, err)
	assert.Empty(t, toolNames(t, decode(t, result)))

	result, err = p.ExecuteTool(ctx, "call_tool", map[string]any{"name": "x_k8s_get"})
	require.NoError(t, err)
	assert.Equal(t, `tool "x_k8s_get" is outside the toolset [preset:none] bound to the session`, errorText(t, result))
}

func TestBoundToolset_AbsentBindingIsUnscoped(t *testing.T) {
	defer registerMockHandler(toolsetFixture())()
	p := NewProvider()

	result, err := p.ExecuteTool(withBound(nil, "", false), "list_tools", nil)
	require.NoError(t, err)
	assert.Len(t, toolNames(t, decode(t, result)), 5)

	result, err = p.ExecuteTool(withBound(nil, "", false), "call_tool", map[string]any{"name": "x_k8s_delete"})
	require.NoError(t, err)
	assert.False(t, result.IsError)

	result, err = p.ExecuteTool(withBound(nil, "server:gh", true), "list_tools", nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"x_gh_issues"}, toolNames(t, decode(t, result)), "the header alone still scopes it")
}

func TestBoundToolset_UnknownPresetErrorsOnEveryMetaTool(t *testing.T) {
	defer registerMockHandler(toolsetFixture())()
	p := NewProvider()
	for _, tool := range MetaToolNames {
		args := map[string]any{"name": "x_k8s_get", "uri": "k8s://pods"}
		result, err := p.ExecuteTool(withBound([]string{"preset:gone"}, "", false), tool, args)
		require.NoError(t, err, tool)
		assert.Contains(t, errorText(t, result), `names unknown preset "gone"`, tool)
	}
}

// A header selecting a family member the binding does not cannot call it.
func TestBoundToolset_FamilyMemberOutsideTheBinding(t *testing.T) {
	for _, tc := range []struct {
		header, member string
		inside         bool
	}{
		{"server:kube", "kube-a", true},
		{"server:kube", "kube-b", false},
		{"server:kube-b", "kube-b", false},
		{"tool:x_kube_get", "kube-b", false},
	} {
		m := familyFixture()
		restore := registerMockHandler(m)
		result, err := NewProvider().ExecuteTool(withBound([]string{"server:kube-a"}, tc.header, true), "call_tool",
			map[string]any{"name": "x_kube_get", "arguments": map[string]any{"server": tc.member}})
		restore()
		require.NoError(t, err)
		if tc.inside {
			assert.False(t, result.IsError, "%s on %s", tc.header, tc.member)
			continue
		}
		assert.Contains(t, errorText(t, result), "[server:kube-a] bound to the session", "%s on %s", tc.header, tc.member)
		assert.Empty(t, m.called, "%s on %s", tc.header, tc.member)
	}
}

func TestBoundToolset_AdmitsTheSignInToABoundProvider(t *testing.T) {
	m := toolsetFixture()
	m.tools = append(m.tools, tagged("core_auth_login", toolset.ToolOrigin{Kind: toolset.OriginKindCore}))
	defer registerMockHandler(m)()
	p := NewProvider()
	ctx := toolset.ContextWithBoundProviders(withBound([]string{"preset:read-only"}, "", false), []string{"github"})

	result, err := p.ExecuteTool(ctx, "call_tool", map[string]any{"name": "core_auth_login", "arguments": map[string]any{"app": "github"}})
	require.NoError(t, err)
	assert.False(t, result.IsError, "the binding releases github, so its sign-in is never outside the toolset")
	assert.Equal(t, []string{"core_auth_login"}, m.called)

	for name, args := range map[string]map[string]any{
		"an App the binding does not release": {"app": "gitlab"},
		"a server sign-in":                    {"server": "gh"},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := p.ExecuteTool(ctx, "call_tool", map[string]any{"name": "core_auth_login", "arguments": args})
			require.NoError(t, err)
			assert.Equal(t, `tool "core_auth_login" is outside the toolset [preset:read-only] bound to the session`, errorText(t, result))
		})
	}

	result, err = p.ExecuteTool(withBound([]string{"preset:read-only"}, "", false), "call_tool", map[string]any{"name": "core_auth_login", "arguments": map[string]any{"app": "github"}})
	require.NoError(t, err)
	assert.Contains(t, errorText(t, result), "outside the toolset", "without a binding's providers the toolset decides")
}
