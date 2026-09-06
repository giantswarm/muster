package reconciler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
	musterv1alpha1 "github.com/giantswarm/muster/pkg/apis/muster/v1alpha1"
)

// Regression tests for issue #1166: a spec.restartRequestedAt on an MCPServer
// whose endpoint is down made the reconciler restart the service on every
// state change of the failed start, because the request was recorded as
// processed only when the restart succeeded. The tests drive the reconciler
// the way the StateChangeBridge does -- one Reconcile per state transition --
// and count attempts on the orchestrator mock.

var errConnectionRefused = errors.New("failed to start MCP server: dial tcp 10.0.0.1:8080: connect: connection refused")

// restartRequestFixture wires a reconciler with a remote server whose
// definition carries a pending restart request and whose service is already
// registered (a failed remote server stays registered, in state failed).
type restartRequestFixture struct {
	mgr           *MockMCPServerManager
	orchAPI       *MockOrchestratorAPI
	registry      *MockServiceRegistry
	statusUpdater *MockStatusUpdater
	reconciler    *MCPServerReconciler
	server        *api.MCPServerInfo
	service       *MockServiceInfo
}

func newRestartRequestFixture(t *testing.T, requestedAt time.Time) *restartRequestFixture {
	t.Helper()
	f := &restartRequestFixture{
		mgr:           NewMockMCPServerManager(),
		orchAPI:       NewMockOrchestratorAPI(),
		registry:      NewMockServiceRegistry(),
		statusUpdater: NewMockStatusUpdater(),
	}
	f.reconciler = NewMCPServerReconciler(f.orchAPI, f.mgr, f.registry).
		WithStatusUpdater(f.statusUpdater, "default")
	f.server = &api.MCPServerInfo{
		Name:               "test-server",
		Type:               "streamable-http",
		URL:                "http://10.0.0.1:8080/mcp",
		AutoStart:          true,
		RestartRequestedAt: &requestedAt,
	}
	f.mgr.AddMCPServer(f.server)
	// The CR the status is written to: its spec.type decides whether the
	// status reads Connected/Failed (remote) or Running/Failed (stdio).
	cr := &musterv1alpha1.MCPServer{Spec: musterv1alpha1.MCPServerSpec{Type: "streamable-http"}}
	cr.Name = "test-server"
	cr.Namespace = "default"
	f.statusUpdater.AddMCPServer(cr)
	f.service = &MockServiceInfo{
		Name:        "test-server",
		ServiceType: api.TypeMCPServer,
		State:       api.StateFailed,
		Health:      api.HealthUnhealthy,
		LastError:   errConnectionRefused,
	}
	f.registry.AddService("test-server", f.service)
	return f
}

// reconcile runs one pass and then mirrors the written status back into the
// definition the way convertCRDToInfo reports it on the next read, so the
// following pass sees what the reconciler recorded.
func (f *restartRequestFixture) reconcile(t *testing.T) ReconcileResult {
	t.Helper()
	result := f.reconciler.Reconcile(context.Background(), lifecycleReconcileRequest())
	if updated := f.statusUpdater.GetLastUpdatedMCPServer(); updated != nil && updated.Status.LastRestartedAt != nil {
		mirrored := updated.Status.LastRestartedAt.Time
		f.server.LastRestartedAt = &mirrored
	}
	return result
}

func (f *restartRequestFixture) lastRestartedAt(t *testing.T) time.Time {
	t.Helper()
	updated := f.statusUpdater.GetLastUpdatedMCPServer()
	require.NotNil(t, updated, "status must have been synced")
	require.NotNil(t, updated.Status.LastRestartedAt, "status.lastRestartedAt must be recorded")
	return updated.Status.LastRestartedAt.Time
}

// TestMCPServerReconciler_FailedRestartRequestIsProcessedOnce: the restart
// fails (endpoint down). The request is recorded in status.lastRestartedAt all
// the same and the reconcile passes the failed start's state changes trigger
// make no further attempt. The failure is still reported once in the result.
func TestMCPServerReconciler_FailedRestartRequestIsProcessedOnce(t *testing.T) {
	requestedAt := time.Date(2026, 9, 5, 18, 33, 46, 0, time.UTC)
	f := newRestartRequestFixture(t, requestedAt)
	f.orchAPI.RestartError = errConnectionRefused

	result := f.reconcile(t)

	require.Error(t, result.Error, "the failed restart is reported once")
	assert.True(t, result.Requeue)
	assert.Equal(t, 1, f.orchAPI.RestartCalls["test-server"])
	assert.True(t, f.lastRestartedAt(t).Equal(requestedAt),
		"a failed attempt processes the request: status.lastRestartedAt mirrors it")

	// The StateChangeBridge queues a reconcile for starting -> failed and the
	// queue retries the failed result; on gazelle these passes restarted the
	// service 131 times in 53 s. None of them may attempt again.
	for pass := 0; pass < 5; pass++ {
		result = f.reconcile(t)
		require.NoError(t, result.Error, "pass %d: nothing left to do once the request is processed", pass)
	}
	assert.Equal(t, 1, f.orchAPI.RestartCalls["test-server"], "exactly one attempt for one request")
}

// TestMCPServerReconciler_NewerRestartRequestAfterFailureRestartsAgain: a
// later spec.restartRequestedAt is a new request and gets its own attempt,
// however the previous one ended.
func TestMCPServerReconciler_NewerRestartRequestAfterFailureRestartsAgain(t *testing.T) {
	first := time.Date(2026, 9, 5, 18, 33, 46, 0, time.UTC)
	f := newRestartRequestFixture(t, first)
	f.orchAPI.RestartError = errConnectionRefused

	require.Error(t, f.reconcile(t).Error)
	require.NoError(t, f.reconcile(t).Error)
	require.Equal(t, 1, f.orchAPI.RestartCalls["test-server"])

	second := first.Add(6 * time.Minute)
	f.server.RestartRequestedAt = &second

	result := f.reconcile(t)

	require.Error(t, result.Error, "the second attempt fails too and is reported")
	assert.Equal(t, 2, f.orchAPI.RestartCalls["test-server"], "a newer request is attempted once more")
	assert.True(t, f.lastRestartedAt(t).Equal(second), "and is mirrored into status")

	require.NoError(t, f.reconcile(t).Error)
	assert.Equal(t, 2, f.orchAPI.RestartCalls["test-server"])
}

// TestMCPServerReconciler_FailedRestartLeavesRetriesToOrchestrator: after the
// consumed attempt the service's own schedule is what the status shows and
// what drives the next attempts. The reconciler mirrors nextRetryAfter, makes
// no attempt of its own while the schedule is pending, and clears the schedule
// from the status once the orchestrator's retry connected.
func TestMCPServerReconciler_FailedRestartLeavesRetriesToOrchestrator(t *testing.T) {
	requestedAt := time.Date(2026, 9, 5, 18, 33, 46, 0, time.UTC)
	f := newRestartRequestFixture(t, requestedAt)
	f.orchAPI.RestartError = errConnectionRefused

	// What the failed Start left behind on the service (issue #1163 fields).
	lastAttempt := requestedAt.Add(300 * time.Millisecond)
	nextRetry := lastAttempt.Add(30 * time.Second)
	f.service.ServiceData = map[string]interface{}{
		api.ServiceDataConsecutiveFailures: 1,
		api.ServiceDataLastAttempt:         lastAttempt,
		api.ServiceDataNextRetryAfter:      nextRetry,
	}

	require.Error(t, f.reconcile(t).Error)

	status := f.statusUpdater.GetLastUpdatedMCPServer().Status
	assert.Equal(t, 1, status.ConsecutiveFailures, "one failure: the reconciler's single attempt")
	require.NotNil(t, status.NextRetryAfter)
	assert.Equal(t, nextRetry.Truncate(time.Second), status.NextRetryAfter.Time, "the orchestrator's schedule is what the status announces")
	assert.Contains(t, status.LastError, "connection refused")

	// The orchestrator fails again on its schedule (attempt 2, 60 s out).
	// Its state changes reconcile; the reconciler must not add an attempt.
	f.service.ServiceData[api.ServiceDataConsecutiveFailures] = 2
	f.service.ServiceData[api.ServiceDataNextRetryAfter] = nextRetry.Add(60 * time.Second)
	require.NoError(t, f.reconcile(t).Error)
	status = f.statusUpdater.GetLastUpdatedMCPServer().Status
	assert.Equal(t, 2, status.ConsecutiveFailures)
	assert.Equal(t, nextRetry.Add(60*time.Second).Truncate(time.Second), status.NextRetryAfter.Time)
	assert.Equal(t, 1, f.orchAPI.RestartCalls["test-server"], "the second attempt was the orchestrator's, not the reconciler's")

	// The orchestrator's next retry connects: schedule gone, request still processed.
	f.service.mu.Lock()
	f.service.State = api.StateConnected
	f.service.Health = api.HealthHealthy
	f.service.LastError = nil
	f.service.ServiceData = map[string]interface{}{api.ServiceDataConsecutiveFailures: 0}
	f.service.mu.Unlock()
	require.NoError(t, f.reconcile(t).Error)
	status = f.statusUpdater.GetLastUpdatedMCPServer().Status
	assert.Equal(t, musterv1alpha1.MCPServerStateConnected, status.State)
	assert.Nil(t, status.NextRetryAfter)
	assert.Equal(t, 0, status.ConsecutiveFailures)
	assert.True(t, f.lastRestartedAt(t).Equal(requestedAt))
	assert.Equal(t, 1, f.orchAPI.RestartCalls["test-server"])
}

// TestMCPServerReconciler_RestartRequestFailedStartOfUnregisteredServiceIsProcessed:
// the "start an unregistered server" case (autoStart=false, never started).
// The orchestrator registers the definition before starting it, so a failed
// start leaves a registered, failed service with its own reconnect schedule.
// The request is processed by that one attempt like any other.
func TestMCPServerReconciler_RestartRequestFailedStartOfUnregisteredServiceIsProcessed(t *testing.T) {
	requestedAt := time.Date(2026, 9, 5, 18, 33, 46, 0, time.UTC)
	f := newRestartRequestFixture(t, requestedAt)
	f.server.AutoStart = false
	f.registry.RemoveService("test-server")
	f.orchAPI.StartError = errConnectionRefused
	f.orchAPI.OnStart = func(name string) {
		f.registry.AddService(name, f.service) // lazy registration, then the start fails
	}

	result := f.reconcile(t)

	require.Error(t, result.Error)
	assert.Equal(t, 1, f.orchAPI.StartCalls["test-server"])
	assert.True(t, f.lastRestartedAt(t).Equal(requestedAt), "processed by the one failed attempt")

	for pass := 0; pass < 3; pass++ {
		require.NoError(t, f.reconcile(t).Error)
	}
	assert.Equal(t, 1, f.orchAPI.StartCalls["test-server"], "the failed service's retries are the orchestrator's")
	assert.Equal(t, 0, f.orchAPI.RestartCalls["test-server"])
}

// TestMCPServerReconciler_RestartRequestStaysPendingWhenNothingWasRegistered:
// when the start fails before any service exists (the definition could not be
// registered), there is no reconnect schedule to fall back on and no state
// change that could re-trigger the pass. The request stays pending and the
// error requeues it on the reconcile queue's own backoff -- the only place a
// retry-until-success may come from.
func TestMCPServerReconciler_RestartRequestStaysPendingWhenNothingWasRegistered(t *testing.T) {
	requestedAt := time.Date(2026, 9, 5, 18, 33, 46, 0, time.UTC)
	f := newRestartRequestFixture(t, requestedAt)
	f.server.AutoStart = false
	f.registry.RemoveService("test-server")
	f.orchAPI.StartError = errors.New("failed to register MCP server: definition invalid")

	result := f.reconcile(t)

	require.Error(t, result.Error)
	assert.True(t, result.Requeue, "retried on the queue's backoff")
	updated := f.statusUpdater.GetLastUpdatedMCPServer()
	require.NotNil(t, updated)
	assert.Nil(t, updated.Status.LastRestartedAt, "not processed: nothing was attempted against a service")

	// The queue's retry attempts again -- and only the queue's retry does.
	require.Error(t, f.reconcile(t).Error)
	assert.Equal(t, 2, f.orchAPI.StartCalls["test-server"])
}

// TestMCPServerReconciler_FailedCreateAttemptConsumesRestartRequest: a fresh
// autoStart=true definition that already carries a restart request. The
// create's start fails; that attempt processes the request instead of leaving
// it for a second attempt on the next state change.
func TestMCPServerReconciler_FailedCreateAttemptConsumesRestartRequest(t *testing.T) {
	requestedAt := time.Date(2026, 9, 5, 18, 33, 46, 0, time.UTC)
	f := newRestartRequestFixture(t, requestedAt)
	f.registry.RemoveService("test-server")
	f.orchAPI.StartError = errConnectionRefused
	f.orchAPI.OnStart = func(name string) {
		f.registry.AddService(name, f.service)
	}

	result := f.reconcile(t)

	require.Error(t, result.Error)
	assert.Equal(t, 1, f.orchAPI.StartCalls["test-server"])
	assert.True(t, f.lastRestartedAt(t).Equal(requestedAt), "the create's attempt processed the request")

	for pass := 0; pass < 3; pass++ {
		require.NoError(t, f.reconcile(t).Error)
	}
	assert.Equal(t, 1, f.orchAPI.StartCalls["test-server"])
	assert.Equal(t, 0, f.orchAPI.RestartCalls["test-server"], "no restart on top of the failed start")
}

// TestMCPServerReconciler_FailedResumeAttemptIsNotRepeated: the same loop in
// its other form. A spec.suspended=false whose start fails used to keep the
// suspension marker, so every state change of the failed start resumed the
// service again. The marker is cleared by the attempt; the failed service's
// retries are the orchestrator's.
func TestMCPServerReconciler_FailedResumeAttemptIsNotRepeated(t *testing.T) {
	f := newRestartRequestFixture(t, time.Date(2026, 9, 5, 18, 33, 46, 0, time.UTC))
	f.server.RestartRequestedAt = nil
	f.server.Suspended = true
	f.service.mu.Lock()
	f.service.State = api.StateConnected
	f.service.LastError = nil
	f.service.mu.Unlock()

	// Suspend: the service is stopped and remembered as held down.
	require.NoError(t, f.reconcile(t).Error)
	require.True(t, f.orchAPI.StoppedServices["test-server"])
	f.service.mu.Lock()
	f.service.State = api.StateStopped
	f.service.mu.Unlock()

	// Resume while the endpoint is down: one failed start.
	f.server.Suspended = false
	f.orchAPI.StartError = errConnectionRefused
	result := f.reconcile(t)
	require.Error(t, result.Error)
	assert.Equal(t, 1, f.orchAPI.StartCalls["test-server"])
	assert.False(t, f.reconciler.isSuspended("test-server"), "the attempt clears the marker")

	// starting -> failed state changes reconcile; no second resume.
	f.service.mu.Lock()
	f.service.State = api.StateFailed
	f.service.LastError = errConnectionRefused
	f.service.mu.Unlock()
	for pass := 0; pass < 3; pass++ {
		require.NoError(t, f.reconcile(t).Error)
	}
	assert.Equal(t, 1, f.orchAPI.StartCalls["test-server"], "the failed service's retries are the orchestrator's")
}

// TestMCPServerReconciler_FailedResumeBeforeRegistrationKeepsMarker: a resume
// whose start failed before any service was registered keeps the marker, so
// the queue's requeue retries the resume itself -- there is no service whose
// schedule could.
func TestMCPServerReconciler_FailedResumeBeforeRegistrationKeepsMarker(t *testing.T) {
	f := newRestartRequestFixture(t, time.Date(2026, 9, 5, 18, 33, 46, 0, time.UTC))
	f.server.RestartRequestedAt = nil
	f.server.AutoStart = false
	f.server.Suspended = true
	f.registry.RemoveService("test-server")

	require.NoError(t, f.reconcile(t).Error)
	require.True(t, f.reconciler.isSuspended("test-server"))

	f.server.Suspended = false
	f.orchAPI.StartError = errors.New("failed to register MCP server: definition invalid")
	result := f.reconcile(t)
	require.Error(t, result.Error)
	assert.True(t, result.Requeue)
	assert.True(t, f.reconciler.isSuspended("test-server"), "nothing was attempted against a service: the resume is still owed")

	require.Error(t, f.reconcile(t).Error)
	assert.Equal(t, 2, f.orchAPI.StartCalls["test-server"], "retried by the queue, not by state changes (none can happen)")
}
