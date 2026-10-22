package credprovider

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"
)

var (
	// ErrBoundToAnother is returned when a bind or unbind names an actor that
	// is bound to a different person.
	ErrBoundToAnother = errors.New("the actor is bound to another person")
	// ErrActorUIDMismatch is returned when an actor's uid differs from the
	// uid its binding is pinned to: the actor was recreated under its name.
	ErrActorUIDMismatch = errors.New("the actor's uid differs from the uid its binding is pinned to")
)

// Binding ties a Session's sandbox actor to the person who started it and the
// providers whose grants the Session may use.
type Binding struct {
	// Subject is the person's identity provider subject, the key their
	// grants are filed under.
	Subject string `json:"sub"`
	// Session names the Session, for the audit log.
	Session string `json:"session"`
	// Providers are the provider instances (credential URI first segment)
	// the Session may use.
	Providers []string `json:"providers"`
	// Toolset is the agent's toolset at the Session's start: the inline
	// selectors that bound every request the actor makes to muster. nil binds
	// none (implicit full access); a binding stored before toolsets were bound
	// reads as nil.
	Toolset []string `json:"toolset,omitempty"`
	// ActorUID is the Substrate uid of the actor the binding is pinned to:
	// the uid of the first actor JWT accepted under it. An actor recreated
	// under the same name has another uid and is refused. Empty until then;
	// a re-bind by the same person keeps it.
	ActorUID string `json:"actorUID,omitempty"`
	// BoundAt is when the binding was last written.
	BoundAt time.Time `json:"boundAt"`
}

// Allows reports whether the binding lists the provider.
func (b *Binding) Allows(provider string) bool {
	return slices.Contains(b.Providers, provider)
}

// Store keeps bindings keyed by actor SPIFFE id, each expiring ttl after its
// last Put or Touch.
type Store interface {
	// Put writes the binding for the actor unless the actor is bound to
	// another subject (ErrBoundToAnother). The current binding's ActorUID
	// carries over when b has none.
	Put(ctx context.Context, actor string, b Binding, ttl time.Duration) error
	// PinActorUID pins the actor's live binding to uid when it is pinned to
	// none, keeping its TTL. It fails with ErrActorUIDMismatch when the
	// binding is pinned to another uid; a missing binding is no error.
	PinActorUID(ctx context.Context, actor, uid string) error
	// Get returns the live binding for the actor, or nil.
	Get(ctx context.Context, actor string) (*Binding, error)
	// Touch restarts the actor's binding TTL; a missing binding is no error.
	Touch(ctx context.Context, actor string, ttl time.Duration) error
	// Delete removes the actor's binding when it belongs to subject
	// (ErrBoundToAnother otherwise); a missing binding is no error.
	Delete(ctx context.Context, actor, subject string) error
}

// MemoryStore is the in-process Store for a muster that runs without Valkey.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]memoryEntry
	now     func() time.Time
}

type memoryEntry struct {
	binding   Binding
	expiresAt time.Time
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: map[string]memoryEntry{}, now: time.Now}
}

// live returns the actor's entry when it has not expired, dropping it
// otherwise. Callers hold mu.
func (s *MemoryStore) live(actor string) (memoryEntry, bool) {
	e, ok := s.entries[actor]
	if !ok {
		return memoryEntry{}, false
	}
	if !s.now().Before(e.expiresAt) {
		delete(s.entries, actor)
		return memoryEntry{}, false
	}
	return e, true
}

// Put implements Store.
func (s *MemoryStore) Put(_ context.Context, actor string, b Binding, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.live(actor); ok {
		if e.binding.Subject != b.Subject {
			return ErrBoundToAnother
		}
		if b.ActorUID == "" {
			b.ActorUID = e.binding.ActorUID
		}
	}
	s.entries[actor] = memoryEntry{binding: b, expiresAt: s.now().Add(ttl)}
	return nil
}

// Get implements Store.
func (s *MemoryStore) Get(_ context.Context, actor string) (*Binding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.live(actor)
	if !ok {
		return nil, nil
	}
	b := e.binding
	return &b, nil
}

// PinActorUID implements Store.
func (s *MemoryStore) PinActorUID(_ context.Context, actor, uid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.live(actor)
	if !ok {
		return nil
	}
	changed, err := pinActorUID(&e.binding, uid)
	if changed {
		s.entries[actor] = e
	}
	return err
}

// pinActorUID pins b to uid when it is pinned to none and reports whether b
// changed.
func pinActorUID(b *Binding, uid string) (bool, error) {
	switch b.ActorUID {
	case uid:
		return false, nil
	case "":
		b.ActorUID = uid
		return true, nil
	default:
		return false, ErrActorUIDMismatch
	}
}

// Touch implements Store.
func (s *MemoryStore) Touch(_ context.Context, actor string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.live(actor); ok {
		e.expiresAt = s.now().Add(ttl)
		s.entries[actor] = e
	}
	return nil
}

// Delete implements Store.
func (s *MemoryStore) Delete(_ context.Context, actor, subject string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.live(actor)
	if !ok {
		return nil
	}
	if e.binding.Subject != subject {
		return ErrBoundToAnother
	}
	delete(s.entries, actor)
	return nil
}
