package metatools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolsetServers(t *testing.T) {
	handler := toolsetFixture()
	p := NewProvider()
	admitted := func(t *testing.T, bound []string, header string, headerPresent bool) []string {
		t.Helper()
		admits, scoped, err := p.toolsetServers(withBound(bound, header, headerPresent), handler)
		require.NoError(t, err)
		require.True(t, scoped)
		var out []string
		for _, server := range []string{"k8s", "gh", "pending"} {
			if admits(server) {
				out = append(out, server)
			}
		}
		return out
	}

	require.Equal(t, []string{"k8s"}, admitted(t, []string{"server:k8s"}, "", false))
	require.Equal(t, []string{"k8s"}, admitted(t, []string{"preset:read-only"}, "", false), "a preset admits the servers of the tools it resolves")
	require.Equal(t, []string{"gh"}, admitted(t, []string{"tool:x_gh_issues"}, "", false))
	require.Equal(t, []string{"pending"}, admitted(t, []string{"server:pending"}, "", false), "a named server whose tools wait for a sign-in is admitted")
	require.Equal(t, []string{"gh"}, admitted(t, []string{"server:k8s", "server:gh"}, "server:gh", true), "the header narrows within the binding")
	require.Empty(t, admitted(t, []string{"server:k8s"}, "server:gh", true), "and never widens it: a disjoint header admits nothing, as list_tools lists nothing")

	t.Run("no toolset is unscoped", func(t *testing.T) {
		_, scoped, err := p.toolsetServers(t.Context(), handler)
		require.NoError(t, err)
		require.False(t, scoped)
	})

	t.Run("an unknown preset is an error", func(t *testing.T) {
		_, scoped, err := p.toolsetServers(withBound([]string{"preset:does-not-exist"}, "", false), handler)
		require.True(t, scoped)
		require.Error(t, err)
	})
}

// muster's own services are judged by its core tools: a server-only toolset
// resolves none, a tool:core_<name> selector or a preset including core tools resolves some.
func TestToolsetCoreTools(t *testing.T) {
	handler := toolsetFixture()
	p := NewProvider()
	resolves := func(t *testing.T, bound []string) bool {
		t.Helper()
		ok, scoped, err := p.toolsetCoreTools(withBound(bound, "", false), handler)
		require.NoError(t, err)
		require.True(t, scoped)
		return ok
	}

	require.False(t, resolves(t, []string{"server:k8s"}))
	require.True(t, resolves(t, []string{"server:k8s", "tool:core_workflow_list"}))
	require.True(t, resolves(t, []string{"preset:full"}))

	t.Run("no toolset is unscoped", func(t *testing.T) {
		_, scoped, err := p.toolsetCoreTools(t.Context(), handler)
		require.NoError(t, err)
		require.False(t, scoped)
	})

	t.Run("an unknown preset is an error", func(t *testing.T) {
		_, scoped, err := p.toolsetCoreTools(withBound([]string{"preset:does-not-exist"}, "", false), handler)
		require.True(t, scoped)
		require.Error(t, err)
	})
}

// A server awaiting sign-in is admitted when the toolset could select one of
// its tools once listed, so core_auth_login reaches the sign-in that
// servers_requiring_auth reports.
func TestToolsetServers_AdmitsTheSignInTheToolsetWouldUnlock(t *testing.T) {
	handler := signedOutFixture()
	defer registerMockHandler(handler)()
	p := NewProvider()
	admits := func(t *testing.T, bound []string) bool {
		t.Helper()
		admits, scoped, err := p.toolsetServers(withBound(bound, "", false), handler)
		require.NoError(t, err)
		require.True(t, scoped)
		return admits("gh")
	}

	require.True(t, admits(t, []string{"tool:x_gh_issues"}))
	require.True(t, admits(t, []string{"preset:read-only"}))
	require.False(t, admits(t, []string{"server:k8s"}))
}
