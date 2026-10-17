package aggregator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/giantswarm/muster/v5/internal/clock"
)

// reconnectWait bounds how long a tool call waits for a backend that lost its
// connection to come back before it is answered with a reconnectingError. It
// covers a backend that is back within seconds (a replaced pod, a dropped
// MCP session); a longer outage gets the retryable answer instead of a call
// that hangs.
var reconnectWait = 10 * time.Second

// reconnectTracker remembers the backends whose registry entry the
// state-change handler removed while their service is still alive: a backend
// whose health probes failed is Failed with a reconnect due, and its entry is
// gone until the reconnect registers it again. Without this memory a call in
// that window cannot tell the backend from one that never existed and is told
// "server not found". With it, the call waits for the re-registration
// (bounded by reconnectWait) or gets a retryable answer.
//
// The zero value is ready to use.
type reconnectTracker struct {
	mu      sync.Mutex
	servers map[string]*reconnectingServer
}

// reconnectingServer is one backend whose connection was lost. back is closed
// when the backend is registered again or is gone for good.
type reconnectingServer struct {
	since time.Time
	back  chan struct{}
}

// lost records that name's entry is removed while its service reconnects. A
// backend that is already tracked keeps its first loss time.
func (t *reconnectTracker) lost(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, tracked := t.servers[name]; tracked {
		return
	}
	if t.servers == nil {
		t.servers = make(map[string]*reconnectingServer)
	}
	t.servers[name] = &reconnectingServer{since: clock.Now(), back: make(chan struct{})}
}

// settled ends the tracking of name -- it is registered again, or it was
// stopped or removed -- and wakes the calls waiting for it. They re-read the
// registry to tell the two apart.
func (t *reconnectTracker) settled(name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if server, tracked := t.servers[name]; tracked {
		close(server.back)
		delete(t.servers, name)
	}
}

// lookup returns the tracking of name, nil if it is not reconnecting.
func (t *reconnectTracker) lookup(name string) *reconnectingServer {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.servers[name]
}

// reconnectingError answers a call to a backend whose connection was lost and
// is being re-established. It is retryable: the same call succeeds once the
// backend is registered again.
type reconnectingError struct {
	server string
	since  time.Duration
}

func (e *reconnectingError) Error() string {
	return fmt.Sprintf("server %s is reconnecting: its connection was lost %s ago and muster is re-establishing it; retry the call shortly",
		e.server, e.since.Round(time.Second))
}

// awaitServer resolves the registry entry of a server that is not registered
// at the moment of a call. A server that is reconnecting is waited for, up to
// reconnectWait or the call's own deadline; one that is not is not found.
// The registry is re-read after the lookup: a registration that lands between
// the dispatcher's read and the lookup has already settled the tracking.
func (a *AggregatorServer) awaitServer(ctx context.Context, name string) (*ServerInfo, error) {
	server := a.reconnects.lookup(name)
	if info, ok := a.registry.GetServerInfo(name); ok && info != nil {
		return info, nil
	}
	if server == nil {
		return nil, fmt.Errorf("server not found: %s", name)
	}

	timer := time.NewTimer(reconnectWait)
	defer timer.Stop()
	select {
	case <-server.back:
		if info, ok := a.registry.GetServerInfo(name); ok && info != nil {
			return info, nil
		}
		if a.reconnects.lookup(name) == nil {
			return nil, fmt.Errorf("server not found: %s", name)
		}
	case <-timer.C:
	case <-ctx.Done():
	}
	return nil, &reconnectingError{server: name, since: clock.Since(server.since)}
}
