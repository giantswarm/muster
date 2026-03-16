package aggregator

import (
	"context"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// Capabilities holds the MCP capabilities for a session+server pair.
type Capabilities struct {
	Tools     []mcp.Tool
	Resources []mcp.Resource
	Prompts   []mcp.Prompt
}

func (c *Capabilities) deepCopy() *Capabilities {
	if c == nil {
		return nil
	}
	return &Capabilities{
		Tools:     append([]mcp.Tool(nil), c.Tools...),
		Resources: append([]mcp.Resource(nil), c.Resources...),
		Prompts:   append([]mcp.Prompt(nil), c.Prompts...),
	}
}

// CapabilityStore is the interface for storing per-session, per-server MCP
// capabilities. Implementations must be safe for concurrent use.
type CapabilityStore interface {
	// Get returns the capabilities for a session+server pair.
	// Returns nil and no error on cache miss.
	Get(ctx context.Context, sessionID, serverName string) (*Capabilities, error)

	// GetAll returns all capabilities for a session, keyed by server name.
	GetAll(ctx context.Context, sessionID string) (map[string]*Capabilities, error)

	// Set stores capabilities for a session+server pair and resets the
	// session-level TTL.
	Set(ctx context.Context, sessionID, serverName string, caps *Capabilities) error

	// Delete removes all capabilities for a session (e.g., on full logout).
	Delete(ctx context.Context, sessionID string) error

	// DeleteEntry removes capabilities for a single session+server pair
	// (e.g., on per-server logout).
	DeleteEntry(ctx context.Context, sessionID, serverName string) error

	// DeleteServer removes capabilities for a server across all sessions
	// (e.g., on deregistration).
	DeleteServer(ctx context.Context, serverName string) error

	// Exists checks whether capabilities exist for a session+server pair.
	Exists(ctx context.Context, sessionID, serverName string) (bool, error)

	// Touch resets the session-level TTL without modifying stored capabilities.
	// This keeps the cache alive as long as the user is actively making requests.
	// Returns true if the session existed and was touched.
	Touch(ctx context.Context, sessionID string) (bool, error)

	// UpdateServer updates the capabilities for a given server across all
	// sessions that have a cached entry for it. Sessions without an entry
	// for the server are not affected. This is used for push-based tool
	// refresh where a notification indicates the server's capabilities
	// have changed and all sessions should see the updated tools.
	UpdateServer(ctx context.Context, serverName string, caps *Capabilities) error
}

// DefaultCapabilityStoreTTL is the session-level TTL for capability entries.
// Set to 30 days so that cached capabilities survive normal inactivity,
// weekends, and vacations. The cache is explicitly cleared on logout via
// the SessionRevocationHandler, so stale entries are not a concern.
const DefaultCapabilityStoreTTL = 30 * 24 * time.Hour

// --- In-memory implementation ---

// inMemorySession holds all server capabilities for a single session.
type inMemorySession struct {
	servers  map[string]*Capabilities
	timer    *time.Timer
	expireAt time.Time
}

// InMemoryCapabilityStore is a map-based CapabilityStore with per-session TTL
// timers. Suitable for single-pod dev/test deployments.
type InMemoryCapabilityStore struct {
	mu       sync.RWMutex
	sessions map[string]*inMemorySession
	ttl      time.Duration
}

// NewInMemoryCapabilityStore creates an in-memory store with the given session TTL.
func NewInMemoryCapabilityStore(ttl time.Duration) *InMemoryCapabilityStore {
	return &InMemoryCapabilityStore{
		sessions: make(map[string]*inMemorySession),
		ttl:      ttl,
	}
}

func (s *InMemoryCapabilityStore) Get(_ context.Context, sessionID, serverName string) (*Capabilities, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return nil, nil
	}
	if time.Now().After(sess.expireAt) {
		return nil, nil
	}
	caps, ok := sess.servers[serverName]
	if !ok {
		return nil, nil
	}
	return caps.deepCopy(), nil
}

func (s *InMemoryCapabilityStore) GetAll(_ context.Context, sessionID string) (map[string]*Capabilities, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return nil, nil
	}
	if time.Now().After(sess.expireAt) {
		return nil, nil
	}
	result := make(map[string]*Capabilities, len(sess.servers))
	for k, v := range sess.servers {
		result[k] = v.deepCopy()
	}
	return result, nil
}

func (s *InMemoryCapabilityStore) Set(_ context.Context, sessionID, serverName string, caps *Capabilities) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		sess = &inMemorySession{
			servers: make(map[string]*Capabilities),
		}
		s.sessions[sessionID] = sess
	}

	// Deep copy the capabilities to prevent mutation by callers.
	stored := &Capabilities{
		Tools:     append([]mcp.Tool(nil), caps.Tools...),
		Resources: append([]mcp.Resource(nil), caps.Resources...),
		Prompts:   append([]mcp.Prompt(nil), caps.Prompts...),
	}
	sess.servers[serverName] = stored

	// Reset session-level TTL.
	sess.expireAt = time.Now().Add(s.ttl)
	if sess.timer != nil {
		sess.timer.Stop()
	}
	sess.timer = time.AfterFunc(s.ttl, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		// Only delete if the session hasn't been renewed since the timer was set.
		if sess2, exists := s.sessions[sessionID]; exists && time.Now().After(sess2.expireAt) {
			if sess2.timer != nil {
				sess2.timer.Stop()
			}
			delete(s.sessions, sessionID)
		}
	})

	return nil
}

func (s *InMemoryCapabilityStore) Delete(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sess, ok := s.sessions[sessionID]; ok {
		if sess.timer != nil {
			sess.timer.Stop()
		}
		delete(s.sessions, sessionID)
	}
	return nil
}

func (s *InMemoryCapabilityStore) DeleteEntry(_ context.Context, sessionID, serverName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sess, ok := s.sessions[sessionID]; ok {
		delete(sess.servers, serverName)
	}
	return nil
}

func (s *InMemoryCapabilityStore) DeleteServer(_ context.Context, serverName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, sess := range s.sessions {
		delete(sess.servers, serverName)
	}
	return nil
}

func (s *InMemoryCapabilityStore) UpdateServer(_ context.Context, serverName string, caps *Capabilities) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	stored := caps.deepCopy()

	for _, sess := range s.sessions {
		if _, ok := sess.servers[serverName]; ok {
			sess.servers[serverName] = stored.deepCopy()
		}
	}
	return nil
}

func (s *InMemoryCapabilityStore) Exists(_ context.Context, sessionID, serverName string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return false, nil
	}
	if time.Now().After(sess.expireAt) {
		return false, nil
	}
	_, ok = sess.servers[serverName]
	return ok, nil
}

func (s *InMemoryCapabilityStore) Touch(_ context.Context, sessionID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return false, nil
	}
	if time.Now().After(sess.expireAt) {
		return false, nil
	}
	if len(sess.servers) == 0 {
		return false, nil
	}

	sess.expireAt = time.Now().Add(s.ttl)
	if sess.timer != nil {
		sess.timer.Stop()
	}
	sess.timer = time.AfterFunc(s.ttl, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if sess2, exists := s.sessions[sessionID]; exists && time.Now().After(sess2.expireAt) {
			if sess2.timer != nil {
				sess2.timer.Stop()
			}
			delete(s.sessions, sessionID)
		}
	})

	return true, nil
}

// Stop cleans up all timers. Should be called when the store is no longer needed.
func (s *InMemoryCapabilityStore) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, sess := range s.sessions {
		if sess.timer != nil {
			sess.timer.Stop()
		}
		delete(s.sessions, id)
	}
}
