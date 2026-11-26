package aggregator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/approval"
)

func useApprovalMetrics(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	previous := approval.DefaultMetrics()
	approval.SetDefaultMetrics(approval.NewMetrics(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)), nil))
	t.Cleanup(func() { approval.SetDefaultMetrics(previous) })
	return reader
}

// counted sums a counter's points carrying every wanted attribute.
func counted(t *testing.T, reader *sdkmetric.ManualReader, name string, want map[string]string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	var total int64
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			var points []metricdata.DataPoint[int64]
			switch data := m.Data.(type) {
			case metricdata.Sum[int64]:
				points = data.DataPoints
			case metricdata.Gauge[int64]:
				points = data.DataPoints
			}
			if m.Name != name {
				continue
			}
			for _, p := range points {
				matches := true
				for key, value := range want {
					if got, ok := p.Attributes.Value(attribute.Key(key)); !ok || got.AsString() != value {
						matches = false
					}
				}
				if matches {
					total += p.Value
				}
			}
		}
	}
	return total
}

func TestApprovalMetrics_AskDecideUse(t *testing.T) {
	reader := useApprovalMetrics(t)
	rig := newGateRig(t)
	args := map[string]any{"pr": 7.0}

	result, err := rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", args)
	require.NoError(t, err)
	id := approvalIDOf(t, result)
	assert.EqualValues(t, 1, counted(t, reader, "muster.approval.requests", map[string]string{"mcpserver.name": "github", "caller_kind": "person", "source": api.ApprovalSourceConnector}))
	assert.EqualValues(t, 1, counted(t, reader, "muster.approval.pending", nil))

	require.NoError(t, rig.store.Decide(context.Background(), id, true, alice))
	_, err = rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", args)
	require.NoError(t, err)
	assert.EqualValues(t, 1, counted(t, reader, "muster.approval.decisions", map[string]string{"outcome": "used"}))
	assert.Zero(t, counted(t, reader, "muster.approval.pending", nil), "a used approval no longer waits")

	// Allow calls ask nothing.
	_, err = rig.server.CallToolInternal(rig.ctx, "x_github_get_issue", map[string]any{})
	require.NoError(t, err)
	assert.EqualValues(t, 1, counted(t, reader, "muster.approval.requests", nil))
}

func TestApprovalMetrics_Refusals(t *testing.T) {
	reader := useApprovalMetrics(t)
	rig := newGateRig(t)

	_, err := rig.server.CallToolInternal(rig.ctx, "x_github_delete_repository", map[string]any{})
	require.Error(t, err)
	assert.EqualValues(t, 1, counted(t, reader, "muster.approval.refusals", map[string]string{"reason": approval.ReasonBlocked, "caller_kind": "person"}))

	api.RegisterBrowserIdentity(nil)
	_, err = rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", map[string]any{"pr": 1.0})
	require.Error(t, err)
	assert.EqualValues(t, 1, counted(t, reader, "muster.approval.refusals", map[string]string{"reason": approval.ReasonUnavailable}))
	assert.Zero(t, counted(t, reader, "muster.approval.requests", nil))
}

func TestApprovalMetrics_StoreOutageAndCaps(t *testing.T) {
	reader := useApprovalMetrics(t)
	rig := newGateRig(t)

	// The per-session cap (3 pending) refuses the fourth distinct ask.
	for i := range approval.MaxPendingPerSession {
		_, err := rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", map[string]any{"pr": float64(i)})
		require.NoError(t, err)
	}
	_, err := rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", map[string]any{"pr": 99.0})
	require.Error(t, err)
	assert.EqualValues(t, 1, counted(t, reader, "muster.approval.cap_hits", map[string]string{"scope": api.ApprovalLimitSession}))
	assert.EqualValues(t, 1, counted(t, reader, "muster.approval.refusals", map[string]string{"reason": approval.ReasonCapReached}))
	assert.EqualValues(t, approval.MaxPendingPerSession, counted(t, reader, "muster.approval.requests", nil))

	api.RegisterApprovalStore(&failingStore{})
	_, err = rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", map[string]any{"pr": 1.0})
	require.Error(t, err)
	assert.EqualValues(t, 1, counted(t, reader, "muster.approval.refusals", map[string]string{"reason": approval.ReasonStoreUnavailable}))
}

func TestApprovalMetrics_SweepKeepsWhatStillWaits(t *testing.T) {
	reader := useApprovalMetrics(t)
	rig := newGateRig(t)
	_, err := rig.server.CallToolInternal(rig.ctx, "x_github_merge_pull_request", map[string]any{"pr": 5.0})
	require.NoError(t, err)

	rig.server.sweepApprovals(context.Background())
	assert.EqualValues(t, 1, counted(t, reader, "muster.approval.pending", nil), "still pending: nothing expires yet")
	assert.Zero(t, counted(t, reader, "muster.approval.decisions", map[string]string{"outcome": "expired"}))
}
