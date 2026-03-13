package aggregator

import (
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// CacheKey identifies a cached capability set by session and server name.
// Keyed by session ID (token family ID) for per-login-session isolation.
type CacheKey struct {
	SessionID  string // Token family ID from mcp-oauth (per-login session)
	ServerName string // MCP server name
}

// CacheEntry holds the cached MCP capabilities for a session+server pair.
// Entries are immutable once stored; Set replaces the entire entry.
type CacheEntry struct {
	Tools     []mcp.Tool
	Resources []mcp.Resource
	Prompts   []mcp.Prompt
	StoredAt  time.Time
	ExpiresAt time.Time
	// graceDeadline is the point after which the entry is eligible for eviction.
	// Set to StoredAt + 2*TTL.
	graceDeadline time.Time
}

// IsExpired returns true if the entry has passed its TTL.
func (e *CacheEntry) IsExpired() bool {
	return time.Now().After(e.ExpiresAt)
}

// IsStale returns true if the entry is expired but still within the grace period
// (usable for stale-while-revalidate). Returns false if the entry is fresh or
// has passed the grace period.
func (e *CacheEntry) IsStale() bool {
	now := time.Now()
	return now.After(e.ExpiresAt) && !now.After(e.graceDeadline)
}

// CapabilityCache stores per-session, per-server MCP capabilities independently
// of connection state. It supports TTL-based expiry with stale-while-revalidate
// semantics: expired entries are still returned by Get so callers can serve
// stale data while triggering a background refresh.
//
// A background goroutine periodically evicts entries that have passed the grace
// period (2x TTL). Callers must call Stop() to prevent goroutine leaks.
type CapabilityCache struct {
	mu         sync.RWMutex
	entries    map[CacheKey]*CacheEntry
	defaultTTL time.Duration
	stopCh     chan struct{}
	stopped    chan struct{}
	stopOnce   sync.Once
}

// NewCapabilityCache creates a cache with the given default TTL and starts a
// background cleanup goroutine that runs every defaultTTL/2. Callers must call
// Stop() when the cache is no longer needed.
func NewCapabilityCache(defaultTTL time.Duration) *CapabilityCache {
	c := &CapabilityCache{
		entries:    make(map[CacheKey]*CacheEntry),
		defaultTTL: defaultTTL,
		stopCh:     make(chan struct{}),
		stopped:    make(chan struct{}),
	}

	cleanupInterval := defaultTTL / 2
	if cleanupInterval <= 0 {
		cleanupInterval = time.Second
	}

	go c.cleanupLoop(cleanupInterval)

	return c
}

// Get returns the cached capabilities for a session+server pair. It returns the
// entry and true if found (even if expired, supporting stale-while-revalidate).
// The caller should check entry.IsExpired() to decide whether to trigger a
// background refresh. Returns nil and false only if no entry exists.
func (c *CapabilityCache) Get(sessionID, serverName string) (*CacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[CacheKey{SessionID: sessionID, ServerName: serverName}]
	return entry, ok
}

// Set stores capabilities for a session+server pair with the default TTL.
func (c *CapabilityCache) Set(sessionID, serverName string, tools []mcp.Tool, resources []mcp.Resource, prompts []mcp.Prompt) {
	c.SetWithTTL(sessionID, serverName, tools, resources, prompts, c.defaultTTL)
}

// SetWithTTL stores capabilities for a session+server pair with a custom TTL.
func (c *CapabilityCache) SetWithTTL(sessionID, serverName string, tools []mcp.Tool, resources []mcp.Resource, prompts []mcp.Prompt, ttl time.Duration) {
	now := time.Now()
	entry := &CacheEntry{
		Tools:         append([]mcp.Tool(nil), tools...),
		Resources:     append([]mcp.Resource(nil), resources...),
		Prompts:       append([]mcp.Prompt(nil), prompts...),
		StoredAt:      now,
		ExpiresAt:     now.Add(ttl),
		graceDeadline: now.Add(2 * ttl),
	}

	c.mu.Lock()
	c.entries[CacheKey{SessionID: sessionID, ServerName: serverName}] = entry
	c.mu.Unlock()
}

// InvalidateSession removes all cached entries for a session (e.g., on logout
// via token family revocation).
func (c *CapabilityCache) InvalidateSession(sessionID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key := range c.entries {
		if key.SessionID == sessionID {
			delete(c.entries, key)
		}
	}
}

// InvalidateServer removes all cached entries for a server (e.g., on deregistration).
func (c *CapabilityCache) InvalidateServer(serverName string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key := range c.entries {
		if key.ServerName == serverName {
			delete(c.entries, key)
		}
	}
}

// Invalidate removes the cached entry for a specific session+server pair.
func (c *CapabilityCache) Invalidate(sessionID, serverName string) {
	c.mu.Lock()
	delete(c.entries, CacheKey{SessionID: sessionID, ServerName: serverName})
	c.mu.Unlock()
}

// Stop stops the background cleanup goroutine. It is safe to call multiple
// times but only the first call has an effect.
func (c *CapabilityCache) Stop() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
	<-c.stopped
}

// cleanupLoop periodically evicts entries that have passed their grace deadline.
func (c *CapabilityCache) cleanupLoop(interval time.Duration) {
	defer close(c.stopped)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.evictExpired()
		}
	}
}

// evictExpired removes entries that have passed the grace period (2x TTL).
func (c *CapabilityCache) evictExpired() {
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	for key, entry := range c.entries {
		if now.After(entry.graceDeadline) {
			delete(c.entries, key)
		}
	}
}
