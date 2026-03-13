package aggregator

import (
	"context"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/metatools"
)

func TestSessionToolFilter_ReturnsOnlyMetaTools(t *testing.T) {
	expectedMetaToolNames := metaToolNames()

	t.Run("returns meta-tools from provider", func(t *testing.T) {
		agg := &AggregatorServer{
			registry:        NewServerRegistry("x"),
			toolManager:     newActiveItemManager(),
			subjectSessions: newSubjectSessionTracker(),
		}

		ctx := api.WithSubject(context.Background(), "user@example.com")
		tools := agg.sessionToolFilter(ctx, nil)

		require.NotEmpty(t, tools, "sessionToolFilter should return meta-tools")

		toolNames := toolNameSet(tools)
		for _, name := range expectedMetaToolNames {
			assert.Contains(t, toolNames, name,
				"meta-tool %q should be present in sessionToolFilter result", name)
		}
		assert.Equal(t, len(expectedMetaToolNames), len(tools),
			"should return exactly the meta-tools, no more")
	})

	t.Run("does not include downstream server tools", func(t *testing.T) {
		reg := NewServerRegistry("x")
		client := &mockMCPClient{
			tools: []mcp.Tool{
				{Name: "list_files"},
				{Name: "read_file"},
				{Name: "write_file"},
			},
		}
		err := reg.Register(context.Background(), "filesystem", client, "fs")
		require.NoError(t, err)

		agg := &AggregatorServer{
			registry:        reg,
			toolManager:     newActiveItemManager(),
			subjectSessions: newSubjectSessionTracker(),
		}

		ctx := api.WithSubject(context.Background(), "user@example.com")
		tools := agg.sessionToolFilter(ctx, nil)

		for _, tool := range tools {
			assert.NotContains(t, tool.Name, "list_files",
				"downstream tool should not appear in sessionToolFilter result")
			assert.NotContains(t, tool.Name, "read_file",
				"downstream tool should not appear in sessionToolFilter result")
			assert.NotContains(t, tool.Name, "write_file",
				"downstream tool should not appear in sessionToolFilter result")
		}
	})

	t.Run("does not include OAuth server tools from cache", func(t *testing.T) {
		reg := NewServerRegistry("x")
		err := reg.RegisterPendingAuth(
			"oauth-server",
			"https://oauth.example.com",
			"oauth",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		)
		require.NoError(t, err)

		cache := NewCapabilityCache(0)
		defer cache.Stop()
		cache.Set("session-123", "oauth-server",
			[]mcp.Tool{{Name: "secret_tool"}}, nil, nil)

		agg := &AggregatorServer{
			registry:        reg,
			capabilityCache: cache,
			toolManager:     newActiveItemManager(),
			subjectSessions: newSubjectSessionTracker(),
		}

		ctx := api.WithSubject(context.Background(), "user@example.com")
		ctx = api.WithSessionID(ctx, "session-123")
		tools := agg.sessionToolFilter(ctx, nil)

		for _, tool := range tools {
			assert.NotEqual(t, "secret_tool", tool.Name,
				"OAuth server tools should not appear in sessionToolFilter")
		}
	})

	t.Run("meta-tool count is stable regardless of downstream servers", func(t *testing.T) {
		reg := NewServerRegistry("x")

		agg := &AggregatorServer{
			registry:        reg,
			toolManager:     newActiveItemManager(),
			subjectSessions: newSubjectSessionTracker(),
		}

		ctx := api.WithSubject(context.Background(), "user@example.com")
		toolsBefore := agg.sessionToolFilter(ctx, nil)
		countBefore := len(toolsBefore)

		for i, name := range []string{"server-a", "server-b", "server-c"} {
			client := &mockMCPClient{
				tools: makeNTools(10 * (i + 1)),
			}
			err := reg.Register(context.Background(), name, client, name)
			require.NoError(t, err)
		}

		toolsAfter := agg.sessionToolFilter(ctx, nil)
		assert.Equal(t, countBefore, len(toolsAfter),
			"adding downstream servers must not change sessionToolFilter output")
	})

	t.Run("mixed connected and auth-required servers return only meta-tools", func(t *testing.T) {
		reg := NewServerRegistry("x")

		connectedClient := &mockMCPClient{
			tools: []mcp.Tool{
				{Name: "connected_tool_1"},
				{Name: "connected_tool_2"},
			},
		}
		err := reg.Register(context.Background(), "connected-server", connectedClient, "conn")
		require.NoError(t, err)

		err = reg.RegisterPendingAuth(
			"auth-server",
			"https://auth.example.com",
			"auth",
			&AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		)
		require.NoError(t, err)

		cache := NewCapabilityCache(0)
		defer cache.Stop()
		cache.Set("session-abc", "auth-server",
			[]mcp.Tool{{Name: "auth_tool_1"}, {Name: "auth_tool_2"}, {Name: "auth_tool_3"}}, nil, nil)

		agg := &AggregatorServer{
			registry:        reg,
			capabilityCache: cache,
			toolManager:     newActiveItemManager(),
			subjectSessions: newSubjectSessionTracker(),
		}

		ctx := api.WithSubject(context.Background(), "user@example.com")
		ctx = api.WithSessionID(ctx, "session-abc")
		tools := agg.sessionToolFilter(ctx, nil)

		assert.Equal(t, len(expectedMetaToolNames), len(tools),
			"should return only meta-tools, not downstream tools")

		toolNames := toolNameSet(tools)
		downstreamNames := []string{
			"connected_tool_1", "connected_tool_2",
			"auth_tool_1", "auth_tool_2", "auth_tool_3",
			"x_connected-server_connected_tool_1", "x_auth-server_auth_tool_1",
		}
		for _, name := range downstreamNames {
			assert.NotContains(t, toolNames, name,
				"downstream tool %q should not be in sessionToolFilter result", name)
		}
	})

	t.Run("ignores input tools parameter", func(t *testing.T) {
		agg := &AggregatorServer{
			registry:        NewServerRegistry("x"),
			toolManager:     newActiveItemManager(),
			subjectSessions: newSubjectSessionTracker(),
		}

		inputTools := []mcp.Tool{
			{Name: "injected_tool_1"},
			{Name: "injected_tool_2"},
		}

		ctx := api.WithSubject(context.Background(), "user@example.com")
		tools := agg.sessionToolFilter(ctx, inputTools)

		toolNames := toolNameSet(tools)
		assert.NotContains(t, toolNames, "injected_tool_1",
			"input tools must be ignored by sessionToolFilter")
		assert.NotContains(t, toolNames, "injected_tool_2",
			"input tools must be ignored by sessionToolFilter")
		assert.Equal(t, len(expectedMetaToolNames), len(tools))
	})

	t.Run("tracks subject to MCP session mapping", func(t *testing.T) {
		agg := &AggregatorServer{
			registry:        NewServerRegistry("x"),
			toolManager:     newActiveItemManager(),
			subjectSessions: newSubjectSessionTracker(),
		}

		ctx := api.WithSubject(context.Background(), "user@example.com")
		_ = agg.sessionToolFilter(ctx, nil)

		sessions := agg.subjectSessions.GetSessionIDs("user@example.com")
		assert.Empty(t, sessions, "no MCP session in context means no tracking")
	})

	t.Run("returns same tools for different users", func(t *testing.T) {
		reg := NewServerRegistry("x")
		client := &mockMCPClient{
			tools: []mcp.Tool{{Name: "per_user_tool"}},
		}
		err := reg.Register(context.Background(), "shared-server", client, "shared")
		require.NoError(t, err)

		agg := &AggregatorServer{
			registry:        reg,
			toolManager:     newActiveItemManager(),
			subjectSessions: newSubjectSessionTracker(),
		}

		ctxAlice := api.WithSubject(context.Background(), "alice@example.com")
		ctxBob := api.WithSubject(context.Background(), "bob@example.com")

		toolsAlice := agg.sessionToolFilter(ctxAlice, nil)
		toolsBob := agg.sessionToolFilter(ctxBob, nil)

		assert.Equal(t, len(toolsAlice), len(toolsBob),
			"both users should see the same meta-tools")
		assert.Equal(t, len(expectedMetaToolNames), len(toolsAlice))
	})
}

// metaToolNames returns the expected meta-tool names from the metatools provider.
func metaToolNames() []string {
	provider := metatools.NewProvider()
	var names []string
	for _, t := range provider.GetTools() {
		names = append(names, t.Name)
	}
	return names
}

// toolNameSet converts a slice of tools to a set of tool names.
func toolNameSet(tools []mcp.Tool) map[string]bool {
	m := make(map[string]bool, len(tools))
	for _, t := range tools {
		m[t.Name] = true
	}
	return m
}

// makeNTools generates n test tools with sequential names.
func makeNTools(n int) []mcp.Tool {
	tools := make([]mcp.Tool, n)
	for i := range n {
		tools[i] = mcp.Tool{Name: fmt.Sprintf("tool_%d", i)}
	}
	return tools
}
