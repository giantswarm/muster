package orchestrator

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/config"
	"github.com/giantswarm/muster/v5/internal/services"
)

// toolsetScope admits the listed servers, or fails with err; nil admits means
// no toolset bounds the request.
type toolsetScope struct {
	api.MetaToolsHandler
	admits []string
	core   bool
	err    error
}

func (s toolsetScope) ToolsetCoreTools(context.Context) (bool, bool, error) {
	if s.err != nil {
		return false, true, s.err
	}
	return s.core, s.admits != nil, nil
}

func (s toolsetScope) ToolsetServers(context.Context) (func(string) bool, bool, error) {
	if s.err != nil {
		return nil, true, s.err
	}
	if s.admits == nil {
		return nil, false, nil
	}
	return func(server string) bool { return slices.Contains(s.admits, server) }, true, nil
}

// aggregatorService is muster's own service.
type aggregatorService struct{ mockService }

func (*aggregatorService) GetType() services.ServiceType { return services.TypeAggregator }

// The service of an MCP server outside the toolsets bounding the request is
// absent from core_service_list and missing for core_service_status, as one
// the caller may not see.
func TestServiceTools_NarrowedByToolset(t *testing.T) {
	o := New(Config{Aggregator: config.AggregatorConfig{}})
	for _, name := range []string{"a", "b"} {
		require.NoError(t, o.registry.Register(&mockService{name: name, state: services.StateRunning}))
	}
	require.NoError(t, o.registry.Register(&aggregatorService{mockService{name: "mcp-aggregator", state: services.StateRunning}}))
	adapter := NewAPIAdapter(o)
	listed := func(t *testing.T) []string {
		result, err := adapter.ExecuteTool(t.Context(), "service_list", nil)
		require.NoError(t, err)
		var names []string
		for _, svc := range result.Content[0].(map[string]interface{})["services"].([]api.ServiceStatus) {
			names = append(names, svc.Name)
		}
		slices.Sort(names)
		return names
	}
	use := func(t *testing.T, scope toolsetScope) {
		api.RegisterMetaTools(scope)
		t.Cleanup(func() { api.RegisterMetaTools(nil) })
	}

	t.Run("a toolset admitting a", func(t *testing.T) {
		use(t, toolsetScope{admits: []string{"a"}})
		require.Equal(t, []string{"a"}, listed(t))
		for _, name := range []string{"b", "mcp-aggregator"} {
			result, err := adapter.ExecuteTool(t.Context(), "service_status", map[string]interface{}{"name": name})
			require.NoError(t, err)
			require.True(t, result.IsError, name)
			require.Contains(t, result.Content[0], "service "+name+" not found")
		}
	})

	t.Run("a toolset admitting a and resolving a core tool", func(t *testing.T) {
		use(t, toolsetScope{admits: []string{"a"}, core: true})
		require.Equal(t, []string{"a", "mcp-aggregator"}, listed(t))
		result, err := adapter.ExecuteTool(t.Context(), "service_status", map[string]interface{}{"name": "mcp-aggregator"})
		require.NoError(t, err)
		require.False(t, result.IsError, "%v", result.Content)
	})

	t.Run("no toolset", func(t *testing.T) {
		use(t, toolsetScope{})
		require.Equal(t, []string{"a", "b", "mcp-aggregator"}, listed(t))
	})

	t.Run("a toolset that cannot be resolved", func(t *testing.T) {
		use(t, toolsetScope{err: errors.New("unknown preset")})
		require.Empty(t, listed(t))
	})
}
