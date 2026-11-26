package mcpserver

import (
	"sync"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
	"github.com/giantswarm/muster/v5/internal/clock"
)

// observedServers remembers the last definition of each server the adapter
// read from its definition source: the reconciler reads every server on each
// watch event and resync, and the approval gate reads it live on every call.
// While the API server cannot be reached nobody can change a definition, so
// the last one read is still the server's (api.MCPServerLastObserved).
type observedServers struct {
	mu    sync.RWMutex
	byKey map[string]observedServer
}

// observedServer is one remembered definition and when it was read.
type observedServer struct {
	info api.MCPServerInfo
	at   time.Time
}

// record remembers info as the server's definition, read now.
func (o *observedServers) record(key string, info api.MCPServerInfo) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.byKey == nil {
		o.byKey = make(map[string]observedServer)
	}
	o.byKey[key] = observedServer{info: info, at: clock.Now()}
}

// forget drops a server the definition source reported deleted.
func (o *observedServers) forget(key string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.byKey, key)
}

// get returns a copy of the server's last definition and when it was read.
func (o *observedServers) get(key string) (*api.MCPServerInfo, time.Time, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	observed, ok := o.byKey[key]
	if !ok {
		return nil, time.Time{}, false
	}
	info := observed.info
	return &info, observed.at, true
}

// LastObservedMCPServer implements api.MCPServerLastObserved.
func (a *Adapter) LastObservedMCPServer(key string) (*api.MCPServerInfo, time.Time, bool) {
	return a.observed.get(key)
}
