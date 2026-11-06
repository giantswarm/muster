package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/giantswarm/muster/v5/internal/api"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// scopingMetaTools stands in for the meta-tools handler: it admits the listed
// servers, or fails to resolve the toolset with err; nil admits means no
// toolset bounds the request.
type scopingMetaTools struct {
	api.MetaToolsHandler
	admits []string
	core   bool
	err    error
}

func (s scopingMetaTools) ToolsetCoreTools(context.Context) (bool, bool, error) {
	if s.err != nil {
		return false, true, s.err
	}
	return s.core, s.admits != nil, nil
}

func (s scopingMetaTools) ToolsetServers(context.Context) (func(string) bool, bool, error) {
	if s.err != nil {
		return nil, true, s.err
	}
	if s.admits == nil {
		return nil, false, nil
	}
	return func(server string) bool {
		for _, a := range s.admits {
			if a == server {
				return true
			}
		}
		return false
	}, true, nil
}

func listedServers(t *testing.T, adapter *Adapter) []string {
	t.Helper()
	result, err := adapter.ExecuteTool(t.Context(), "mcpserver_list", nil)
	require.NoError(t, err)
	require.False(t, result.IsError, fmt.Sprint(result.Content...))
	body := result.Content[0].(map[string]interface{})
	var names []string
	if servers, ok := body["mcpServers"].([]api.MCPServerInfo); ok {
		for _, s := range servers {
			names = append(names, s.Name)
		}
	}
	return names
}

// The bound toolset narrows the server list as it narrows list_tools: the
// caller's RBAC lets it see A and B, a toolset of server:A shows A only.
func TestHandleMCPServerListNarrowedByToolset(t *testing.T) {
	servers := []musterv1alpha1.MCPServer{
		{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: "muster"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: "muster"}},
	}
	adapter := NewAdapterWithClient(&namespacedClient{servers: servers}, "muster")
	use := func(t *testing.T, scope scopingMetaTools) {
		api.RegisterMetaTools(scope)
		t.Cleanup(func() { api.RegisterMetaTools(nil) })
	}

	t.Run("a toolset admitting a lists only a", func(t *testing.T) {
		use(t, scopingMetaTools{admits: []string{"a"}})
		require.Equal(t, []string{"a"}, listedServers(t, adapter))

		result, err := adapter.ExecuteTool(t.Context(), "mcpserver_get", map[string]any{"name": "a"})
		require.NoError(t, err)
		require.False(t, result.IsError, fmt.Sprint(result.Content...))

		result, err = adapter.ExecuteTool(t.Context(), "mcpserver_get", map[string]any{"name": "b"})
		require.NoError(t, err)
		require.True(t, result.IsError, "a server outside the toolset is not found")
	})

	t.Run("no toolset lists every server the caller may see", func(t *testing.T) {
		use(t, scopingMetaTools{})
		require.Equal(t, []string{"a", "b"}, listedServers(t, adapter))
	})

	t.Run("a toolset that cannot be resolved lists nothing", func(t *testing.T) {
		use(t, scopingMetaTools{err: errors.New("unknown preset")})
		require.Empty(t, listedServers(t, adapter))
	})
}
