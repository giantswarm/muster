package orchestrator

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/internal/services"
)

type fixedVisibility struct{ view api.ServerVisibility }

func (f fixedVisibility) CallerVisibility(context.Context) api.ServerVisibility { return f.view }
func (f fixedVisibility) LoginAudience() string                                 { return "" }

// The service of an MCP server the caller may not see is absent from
// core_service_list, and core_service_status, start, stop and restart answer
// for it as for a missing service, before anything acts on it
// (agent-permissions D9).
func TestServiceTools_HideServersTheCallerMayNotSee(t *testing.T) {
	o := New(Config{Aggregator: config.AggregatorConfig{}})
	for _, name := range []string{"github", "org-a/github", "org-b/github"} {
		require.NoError(t, o.registry.Register(&mockService{name: name, state: services.StateRunning}))
	}
	adapter := NewAPIAdapter(o)
	ctx := context.Background()

	listed := func() []string {
		result, err := adapter.ExecuteTool(ctx, "service_list", nil)
		require.NoError(t, err)
		var names []string
		for _, svc := range result.Content[0].(map[string]interface{})["services"].([]api.ServiceStatus) {
			names = append(names, svc.Name)
		}
		slices.Sort(names)
		return names
	}
	assert.Equal(t, []string{"github", "org-a/github", "org-b/github"}, listed(), "setting off: every service, as before")

	api.RegisterMCPServerVisibility(fixedVisibility{view: api.ServersOfNamespacesVisible("muster", []string{"muster", "org-a"})})
	t.Cleanup(func() { api.RegisterMCPServerVisibility(nil) })

	assert.Equal(t, []string{"github", "org-a/github"}, listed())
	for _, tool := range []string{"service_status", "service_start", "service_stop", "service_restart"} {
		result, err := adapter.ExecuteTool(ctx, tool, map[string]interface{}{"name": "org-b/github"})
		require.NoError(t, err)
		assert.True(t, result.IsError, tool)
		assert.True(t, strings.Contains(result.Content[0].(string), "service org-b/github not found"), "%s: %v", tool, result.Content)
	}
	result, err := adapter.ExecuteTool(ctx, "service_status", map[string]interface{}{"name": "org-a/github"})
	require.NoError(t, err)
	assert.False(t, result.IsError, "a visible server's status: %v", result.Content)
}
