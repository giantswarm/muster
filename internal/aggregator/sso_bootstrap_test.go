package aggregator

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
	oauthstore "github.com/giantswarm/muster/internal/oauth/store"
	"github.com/giantswarm/muster/pkg/logging"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// lockedBuffer is a log sink the fan-out goroutines write to while the test
// reads it. The logger is not restored afterwards on purpose: background
// goroutines outlive the test and would race on the global logger.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// connectGates lets a test hold each server's connect open until it releases
// it, and counts the connects per server.
type connectGates struct {
	mu    sync.Mutex
	gates map[string]chan struct{}
	calls map[string]*atomic.Int32
}

func newConnectGates(servers ...string) *connectGates {
	g := &connectGates{gates: map[string]chan struct{}{}, calls: map[string]*atomic.Int32{}}
	for _, s := range servers {
		g.gates[s] = make(chan struct{})
		g.calls[s] = &atomic.Int32{}
	}
	return g
}

func (g *connectGates) connect(ctx context.Context, info *ServerInfo, _ string) ssoConnectOutcome {
	g.mu.Lock()
	gate, counter := g.gates[info.Name], g.calls[info.Name]
	g.mu.Unlock()
	if counter != nil {
		counter.Add(1)
	}
	if gate == nil {
		return ssoConnected
	}
	select {
	case <-gate:
		return ssoConnected
	case <-ctx.Done():
		return ssoConnectFailed
	}
}

func (g *connectGates) release(server string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	close(g.gates[server])
}

func (g *connectGates) count(server string) int32 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls[server].Load()
}

// newBootstrapTestAggregator builds an aggregator whose registry holds one
// token-forwarding server per name and whose SSO connects go through gates.
func newBootstrapTestAggregator(t *testing.T, gates *connectGates, servers ...string) *AggregatorServer {
	t.Helper()
	registry := NewServerRegistry("x")
	for _, name := range servers {
		require.NoError(t, registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: name, ToolPrefix: name},
			URL:                "https://" + name + ".invalid",
			AuthInfo:           &AuthInfo{Issuer: "https://dex.example.com"},
			AuthConfig:         &api.MCPServerAuth{ForwardToken: true},
		}))
	}
	authStore := oauthstore.NewInMemorySessionAuthStore(30 * time.Minute)
	t.Cleanup(authStore.Stop)
	pool := NewSessionConnectionPool(time.Hour)
	t.Cleanup(pool.Stop)
	return &AggregatorServer{
		registry:   registry,
		authStore:  authStore,
		connPool:   pool,
		ssoTracker: newSSOTracker(),
		ssoConnect: gates.connect,
		config: AggregatorConfig{
			OAuthServer: OAuthServerConfig{
				Enabled: true,
				Config:  config.OAuthServerConfig{BaseURL: "https://muster.example.com"},
			},
		},
	}
}

func TestSSOBootstrap_WaitServerReleasesOnThatServerAlone(t *testing.T) {
	b := newSSOBootstrap("s", []string{"alpha", "beta"})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	assert.Zero(t, b.waitServer(ctx, "unknown"), "a server the fan-out does not cover needs no wait")

	b.serverFinished("alpha")
	b.serverFinished("alpha") // idempotent
	assert.Zero(t, b.waitServer(ctx, "alpha"), "a finished server needs no wait")

	waited := b.waitServer(ctx, "beta")
	assert.GreaterOrEqual(t, waited, 90*time.Millisecond, "beta is still connecting: the wait ends with the context")
	assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)

	b.finish()
	assert.Zero(t, b.waitServer(context.Background(), "beta"), "finish releases every server")
	assert.Zero(t, b.wait(context.Background()))
}

func TestBeginSessionBootstrap_ReturnsBeforeTheConnectsFinish(t *testing.T) {
	sink := &lockedBuffer{}
	logging.InitForCLI(logging.LevelInfo, sink)

	gates := newConnectGates("alpha", "beta")
	agg := newBootstrapTestAggregator(t, gates, "alpha", "beta")
	sso := ssoSession{userID: "alice", sessionID: "ext-fresh"}

	started := time.Now()
	b := agg.beginSessionBootstrap(sso)
	require.NotNil(t, b)
	assert.Less(t, time.Since(started), 200*time.Millisecond,
		"the request that starts the fan-out is not held for the connects")
	assert.Same(t, b, agg.sessionBootstrap(sso.sessionID), "the fan-out is registered for the session while it runs")

	// A call for alpha waits for alpha's connect alone: beta stays gated.
	gates.release("alpha")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	agg.awaitServerBootstrap(ctx, sso.sessionID, "alpha")
	assert.NoError(t, ctx.Err(), "alpha's connect finished, the wait for it must not run into the deadline")
	assert.NotNil(t, agg.sessionBootstrap(sso.sessionID), "beta is still connecting")

	// A listing waits for the whole fan-out.
	go func() {
		time.Sleep(50 * time.Millisecond)
		gates.release("beta")
	}()
	waited := agg.awaitSessionBootstrap(ctx, sso.sessionID)
	assert.NoError(t, ctx.Err())
	assert.GreaterOrEqual(t, waited, 40*time.Millisecond, "the listing waited for beta")

	require.Eventually(t, func() bool { return agg.sessionBootstrap(sso.sessionID) == nil },
		time.Second, 5*time.Millisecond, "a finished fan-out is forgotten")
	require.Eventually(t, func() bool { return bytes.Contains([]byte(sink.String()), []byte(`msg="SSO: fan-out finished"`)) },
		time.Second, 5*time.Millisecond)
	summary := sink.String()
	assert.Contains(t, summary, "servers=2")
	assert.Contains(t, summary, "connected=2")
	assert.Contains(t, summary, "failed=0")
	assert.Contains(t, summary, "slowestServer=beta")
	assert.Contains(t, summary, "timedOut=false")
}

func TestBeginSessionBootstrap_ConcurrentFirstRequestsShareOneFanOut(t *testing.T) {
	gates := newConnectGates("alpha")
	agg := newBootstrapTestAggregator(t, gates, "alpha")
	sso := ssoSession{userID: "alice", sessionID: "ext-burst"}

	var wg sync.WaitGroup
	results := make([]*ssoBootstrap, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = agg.beginSessionBootstrap(sso)
		}(i)
	}
	wg.Wait()
	for _, b := range results {
		assert.Same(t, results[0], b, "initialize and tools/list arriving together start one fan-out")
	}
	gates.release("alpha")
	results[0].wait(context.Background())
	assert.Equal(t, int32(1), gates.count("alpha"), "one connect per server, however many requests started the session")
}

func TestBeginSessionBootstrap_NothingToConnect(t *testing.T) {
	agg := newBootstrapTestAggregator(t, newConnectGates())
	assert.Nil(t, agg.beginSessionBootstrap(ssoSession{userID: "alice", sessionID: "ext-empty"}),
		"a session with no session-authenticated server has no fan-out")
	assert.Nil(t, agg.sessionBootstrap("ext-empty"))

	agg = newBootstrapTestAggregator(t, newConnectGates("alpha"), "alpha")
	agg.config.OAuthServer.Config = config.OAuthServerConfig{}
	assert.Nil(t, agg.beginSessionBootstrap(ssoSession{userID: "alice", sessionID: "ext-no-issuer"}),
		"without an issuer there is nothing to exchange or forward")
}

func TestAwaitToolOwnersBootstrap_WaitsOnlyForTheOwners(t *testing.T) {
	gates := newConnectGates("alpha", "beta")
	agg := newBootstrapTestAggregator(t, gates, "alpha", "beta")
	sso := ssoSession{userID: "alice", sessionID: "ext-owners"}
	b := agg.beginSessionBootstrap(sso)
	require.NotNil(t, b)
	gates.release("alpha")
	b.waitServer(context.Background(), "alpha")

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	assert.Zero(t, agg.awaitToolOwnersBootstrap(ctx, sso.sessionID, "x_alpha_do", nil),
		"alpha has connected: a call for its tool is not held for beta")
	assert.Zero(t, agg.awaitToolOwnersBootstrap(ctx, sso.sessionID, "core_service_list", nil),
		"a name no server could own waits for nothing")
	assert.Zero(t, agg.awaitToolOwnersBootstrap(ctx, "other-session", "x_beta_do", nil),
		"a session without a fan-out waits for nothing")

	waited := agg.awaitToolOwnersBootstrap(ctx, sso.sessionID, "x_beta_do", nil)
	assert.GreaterOrEqual(t, waited, 90*time.Millisecond, "beta is still connecting: the call is held until the context ends")
	assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)

	gates.release("beta")
	b.wait(context.Background())
	assert.Zero(t, agg.awaitToolOwnersBootstrap(context.Background(), sso.sessionID, "x_beta_do", nil))
}

func TestBeginSessionBootstrap_FanOutTimesOutWithConnectsStillRunning(t *testing.T) {
	// A connect that never returns must not hold the session's requests past
	// the fan-out's deadline. The deadline is a package constant; this test
	// only proves the gate is wired, on a connect that honours cancellation.
	gates := newConnectGates("stuck")
	agg := newBootstrapTestAggregator(t, gates, "stuck")
	b := agg.beginSessionBootstrap(ssoSession{userID: "alice", sessionID: "ext-stuck"})
	require.NotNil(t, b)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	agg.awaitSessionBootstrap(ctx, "ext-stuck")
	assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded, "the caller's context bounds the wait")
	gates.release("stuck")
	b.wait(context.Background())
}

func TestServersInNameSpaceOf(t *testing.T) {
	registry := NewServerRegistry("x")
	require.NoError(t, registry.RegisterPendingAuth(PendingAuthRegistration{
		ServerRegistration: ServerRegistration{Name: "gh", ToolPrefix: "github"},
		URL:                "https://gh.invalid",
		AuthConfig:         &api.MCPServerAuth{ForwardToken: true},
	}))
	family := &api.MCPServerFamily{Name: "kubernetes", InstanceArg: "server"}
	for _, member := range []string{"gazelle-k8s", "glean-k8s"} {
		require.NoError(t, registry.RegisterPendingAuth(PendingAuthRegistration{
			ServerRegistration: ServerRegistration{Name: member, ToolPrefix: member, Family: family},
			URL:                "https://" + member + ".invalid",
			AuthConfig:         &api.MCPServerAuth{ForwardToken: true},
		}))
	}

	assert.Equal(t, []string{"gh"}, registry.ServersInNameSpaceOf("x_github_list_issues", nil),
		"the server whose prefix the name carries")
	assert.Equal(t, []string{"gazelle-k8s", "glean-k8s"}, registry.ServersInNameSpaceOf("x_kubernetes_get_nodes", nil),
		"every member of the family whose name space the name lies in")
	assert.Equal(t, []string{"glean-k8s"}, registry.ServersInNameSpaceOf("x_kubernetes_get_nodes", map[string]any{"server": "glean-k8s"}),
		"the member the call's instance argument selects")
	assert.Equal(t, []string{"gazelle-k8s"}, registry.ServersInNameSpaceOf("x_gazelle-k8s_get_nodes", nil),
		"a per-server name of a family member")
	assert.Empty(t, registry.ServersInNameSpaceOf("core_service_list", nil))
	assert.Empty(t, registry.ServersInNameSpaceOf("x_unknown_tool", nil))
}

func TestSessionBootstrapped_RemembersASessionsFirstFanOut(t *testing.T) {
	gates := newConnectGates("alpha")
	agg := newBootstrapTestAggregator(t, gates, "alpha")

	assert.False(t, agg.sessionBootstrapped("ext-new"), "a session that never started a fan-out")

	b := agg.beginSessionBootstrap(ssoSession{userID: "alice", sessionID: "ext-new"})
	require.NotNil(t, b)
	assert.True(t, agg.sessionBootstrapped("ext-new"), "recorded as soon as the fan-out starts")
	gates.release("alpha")
	b.wait(context.Background())
	assert.True(t, agg.sessionBootstrapped("ext-new"), "and after it finished: its later requests do not clear the person's failures again")

	agg.ssoBootstrapsMu.Lock()
	agg.ssoBootstrapped["ext-old"] = time.Now().Add(-ssoBootstrappedRetention - time.Second)
	agg.ssoBootstrapsMu.Unlock()
	assert.False(t, agg.sessionBootstrapped("ext-old"), "a session past the retention is new again")
	agg.ssoBootstrapsMu.Lock()
	_, kept := agg.ssoBootstrapped["ext-old"]
	agg.ssoBootstrapsMu.Unlock()
	assert.False(t, kept, "and its record is dropped")
}
