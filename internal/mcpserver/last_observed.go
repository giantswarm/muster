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
	// now is the clock; nil is clock.Now.
	now func() time.Time
}

func (o *observedServers) clockNow() time.Time {
	if o.now != nil {
		return o.now()
	}
	return clock.Now()
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
	o.byKey[key] = observedServer{info: info, at: o.clockNow()}
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

// replaceAll remembers infos as the definitions of all servers and forgets
// every other: a server a successful listing does not return is deleted or
// no longer served. startedAt is when the listing's read began: a definition
// recorded after it (a single read that finished meanwhile) is newer than the
// listing and is kept.
func (o *observedServers) replaceAll(infos []api.MCPServerInfo, startedAt time.Time) {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := o.clockNow()
	next := make(map[string]observedServer, len(infos))
	for key, observed := range o.byKey {
		if observed.at.After(startedAt) {
			next[key] = observed
		}
	}
	for _, info := range infos {
		key := info.ServerKey()
		if _, newer := next[key]; newer {
			continue
		}
		next[key] = observedServer{info: info, at: now}
	}
	o.byKey = next
}

// all returns copies of every remembered definition and the oldest read.
func (o *observedServers) all() ([]api.MCPServerInfo, time.Time, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	infos := make([]api.MCPServerInfo, 0, len(o.byKey))
	var oldest time.Time
	for _, observed := range o.byKey {
		infos = append(infos, observed.info)
		if oldest.IsZero() || observed.at.Before(oldest) {
			oldest = observed.at
		}
	}
	return infos, oldest, len(infos) > 0
}

// LastObservedMCPServers implements api.MCPServerLastObserved.
func (a *Adapter) LastObservedMCPServers() ([]api.MCPServerInfo, time.Time, bool) {
	return a.observed.all()
}

// LastObservedMCPServer implements api.MCPServerLastObserved.
func (a *Adapter) LastObservedMCPServer(key string) (*api.MCPServerInfo, time.Time, bool) {
	return a.observed.get(key)
}
