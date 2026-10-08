package aggregator

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/giantswarm/muster/v5/internal/clock"
	oauthstore "github.com/giantswarm/muster/v5/internal/oauth/store"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The data point of issue #1440: a backend redeployed with a tool whose
// input schema moved its field filters under `filters`. Every session that
// listed the old process must be served the new schema without a restart,
// whether or not it still holds a connection to the backend.

func listIssuesWith(fields ...string) mcp.Tool {
	properties := make(map[string]any, len(fields))
	for _, field := range fields {
		properties[field] = map[string]any{"type": "string"}
	}
	return mcp.Tool{
		Name:        "list_issues",
		Description: "List issues",
		InputSchema: mcp.ToolInputSchema{Type: "object", Properties: properties},
	}
}

var (
	oldListIssues = listIssuesWith("board", "Team")
	newListIssues = listIssuesWith("board", "filters")
)

// recoveringMockClient is a notifMockClient whose MCP session a test can
// have "recovered": the handler the aggregator registered runs, as it does
// when the backend forgot the session and the client re-initialised.
type recoveringMockClient struct {
	notifMockClient
	recoveredMu sync.Mutex
	recovered   func()
	callErr     error
}

func (m *recoveringMockClient) OnSessionRecovered(handler func()) {
	m.recoveredMu.Lock()
	m.recovered = handler
	m.recoveredMu.Unlock()
}

func (m *recoveringMockClient) recover() {
	m.recoveredMu.Lock()
	handler := m.recovered
	m.recoveredMu.Unlock()
	if handler != nil {
		handler()
	}
}

func (m *recoveringMockClient) CallTool(_ context.Context, _ string, _ map[string]interface{}) (*mcp.CallToolResult, error) {
	if m.callErr != nil {
		return nil, m.callErr
	}
	return &mcp.CallToolResult{}, nil
}

// freshnessFixture is an aggregator with a per-session server "pro" and the
// stores a session's catalogue is served from.
type freshnessFixture struct {
	a        *AggregatorServer
	registry *ServerRegistry
	store    *oauthstore.InMemoryCapabilityStore
	pool     *SessionConnectionPool
	info     *ServerInfo
}

func newFreshnessFixture(t *testing.T) *freshnessFixture {
	t.Helper()
	store := oauthstore.NewInMemoryCapabilityStore(time.Hour)
	pool := NewSessionConnectionPool(time.Hour)
	t.Cleanup(pool.Stop)
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry, capabilityStore: store, connPool: pool}
	perSessionServer(t, registry, "pro")
	a.wirePoolNotificationCallback("pro")
	info, ok := registry.GetServerInfo("pro")
	require.True(t, ok)
	return &freshnessFixture{a: a, registry: registry, store: store, pool: pool, info: info}
}

// listedBefore stores the old schema for the session as listed a minute ago,
// before anything a test marks.
func (f *freshnessFixture) listedBefore(t *testing.T, sessionID string) {
	t.Helper()
	require.NoError(t, f.store.Set(context.Background(), sessionID, "pro", &oauthstore.Capabilities{
		Tools:    []mcp.Tool{oldListIssues},
		ListedAt: clock.Now().Add(-time.Minute),
	}))
}

// servesNewSchema asserts the session's catalogue carries list_issues with
// the schema of the new process.
func (f *freshnessFixture) servesNewSchema(t *testing.T, sessionID string) {
	t.Helper()
	tools := f.a.GetToolsForSession(context.Background(), sessionID)
	require.Len(t, tools, 1)
	assert.Equal(t, "x_pro_list_issues", tools[0].Name)
	assert.Contains(t, tools[0].InputSchema.Properties, "filters")
	assert.NotContains(t, tools[0].InputSchema.Properties, "Team")
}

func (f *freshnessFixture) entry(t *testing.T, sessionID string) *oauthstore.Capabilities {
	t.Helper()
	caps, err := f.store.Get(context.Background(), sessionID, "pro")
	require.NoError(t, err)
	require.NotNil(t, caps)
	return caps
}

// TestSessionView_RelistsAnEntryThatPredatesTheRoll: the server's backend
// was seen to change; a session whose entry was listed before is re-listed
// through its pooled connection on its next read, once.
func TestSessionView_RelistsAnEntryThatPredatesTheRoll(t *testing.T) {
	f := newFreshnessFixture(t)
	f.listedBefore(t, "sess-1")
	client := &notifMockClient{tools: []mcp.Tool{newListIssues}}
	f.pool.Put("sess-1", "pro", client)
	require.True(t, f.info.MarkRolled())

	f.servesNewSchema(t, "sess-1")

	assert.Equal(t, int32(1), atomic.LoadInt32(&client.listToolsCalls))
	assert.False(t, f.info.PredatesRoll(f.entry(t, "sess-1").ListedAt), "the entry is stamped with its listing")
	f.servesNewSchema(t, "sess-1")
	assert.Equal(t, int32(1), atomic.LoadInt32(&client.listToolsCalls), "a re-listed entry is served as it is")
}

// TestSessionView_ServesAnEntryListedAfterTheRoll: an entry listed after the
// change needs no listing, nor does any entry while no change was seen.
func TestSessionView_ServesAnEntryListedAfterTheRoll(t *testing.T) {
	f := newFreshnessFixture(t)
	client := &notifMockClient{tools: []mcp.Tool{newListIssues}}
	f.pool.Put("sess-1", "pro", client)
	require.NoError(t, f.store.Set(context.Background(), "sess-1", "pro", &oauthstore.Capabilities{Tools: []mcp.Tool{newListIssues}}))
	f.servesNewSchema(t, "sess-1")
	assert.Equal(t, int32(0), atomic.LoadInt32(&client.listToolsCalls), "nothing changed, nothing is listed")

	require.True(t, f.info.MarkRolled())
	require.NoError(t, f.store.Set(context.Background(), "sess-1", "pro", &oauthstore.Capabilities{Tools: []mcp.Tool{newListIssues}}))
	f.servesNewSchema(t, "sess-1")
	assert.Equal(t, int32(0), atomic.LoadInt32(&client.listToolsCalls), "listed after the change, served as it is")
}

// TestSessionView_ServesTheEntryWhenNoListingCanBeMade: a session without a
// connection, and no way to open one, is served its entry and not asked
// again on every read.
func TestSessionView_ServesTheEntryWhenNoListingCanBeMade(t *testing.T) {
	f := newFreshnessFixture(t)
	f.listedBefore(t, "sess-1")
	require.True(t, f.info.MarkRolled())

	tools := f.a.GetToolsForSession(context.Background(), "sess-1")
	require.Len(t, tools, 1)
	assert.Contains(t, tools[0].InputSchema.Properties, "Team", "the stale entry is served rather than nothing")
	assert.False(t, f.info.PredatesRoll(f.entry(t, "sess-1").ListedAt), "the entry is judged against the change")
}

// TestSessionRecovery_RelistsTheSessionAndMarksTheServer: a pooled connection
// recovered a lost MCP session -- a new process answers -- so the session is
// re-listed through it and every other session's older entry is re-listed on
// its next read.
func TestSessionRecovery_RelistsTheSessionAndMarksTheServer(t *testing.T) {
	f := newFreshnessFixture(t)
	f.listedBefore(t, "sess-1")
	f.listedBefore(t, "sess-2")
	recovering := &recoveringMockClient{notifMockClient: notifMockClient{tools: []mcp.Tool{newListIssues}}}
	f.pool.Put("sess-1", "pro", recovering)
	other := &notifMockClient{tools: []mcp.Tool{newListIssues}}
	f.pool.Put("sess-2", "pro", other)
	require.True(t, f.info.RolledAt().IsZero())

	recovering.recover()

	assert.False(t, f.info.RolledAt().IsZero(), "the recovery marks the server")
	assert.Equal(t, []string{"list_issues"}, namesOf(f.entry(t, "sess-1").Tools))
	assert.Contains(t, f.entry(t, "sess-1").Tools[0].InputSchema.Properties, "filters", "the recovered session is re-listed at once")
	assert.Equal(t, int32(0), atomic.LoadInt32(&other.listToolsCalls), "the other session is left to its next read")

	f.servesNewSchema(t, "sess-2")
	assert.Equal(t, int32(1), atomic.LoadInt32(&other.listToolsCalls))
}

// TestSessionRecovery_OfAnEvictedConnectionMarksOnly: the mark stands even
// when the recovered connection is no longer the session's pooled one; the
// session re-lists on its next read like any other.
func TestSessionRecovery_OfAnEvictedConnectionMarksOnly(t *testing.T) {
	f := newFreshnessFixture(t)
	f.listedBefore(t, "sess-1")
	recovering := &recoveringMockClient{notifMockClient: notifMockClient{tools: []mcp.Tool{newListIssues}}}
	f.pool.Put("sess-1", "pro", recovering)
	f.pool.Evict("sess-1", "pro")

	recovering.recover()

	assert.False(t, f.info.RolledAt().IsZero())
	assert.Equal(t, int32(0), atomic.LoadInt32(&recovering.listToolsCalls), "an evicted connection is not listed")
}

// TestSharedRecovery_RelistsTheServer: a shared client that recovered its
// session re-lists the server's catalogue at once.
func TestSharedRecovery_RelistsTheServer(t *testing.T) {
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry}
	client := &recoveringMockClient{notifMockClient: notifMockClient{tools: []mcp.Tool{oldListIssues}}}
	require.NoError(t, a.RegisterServer(context.Background(), ServerRegistration{Name: "srv"}, client))
	drainRegistryUpdate(registry)
	client.setTools([]mcp.Tool{newListIssues})

	client.recover()

	info, _ := registry.GetServerInfo("srv")
	info.mu.RLock()
	defer info.mu.RUnlock()
	require.Len(t, info.Tools, 1)
	assert.Contains(t, info.Tools[0].InputSchema.Properties, "filters")
}

// TestRefreshSessionCapabilities_AChangeMarksTheServerOnce: a listing that
// answers differently from an entry listed after the last change is a new
// change; one from an entry that predates the mark is the known change and
// marks nothing, so the sessions re-listing after one roll do not keep each
// other re-listing. An unchanged listing of a stale entry stamps it.
func TestRefreshSessionCapabilities_AChangeMarksTheServerOnce(t *testing.T) {
	f := newFreshnessFixture(t)
	ctx := context.Background()
	f.listedBefore(t, "sess-1")
	f.listedBefore(t, "sess-2")
	f.listedBefore(t, "sess-3")

	require.NoError(t, f.a.refreshSessionCapabilities(ctx, "pro", "sess-1", &notifMockClient{tools: []mcp.Tool{newListIssues}}, refreshByPoll))
	marked := f.info.RolledAt()
	require.False(t, marked.IsZero(), "a changed listing of a fresh entry marks the server")

	require.NoError(t, f.a.refreshSessionCapabilities(ctx, "pro", "sess-2", &notifMockClient{tools: []mcp.Tool{newListIssues}}, refreshByRead))
	assert.Equal(t, marked, f.info.RolledAt(), "a changed listing of a stale entry marks nothing")
	assert.False(t, f.info.PredatesRoll(f.entry(t, "sess-2").ListedAt))

	require.NoError(t, f.a.refreshSessionCapabilities(ctx, "pro", "sess-3", &notifMockClient{tools: []mcp.Tool{oldListIssues}}, refreshByRead))
	assert.Equal(t, marked, f.info.RolledAt())
	assert.False(t, f.info.PredatesRoll(f.entry(t, "sess-3").ListedAt), "an unchanged stale entry is stamped as listed")
	assert.Equal(t, []string{"list_issues"}, namesOf(f.entry(t, "sess-3").Tools))
}

// TestMarkRolled_CoalescesTheSignsOfOneChange: a recovery and the listing it
// enabled arrive moments apart and are one change.
func TestMarkRolled_CoalescesTheSignsOfOneChange(t *testing.T) {
	info := &ServerInfo{Name: "pro"}
	require.True(t, info.MarkRolled())
	first := info.RolledAt()
	assert.False(t, info.MarkRolled())
	assert.Equal(t, first, info.RolledAt())
}

func invalidParamsRefusal() error {
	return fmt.Errorf("failed to call tool: %w", mcp.ErrInvalidParams)
}

// TestRefusedSessionArguments_RelistsAndHintsWhenTheSchemaChanged: the
// backend refused the call's arguments; the session's entry is re-listed and,
// since the tool's schema changed, the refusal says so.
func TestRefusedSessionArguments_RelistsAndHintsWhenTheSchemaChanged(t *testing.T) {
	f := newFreshnessFixture(t)
	f.listedBefore(t, "sess-1")
	client := &notifMockClient{tools: []mcp.Tool{newListIssues}}

	err := f.a.refusedSessionArguments(context.Background(), "pro", "sess-1", "list_issues", client, invalidParamsRefusal())

	require.ErrorIs(t, err, mcp.ErrInvalidParams)
	assert.Contains(t, err.Error(), "the input schema of list_issues on pro changed since it was described")
	assert.Contains(t, f.entry(t, "sess-1").Tools[0].InputSchema.Properties, "filters")
	assert.False(t, f.info.RolledAt().IsZero(), "the change marks the server for the other sessions")
}

// TestRefusedSessionArguments_LeavesARefusalOfTheCurrentSchemaAlone: the
// caller's arguments were wrong for the schema the catalogue serves; the
// re-listing changes nothing and the refusal carries no hint.
func TestRefusedSessionArguments_LeavesARefusalOfTheCurrentSchemaAlone(t *testing.T) {
	f := newFreshnessFixture(t)
	f.listedBefore(t, "sess-1")
	client := &notifMockClient{tools: []mcp.Tool{oldListIssues}}

	err := f.a.refusedSessionArguments(context.Background(), "pro", "sess-1", "list_issues", client, invalidParamsRefusal())

	require.ErrorIs(t, err, mcp.ErrInvalidParams)
	assert.NotContains(t, err.Error(), "changed since it was described")
	assert.True(t, f.info.RolledAt().IsZero())
}

// TestCallSharedTool_RefusedArgumentsRelistTheServer: the same for a server
// with a shared client, through the call path.
func TestCallSharedTool_RefusedArgumentsRelistTheServer(t *testing.T) {
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry}
	client := &recoveringMockClient{notifMockClient: notifMockClient{tools: []mcp.Tool{oldListIssues}}, callErr: invalidParamsRefusal()}
	require.NoError(t, registry.Register(context.Background(), ServerRegistration{Name: "srv"}, client))
	client.setTools([]mcp.Tool{newListIssues})

	_, err := a.callSharedTool(context.Background(), client, "srv", "list_issues", map[string]any{"Team": "x"})

	require.ErrorIs(t, err, mcp.ErrInvalidParams)
	assert.Contains(t, err.Error(), "the input schema of list_issues on srv changed since it was described")
	info, _ := registry.GetServerInfo("srv")
	info.mu.RLock()
	defer info.mu.RUnlock()
	assert.Contains(t, info.Tools[0].InputSchema.Properties, "filters")
}

// TestCallSharedTool_OtherErrorsPassThrough: only an invalid-params refusal
// re-lists; any other failure is the caller's as it was.
func TestCallSharedTool_OtherErrorsPassThrough(t *testing.T) {
	registry := NewServerRegistry("x")
	a := &AggregatorServer{registry: registry}
	refused := errors.New("boom")
	client := &recoveringMockClient{notifMockClient: notifMockClient{tools: []mcp.Tool{oldListIssues}}, callErr: refused}
	require.NoError(t, registry.Register(context.Background(), ServerRegistration{Name: "srv"}, client))
	before := atomic.LoadInt32(&client.listToolsCalls)

	_, err := a.callSharedTool(context.Background(), client, "srv", "list_issues", nil)

	assert.ErrorIs(t, err, refused)
	assert.Equal(t, before, atomic.LoadInt32(&client.listToolsCalls))
}

// TestRelistOnConnect_RelistsAnExistingEntryOnly: the connection a call just
// opened re-lists the session's entry from an earlier connection; a session
// without an entry is listed by the connect that gives it one.
func TestRelistOnConnect_RelistsAnExistingEntryOnly(t *testing.T) {
	f := newFreshnessFixture(t)
	ctx := context.Background()
	f.listedBefore(t, "sess-1")
	listed := &notifMockClient{tools: []mcp.Tool{newListIssues}}
	f.pool.Put("sess-1", "pro", listed)
	unlisted := &notifMockClient{tools: []mcp.Tool{newListIssues}}
	f.pool.Put("sess-2", "pro", unlisted)

	f.a.relistOnConnect(ctx, "pro", "sess-1", listed)
	f.a.relistOnConnect(ctx, "pro", "sess-2", unlisted)

	require.Eventually(t, func() bool {
		caps, _ := f.store.Get(ctx, "sess-1", "pro")
		return caps != nil && len(caps.Tools) == 1 && caps.Tools[0].InputSchema.Properties["filters"] != nil
	}, 5*time.Second, 10*time.Millisecond, "the entry from the earlier connection is re-listed")
	assert.Equal(t, int32(0), atomic.LoadInt32(&unlisted.listToolsCalls))
}
