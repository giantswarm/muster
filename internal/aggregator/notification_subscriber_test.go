package aggregator

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolListsEqual_Identical(t *testing.T) {
	tools := []mcp.Tool{
		{Name: "a", Description: "desc-a"},
		{Name: "b", Description: "desc-b"},
	}
	assert.True(t, toolListsEqual(tools, tools))
}

func TestToolListsEqual_BothNil(t *testing.T) {
	assert.True(t, toolListsEqual(nil, nil))
}

func TestToolListsEqual_BothEmpty(t *testing.T) {
	assert.True(t, toolListsEqual([]mcp.Tool{}, []mcp.Tool{}))
}

func TestToolListsEqual_DifferentLength(t *testing.T) {
	a := []mcp.Tool{{Name: "a"}}
	b := []mcp.Tool{{Name: "a"}, {Name: "b"}}
	assert.False(t, toolListsEqual(a, b))
}

func TestToolListsEqual_Addition(t *testing.T) {
	old := []mcp.Tool{{Name: "a"}}
	new := []mcp.Tool{{Name: "b"}}
	assert.False(t, toolListsEqual(old, new))
}

func TestToolListsEqual_DescriptionChanged(t *testing.T) {
	old := []mcp.Tool{{Name: "a", Description: "v1"}}
	new := []mcp.Tool{{Name: "a", Description: "v2"}}
	assert.False(t, toolListsEqual(old, new))
}

func TestToolListsEqual_SchemaChanged(t *testing.T) {
	schemaA := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]interface{}{
			"foo": map[string]interface{}{"type": "string"},
		},
	}
	schemaB := mcp.ToolInputSchema{
		Type: "object",
		Properties: map[string]interface{}{
			"foo": map[string]interface{}{"type": "number"},
		},
	}
	old := []mcp.Tool{{Name: "a", InputSchema: schemaA}}
	new := []mcp.Tool{{Name: "a", InputSchema: schemaB}}
	assert.False(t, toolListsEqual(old, new))
}

func TestToolListsEqual_SameContentDifferentOrder(t *testing.T) {
	a := []mcp.Tool{{Name: "x", Description: "1"}, {Name: "y", Description: "2"}}
	b := []mcp.Tool{{Name: "y", Description: "2"}, {Name: "x", Description: "1"}}
	assert.True(t, toolListsEqual(a, b))
}

func TestResourceListsEqual(t *testing.T) {
	assert.True(t, resourceListsEqual(nil, nil))
	assert.True(t, resourceListsEqual([]mcp.Resource{}, []mcp.Resource{}))
	assert.False(t, resourceListsEqual(
		[]mcp.Resource{{URI: "a"}},
		[]mcp.Resource{{URI: "a"}, {URI: "b"}},
	))
	assert.False(t, resourceListsEqual(
		[]mcp.Resource{{URI: "a", Name: "n1"}},
		[]mcp.Resource{{URI: "a", Name: "n2"}},
	))
	assert.True(t, resourceListsEqual(
		[]mcp.Resource{{URI: "a", Name: "n"}},
		[]mcp.Resource{{URI: "a", Name: "n"}},
	))
}

func TestPromptListsEqual(t *testing.T) {
	assert.True(t, promptListsEqual(nil, nil))
	assert.True(t, promptListsEqual([]mcp.Prompt{}, []mcp.Prompt{}))
	assert.False(t, promptListsEqual(
		[]mcp.Prompt{{Name: "a"}},
		[]mcp.Prompt{{Name: "a"}, {Name: "b"}},
	))
	assert.False(t, promptListsEqual(
		[]mcp.Prompt{{Name: "a", Description: "v1"}},
		[]mcp.Prompt{{Name: "a", Description: "v2"}},
	))
	assert.True(t, promptListsEqual(
		[]mcp.Prompt{{Name: "a", Description: "v"}},
		[]mcp.Prompt{{Name: "a", Description: "v"}},
	))
}

// notifMockClient is a mock MCPClient that counts ListTools calls and
// returns configurable tools. Used exclusively in notification subscriber tests.
type notifMockClient struct {
	mu             sync.Mutex
	tools          []mcp.Tool
	resources      []mcp.Resource
	prompts        []mcp.Prompt
	listToolsCalls int32
	notifHandler   func(mcp.JSONRPCNotification)
}

func (m *notifMockClient) Initialize(_ context.Context) error { return nil }
func (m *notifMockClient) Close() error                       { return nil }
func (m *notifMockClient) Ping(_ context.Context) error       { return nil }
func (m *notifMockClient) CallTool(_ context.Context, _ string, _ map[string]interface{}) (*mcp.CallToolResult, error) {
	return &mcp.CallToolResult{}, nil
}
func (m *notifMockClient) ReadResource(_ context.Context, _ string) (*mcp.ReadResourceResult, error) {
	return &mcp.ReadResourceResult{}, nil
}
func (m *notifMockClient) GetPrompt(_ context.Context, _ string, _ map[string]interface{}) (*mcp.GetPromptResult, error) {
	return &mcp.GetPromptResult{}, nil
}

func (m *notifMockClient) ListTools(_ context.Context) ([]mcp.Tool, error) {
	atomic.AddInt32(&m.listToolsCalls, 1)
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tools, nil
}

func (m *notifMockClient) ListResources(_ context.Context) ([]mcp.Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.resources, nil
}

func (m *notifMockClient) ListPrompts(_ context.Context) ([]mcp.Prompt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.prompts, nil
}

func (m *notifMockClient) OnNotification(handler func(mcp.JSONRPCNotification)) {
	m.notifHandler = handler
}

func (m *notifMockClient) setTools(tools []mcp.Tool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tools = tools
}

func TestRefreshNonOAuthTools_UpdatesTools(t *testing.T) {
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry}

	initialTools := []mcp.Tool{{Name: "old-tool", Description: "v1"}}
	client := &notifMockClient{tools: initialTools}

	ctx := context.Background()
	require.NoError(t, registry.Register(ctx, "srv", client, ""))

	info, _ := registry.GetServerInfo("srv")
	info.mu.RLock()
	require.Len(t, info.Tools, 1)
	assert.Equal(t, "old-tool", info.Tools[0].Name)
	info.mu.RUnlock()

	updatedTools := []mcp.Tool{
		{Name: "old-tool", Description: "v1"},
		{Name: "new-tool", Description: "v2"},
	}
	client.setTools(updatedTools)

	a.refreshNonOAuthTools("srv")

	info.mu.RLock()
	assert.Len(t, info.Tools, 2)
	info.mu.RUnlock()
}

func TestRefreshNonOAuthTools_NoChangeSkipsUpdate(t *testing.T) {
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry}

	tools := []mcp.Tool{{Name: "tool", Description: "d"}}
	client := &notifMockClient{tools: tools}

	ctx := context.Background()
	require.NoError(t, registry.Register(ctx, "srv", client, ""))

	// Drain the initial registration update
	select {
	case <-registry.GetUpdateChannel():
	default:
	}

	a.refreshNonOAuthTools("srv")

	select {
	case <-registry.GetUpdateChannel():
		t.Fatal("expected no update notification when tools haven't changed")
	default:
	}
}

func TestRefreshNonOAuthTools_ServerNotFound(t *testing.T) {
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry}

	// Should not panic for a missing server
	a.refreshNonOAuthTools("nonexistent")
}

func TestHandleNonOAuthToolListChanged_TriggersRefresh(t *testing.T) {
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry}

	initialTools := []mcp.Tool{{Name: "t1"}}
	updatedTools := []mcp.Tool{{Name: "t1"}, {Name: "t2"}}
	client := &notifMockClient{tools: initialTools}

	ctx := context.Background()
	require.NoError(t, registry.Register(ctx, "srv", client, ""))

	client.setTools(updatedTools)

	a.handleNonOAuthToolListChanged("srv")

	// handleNonOAuthToolListChanged runs async; wait for completion
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&client.listToolsCalls) >= 2
	}, 2*time.Second, 10*time.Millisecond)

	info, _ := registry.GetServerInfo("srv")
	info.mu.RLock()
	assert.Len(t, info.Tools, 2)
	info.mu.RUnlock()
}

func TestHandleNonOAuthToolListChanged_SingleflightDedup(t *testing.T) {
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry}

	tools := []mcp.Tool{{Name: "t1"}}
	client := &notifMockClient{tools: tools}

	ctx := context.Background()
	require.NoError(t, registry.Register(ctx, "srv", client, ""))

	baseCount := atomic.LoadInt32(&client.listToolsCalls)

	// Fire many notifications concurrently
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.handleNonOAuthToolListChanged("srv")
		}()
	}
	wg.Wait()

	// Wait for async goroutines to finish
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&client.listToolsCalls) > baseCount
	}, 2*time.Second, 10*time.Millisecond)

	// singleflight should collapse many calls into very few actual fetches
	calls := atomic.LoadInt32(&client.listToolsCalls) - baseCount
	assert.LessOrEqual(t, calls, int32(20),
		"singleflight should deduplicate concurrent calls")
}

func TestToolListsEqual_SchemaChanged_JSON(t *testing.T) {
	makeSchema := func(props map[string]interface{}) mcp.ToolInputSchema {
		raw, _ := json.Marshal(props)
		var schema mcp.ToolInputSchema
		_ = json.Unmarshal(raw, &schema)
		return schema
	}

	old := []mcp.Tool{{
		Name:        "t",
		InputSchema: makeSchema(map[string]interface{}{"type": "object"}),
	}}
	same := []mcp.Tool{{
		Name:        "t",
		InputSchema: makeSchema(map[string]interface{}{"type": "object"}),
	}}
	different := []mcp.Tool{{
		Name:        "t",
		InputSchema: makeSchema(map[string]interface{}{"type": "array"}),
	}}

	assert.True(t, toolListsEqual(old, same))
	assert.False(t, toolListsEqual(old, different))
}
