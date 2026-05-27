package store

import (
	"context"
	"sync"
	"time"
)

// SessionAuthStore tracks per-session, per-server authentication state.
// Implementations must be safe for concurrent use.
type SessionAuthStore interface {
	IsAuthenticated(ctx context.Context, sessionID, serverName string) (bool, error)
	MarkAuthenticated(ctx context.Context, sessionID, serverName string) error
	Revoke(ctx context.Context, sessionID, serverName string) error
	RevokeSession(ctx context.Context, sessionID string) error
	RevokeServer(ctx context.Context, serverName string) error
	Touch(ctx context.Context, sessionID string) (bool, error)
}

// inMemoryAuthSession holds authenticated server names for a single session.
//
// Expiry uses two complementary mechanisms:
//   - expireAt: a soft deadline checked in IsAuthenticated (under RLock).
//   - timer: a hard cleanup that fires after the TTL to delete the session.
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
	s.resetSessionTTL(sessionID, sess)

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

	s.resetSessionTTL(sessionID, sess)

	return true, nil
}

// resetSessionTTL updates the soft expireAt deadline and restarts the hard
// cleanup timer. The caller must hold s.mu (write lock).
func (s *InMemorySessionAuthStore) resetSessionTTL(sessionID string, sess *inMemoryAuthSession) {
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
}

// Stop cleans up all timers. Call when the store is no longer needed.
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
