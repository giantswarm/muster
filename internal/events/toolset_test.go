package events

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
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

// core_events drops the events about an MCP server outside the toolsets
// bounding the request; events about anything else stay.
func TestEventsQuery_NarrowedByToolset(t *testing.T) {
	event := func(kind, namespace, name string) api.EventResult {
		return api.EventResult{Namespace: namespace, Reason: "Seen-" + namespace + "-" + name,
			InvolvedObject: api.ObjectReference{Kind: kind, Name: name, Namespace: namespace}}
	}
	adapter := NewAdapter(&eventsClient{events: []api.EventResult{
		event("MCPServer", "muster", "a"),
		event("MCPServer", "org-a", "b"),
		event("Workflow", "muster", "deploy"),
	}}, "muster")
	query := func(t *testing.T) string {
		result, err := adapter.ExecuteTool(t.Context(), "events", map[string]interface{}{})
		require.NoError(t, err)
		require.False(t, result.IsError)
		return result.Content[0].(string)
	}
	use := func(t *testing.T, scope toolsetScope) {
		api.RegisterMetaTools(scope)
		t.Cleanup(func() { api.RegisterMetaTools(nil) })
	}

	t.Run("a toolset admitting org-a/b", func(t *testing.T) {
		use(t, toolsetScope{admits: []string{"org-a/b"}})
		got := query(t)
		require.Contains(t, got, "Seen-org-a-b")
		require.Contains(t, got, "Seen-muster-deploy")
		require.NotContains(t, got, "Seen-muster-a")
	})

	t.Run("no toolset", func(t *testing.T) {
		use(t, toolsetScope{})
		got := query(t)
		require.Contains(t, got, "Seen-muster-a")
		require.Contains(t, got, "Seen-org-a-b")
	})

	t.Run("a toolset that cannot be resolved", func(t *testing.T) {
		use(t, toolsetScope{err: errors.New("unknown preset")})
		got := query(t)
		require.NotContains(t, got, "Seen-muster-a")
		require.NotContains(t, got, "Seen-org-a-b")
		require.Contains(t, got, "Seen-muster-deploy")
	})
}
