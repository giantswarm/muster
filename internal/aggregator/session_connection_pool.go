package aggregator

import (
	"sync"
	"time"

	"github.com/giantswarm/muster/pkg/logging"
)

// poolKey uniquely identifies a connection by session and server.
type poolKey struct {
	SessionID  string
	ServerName string
}

// poolEntry holds a live MCP client and metadata for a pooled connection.
type poolEntry struct {
	Client    MCPClient
	CreatedAt time.Time
}

// SessionConnectionPool maintains a per-(session, server) pool of live MCP
// clients. It is orthogonal to the CapabilityStore: the store caches what
// tools exist; the pool caches the live connection used to call them.
//
// Token rotation is handled transparently by the headerFunc / MusterTokenStore
// pattern — persistent clients resolve fresh tokens on every HTTP request.
//
// All methods are safe for concurrent use.
type SessionConnectionPool struct {
	mu   sync.RWMutex
	pool map[poolKey]*poolEntry
}

// NewSessionConnectionPool creates an empty connection pool.
func NewSessionConnectionPool() *SessionConnectionPool {
	return &SessionConnectionPool{
		pool: make(map[poolKey]*poolEntry),
	}
}

// Get returns the pooled client for the given session and server, or false
// if no entry exists.
func (p *SessionConnectionPool) Get(sessionID, serverName string) (MCPClient, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	key := poolKey{SessionID: sessionID, ServerName: serverName}
	entry, ok := p.pool[key]
	if !ok {
		return nil, false
	}
	return entry.Client, true
}

// Put stores a client in the pool, closing any previously pooled client for
// the same (session, server) key.
func (p *SessionConnectionPool) Put(sessionID, serverName string, client MCPClient) {
	key := poolKey{SessionID: sessionID, ServerName: serverName}

	p.mu.Lock()
	old, exists := p.pool[key]
	p.pool[key] = &poolEntry{
		Client:    client,
		CreatedAt: time.Now(),
	}
	p.mu.Unlock()

	if exists && old.Client != nil {
		closeQuietly(old.Client, sessionID, serverName, "replaced")
	}
}

// Evict removes and closes a single pooled entry.
func (p *SessionConnectionPool) Evict(sessionID, serverName string) {
	key := poolKey{SessionID: sessionID, ServerName: serverName}

	p.mu.Lock()
	entry, ok := p.pool[key]
	if ok {
		delete(p.pool, key)
	}
	p.mu.Unlock()

	if ok && entry.Client != nil {
		closeQuietly(entry.Client, sessionID, serverName, "evicted")
	}
}

// EvictSession removes and closes all pooled entries for the given session.
func (p *SessionConnectionPool) EvictSession(sessionID string) {
	p.mu.Lock()
	var evicted []poolEntry
	for key, entry := range p.pool {
		if key.SessionID == sessionID {
			evicted = append(evicted, *entry)
			delete(p.pool, key)
		}
	}
	p.mu.Unlock()

	for i := range evicted {
		if evicted[i].Client != nil {
			closeQuietly(evicted[i].Client, sessionID, "", "session-evict")
		}
	}
}

// EvictServer removes and closes all pooled entries for the given server
// across every session.
func (p *SessionConnectionPool) EvictServer(serverName string) {
	p.mu.Lock()
	var evicted []poolEntry
	for key, entry := range p.pool {
		if key.ServerName == serverName {
			evicted = append(evicted, *entry)
			delete(p.pool, key)
		}
	}
	p.mu.Unlock()

	for i := range evicted {
		if evicted[i].Client != nil {
			closeQuietly(evicted[i].Client, "", serverName, "server-evict")
		}
	}
}

// DrainAll closes and removes every entry in the pool. Intended for use
// during graceful shutdown.
func (p *SessionConnectionPool) DrainAll() {
	p.mu.Lock()
	entries := make([]poolEntry, 0, len(p.pool))
	for _, entry := range p.pool {
		entries = append(entries, *entry)
	}
	p.pool = make(map[poolKey]*poolEntry)
	p.mu.Unlock()

	for i := range entries {
		if entries[i].Client != nil {
			if err := entries[i].Client.Close(); err != nil {
				logging.Debug("ConnPool", "Error closing client during drain: %v", err)
			}
		}
	}

	logging.Debug("ConnPool", "Drained %d pooled connections", len(entries))
}

// Len returns the current number of pooled connections (useful for testing).
func (p *SessionConnectionPool) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.pool)
}

// closeQuietly closes a client and logs any errors at debug level.
func closeQuietly(client MCPClient, sessionID, serverName, reason string) {
	if err := client.Close(); err != nil {
		logging.Debug("ConnPool", "Error closing client (%s) session=%s server=%s: %v",
			reason, logging.TruncateIdentifier(sessionID), serverName, err)
	}
}
