package aggregator

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	oauthstore "github.com/giantswarm/muster/internal/oauth/store"
)

// readCountingStore counts how a session listing reads the capability store.
type readCountingStore struct {
	oauthstore.CapabilityStore
	gets    atomic.Int64
	getAlls atomic.Int64
}

func (s *readCountingStore) Get(ctx context.Context, sessionID, serverName string) (*oauthstore.Capabilities, error) {
	s.gets.Add(1)
	return s.CapabilityStore.Get(ctx, sessionID, serverName)
}

func (s *readCountingStore) GetAll(ctx context.Context, sessionID string) (map[string]*oauthstore.Capabilities, error) {
	s.getAlls.Add(1)
	return s.CapabilityStore.GetAll(ctx, sessionID)
}

// Every session-scoped listing reads the capability store once for the whole
// session, not once per registered server: with 81 session-authenticated
// servers a listing cost ~160 Valkey round trips per meta-tool call (#1225).
func TestServerRegistry_SessionListingsReadTheStoreOnce(t *testing.T) {
	const sessionID = "session-once"
	reg := NewServerRegistry("x")
	const servers = 40
	names := make([]string, 0, servers)
	for i := 0; i < servers; i++ {
		name := "mc" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + "-mcp-kubernetes"
		names = append(names, name)
		registerAuthFamilyMember(t, reg, name, "kubernetes", "management_cluster")
	}
	require.NoError(t, reg.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "never-connected"},
		URL:                "https://never.example.com",
	}))

	base := oauthstore.NewInMemoryCapabilityStore(30 * time.Minute)
	t.Cleanup(base.Stop)
	ctx := context.Background()
	for _, name := range names {
		require.NoError(t, base.Set(ctx, sessionID, name, &oauthstore.Capabilities{
			Tools:     []mcp.Tool{{Name: "list", Description: "List resources"}},
			Resources: []mcp.Resource{{URI: "k8s://" + name + "/pods", Name: "pods"}},
			Prompts:   []mcp.Prompt{{Name: "triage"}},
		}))
	}
	store := &readCountingStore{CapabilityStore: base}

	reset := func() { store.gets.Store(0); store.getAlls.Store(0) }
	assertOneRead := func(t *testing.T, what string) {
		t.Helper()
		assert.Equal(t, int64(1), store.getAlls.Load(), "%s reads the session once", what)
		assert.Zero(t, store.gets.Load(), "%s never reads per server", what)
	}

	reset()
	tools := reg.GetAllToolsForSession(ctx, store, sessionID)
	assertOneRead(t, "GetAllToolsForSession")
	require.Len(t, tools, 1, "forty members of one family group into one exposed tool")
	assert.Equal(t, "x_kubernetes_list", tools[0].Name)
	assert.Len(t, reg.GetToolServerNames("x_kubernetes_list"), servers)

	reset()
	resources := reg.GetAllResourcesForSession(ctx, store, sessionID)
	assertOneRead(t, "GetAllResourcesForSession")
	assert.Len(t, resources, servers)

	reset()
	prompts := reg.GetAllPromptsForSession(ctx, store, sessionID)
	assertOneRead(t, "GetAllPromptsForSession")
	assert.Len(t, prompts, servers)

	reset()
	members, visible := reg.FamilyMembersForSession(ctx, store, sessionID, "kubernetes")
	assertOneRead(t, "FamilyMembersForSession")
	assert.Len(t, members, servers)
	assert.Len(t, visible, servers)

	// A session the store knows nothing about lists no session-authenticated
	// tool and still costs one read.
	reset()
	assert.Empty(t, reg.GetAllToolsForSession(ctx, store, "stranger"))
	assertOneRead(t, "an unknown session")

	// Without a store there is nothing to read.
	assert.Empty(t, reg.GetAllToolsForSession(ctx, nil, sessionID))
}
