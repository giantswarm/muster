package aggregator

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
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
}

func (m outageManager) GetMCPServer(ctx context.Context, name string) (*api.MCPServerInfo, error) {
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
	return infos, clock.Now().Add(-3 * time.Second), len(infos) > 0
}

func (m outageManager) LastObservedMCPServer(key string) (*api.MCPServerInfo, time.Time, bool) {
	info, ok := m.observed[key]
	if !ok {
		return nil, time.Time{}, false
	}
	return &info, clock.Now().Add(-3 * time.Second), true
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
	stub := api.GetMCPServerManager().(*stubMCPServerManager)
	seen := map[string]api.MCPServerInfo{}
	for _, name := range observed {
		seen[name] = stub.servers[name]
	}
	api.RegisterMCPServerManager(outageManager{stubMCPServerManager: stub, readErr: readErr, observed: seen})
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
		"connection refused":  {connectionRefused(), true},
		"service unavailable": {apierrors.NewServiceUnavailable("etcd"), true},
		"server timeout":      {apierrors.NewServerTimeout(gr, "get", 1), true},
		"too many requests":   {apierrors.NewTooManyRequests("slow down", 1), true},
		"deadline exceeded":   {fmt.Errorf("get: %w", context.DeadlineExceeded), true},
		"canceled":            {fmt.Errorf("get: %w", context.Canceled), false},
		"not found":           {apierrors.NewNotFound(gr, "github"), false},
		"muster not found":    {api.NewMCPServerNotFoundError("github"), false},
		"forbidden":           {apierrors.NewForbidden(gr, "github", errors.New("RBAC")), false},
		"unauthorized":        {apierrors.NewUnauthorized("token expired"), false},
		"decode":              {errors.New("json: cannot unmarshal"), false},
		"nil":                 {nil, false},
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
	t.Run("other failure", func(t *testing.T) {
		rig := newGateRig(t)
		withOutage(t, errors.New("json: cannot unmarshal"), []string{"github"})
		got, found := approvalOf(t, rig.server.applyApprovals(rig.ctx, tools), "x_github_get_issue")
		require.True(t, found)
		assert.Equal(t, ask, got, "only an unreachable API server falls back")
	})
}
