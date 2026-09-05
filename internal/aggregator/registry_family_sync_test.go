package aggregator

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	oauthstore "github.com/giantswarm/muster/internal/oauth/store"
)

// The family routing index has to follow what members offer right now
// (#1162): a member that keeps its registration but re-lists fewer tools
// must lose its provider entries for the tools it dropped, a tool no member
// offers any more must disappear, and a member whose service is down must not
// contribute at all -- while a member that merely did not take part in a
// per-session pass keeps its entries (PR #670).

func familyTool(name string) mcp.Tool {
	return mcp.Tool{
		Name:        name,
		Description: "Family tool " + name,
		InputSchema: mcp.ToolInputSchema{
			Type:       "object",
			Properties: map[string]any{"namespace": map[string]any{"type": "string"}},
		},
	}
}

func exposedNames(tools []mcp.Tool) []string {
	names := make([]string, 0, len(tools))
	for _, t := range tools {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	return names
}

func findTool(t *testing.T, tools []mcp.Tool, name string) mcp.Tool {
	t.Helper()
	for _, tool := range tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %s not in %v", name, exposedNames(tools))
	return mcp.Tool{}
}

func TestFamilyIndex_MemberRelistsSubset_Global(t *testing.T) {
	ctx := context.Background()
	registry := NewServerRegistry("x")

	listPods, pause := familyTool("list_pods"), familyTool("pause_cluster")
	clientA := &mockMCPClient{tools: []mcp.Tool{listPods, pause}}
	clientB := &mockMCPClient{tools: []mcp.Tool{listPods, pause}}
	require.NoError(t, registry.Register(ctx, ServerRegistration{Name: "k8s-a", Family: family("kubernetes", "management_cluster")}, clientA))
	require.NoError(t, registry.Register(ctx, ServerRegistration{Name: "k8s-b", Family: family("kubernetes", "management_cluster")}, clientB))

	tools := registry.GetAllTools()
	assert.Equal(t, []string{"x_kubernetes_list_pods", "x_kubernetes_pause_cluster"}, exposedNames(tools))
	assert.Equal(t, []string{"k8s-a", "k8s-b"}, registry.GetToolServerNames("x_kubernetes_pause_cluster"))

	// k8s-b re-lists without pause_cluster (a read-only rollout hid it).
	infoB, ok := registry.GetServerInfo("k8s-b")
	require.True(t, ok)
	infoB.UpdateTools([]mcp.Tool{listPods})

	tools = registry.GetAllTools()
	assert.Equal(t, []string{"x_kubernetes_list_pods", "x_kubernetes_pause_cluster"}, exposedNames(tools),
		"k8s-a still offers pause_cluster, so the family tool stays exposed")
	assert.Equal(t, []string{"k8s-a"}, registry.GetToolServerNames("x_kubernetes_pause_cluster"),
		"k8s-b must be withdrawn as a provider of the tool it dropped")
	assert.Equal(t, []string{"k8s-a", "k8s-b"}, registry.GetToolServerNames("x_kubernetes_list_pods"),
		"k8s-b keeps providing the tool it still offers")
	assert.Contains(t, findTool(t, tools, "x_kubernetes_pause_cluster").Description, "(available on servers: k8s-a)")
	_, err := registry.ResolveToolNameForServer("x_kubernetes_pause_cluster", "k8s-b")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not available on server \"k8s-b\"")
	original, err := registry.ResolveToolNameForServer("x_kubernetes_pause_cluster", "k8s-a")
	require.NoError(t, err)
	assert.Equal(t, "pause_cluster", original)

	// k8s-a drops it too: the name is offered by nobody and must vanish.
	infoA, ok := registry.GetServerInfo("k8s-a")
	require.True(t, ok)
	infoA.UpdateTools([]mcp.Tool{listPods})

	tools = registry.GetAllTools()
	assert.Equal(t, []string{"x_kubernetes_list_pods"}, exposedNames(tools))
	assert.False(t, registry.IsFamilyTool("x_kubernetes_pause_cluster"), "bucket without providers is deleted")
	assert.Nil(t, registry.GetToolServerNames("x_kubernetes_pause_cluster"))
	_, _, err = registry.ResolveToolName("x_kubernetes_pause_cluster")
	require.Error(t, err)

	// And it comes back when a member offers it again.
	infoB.UpdateTools([]mcp.Tool{listPods, pause})
	tools = registry.GetAllTools()
	assert.Equal(t, []string{"x_kubernetes_list_pods", "x_kubernetes_pause_cluster"}, exposedNames(tools))
	assert.Equal(t, []string{"k8s-b"}, registry.GetToolServerNames("x_kubernetes_pause_cluster"))
}

func TestFamilyIndex_MemberRelistsSubset_PerSession(t *testing.T) {
	ctx := context.Background()
	registry := NewServerRegistry("x")
	for _, name := range []string{"k8s-a", "k8s-b"} {
		require.NoError(t, registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: name, Family: family("kubernetes", "management_cluster")},
			URL:                "https://" + name + ".example.com",
			AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			AuthConfig:         &api.MCPServerAuth{ForwardToken: true},
		}))
	}

	store := oauthstore.NewInMemoryCapabilityStore(time.Hour)
	defer store.Stop()
	listPods, pause := familyTool("list_pods"), familyTool("pause_cluster")
	both := &oauthstore.Capabilities{Tools: []mcp.Tool{listPods, pause}}
	require.NoError(t, store.Set(ctx, "session-1", "k8s-a", both))
	require.NoError(t, store.Set(ctx, "session-1", "k8s-b", both))

	tools := registry.GetAllToolsForSession(ctx, store, "session-1")
	assert.Equal(t, []string{"x_kubernetes_list_pods", "x_kubernetes_pause_cluster"}, exposedNames(tools))
	assert.Equal(t, []string{"k8s-a", "k8s-b"}, registry.GetToolServerNames("x_kubernetes_pause_cluster"))

	// The session reconnects to k8s-b, which now lists only list_pods.
	require.NoError(t, store.Set(ctx, "session-1", "k8s-b", &oauthstore.Capabilities{Tools: []mcp.Tool{listPods}}))

	tools = registry.GetAllToolsForSession(ctx, store, "session-1")
	pauseTool := findTool(t, tools, "x_kubernetes_pause_cluster")
	assert.Contains(t, pauseTool.Description, "(available on servers: k8s-a)")
	assert.NotContains(t, pauseTool.Description, "k8s-b")
	enum, _ := pauseTool.InputSchema.Properties["management_cluster"].(map[string]any)["enum"].([]any)
	assert.Equal(t, []any{"k8s-a"}, enum, "the instance enum follows the current providers")
	assert.Equal(t, []string{"k8s-a"}, registry.GetToolServerNames("x_kubernetes_pause_cluster"))
	assert.Equal(t, []string{"k8s-a", "k8s-b"}, registry.GetToolServerNames("x_kubernetes_list_pods"))

	// k8s-a follows: the tool is gone for the session and from the index.
	require.NoError(t, store.Set(ctx, "session-1", "k8s-a", &oauthstore.Capabilities{Tools: []mcp.Tool{listPods}}))
	tools = registry.GetAllToolsForSession(ctx, store, "session-1")
	assert.Equal(t, []string{"x_kubernetes_list_pods"}, exposedNames(tools))
	assert.False(t, registry.IsFamilyTool("x_kubernetes_pause_cluster"))
}

func TestFamilyIndex_OtherSessionsProvidersAreKept(t *testing.T) {
	// PR #670: a per-session pass only knows the members that session is
	// authenticated to. It must not erase another session's providers.
	ctx := context.Background()
	registry := NewServerRegistry("x")
	for _, name := range []string{"k8s-a", "k8s-b"} {
		require.NoError(t, registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: name, Family: family("kubernetes", "management_cluster")},
			URL:                "https://" + name + ".example.com",
			AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
		}))
	}
	store := oauthstore.NewInMemoryCapabilityStore(time.Hour)
	defer store.Stop()
	listPods, pause := familyTool("list_pods"), familyTool("pause_cluster")
	require.NoError(t, store.Set(ctx, "session-a", "k8s-a", &oauthstore.Capabilities{Tools: []mcp.Tool{listPods, pause}}))
	require.NoError(t, store.Set(ctx, "session-b", "k8s-b", &oauthstore.Capabilities{Tools: []mcp.Tool{listPods, pause}}))

	_ = registry.GetAllToolsForSession(ctx, store, "session-a")
	_ = registry.GetAllToolsForSession(ctx, store, "session-b")
	assert.Equal(t, []string{"k8s-a", "k8s-b"}, registry.GetToolServerNames("x_kubernetes_pause_cluster"),
		"both sessions' members are routable")

	// session-b's member drops pause_cluster; session-a's member is untouched.
	require.NoError(t, store.Set(ctx, "session-b", "k8s-b", &oauthstore.Capabilities{Tools: []mcp.Tool{listPods}}))
	_ = registry.GetAllToolsForSession(ctx, store, "session-b")
	assert.Equal(t, []string{"k8s-a"}, registry.GetToolServerNames("x_kubernetes_pause_cluster"))
	assert.Equal(t, []string{"k8s-a", "k8s-b"}, registry.GetToolServerNames("x_kubernetes_list_pods"))

	// A pass for a session that knows neither member changes nothing.
	_ = registry.GetAllToolsForSession(ctx, store, "session-unrelated")
	assert.Equal(t, []string{"k8s-a"}, registry.GetToolServerNames("x_kubernetes_pause_cluster"))
	assert.Equal(t, []string{"k8s-a", "k8s-b"}, registry.GetToolServerNames("x_kubernetes_list_pods"))

	// The global pass skips auth-required servers entirely (ADR-008) and so
	// must leave their session-established providers alone as well.
	assert.Empty(t, registry.GetAllTools())
	assert.Equal(t, []string{"k8s-a"}, registry.GetToolServerNames("x_kubernetes_pause_cluster"))
	assert.Equal(t, []string{"k8s-a", "k8s-b"}, registry.GetToolServerNames("x_kubernetes_list_pods"))
}

// stateStubServiceRegistry answers GetState for a fixed set of services so
// ServerInfo.IsDown can be exercised without an orchestrator.
type stateStubServiceRegistry struct {
	states map[string]api.ServiceState
}

type stubServiceInfo struct {
	name  string
	state api.ServiceState
}

func (s stubServiceInfo) GetName() string                        { return s.name }
func (s stubServiceInfo) GetType() api.ServiceType               { return api.TypeMCPServer }
func (s stubServiceInfo) GetState() api.ServiceState             { return s.state }
func (s stubServiceInfo) GetHealth() api.HealthStatus            { return api.HealthUnknown }
func (s stubServiceInfo) GetLastError() error                    { return nil }
func (s stubServiceInfo) GetServiceData() map[string]interface{} { return nil }
func (r *stateStubServiceRegistry) GetAll() []api.ServiceInfo    { return nil }
func (r *stateStubServiceRegistry) GetByType(api.ServiceType) []api.ServiceInfo {
	return nil
}
func (r *stateStubServiceRegistry) Get(name string) (api.ServiceInfo, bool) {
	state, ok := r.states[name]
	if !ok {
		return nil, false
	}
	return stubServiceInfo{name: name, state: state}, true
}

func TestFamilyIndex_DownMemberContributesNothing(t *testing.T) {
	ctx := context.Background()
	states := &stateStubServiceRegistry{states: map[string]api.ServiceState{
		"k8s-a": api.StateAuthRequired,
		"k8s-b": api.StateAuthRequired,
	}}
	api.RegisterServiceRegistry(states)
	t.Cleanup(func() { api.RegisterServiceRegistry(nil) })

	registry := NewServerRegistry("x")
	for _, name := range []string{"k8s-a", "k8s-b"} {
		require.NoError(t, registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: name, Family: family("kubernetes", "management_cluster")},
			URL:                "https://" + name + ".example.com",
			AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com", Scope: "openid"},
			AuthConfig:         &api.MCPServerAuth{ForwardToken: true},
		}))
	}
	store := oauthstore.NewInMemoryCapabilityStore(time.Hour)
	defer store.Stop()
	listPods, pause := familyTool("list_pods"), familyTool("pause_cluster")
	require.NoError(t, store.Set(ctx, "session-1", "k8s-a", &oauthstore.Capabilities{
		Tools: []mcp.Tool{listPods}, Resources: []mcp.Resource{{URI: "k8s://a", Name: "a"}}, Prompts: []mcp.Prompt{{Name: "pa"}},
	}))
	require.NoError(t, store.Set(ctx, "session-1", "k8s-b", &oauthstore.Capabilities{
		Tools: []mcp.Tool{listPods, pause}, Resources: []mcp.Resource{{URI: "k8s://b", Name: "b"}}, Prompts: []mcp.Prompt{{Name: "pb"}},
	}))

	tools := registry.GetAllToolsForSession(ctx, store, "session-1")
	assert.Equal(t, []string{"x_kubernetes_list_pods", "x_kubernetes_pause_cluster"}, exposedNames(tools))
	assert.Equal(t, []string{"k8s-a", "k8s-b"}, registry.GetToolServerNames("x_kubernetes_list_pods"))
	assert.Len(t, registry.GetAllResourcesForSession(ctx, store, "session-1"), 2)
	assert.Len(t, registry.GetAllPromptsForSession(ctx, store, "session-1"), 2)

	// k8s-b's probe fails: the service is Failed, the pending-auth entry and
	// the cached capabilities survive. It must stop contributing.
	for _, down := range []api.ServiceState{api.StateFailed, api.StateUnreachable, api.StateDisconnected} {
		states.states["k8s-b"] = down
		tools = registry.GetAllToolsForSession(ctx, store, "session-1")
		assert.Equal(t, []string{"x_kubernetes_list_pods"}, exposedNames(tools), "state %s", down)
		assert.Contains(t, findTool(t, tools, "x_kubernetes_list_pods").Description, "(available on servers: k8s-a)")
		assert.Equal(t, []string{"k8s-a"}, registry.GetToolServerNames("x_kubernetes_list_pods"), "state %s", down)
		assert.False(t, registry.IsFamilyTool("x_kubernetes_pause_cluster"), "state %s", down)
		assert.Len(t, registry.GetAllResourcesForSession(ctx, store, "session-1"), 1, "state %s", down)
		assert.Len(t, registry.GetAllPromptsForSession(ctx, store, "session-1"), 1, "state %s", down)
	}

	// Transitional and auth-required states are not down.
	for _, up := range []api.ServiceState{api.StateStarting, api.StateAuthRequired, api.StateConnected} {
		states.states["k8s-b"] = up
		tools = registry.GetAllToolsForSession(ctx, store, "session-1")
		assert.Equal(t, []string{"x_kubernetes_list_pods", "x_kubernetes_pause_cluster"}, exposedNames(tools), "state %s", up)
		assert.Equal(t, []string{"k8s-a", "k8s-b"}, registry.GetToolServerNames("x_kubernetes_list_pods"), "state %s", up)
	}
}

func TestFamilyIndex_NotConnectedGlobalMemberContributesNothing(t *testing.T) {
	ctx := context.Background()
	states := &stateStubServiceRegistry{states: map[string]api.ServiceState{
		"k8s-a": api.StateConnected,
		"k8s-b": api.StateConnected,
	}}
	api.RegisterServiceRegistry(states)
	t.Cleanup(func() { api.RegisterServiceRegistry(nil) })

	registry := NewServerRegistry("x")
	listPods := familyTool("list_pods")
	require.NoError(t, registry.Register(ctx, ServerRegistration{Name: "k8s-a", Family: family("kubernetes", "management_cluster")}, &mockMCPClient{tools: []mcp.Tool{listPods}}))
	require.NoError(t, registry.Register(ctx, ServerRegistration{Name: "k8s-b", Family: family("kubernetes", "management_cluster")}, &mockMCPClient{tools: []mcp.Tool{listPods}}))
	_ = registry.GetAllTools()
	assert.Equal(t, []string{"k8s-a", "k8s-b"}, registry.GetToolServerNames("x_kubernetes_list_pods"))

	states.states["k8s-b"] = api.StateFailed
	tools := registry.GetAllTools()
	assert.Contains(t, findTool(t, tools, "x_kubernetes_list_pods").Description, "(available on servers: k8s-a)")
	assert.Equal(t, []string{"k8s-a"}, registry.GetToolServerNames("x_kubernetes_list_pods"),
		"a registered member that is not connected withdraws its routing entries")
}

func TestDropSessionCapabilities(t *testing.T) {
	ctx := context.Background()
	store := oauthstore.NewInMemoryCapabilityStore(time.Hour)
	defer store.Stop()
	a := &AggregatorServer{capabilityStore: store, subjectSessions: newSubjectSessionTracker()}

	require.NoError(t, store.Set(ctx, "session-1", "k8s-b", &oauthstore.Capabilities{Tools: []mcp.Tool{familyTool("list_pods")}}))
	require.NoError(t, store.Set(ctx, "session-1", "k8s-a", &oauthstore.Capabilities{Tools: []mcp.Tool{familyTool("list_pods")}}))
	require.NoError(t, store.Set(ctx, "session-2", "k8s-b", &oauthstore.Capabilities{Tools: []mcp.Tool{familyTool("list_pods")}}))

	a.dropSessionCapabilities(ctx, "session-1", "user", "k8s-b", "SSO connection failed")

	gone, err := store.Get(ctx, "session-1", "k8s-b")
	require.NoError(t, err)
	assert.Nil(t, gone, "the failed server's entry for the session is dropped")
	kept, err := store.Get(ctx, "session-1", "k8s-a")
	require.NoError(t, err)
	assert.NotNil(t, kept, "other servers of the session are untouched")
	other, err := store.Get(ctx, "session-2", "k8s-b")
	require.NoError(t, err)
	assert.NotNil(t, other, "other sessions are untouched")

	// Dropping what is not there is a no-op, also without a store.
	a.dropSessionCapabilities(ctx, "session-1", "user", "k8s-b", "again")
	(&AggregatorServer{}).dropSessionCapabilities(ctx, "session-1", "user", "k8s-b", "no store")
}

func TestCapabilityMethodsFor(t *testing.T) {
	assert.Nil(t, capabilityMethodsFor(false, false, false))
	assert.Equal(t, []string{"notifications/tools/list_changed"}, capabilityMethodsFor(true, false, false))
	assert.Equal(t,
		[]string{"notifications/tools/list_changed", "notifications/resources/list_changed", "notifications/prompts/list_changed"},
		capabilityMethodsFor(true, true, true))
	assert.Equal(t, capabilityNotifications(&ConnectionResult{ToolCount: 3, PromptCount: 1}),
		capabilityMethodsFor(true, false, true))
}
