package oauth

import (
	"context"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/giantswarm/muster/internal/api"
)

// inMemorySession holds all server capabilities for a single session.
type inMemorySession struct {
	servers  map[string]*api.Capabilities
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

func (s *InMemoryCapabilityStore) Get(_ context.Context, sessionID, serverName string) (*api.Capabilities, error) {
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
	return caps.DeepCopy(), nil
}

func (s *InMemoryCapabilityStore) GetAll(_ context.Context, sessionID string) (map[string]*api.Capabilities, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		return nil, nil
	}
	if time.Now().After(sess.expireAt) {
		return nil, nil
	}
	result := make(map[string]*api.Capabilities, len(sess.servers))
	for k, v := range sess.servers {
		result[k] = v.DeepCopy()
	}
	return result, nil
}

func (s *InMemoryCapabilityStore) Set(_ context.Context, sessionID, serverName string, caps *api.Capabilities) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sess, ok := s.sessions[sessionID]
	if !ok {
		sess = &inMemorySession{
			servers: make(map[string]*api.Capabilities),
		}
		s.sessions[sessionID] = sess
	}

	// Deep copy to prevent mutation by callers.
	stored := &api.Capabilities{
		Tools:     append([]mcp.Tool(nil), caps.Tools...),
		Resources: append([]mcp.Resource(nil), caps.Resources...),
		Prompts:   append([]mcp.Prompt(nil), caps.Prompts...),
	}
	sess.servers[serverName] = stored

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

func (s *InMemoryCapabilityStore) ListSessions(_ context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	out := make([]string, 0, len(s.sessions))
	for id, sess := range s.sessions {
		if now.After(sess.expireAt) {
			continue
		}
		out = append(out, id)
	}
	return out, nil
}

// Stop cleans up all timers. Call when the store is no longer needed.
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
