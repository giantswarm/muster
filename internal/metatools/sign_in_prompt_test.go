package metatools

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/v5/internal/api"
)

func signedOutMiro() *mockMetaToolsHandler {
	return &mockMetaToolsHandler{
		tools: []mcp.Tool{{Name: "x_kubernetes_list_pods", Description: "List pods"}},
		serversRequiringAuth: []api.ServerAuthInfo{
			{Name: "miro", Status: "auth_required", AuthTool: "core_auth_login", ToolPrefix: "x_miro_"},
			{Name: "github", Status: "auth_required", AuthTool: "core_auth_login", ToolPrefix: "x_github_"},
		},
	}
}

func filterToolsAs(t *testing.T, ctx context.Context, mock *mockMetaToolsHandler, args map[string]any) *api.CallToolResult {
	t.Helper()
	defer registerMockHandler(mock)()
	result, err := NewProvider().ExecuteTool(ctx, "filter_tools", args)
	require.NoError(t, err)
	return result
}

func TestFilterTools_AgentSearchNamingASignedOutServerAnswersItsSignIn(t *testing.T) {
	prompt := api.WithSignInPrompt(context.Background())

	t.Run("a query naming the server answers its sign-in", func(t *testing.T) {
		mock := signedOutMiro()
		result := filterToolsAs(t, prompt, mock, map[string]any{"query": "Miro get current user profile"})
		require.Equal(t, []string{"miro"}, mock.signIns)
		require.True(t, result.IsError)
		require.Contains(t, result.Content[0], "auth_required: server 'miro'")
	})

	t.Run("a pattern under the server's prefix answers its sign-in", func(t *testing.T) {
		mock := signedOutMiro()
		filterToolsAs(t, prompt, mock, map[string]any{"pattern": "x_miro_*"})
		require.Equal(t, []string{"miro"}, mock.signIns)
	})

	t.Run("a stored grant connects the session and the listing shows the server's tools", func(t *testing.T) {
		mock := signedOutMiro()
		mock.connectOnSignIn = map[string][]mcp.Tool{"miro": {{Name: "x_miro_user_who_am_i", Description: "Who am I"}}}
		result := filterToolsAs(t, prompt, mock, map[string]any{"pattern": "x_miro_*"})
		require.False(t, result.IsError)
		require.Contains(t, result.Content[0], "x_miro_user_who_am_i")
	})

	for name, args := range map[string]map[string]any{
		"a broad search":                  {"query": "list my boards"},
		"a pattern that only could match": {"pattern": "x_*"},
		"a word containing the name":      {"query": "miroslav's notes"},
		"a search naming two servers":     {"query": "copy miro boards to github"},
	} {
		t.Run(name+" lists without a sign-in", func(t *testing.T) {
			mock := signedOutMiro()
			result := filterToolsAs(t, prompt, mock, args)
			require.Empty(t, mock.signIns)
			require.False(t, result.IsError)
		})
	}

	t.Run("a server the person cannot sign in to from here is only listed", func(t *testing.T) {
		mock := signedOutMiro()
		mock.serversRequiringAuth[0].AuthTool = ""
		filterToolsAs(t, prompt, mock, map[string]any{"query": "miro"})
		require.Empty(t, mock.signIns)
	})

	t.Run("a request without the prompt mark keeps the listing", func(t *testing.T) {
		mock := signedOutMiro()
		result := filterToolsAs(t, context.Background(), mock, map[string]any{"query": "miro"})
		require.Empty(t, mock.signIns)
		require.False(t, result.IsError)
		require.Contains(t, result.Content[0], `"servers_requiring_auth"`)
	})
}

func TestContainsWord(t *testing.T) {
	require.True(t, containsWord("who am i in Miro?", "miro"))
	require.True(t, containsWord("agentlab-mcp-kubernetes pods", "agentlab-mcp-kubernetes"))
	require.False(t, containsWord("miroslav", "miro"))
	require.False(t, containsWord("", "miro"))
}
