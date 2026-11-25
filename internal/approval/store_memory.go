package approval

import (
	"context"
	"sync"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
)

// MemoryStore keeps approvals in the process. It is local to one replica,
// so muster never registers it as its approval store (approval needs the
// shared store on Valkey); it serves tests that exercise the round trip
// without a Valkey.
type MemoryStore struct {
	mu        sync.Mutex
	requests  map[string]*api.ApprovalRequest
	bindings  map[string]string
	decisions map[string]decision
	flows     map[string]memoryFlow
	now       func() time.Time
}

type memoryFlow struct {
	flow    api.ApprovalPageFlow
	expires time.Time
}

var _ api.ApprovalStore = (*MemoryStore)(nil)

// NewMemoryStore returns an empty in-process store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		requests:  map[string]*api.ApprovalRequest{},
		bindings:  map[string]string{},
		decisions: map[string]decision{},
		flows:     map[string]memoryFlow{},
		now:       time.Now,
	}
}

// SetClock replaces the store's clock, for tests of expiry.
func (s *MemoryStore) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.now = now
}

// expire drops expired requests; s.mu is held.
func (s *MemoryStore) expire() {
	now := s.now()
	for id, r := range s.requests {
		if !now.Before(r.ExpiresAt) {
			delete(s.requests, id)
			delete(s.bindings, r.BindingKey())
			delete(s.decisions, id)
		}
	}
	for key, f := range s.flows {
		if !now.Before(f.expires) {
			delete(s.flows, key)
		}
	}
}

// view returns a copy of a request with its status; s.mu is held.
func (s *MemoryStore) view(id string) (*api.ApprovalRequest, bool) {
	r, ok := s.requests[id]
	if !ok {
		return nil, false
	}
	out := *r
	out.Status = api.ApprovalStatusPending
	if d, ok := s.decisions[id]; ok {
		out.Status = api.ApprovalStatusDeclined
		if d.Approved {
			out.Status = api.ApprovalStatusApproved
		}
		out.DecidedBy = d.By
	}
	return &out, true
}

// Create implements api.ApprovalStore.
func (s *MemoryStore) Create(_ context.Context, request *api.ApprovalRequest) (*api.ApprovalRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	if id, ok := s.bindings[request.BindingKey()]; ok {
		if existing, ok := s.view(id); ok {
			return existing, nil
		}
	}
	if !s.now().Before(request.ExpiresAt) {
		return nil, api.ErrApprovalNotFound
	}
	person, session := 0, 0
	for id, r := range s.requests {
		if _, decided := s.decisions[id]; decided {
			continue // a decided request no longer waits
		}
		if r.Identity == request.Identity {
			person++
		}
		if r.Identity == request.Identity && r.SessionID == request.SessionID {
			session++
		}
	}
	if person >= MaxPendingPerPerson {
		return nil, &api.ApprovalLimitError{Scope: api.ApprovalLimitPerson, Limit: MaxPendingPerPerson}
	}
	if session >= MaxPendingPerSession {
		return nil, &api.ApprovalLimitError{Scope: api.ApprovalLimitSession, Limit: MaxPendingPerSession}
	}
	stored := *request
	s.requests[request.ID] = &stored
	s.bindings[request.BindingKey()] = request.ID
	created, _ := s.view(request.ID)
	return created, nil
}

// Find implements api.ApprovalStore.
func (s *MemoryStore) Find(_ context.Context, bindingKey string) (*api.ApprovalRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	id, ok := s.bindings[bindingKey]
	if !ok {
		return nil, api.ErrApprovalNotFound
	}
	r, ok := s.view(id)
	if !ok {
		return nil, api.ErrApprovalNotFound
	}
	return r, nil
}

// Consume implements api.ApprovalStore.
func (s *MemoryStore) Consume(_ context.Context, bindingKey string) (*api.ApprovalRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	id, ok := s.bindings[bindingKey]
	if !ok {
		return nil, api.ErrApprovalNotFound
	}
	r, ok := s.view(id)
	if !ok {
		return nil, api.ErrApprovalNotFound
	}
	if r.Status == api.ApprovalStatusPending {
		return r, nil
	}
	delete(s.requests, id)
	delete(s.bindings, bindingKey)
	delete(s.decisions, id)
	return r, nil
}

// Get implements api.ApprovalStore.
func (s *MemoryStore) Get(_ context.Context, id string) (*api.ApprovalRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	r, ok := s.view(id)
	if !ok {
		return nil, api.ErrApprovalNotFound
	}
	return r, nil
}

// Decide implements api.ApprovalStore.
func (s *MemoryStore) Decide(_ context.Context, id string, approve bool, by api.SignInIdentity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	if _, ok := s.requests[id]; !ok {
		return api.ErrApprovalNotFound
	}
	if _, ok := s.decisions[id]; ok {
		return api.ErrApprovalDecided
	}
	s.decisions[id] = decision{Approved: approve, By: by}
	return nil
}

// PutFlow implements api.ApprovalStore.
func (s *MemoryStore) PutFlow(_ context.Context, key string, flow api.ApprovalPageFlow, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flows[key] = memoryFlow{flow: flow, expires: s.now().Add(ttl)}
	return nil
}

// GetFlow implements api.ApprovalStore.
func (s *MemoryStore) GetFlow(_ context.Context, key string) (*api.ApprovalPageFlow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire()
	f, ok := s.flows[key]
	if !ok {
		return nil, api.ErrApprovalNotFound
	}
	flow := f.flow
	return &flow, nil
}

// DeleteFlow implements api.ApprovalStore.
func (s *MemoryStore) DeleteFlow(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.flows, key)
	return nil
}
