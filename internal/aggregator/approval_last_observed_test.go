package aggregator

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/clock"
)

// outageManager is a manager whose live reads fail with readErr and which
// remembers the definitions in observed (api.MCPServerLastObserved).
type outageManager struct {
	*stubMCPServerManager
	readErr  error
	observed map[string]api.MCPServerInfo
	// age is how long ago the definitions were observed (3s when zero);
	// reads counts the live GetMCPServer calls; throttled is how many of
	// them fail with HTTP 429 before readErr or the stub answers.
	age       time.Duration
	reads     *atomic.Int32
	throttled int32
}

func (m outageManager) observedAt() time.Time {
	if m.age == 0 {
		return clock.Now().Add(-3 * time.Second)
	}
	return clock.Now().Add(-m.age)
}

func (m outageManager) GetMCPServer(ctx context.Context, name string) (*api.MCPServerInfo, error) {
	if m.reads != nil && m.reads.Add(1) <= m.throttled {
		return nil, fmt.Errorf("failed to get MCPServer %s: %w", name, apierrors.NewTooManyRequests("slow down", 0))
	}
	if m.readErr != nil {
		return nil, m.readErr
	}
	return m.stubMCPServerManager.GetMCPServer(ctx, name)
}

func (m outageManager) ListMCPServers(ctx context.Context) ([]api.MCPServerInfo, error) {
	if m.readErr != nil {
		return nil, m.readErr
	}
	return m.stubMCPServerManager.ListMCPServers(ctx)
}

func (m outageManager) LastObservedMCPServers() ([]api.MCPServerInfo, time.Time, bool) {
	infos := make([]api.MCPServerInfo, 0, len(m.observed))
	for _, info := range m.observed {
		infos = append(infos, info)
	}
	return infos, m.observedAt(), len(infos) > 0
}

func (m outageManager) LastObservedMCPServer(key string) (*api.MCPServerInfo, time.Time, bool) {
	info, ok := m.observed[key]
	if !ok {
		return nil, time.Time{}, false
	}
	return &info, m.observedAt(), true
}

// connectionRefused is what a live read returns once the API server is gone:
// client-go wraps the dial error in a *url.Error.
func connectionRefused() error {
	dial := &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	return fmt.Errorf("failed to get MCPServer github: %w", &url.Error{Op: "Get", URL: "https://apiserver/apis", Err: dial})
}

// withOutage replaces the rig's manager by one whose reads fail with readErr
// and which observed the servers of observed (all of the stub's when nil).
func withOutage(t *testing.T, readErr error, observed []string) {
	t.Helper()
	withOutageManager(t, outageManager{readErr: readErr}, observed)
}

// withOutageManager is withOutage for a manager configured by the caller
// (its age, its throttling); the stub and the observed servers are filled in.
// The throttle waits are cut to nothing: no test sleeps.
func withOutageManager(t *testing.T, m outageManager, observed []string) {
	t.Helper()
	saved := throttleBackoffs
	throttleBackoffs = []time.Duration{0, 0, 0}
	t.Cleanup(func() { throttleBackoffs = saved })
	stub := api.GetMCPServerManager().(*stubMCPServerManager)
	seen := map[string]api.MCPServerInfo{}
	for _, name := range observed {
		seen[name] = stub.servers[name]
	}
	m.stubMCPServerManager, m.observed = stub, seen
	api.RegisterMCPServerManager(m)
	t.Cleanup(func() { api.RegisterMCPServerManager(stub) })
}

// While the API server cannot be reached, a call is decided by the server's
// last observed settings: Allow runs, Ask asks, Block is not found.
func TestGate_UnreachableAPIServerUsesTheLastObservedSettings(t *testing.T) {
	rig := newGateRig(t)
	withOutage(t, connectionRefused(), []string{"github"})

	result, err := rig.server.CallToolInternal(rig.ctx, "x_github_get_issue", map[string]any{})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	assert.Equal(t, 1, rig.client.count("get_issue"))

	result, err = rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", map[string]any{})
	require.NoError(t, err)
	approvalIDOf(t, result)
	assert.Zero(t, rig.client.count("merge_pull_request"))

	_, err = rig.server.CallToolInternal(rig.ctx, "x_github_delete_repository", map[string]any{})
	require.EqualError(t, err, "tool not found: x_github_delete_repository")
	assert.Zero(t, rig.client.count("delete_repository"))
}

// A server muster never observed has no settings to fall back to: its
// calls are still refused during the outage.
func TestGate_UnreachableAPIServerNeverObservedRefuses(t *testing.T) {
	rig := newGateRig(t)
	withOutage(t, connectionRefused(), nil)

	_, err := rig.server.CallToolInternal(rig.ctx, "x_github_get_issue", map[string]any{})
	require.ErrorContains(t, err, "the approval settings of server github could not be read")
	require.ErrorContains(t, err, "connection refused")
	assert.Zero(t, rig.client.count("get_issue"))
}

// A deleted server and one muster may not read are refused even though
// muster observed them before: only an unreachable API server falls back.
func TestGate_NotFoundOrForbiddenRefusesDespiteAnObservedDefinition(t *testing.T) {
	gr := schema.GroupResource{Group: "muster.giantswarm.io", Resource: "mcpservers"}
	for name, readErr := range map[string]error{
		"not found": api.NewMCPServerNotFoundError("github"),
		"forbidden": fmt.Errorf("failed to get MCPServer github: %w", apierrors.NewForbidden(gr, "github", errors.New("RBAC"))),
		"decode":    fmt.Errorf("failed to get MCPServer github: %w", errors.New("json: cannot unmarshal string into Go struct field")),
		// Throttling that queues the read (API priority and fairness) ends
		// in the call's own deadline, not in a 429: no fallback (D-GO-39).
		"client deadline": fmt.Errorf("failed to get MCPServer github: %w", &url.Error{Op: "Get", URL: "https://apiserver/apis", Err: context.DeadlineExceeded}),
	} {
		t.Run(name, func(t *testing.T) {
			rig := newGateRig(t)
			withOutage(t, readErr, []string{"github"})

			_, err := rig.server.CallToolInternal(rig.ctx, "x_github_get_issue", map[string]any{})
			require.ErrorContains(t, err, "the approval settings of server github could not be read")
			assert.Zero(t, rig.client.count("get_issue"))
		})
	}
}

// The agent's settings and the governance floors do not come from the API
// server: during the outage they tighten the last observed settings exactly
// as they tighten the live ones.
func TestGate_UnreachableAPIServerKeepsAgentSettingsAndFloors(t *testing.T) {
	rig, platform := governedRig(t, api.GovernanceGoverned)
	withOutage(t, connectionRefused(), []string{"github", "agent-manager", "kagent"})

	blocked := boundAgent(rig, &api.AgentApproval{Entries: []api.AgentApprovalEntry{{Entry: "tool:x_github_get_issue", Setting: api.ApprovalBlock}}})
	_, err := rig.server.CallToolInternal(blocked, "x_github_get_issue", map[string]any{})
	require.EqualError(t, err, "tool not found: x_github_get_issue")
	assert.Zero(t, rig.client.count("get_issue"))

	result, err := rig.server.CallToolInternal(agentContext(rig, "floor"), "x_agent-manager_update_agent", map[string]any{})
	require.NoError(t, err)
	request, err := rig.store.Get(context.Background(), approvalIDOf(t, result))
	require.NoError(t, err)
	assert.Equal(t, api.ApprovalSourceGovernance, request.Display.Source)
	assert.Zero(t, platform.count("update_agent"))

	_, err = rig.server.CallToolInternal(agentContext(rig, "read"), "x_agent-manager_list_agents", map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, 1, platform.count("list_agents"), "a safe-listed read runs")
}

func TestAPIServerUnreachable(t *testing.T) {
	gr := schema.GroupResource{Resource: "mcpservers"}
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"connection refused":      {connectionRefused(), true},
		"service unavailable":     {apierrors.NewServiceUnavailable("etcd"), true},
		"server timeout":          {apierrors.NewServerTimeout(gr, "get", 1), true},
		"too many requests":       {apierrors.NewTooManyRequests("slow down", 1), false},
		"deadline exceeded":       {fmt.Errorf("get: %w", context.DeadlineExceeded), false},
		"deadline in a url error": {&url.Error{Op: "Get", URL: "https://api", Err: context.DeadlineExceeded}, false},
		"dial timeout":            {&net.OpError{Op: "dial", Err: errors.New("i/o timeout")}, true},
		"canceled":                {fmt.Errorf("get: %w", context.Canceled), false},
		"not found":               {apierrors.NewNotFound(gr, "github"), false},
		"muster not found":        {api.NewMCPServerNotFoundError("github"), false},
		"forbidden":               {apierrors.NewForbidden(gr, "github", errors.New("RBAC")), false},
		"unauthorized":            {apierrors.NewUnauthorized("token expired"), false},
		"decode":                  {errors.New("json: cannot unmarshal"), false},
		"nil":                     {nil, false},
	} {
		assert.Equal(t, tc.want, apiServerUnreachable(tc.err), name)
	}
}

// A listing agrees with the calls while the API server cannot be reached:
// the last observed settings report Allow, Ask and Block as a call would be
// decided; a server never observed is reported unavailable, and so is any
// failure that is not an unreachable API server.
func TestApplyApprovals_UnreachableAPIServerListsByTheLastObservedSettings(t *testing.T) {
	tools := []mcp.Tool{
		approvalSoloTool("github", "get_issue"),
		approvalSoloTool("github", "merge_pull_request"),
		approvalSoloTool("github", "delete_repository"),
	}
	ask := api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceUnavailable}

	t.Run("observed", func(t *testing.T) {
		rig := newGateRig(t)
		withOutage(t, connectionRefused(), []string{"github"})
		listed := rig.server.applyApprovals(rig.ctx, tools)
		got, found := approvalOf(t, listed, "x_github_get_issue")
		require.True(t, found)
		assert.Equal(t, api.ApprovalSettingAllow, got.Setting)
		got, _ = approvalOf(t, listed, "x_github_merge_pull_request")
		assert.Equal(t, api.ApprovalSettingAsk, got.Setting)
		assert.NotEqual(t, api.ApprovalSourceUnavailable, got.Source)
		_, found = approvalOf(t, listed, "x_github_delete_repository")
		assert.False(t, found, "a tool the last observed settings block leaves the listing, as the call refuses it")
	})
	t.Run("never observed", func(t *testing.T) {
		rig := newGateRig(t)
		withOutage(t, connectionRefused(), nil)
		got, found := approvalOf(t, rig.server.applyApprovals(rig.ctx, tools), "x_github_get_issue")
		require.True(t, found)
		assert.Equal(t, ask, got, "a server never observed is unavailable")
	})
	t.Run("client deadline", func(t *testing.T) {
		rig := newGateRig(t)
		withOutage(t, &url.Error{Op: "Get", URL: "https://apiserver/apis", Err: context.DeadlineExceeded}, []string{"github"})
		got, found := approvalOf(t, rig.server.applyApprovals(rig.ctx, tools), "x_github_get_issue")
		require.True(t, found)
		assert.Equal(t, ask, got, "the call's own deadline is no outage")
	})
	t.Run("other failure", func(t *testing.T) {
		rig := newGateRig(t)
		withOutage(t, errors.New("json: cannot unmarshal"), []string{"github"})
		got, found := approvalOf(t, rig.server.applyApprovals(rig.ctx, tools), "x_github_get_issue")
		require.True(t, found)
		assert.Equal(t, ask, got, "only an unreachable API server falls back")
	})
}

func setMaxAge(t *testing.T, d time.Duration) {
	t.Helper()
	lastObservedMaxAge.Store(int64(d))
	t.Cleanup(func() { lastObservedMaxAge.Store(0) })
}

// A definition older than the maximum age no longer decides a call: it is
// refused as "approval settings unavailable", whatever the definition said.
// The default is 10 minutes, and a configured age replaces it.
func TestGate_UnreachableAPIServerRefusesAnOverAgeDefinition(t *testing.T) {
	for name, tc := range map[string]struct {
		age    time.Duration
		maxAge time.Duration
		runs   bool
	}{
		"within the default": {age: DefaultLastObservedMaxAge - time.Second, runs: true},
		"beyond the default": {age: DefaultLastObservedMaxAge + time.Second},
		"configured longer":  {age: 30 * time.Minute, maxAge: time.Hour, runs: true},
		"configured shorter": {age: 2 * time.Minute, maxAge: time.Minute},
		"configured within":  {age: 30 * time.Second, maxAge: time.Minute, runs: true},
	} {
		t.Run(name, func(t *testing.T) {
			rig := newGateRig(t)
			if tc.maxAge > 0 {
				setMaxAge(t, tc.maxAge)
			}
			withOutageManager(t, outageManager{readErr: connectionRefused(), age: tc.age}, []string{"github"})

			result, err := rig.server.CallToolInternal(rig.ctx, "x_github_get_issue", map[string]any{})
			if tc.runs {
				require.NoError(t, err)
				assert.False(t, result.IsError)
				assert.Equal(t, 1, rig.client.count("get_issue"))
				return
			}
			require.ErrorContains(t, err, "the approval settings of server github could not be read: approval settings unavailable")
			require.ErrorContains(t, err, "old (maximum ")
			assert.Zero(t, rig.client.count("get_issue"))
		})
	}
}

// A throttled read (HTTP 429) is retried within the call: it recovers when
// the API server answers on a retry, and is refused, never decided by the
// last observed definition, when it stays throttled.
func TestGate_ThrottledReadRetriesThenRefuses(t *testing.T) {
	t.Run("recovers on a retry", func(t *testing.T) {
		rig := newGateRig(t)
		reads := &atomic.Int32{}
		withOutageManager(t, outageManager{reads: reads, throttled: 2}, []string{"github"})

		result, err := rig.server.CallToolInternal(rig.ctx, "x_github_get_issue", map[string]any{})
		require.NoError(t, err)
		assert.False(t, result.IsError)
		assert.EqualValues(t, 3, reads.Load(), "two throttled reads, then the answer")
		assert.Equal(t, 1, rig.client.count("get_issue"))
	})
	t.Run("stays throttled", func(t *testing.T) {
		rig := newGateRig(t)
		reads := &atomic.Int32{}
		withOutageManager(t, outageManager{reads: reads, throttled: 100}, []string{"github"})

		_, err := rig.server.CallToolInternal(rig.ctx, "x_github_get_issue", map[string]any{})
		require.ErrorContains(t, err, "approval settings unavailable: the API server throttled the read (HTTP 429)")
		assert.EqualValues(t, 1+len(throttleBackoffs), reads.Load(), "the first read and one per backoff")
		assert.Zero(t, rig.client.count("get_issue"), "an observed Allow does not decide a throttled call")
	})
	t.Run("deadline ends the retries", func(t *testing.T) {
		rig := newGateRig(t)
		reads := &atomic.Int32{}
		withOutageManager(t, outageManager{reads: reads, throttled: 100}, []string{"github"})
		throttleBackoffs = []time.Duration{time.Hour, time.Hour}
		ctx, cancel := context.WithTimeout(rig.ctx, time.Minute)
		defer cancel()

		_, err := rig.server.CallToolInternal(ctx, "x_github_get_issue", map[string]any{})
		require.ErrorContains(t, err, "the API server throttled the read")
		assert.EqualValues(t, 1, reads.Load(), "a wait that outlasts the call's deadline is not taken")
	})
}

// A listing agrees with the calls: a last observed definition beyond the
// maximum age is reported unavailable, and a throttled listing is never
// answered from the last observed definitions.
func TestApplyApprovals_OverAgeAndThrottledListingsAreUnavailable(t *testing.T) {
	tools := []mcp.Tool{approvalSoloTool("github", "get_issue")}
	ask := api.ToolApproval{Setting: api.ApprovalSettingAsk, Source: api.ApprovalSourceUnavailable}

	for name, readErr := range map[string]error{
		"over age":  connectionRefused(),
		"throttled": apierrors.NewTooManyRequests("slow down", 0),
	} {
		t.Run(name, func(t *testing.T) {
			rig := newGateRig(t)
			age := time.Duration(0)
			if name == "over age" {
				age = DefaultLastObservedMaxAge + time.Second
			}
			withOutageManager(t, outageManager{readErr: readErr, age: age}, []string{"github"})
			got, found := approvalOf(t, rig.server.applyApprovals(rig.ctx, tools), "x_github_get_issue")
			require.True(t, found)
			assert.Equal(t, ask, got)
		})
	}
}

// The configured maximum age is stored at start, and zero leaves the default
// (newApprovalSettings, D-GO-40).
func TestNewApprovalSettings_StoresTheConfiguredMaxAge(t *testing.T) {
	t.Cleanup(func() { lastObservedMaxAge.Store(0); api.RegisterApprovalStore(nil) })
	newApprovalSettings(AggregatorConfig{ApprovalLastObservedMaxAge: 7 * time.Minute}, storeBundle{})
	assert.Equal(t, 7*time.Minute, maxLastObservedAge())
	newApprovalSettings(AggregatorConfig{}, storeBundle{})
	assert.Equal(t, DefaultLastObservedMaxAge, maxLastObservedAge())
}

// The default covers more than the reconciler's resync interval: a server
// nobody called since the last resync is at most one interval old, and an
// outage right before the next resync must leave cover (D-GO-39).
func TestDefaultLastObservedMaxAgeIsAboveTheResyncInterval(t *testing.T) {
	const resync = 10 * time.Minute // the reconciler's default (reconciler.DefaultResyncInterval)
	assert.GreaterOrEqual(t, DefaultLastObservedMaxAge, 3*resync)
}
