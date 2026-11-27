package approval

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/giantswarm/muster/v5/internal/api"
)

// useMetrics installs approval metrics reading from a manual reader for the
// test.
func useMetrics(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	previous := DefaultMetrics()
	SetDefaultMetrics(NewMetrics(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)), nil))
	t.Cleanup(func() { SetDefaultMetrics(previous) })
	return reader
}

// sum returns the counter's value over the points carrying every given
// attribute.
func sum(t *testing.T, reader *sdkmetric.ManualReader, name string, want map[string]string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	var total int64
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != name {
				continue
			}
			var points []metricdata.DataPoint[int64]
			switch data := m.Data.(type) {
			case metricdata.Sum[int64]:
				points = data.DataPoints
			case metricdata.Gauge[int64]:
				points = data.DataPoints
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

func histogramCount(t *testing.T, reader *sdkmetric.ManualReader, name, outcome string) uint64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	var count uint64
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			if data, ok := m.Data.(metricdata.Histogram[float64]); ok && m.Name == name {
				for _, p := range data.DataPoints {
					if got, _ := p.Attributes.Value(attribute.Key(attrOutcome)); got.AsString() == outcome {
						count += p.Count
					}
				}
			}
		}
	}
	return count
}

func TestMetrics_PageDecisionsAreCounted(t *testing.T) {
	reader := useMetrics(t)
	rig := newPageRig(t)
	request := rig.create(t, map[string]any{"pr": "1"})
	rig.page.Now = func() time.Time { return request.CreatedAt.Add(42 * time.Second) }
	client := browser(t)
	_, body := rig.open(t, client, rig.page.URL(request.ID))
	token := tokenPattern.FindStringSubmatch(body)[1]

	resp, err := client.PostForm(rig.page.URL(request.ID), url.Values{"token": {token}, "decision": {"decline"}})
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	assert.EqualValues(t, 1, sum(t, reader, "muster.approval.decisions", map[string]string{"outcome": "declined"}))
	assert.EqualValues(t, 0, sum(t, reader, "muster.approval.decisions", map[string]string{"outcome": "approved"}))
	assert.EqualValues(t, 1, histogramCount(t, reader, "muster.approval.time_to_decision", "declined"))
}

func TestMetrics_FailedPageChecksAreRefusals(t *testing.T) {
	reader := useMetrics(t)
	rig := newPageRig(t)
	request := rig.create(t, map[string]any{"pr": "1"})
	client := browser(t)
	rig.open(t, client, rig.page.URL(request.ID))

	// A forged token is refused by a page check and decides nothing.
	resp, err := client.PostForm(rig.page.URL(request.ID), url.Values{"token": {"forged"}, "decision": {"approve"}})
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.EqualValues(t, 1, sum(t, reader, "muster.approval.refusals", map[string]string{"reason": ReasonPageCheck}))
	assert.EqualValues(t, 0, sum(t, reader, "muster.approval.decisions", map[string]string{"outcome": "approved"}))
}

func TestPending_SweepCountsOnlyWhatExpiredUndecided(t *testing.T) {
	reader := useMetrics(t)
	store := NewMemoryStore()
	now := time.Now()
	store.SetClock(func() time.Time { return now })
	m := DefaultMetrics()
	for _, id := range []string{"waits", "decided", "expires"} {
		request := newRequest(t, id, alice, "s-"+id, map[string]any{"id": id})
		request.CreatedAt, request.ExpiresAt = now, now.Add(5*time.Minute)
		if id == "waits" {
			request.ExpiresAt = now.Add(time.Hour)
		}
		created, err := store.Create(context.Background(), request)
		require.NoError(t, err)
		m.Pending().Track(created)
	}
	require.NoError(t, store.Decide(context.Background(), "decided", true, alice))

	m.Pending().Sweep(context.Background(), store, now, m)
	assert.Equal(t, 2, m.Pending().Len(), "a decided request is no longer pending here")
	assert.Zero(t, sum(t, reader, "muster.approval.decisions", map[string]string{"outcome": "expired"}))

	later := now.Add(10 * time.Minute)
	store.SetClock(func() time.Time { return later })
	m.Pending().Sweep(context.Background(), store, later, m)
	assert.Equal(t, 1, m.Pending().Len())
	assert.EqualValues(t, 1, sum(t, reader, "muster.approval.decisions", map[string]string{"outcome": "expired"}))
	assert.EqualValues(t, 1, sum(t, reader, "muster.approval.pending", nil), "the gauge reads the tracker")
}

func TestLabels(t *testing.T) {
	assert.Equal(t, "github", ServerLabel(api.ApprovalTarget{Kind: api.ApprovalTargetTool, Server: "github"}))
	assert.Equal(t, "workflow", ServerLabel(api.ApprovalTarget{Kind: api.ApprovalTargetWorkflow}))
	assert.Equal(t, "actor", CallerKindLabel(api.CallerKindActor))
	assert.Equal(t, "other", CallerKindLabel("x"))
	assert.Equal(t, "other", sourceLabel("whatever"))
}
