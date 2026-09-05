package reconciler

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

// TestApplyStatusFromServiceMirrorsRetrySchedule: the CRD has carried
// consecutiveFailures, lastAttempt and nextRetryAfter since the fields were
// introduced, but nothing wrote them, so `kubectl get mcpserver -o yaml`
// showed a Failed server with the raw error only. The status now mirrors the
// service's schedule, including the HTTP status of the failed attempt, and
// clears it once the server is back (issue #1163).
func TestApplyStatusFromServiceMirrorsRetrySchedule(t *testing.T) {
	lastAttempt := time.Date(2026, 9, 5, 15, 53, 4, 700_000_000, time.UTC)
	nextRetry := lastAttempt.Add(2 * time.Minute)

	registry := NewMockServiceRegistry()
	registry.AddService("behind-gateway", &MockServiceInfo{
		Name:        "behind-gateway",
		ServiceType: api.TypeMCPServer,
		State:       api.StateUnreachable,
		LastError:   errors.New("failed to initialize streamable-http MCP client: request failed with status 504: Gateway Timeout"),
		ServiceData: map[string]interface{}{
			api.ServiceDataConsecutiveFailures:   4,
			api.ServiceDataLastAttempt:           lastAttempt,
			api.ServiceDataNextRetryAfter:        nextRetry,
			api.ServiceDataLastFailureHTTPStatus: 504,
		},
	})
	r := &MCPServerReconciler{serviceRegistry: registry}

	server := &musterv1alpha1.MCPServer{Spec: musterv1alpha1.MCPServerSpec{Type: "streamable-http"}}
	r.applyStatusFromService(server, "behind-gateway", nil, nil)

	assert.Equal(t, musterv1alpha1.MCPServerStateFailed, server.Status.State)
	assert.Contains(t, server.Status.LastError, "status 504")
	assert.Equal(t, 4, server.Status.ConsecutiveFailures)
	assert.Equal(t, 504, server.Status.LastFailureHTTPStatus)
	require.NotNil(t, server.Status.LastAttempt)
	assert.Equal(t, lastAttempt.Truncate(time.Second), server.Status.LastAttempt.Time,
		"Kubernetes times have second granularity; truncate, never round into the future")
	require.NotNil(t, server.Status.NextRetryAfter)
	assert.Equal(t, nextRetry.Truncate(time.Second), server.Status.NextRetryAfter.Time)

	// The server is back: the schedule is gone from the service and must be
	// gone from the status, not left as a stale forecast.
	registry.AddService("behind-gateway", &MockServiceInfo{
		Name:        "behind-gateway",
		ServiceType: api.TypeMCPServer,
		State:       api.StateConnected,
		ServiceData: map[string]interface{}{
			api.ServiceDataConsecutiveFailures: 0,
			api.ServiceDataLastAttempt:         lastAttempt.Add(3 * time.Minute),
		},
	})
	r.applyStatusFromService(server, "behind-gateway", nil, nil)

	assert.Equal(t, musterv1alpha1.MCPServerStateConnected, server.Status.State)
	assert.Empty(t, server.Status.LastError)
	assert.Equal(t, 0, server.Status.ConsecutiveFailures)
	assert.Equal(t, 0, server.Status.LastFailureHTTPStatus)
	assert.Nil(t, server.Status.NextRetryAfter)
	require.NotNil(t, server.Status.LastAttempt, "the last attempt stays as a diagnostic")
}

// TestApplyRetryScheduleToleratesMissingData: a service that publishes no
// data (nil map, or a service type without a schedule) clears the fields
// rather than panicking or keeping old values.
func TestApplyRetryScheduleToleratesMissingData(t *testing.T) {
	stale := time.Now()
	status := &musterv1alpha1.MCPServerStatus{ConsecutiveFailures: 3, LastFailureHTTPStatus: 502}
	status.NextRetryAfter = metaTimeFromServiceData(map[string]interface{}{"t": stale}, "t")
	require.NotNil(t, status.NextRetryAfter)

	applyRetrySchedule(status, nil)

	assert.Equal(t, 0, status.ConsecutiveFailures)
	assert.Equal(t, 0, status.LastFailureHTTPStatus)
	assert.Nil(t, status.NextRetryAfter)
	assert.Nil(t, status.LastAttempt)
}
