package mcpserver

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/api"
)

// countingEventManager is a minimal api.EventManagerHandler that records how
// many events were created per reason, used to measure the health-check event
// spam gate.
type countingEventManager struct {
	mu     sync.Mutex
	counts map[string]int
}

func (c *countingEventManager) CreateEventWithData(_ context.Context, _ api.ObjectReference, reason string, _ api.EventData) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[reason]++
	return nil
}

func (c *countingEventManager) DefaultNamespace() string { return "default" }

func (c *countingEventManager) QueryEvents(_ context.Context, _ api.EventQueryOptions) (*api.EventQueryResult, error) {
	return &api.EventQueryResult{}, nil
}

func (c *countingEventManager) WatchEvents(_ context.Context, _ api.EventQueryOptions) (<-chan api.EventResult, error) {
	ch := make(chan api.EventResult)
	close(ch)
	return ch, nil
}

func (c *countingEventManager) IsKubernetesMode() bool { return false }

func (c *countingEventManager) count(reason string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[reason]
}

// TestHealthCheckEventSpamGate proves that MCPServerHealthCheckFailed is emitted
// only on the healthy->unhealthy transition, not on every failing health check.
// This is the dominant event-volume source the gate exists to suppress.
func TestHealthCheckEventSpamGate(t *testing.T) {
	cm := &countingEventManager{counts: map[string]int{}}
	api.RegisterEventManager(cm)
	defer api.RegisterEventManager(nil)

	svc, err := NewService(&api.MCPServer{
		Name:    "spam-gate-test",
		Type:    api.MCPServerTypeStdio,
		Command: "echo",
	})
	require.NoError(t, err)

	const reason = "MCPServerHealthCheckFailed"

	// Ten consecutive failing health checks (as the 30s poll loop would produce
	// for a persistently-unhealthy server) emit exactly one event.
	for i := 0; i < 10; i++ {
		svc.emitHealthCheckFailedOnce("ping failed")
	}
	require.Equal(t, 1, cm.count(reason), "expected a single event across repeated failures")

	// A recovery clears the gate; the next failure transition emits again.
	svc.resetHealthCheckEventGate()
	for i := 0; i < 5; i++ {
		svc.emitHealthCheckFailedOnce("ping failed again")
	}
	require.Equal(t, 2, cm.count(reason), "expected one additional event after recovery+refailure")
}
