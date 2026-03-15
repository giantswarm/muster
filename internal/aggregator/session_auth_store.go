package aggregator

import (
	"context"
	"sync"
	"time"
)

// SessionAuthStore is the interface for tracking per-session, per-server
// authentication state. It answers the single question: "may this session
// call tools on this server?" Implementations must be safe for concurrent use.
//
// This is deliberately separate from CapabilityStore so that auth state and
// cached capabilities can evolve independently -- clearing stale capabilities
// (e.g. on tool-change detection or server health transitions) does not
// accidentally revoke a user's authentication.
//
// Two implementations are provided:
//   - InMemorySessionAuthStore: map-based with per-session TTL timers (dev/test)
//   - ValkeySessionAuthStore: hash-per-session with EXPIRE (production, cross-replica)
type SessionAuthStore interface {
	// IsAuthenticated returns true if the session has authenticated to the server.
	IsAuthenticated(ctx context.Context, sessionID, serverName string) (bool, error)

	// MarkAuthenticated records successful authentication for a session+server pair
	// and resets the session-level TTL.
	MarkAuthenticated(ctx context.Context, sessionID, serverName string) error

	// Revoke removes auth state for a single session+server pair (per-server logout).
	Revoke(ctx context.Context, sessionID, serverName string) error

	// RevokeSession removes all auth state for a session (full logout / token revocation).
	RevokeSession(ctx context.Context, sessionID string) error

	// RevokeServer removes auth state for a server across all sessions (deregistration).
	RevokeServer(ctx context.Context, serverName string) error

	// Touch extends the session-level TTL without modifying auth state.
	// Returns true if the session existed (with at least one authenticated server)
	// and was touched.
	Touch(ctx context.Context, sessionID string) (bool, error)
}

// --- In-memory implementation ---

// inMemoryAuthSession holds authenticated server names for a single session.
type inMemoryAuthSession struct {
	servers  map[string]bool
	timer    *time.Timer
	expireAt time.Time
}

// InMemorySessionAuthStore is a map-based SessionAuthStore with per-session TTL
// timers. Suitable for single-pod dev/test deployments.
type InMemorySessionAuthStore struct {
	mu       sync.RWMutex
	sessions map[string]*inMemoryAuthSession
	ttl      time.Duration
}

// NewInMemorySessionAuthStore creates an in-memory auth store with the given session TTL.
func NewInMemorySessionAuthStore(ttl time.Duration) *InMemorySessionAuthStore {
	return &InMemorySessionAuthStore{
		sessions: make(map[string]*inMemoryAuthSession),
		ttl:      ttl,
	}
}

func (s *InMemorySessionAuthStore) IsAuthenticated(_ context.Context, sessionID, serverName string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return false, nil
	}
	if time.Now().After(sess.expireAt) {
		return false, nil
	}
	return sess.servers[serverName], nil
}

func (s *InMemorySessionAuthStore) MarkAuthenticated(_ context.Context, sessionID, serverName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		sess = &inMemoryAuthSession{
			servers: make(map[string]bool),
		}
		s.sessions[sessionID] = sess
	}

	sess.servers[serverName] = true

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

	return nil
}

func (s *InMemorySessionAuthStore) Revoke(_ context.Context, sessionID, serverName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sess, ok := s.sessions[sessionID]; ok {
		delete(sess.servers, serverName)
	}
	return nil
}

func (s *InMemorySessionAuthStore) RevokeSession(_ context.Context, sessionID string) error {
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

func (s *InMemorySessionAuthStore) RevokeServer(_ context.Context, serverName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, sess := range s.sessions {
		delete(sess.servers, serverName)
	}
	return nil
}

func (s *InMemorySessionAuthStore) Touch(_ context.Context, sessionID string) (bool, error) {
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
func (s *InMemorySessionAuthStore) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for id, sess := range s.sessions {
		if sess.timer != nil {
			sess.timer.Stop()
		}
		delete(s.sessions, id)
	}
}
