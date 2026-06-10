package aggregator

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
)

func family(name, instanceArg string) *api.MCPServerFamily {
	return &api.MCPServerFamily{Name: name, InstanceArg: instanceArg}
}

func TestServerRegistry_AlwaysPrefixing(t *testing.T) {
	tests := []struct {
		name     string
		servers  map[string]*ServerInfo
		expected map[string]string // tool/prompt name -> expected exposed name
	}{
		{
			name: "All tools get prefixed",
			servers: map[string]*ServerInfo{
				"serverA": {
					Name: "serverA",
					Tools: []mcp.Tool{
						{Name: "read_file"},
						{Name: "write_file"},
					},
				},
				"serverB": {
					Name: "serverB",
					Tools: []mcp.Tool{
						{Name: "search"},
						{Name: "analyze"},
					},
				},
			},
			expected: map[string]string{
				"serverA.read_file":  "x_serverA_read_file",
				"serverA.write_file": "x_serverA_write_file",
				"serverB.search":     "x_serverB_search",
				"serverB.analyze":    "x_serverB_analyze",
			},
		},
		{
			name: "Tools with same names get prefixed",
			servers: map[string]*ServerInfo{
				"serverA": {
					Name: "serverA",
					Tools: []mcp.Tool{
						{Name: "read_file"},
						{Name: "search"},
					},
				},
				"serverB": {
					Name: "serverB",
					Tools: []mcp.Tool{
						{Name: "search"},
						{Name: "analyze"},
					},
				},
			},
			expected: map[string]string{
				"serverA.read_file": "x_serverA_read_file",
				"serverA.search":    "x_serverA_search",
				"serverB.search":    "x_serverB_search",
				"serverB.analyze":   "x_serverB_analyze",
			},
		},
		{
			name: "Multiple servers with same tool",
			servers: map[string]*ServerInfo{
				"serverA": {
					Name: "serverA",
					Tools: []mcp.Tool{
						{Name: "common_tool"},
					},
				},
				"serverB": {
					Name: "serverB",
					Tools: []mcp.Tool{
						{Name: "common_tool"},
					},
				},
				"serverC": {
					Name: "serverC",
					Tools: []mcp.Tool{
						{Name: "common_tool"},
						{Name: "unique_tool"},
					},
				},
			},
			expected: map[string]string{
				"serverA.common_tool": "x_serverA_common_tool",
				"serverB.common_tool": "x_serverB_common_tool",
				"serverC.common_tool": "x_serverC_common_tool",
				"serverC.unique_tool": "x_serverC_unique_tool",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := NewServerRegistry("x")

			for serverName := range tt.servers {
				registry.SetServerPrefix(serverName, serverName)
			}

			for key, expectedName := range tt.expected {
				parts := splitKey(key)
				serverName := parts[0]
				toolName := parts[1]

				actualName := registry.ExposedToolName(serverName, toolName)
				assert.Equal(t, expectedName, actualName,
					"Tool %s on server %s should be exposed as %s, but got %s",
					toolName, serverName, expectedName, actualName)
			}
		})
	}
}

func TestServerRegistry_ResolveName(t *testing.T) {
	registry := NewServerRegistry("x")

	registry.SetServerPrefix("serverA", "serverA")
	registry.SetServerPrefix("serverB", "serverB")
	registry.SetServerPrefix("serverC", "serverC")

	// Register names via ExposedToolName/ExposedPromptName
	registry.ExposedToolName("serverA", "unique_tool")
	registry.ExposedToolName("serverA", "shared_tool")
	registry.ExposedToolName("serverB", "shared_tool")

	registry.ExposedPromptName("serverA", "unique_prompt")
	registry.ExposedPromptName("serverB", "shared_prompt")
	registry.ExposedPromptName("serverC", "shared_prompt")

	tests := []struct {
		exposedName      string
		expectedServer   string
		expectedOriginal string
		expectedItemType string
		expectError      bool
	}{
		{"x_serverA_unique_tool", "serverA", "unique_tool", "tool", false},
		{"x_serverA_shared_tool", "serverA", "shared_tool", "tool", false},
		{"x_serverB_shared_tool", "serverB", "shared_tool", "tool", false},
		{"x_serverA_unique_prompt", "serverA", "unique_prompt", "prompt", false},
		{"x_serverB_shared_prompt", "serverB", "shared_prompt", "prompt", false},
		{"x_serverC_shared_prompt", "serverC", "shared_prompt", "prompt", false},
		{"non_existent", "", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.exposedName, func(t *testing.T) {
			var serverName, originalName string
			var err error

			switch tt.expectedItemType {
			case "tool":
				serverName, originalName, err = registry.ResolveToolName(tt.exposedName)
			case "prompt":
				serverName, originalName, err = registry.ResolvePromptName(tt.exposedName)
			default:
				// For the error case, try ResolveToolName
				serverName, originalName, err = registry.ResolveToolName(tt.exposedName)
			}

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedServer, serverName)
				assert.Equal(t, tt.expectedOriginal, originalName)
			}
		})
	}
}

// Helper function to split "server.tool" into ["server", "tool"]
func splitKey(key string) []string {
	for i := 0; i < len(key); i++ {
		if key[i] == '.' {
			return []string{key[:i], key[i+1:]}
		}
	}
	return []string{key}
}

func TestServerRegistry_FamilyGrouping(t *testing.T) {
	ctx := context.Background()

	t.Run("single instance with family exposes family-prefixed tool with server enum", func(t *testing.T) {
		registry := NewServerRegistry("x")
		client := &mockMCPClient{tools: []mcp.Tool{
			{Name: "list_pods", Description: "List pods"},
		}}

		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:       "mcp-kubernetes-graveler",
			ToolPrefix: "k8s-graveler",
			Family:     family("kubernetes", "management_cluster"),
		}, client))

		tools := registry.GetAllTools()
		require.Len(t, tools, 1)

		exposed := tools[0]
		assert.Equal(t, "x_kubernetes_list_pods", exposed.Name)
		assert.Contains(t, exposed.Description, "(available on servers: mcp-kubernetes-graveler)")

		require.NotNil(t, exposed.InputSchema.Properties)
		instanceProp, ok := exposed.InputSchema.Properties["management_cluster"].(map[string]any)
		require.True(t, ok, "configured instance arg must be present even for single-instance families")
		assert.Equal(t, "string", instanceProp["type"])
		assert.Equal(t, []any{"mcp-kubernetes-graveler"}, instanceProp["enum"])
		assert.Contains(t, exposed.InputSchema.Required, "management_cluster")
	})

	t.Run("two instances in the same family deduplicate with server enum listing both", func(t *testing.T) {
		registry := NewServerRegistry("x")
		toolA := []mcp.Tool{{Name: "list_pods", Description: "List pods"}}
		toolB := []mcp.Tool{{Name: "list_pods", Description: "List pods"}}

		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:       "mcp-kubernetes-graveler",
			ToolPrefix: "k8s-graveler",
			Family:     family("kubernetes", "management_cluster"),
		}, &mockMCPClient{tools: toolA}))
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:       "mcp-kubernetes-gazelle",
			ToolPrefix: "k8s-gazelle",
			Family:     family("kubernetes", "management_cluster"),
		}, &mockMCPClient{tools: toolB}))

		tools := registry.GetAllTools()
		require.Len(t, tools, 1, "family-grouped tools must collapse to a single exposed entry")
		exposed := tools[0]
		assert.Equal(t, "x_kubernetes_list_pods", exposed.Name)

		instanceProp := exposed.InputSchema.Properties["management_cluster"].(map[string]any)
		assert.Equal(t, []any{"mcp-kubernetes-gazelle", "mcp-kubernetes-graveler"}, instanceProp["enum"])
	})

	t.Run("non-family servers retain per-server prefixing", func(t *testing.T) {
		registry := NewServerRegistry("x")
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:       "slack",
			ToolPrefix: "slack",
		}, &mockMCPClient{tools: []mcp.Tool{{Name: "send_message"}}}))

		tools := registry.GetAllTools()
		require.Len(t, tools, 1)
		assert.Equal(t, "x_slack_send_message", tools[0].Name)
		// No instance-arg parameter injected for non-family tools.
		_, has := tools[0].InputSchema.Properties["management_cluster"]
		assert.False(t, has)
		_, has = tools[0].InputSchema.Properties["server"]
		assert.False(t, has)
	})

	t.Run("diverging descriptions within a family fall back to per-server prefixing for that tool", func(t *testing.T) {
		registry := NewServerRegistry("x")
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:       "k8s-graveler",
			ToolPrefix: "k8s-graveler",
			Family:     family("kubernetes", "management_cluster"),
		}, &mockMCPClient{tools: []mcp.Tool{
			{Name: "list_pods", Description: "v1 API"},
			{Name: "get_node", Description: "Get node"},
		}}))
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:       "k8s-gazelle",
			ToolPrefix: "k8s-gazelle",
			Family:     family("kubernetes", "management_cluster"),
		}, &mockMCPClient{tools: []mcp.Tool{
			{Name: "list_pods", Description: "v2 API"},  // diverges
			{Name: "get_node", Description: "Get node"}, // matches
		}}))

		tools := registry.GetAllTools()
		names := make([]string, len(tools))
		for i, tool := range tools {
			names[i] = tool.Name
		}
		sort.Strings(names)
		// get_node deduplicates to family, list_pods falls back per-server.
		assert.Equal(t, []string{
			"x_k8s-gazelle_list_pods",
			"x_k8s-graveler_list_pods",
			"x_kubernetes_get_node",
		}, names)
	})

	t.Run("Deregister cleans family mappings and resolves remaining instance via server arg", func(t *testing.T) {
		registry := NewServerRegistry("x")
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:       "k8s-a",
			ToolPrefix: "k8s-a",
			Family:     family("kubernetes", "management_cluster"),
		}, &mockMCPClient{tools: []mcp.Tool{{Name: "list_pods", Description: "L"}}}))
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:       "k8s-b",
			ToolPrefix: "k8s-b",
			Family:     family("kubernetes", "management_cluster"),
		}, &mockMCPClient{tools: []mcp.Tool{{Name: "list_pods", Description: "L"}}}))

		// Prime the family routing index by listing tools.
		_ = registry.GetAllTools()

		require.NoError(t, registry.Deregister("k8s-a"))

		// Re-list to recompute the routing index now that one server is gone.
		_ = registry.GetAllTools()

		// "k8s-a" must no longer be a routing target — only k8s-b remains.
		_, err := registry.ResolveToolNameForServer("x_kubernetes_list_pods", "k8s-a")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not available on server")

		// k8s-b still resolves cleanly.
		originalName, err := registry.ResolveToolNameForServer("x_kubernetes_list_pods", "k8s-b")
		require.NoError(t, err)
		assert.Equal(t, "list_pods", originalName)

		providers := registry.GetToolServerNames("x_kubernetes_list_pods")
		assert.Equal(t, []string{"k8s-b"}, providers)
	})

	t.Run("ResolveToolName surfaces 'server parameter is required' error for multi-instance families", func(t *testing.T) {
		registry := NewServerRegistry("x")
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:   "a",
			Family: family("kubernetes", "management_cluster"),
		}, &mockMCPClient{tools: []mcp.Tool{{Name: "list_pods", Description: "L"}}}))
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:   "b",
			Family: family("kubernetes", "management_cluster"),
		}, &mockMCPClient{tools: []mcp.Tool{{Name: "list_pods", Description: "L"}}}))
		_ = registry.GetAllTools()

		_, _, err := registry.ResolveToolName("x_kubernetes_list_pods")
		require.Error(t, err)
		assert.True(t, strings.Contains(err.Error(), `"management_cluster" parameter is required`),
			"expected error to mention required instance arg, got: %v", err)
	})

	t.Run("ResolveToolNameForServer rejects unknown server", func(t *testing.T) {
		registry := NewServerRegistry("x")
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:   "a",
			Family: family("kubernetes", "management_cluster"),
		}, &mockMCPClient{tools: []mcp.Tool{{Name: "list_pods", Description: "L"}}}))
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:   "b",
			Family: family("kubernetes", "management_cluster"),
		}, &mockMCPClient{tools: []mcp.Tool{{Name: "list_pods", Description: "L"}}}))
		_ = registry.GetAllTools()

		_, err := registry.ResolveToolNameForServer("x_kubernetes_list_pods", "nonexistent")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not available on server")
	})

	t.Run("family deep-copy does not mutate cached per-server tool schema", func(t *testing.T) {
		registry := NewServerRegistry("x")
		tool := mcp.Tool{
			Name:        "list_pods",
			Description: "L",
			InputSchema: mcp.ToolInputSchema{
				Type:       "object",
				Properties: map[string]any{"namespace": map[string]any{"type": "string"}},
				Required:   []string{"namespace"},
			},
		}
		client := &mockMCPClient{tools: []mcp.Tool{tool}}
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:   "a",
			Family: family("kubernetes", "management_cluster"),
		}, client))
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:   "b",
			Family: family("kubernetes", "management_cluster"),
		}, &mockMCPClient{tools: []mcp.Tool{tool}}))

		// Repeated GetAllTools calls must not stack-mutate cached schemas.
		_ = registry.GetAllTools()
		_ = registry.GetAllTools()

		serverInfo, ok := registry.GetServerInfo("a")
		require.True(t, ok)
		cached := serverInfo.Tools[0]
		_, leaked := cached.InputSchema.Properties["management_cluster"]
		assert.False(t, leaked, "instance enum must not bleed back into the backend's cached tool schema")
		assert.Equal(t, []string{"namespace"}, cached.InputSchema.Required,
			"Required slice must not accumulate the instance arg across calls")
	})

	t.Run("family deep-copy survives mutation of nested object properties", func(t *testing.T) {
		registry := NewServerRegistry("x")
		tool := mcp.Tool{
			Name:        "list_pods",
			Description: "L",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]any{
					"filter": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"namespace": map[string]any{"type": "string"},
						},
						"required": []any{"namespace"},
					},
				},
			},
		}
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:   "a",
			Family: family("kubernetes", "management_cluster"),
		}, &mockMCPClient{tools: []mcp.Tool{tool}}))

		got := registry.GetAllTools()
		require.Len(t, got, 1)

		filter, ok := got[0].InputSchema.Properties["filter"].(map[string]any)
		require.True(t, ok)
		nested, ok := filter["properties"].(map[string]any)
		require.True(t, ok)
		nested["namespace"] = map[string]any{"type": "object"} // mutate the exposed copy
		filter["required"] = []any{"namespace", "INJECTED"}

		serverInfo, ok := registry.GetServerInfo("a")
		require.True(t, ok)
		cached := serverInfo.Tools[0]
		cachedFilter := cached.InputSchema.Properties["filter"].(map[string]any)
		cachedNested := cachedFilter["properties"].(map[string]any)
		assert.Equal(t, "string", cachedNested["namespace"].(map[string]any)["type"],
			"mutating the exposed copy's nested property must not leak into the cached schema")
		assert.Equal(t, []any{"namespace"}, cachedFilter["required"],
			"mutating the exposed copy's nested required slice must not leak into the cached schema")
	})

	t.Run("tools/list order is stable across calls", func(t *testing.T) {
		registry := NewServerRegistry("x")
		// Three servers + three tools each, mixed family/solo, to exercise
		// both contribution-sort and family-bucket-sort paths.
		tools := func(names ...string) []mcp.Tool {
			out := make([]mcp.Tool, len(names))
			for i, n := range names {
				out[i] = mcp.Tool{Name: n, Description: "d", InputSchema: mcp.ToolInputSchema{Type: "object"}}
			}
			return out
		}
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:   "k8s-c",
			Family: family("kubernetes", "management_cluster"),
		}, &mockMCPClient{tools: tools("delete_pod", "list_pods", "get_node")}))
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:   "k8s-a",
			Family: family("kubernetes", "management_cluster"),
		}, &mockMCPClient{tools: tools("delete_pod", "list_pods", "get_node")}))
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name: "solo",
		}, &mockMCPClient{tools: tools("ping", "scan")}))

		names := func(ts []mcp.Tool) []string {
			out := make([]string, len(ts))
			for i, t := range ts {
				out[i] = t.Name
			}
			return out
		}
		first := names(registry.GetAllTools())
		for i := 0; i < 10; i++ {
			assert.Equal(t, first, names(registry.GetAllTools()),
				"tools/list order must be stable across repeated calls (iteration %d)", i)
		}
	})

	t.Run("family.instanceArg colliding with a tool input property falls back to per-server prefixing", func(t *testing.T) {
		registry := NewServerRegistry("x")
		tool := mcp.Tool{
			Name:        "list_pods",
			Description: "L",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]any{
					"cluster":   map[string]any{"type": "string", "description": "Tool-defined cluster"},
					"namespace": map[string]any{"type": "string"},
				},
				Required: []string{"cluster"},
			},
		}
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:   "k8s-single",
			Family: family("kubernetes", "cluster"), // collides with the tool's "cluster" property
		}, &mockMCPClient{tools: []mcp.Tool{tool}}))

		got := registry.GetAllTools()
		var exposedNames []string
		for _, t := range got {
			exposedNames = append(exposedNames, t.Name)
		}
		assert.Contains(t, exposedNames, "x_k8s-single_list_pods",
			"collision must surface per-server fallback name")
		assert.NotContains(t, exposedNames, "x_kubernetes_list_pods",
			"collision must NOT surface the family-prefixed name (the tool-defined property would be overwritten)")
	})

	t.Run("asymmetric family.instanceArg collision (only one contributor declares the property) falls back", func(t *testing.T) {
		registry := NewServerRegistry("x")
		toolClean := mcp.Tool{
			Name:        "list_pods",
			Description: "L",
			InputSchema: mcp.ToolInputSchema{
				Type:       "object",
				Properties: map[string]any{"namespace": map[string]any{"type": "string"}},
			},
		}
		toolColliding := mcp.Tool{
			Name:        "list_pods",
			Description: "L",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]any{
					"namespace": map[string]any{"type": "string"},
					"cluster":   map[string]any{"type": "string", "description": "Tool-defined cluster"},
				},
			},
		}
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:   "k8s-a",
			Family: family("kubernetes", "cluster"),
		}, &mockMCPClient{tools: []mcp.Tool{toolClean}}))
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:   "k8s-b",
			Family: family("kubernetes", "cluster"),
		}, &mockMCPClient{tools: []mcp.Tool{toolColliding}}))

		got := registry.GetAllTools()
		var exposedNames []string
		for _, t := range got {
			exposedNames = append(exposedNames, t.Name)
		}
		assert.Contains(t, exposedNames, "x_k8s-a_list_pods",
			"asymmetric collision must surface per-server name for clean contributor")
		assert.Contains(t, exposedNames, "x_k8s-b_list_pods",
			"asymmetric collision must surface per-server name for colliding contributor")
		assert.NotContains(t, exposedNames, "x_kubernetes_list_pods",
			"asymmetric collision must NOT surface the family-prefixed name")
	})

	t.Run("concurrent GetAllToolsForSession does not race", func(t *testing.T) {
		// Hammer two concurrent session listings + global listings to surface
		// any race in the upsert / read paths under `go test -race`. The fix
		// from PR #670 (union semantics, no destructive reset) should make
		// these read-write interleavings safe.
		registry := NewServerRegistry("x")
		tool := mcp.Tool{
			Name:        "list_pods",
			Description: "L",
			InputSchema: mcp.ToolInputSchema{
				Type:       "object",
				Properties: map[string]any{"namespace": map[string]any{"type": "string"}},
			},
		}
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:   "k8s-a",
			Family: family("kubernetes", "management_cluster"),
		}, &mockMCPClient{tools: []mcp.Tool{tool}}))
		require.NoError(t, registry.Register(ctx, ServerRegistration{
			Name:   "k8s-b",
			Family: family("kubernetes", "management_cluster"),
		}, &mockMCPClient{tools: []mcp.Tool{tool}}))

		const goroutines = 8
		const iterations = 50
		done := make(chan struct{}, goroutines)
		for g := 0; g < goroutines; g++ {
			go func() {
				defer func() { done <- struct{}{} }()
				for i := 0; i < iterations; i++ {
					_ = registry.GetAllTools()
					_, err := registry.ResolveToolNameForServer("x_kubernetes_list_pods", "k8s-a")
					assert.NoError(t, err)
				}
			}()
		}
		for g := 0; g < goroutines; g++ {
			<-done
		}
	})
}

func TestServerRegistry_RegisterPendingAuthIdempotent(t *testing.T) {
	t.Run("re-registering a pending auth server is a no-op", func(t *testing.T) {
		registry := NewServerRegistry("x")
		registration := PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: "oauth-server", ToolPrefix: "oauth"},
			URL:                "https://oauth.example.com",
			AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		}

		require.NoError(t, registry.RegisterPendingAuth(registration))
		require.NoError(t, registry.RegisterPendingAuth(registration))

		info, exists := registry.servers["oauth-server"]
		require.True(t, exists)
		require.True(t, info.RequiresSessionAuth())
	})

	t.Run("registering over an active server returns an error", func(t *testing.T) {
		registry := NewServerRegistry("x")
		registry.servers["active-server"] = &ServerInfo{Name: "active-server"}

		err := registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: "active-server"},
			URL:                "https://active.example.com",
		})
		require.ErrorContains(t, err, "already registered")
	})
}
