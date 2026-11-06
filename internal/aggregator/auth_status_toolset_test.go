package aggregator

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

// toolsetScope stands in for the meta-tools handler's toolset resolution: it
// admits the listed servers, or fails with err; nil admits means no toolset
// bounds the request.
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

func useToolsetScope(t *testing.T, scope toolsetScope) {
	t.Helper()
	api.RegisterMetaTools(scope)
	t.Cleanup(func() { api.RegisterMetaTools(nil) })
}

func authStatusServers(a *AggregatorServer, ctx context.Context) []string {
	var names []string
	for _, s := range a.sessionAuthStatus(ctx).Servers {
		names = append(names, s.Name)
	}
	slices.Sort(names)
	return names
}

// auth://status lists the servers of the toolsets bounding the request only,
// as core_mcpserver_list does.
func TestSessionAuthStatus_NarrowedByToolset(t *testing.T) {
	ctx := testSessionCtx()

	t.Run("a toolset admitting org-a/github", func(t *testing.T) {
		a := visibilityTestServer(t)
		useToolsetScope(t, toolsetScope{admits: []string{"org-a/github"}})
		require.Equal(t, []string{"org-a/github"}, authStatusServers(a, ctx))
	})

	t.Run("no toolset", func(t *testing.T) {
		a := visibilityTestServer(t)
		useToolsetScope(t, toolsetScope{})
		require.Equal(t, []string{"github", "org-a/github", "org-b/github", "org-b/hubspot"}, authStatusServers(a, ctx))
	})

	t.Run("a toolset that cannot be resolved", func(t *testing.T) {
		a := visibilityTestServer(t)
		useToolsetScope(t, toolsetScope{err: errors.New("unknown preset")})
		require.Empty(t, authStatusServers(a, ctx))
	})

	t.Run("core_auth_login answers a server outside the toolset as missing", func(t *testing.T) {
		a := visibilityTestServer(t)
		useToolsetScope(t, toolsetScope{admits: []string{"org-a/github"}})
		login, err := NewAuthToolProvider(a).handleAuthLogin(ctx, map[string]any{"server": "org-b/hubspot"})
		require.NoError(t, err)
		require.True(t, login.IsError)
		require.Contains(t, login.Content[0], "not found")
	})
}
