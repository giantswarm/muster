package credprovider

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
)

// ErrBoundToAnother is returned when a bind or unbind names an actor that is
// bound to a different person.
var ErrBoundToAnother = errors.New("the actor is bound to another person")

// ErrSenderBound is returned when a bind would replace a binding an approval
// sender wrote without that same sender: the person could otherwise strip
// the agent's own Ask and Block from their Session (M2-S3-S-01).
var ErrSenderBound = errors.New("the binding carries an agent's approval settings: only the approval sender that wrote it (agent-manager) can replace it")

// sealFactor is how many binding TTLs an actor's sender seal outlives its
// last bind or Touch: after a sender-written binding is deleted or expires,
// a bind of that actor without the sender stays refused for that long
// (M2-S3-R-01). An actor is one Session's sandbox, so by then it is gone.
const sealFactor = 2

// checkReplace refuses a write of b over the live binding existing, or,
// without one, over the actor's sender seal (the sender of the last
// sender-written binding, "" when none): another person's binding, or a
// binding an approval sender wrote, replaced without that sender. A binding
// stored before senders were named accepts any sender.
func checkReplace(existing *Binding, seal string, sealed bool, b Binding) error {
	if existing != nil {
		if existing.Subject != b.Subject {
			return ErrBoundToAnother
		}
		if existing.ApprovalSender && (!b.ApprovalSender || (existing.Sender != "" && existing.Sender != b.Sender)) {
			return ErrSenderBound
		}
		return nil
	}
	if sealed && (!b.ApprovalSender || (seal != "" && seal != b.Sender)) {
		return ErrSenderBound
	}
	return nil
}

// checkDelete refuses removing a sender-written binding without its sender:
// the person could otherwise unbind and bind again without the agent's
// settings (M2-S3-R-01).
func checkDelete(existing *Binding, subject, sender string) error {
	if existing.Subject != subject {
		return ErrBoundToAnother
	}
	if existing.ApprovalSender && (sender == "" || (existing.Sender != "" && existing.Sender != sender)) {
		return ErrSenderBound
	}
	return nil
}

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
	// ApprovalSender is true when the bind came from a sender Kubernetes
	// allows to send approval settings (sender_review.go). Approval settings
	// in a binding are honoured only then.
	ApprovalSender bool `json:"approvalSender,omitempty"`
	// Sender is the approval sender's Kubernetes identity; only it can
	// replace this binding.
	Sender string `json:"sender,omitempty"`
	// Agent is the Session's agent, <namespace>/<name>; set only by an
	// approval sender.
	Agent string `json:"agent,omitempty"`
	// Approval is the agent's approval settings at the Session's start:
	// entries with Allow, Ask or Block, the default and the grant marks.
	// Set only by an approval sender; grant marks can later be removed by
	// one (Store.DropGrants), nothing else changes.
	Approval *api.AgentApproval `json:"approval,omitempty"`
	// SignInIssuer and SignInSubject are the issuer and raw sub of the
	// bind request's bearer: who the person signed in as, which a sign-in
	// link the actor's requests are answered with must be completed by
	// (api.SignInIdentity). Empty on a binding stored before they were
	// recorded, whose sign-in links then complete for nobody.
	SignInIssuer  string `json:"signInIss,omitempty"`
	SignInSubject string `json:"signInSub,omitempty"`
	// LoginSession is the muster session whose login ID token the actor's
	// requests carry: the session the bind request's bearer came from, a
	// muster login family or a session a client forwarded its own token in.
	// Empty when none was found and on a binding stored before it was
	// recorded: the actor then carries no login ID token of the person.
	LoginSession string `json:"loginSession,omitempty"`
	// LoginIssuer and LoginSubject are the iss and sub of the forwarded
	// token that pinned a forwarded session, which a token of that session
	// must carry to serve the actor: a forwarded session's ID is no
	// authorization key. Empty for a login family.
	LoginIssuer  string `json:"loginIss,omitempty"`
	LoginSubject string `json:"loginSub,omitempty"`
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
	// another subject (ErrBoundToAnother) or the live binding came from an
	// approval sender b does not come from (ErrSenderBound).
	Put(ctx context.Context, actor string, b Binding, ttl time.Duration) error
	// Get returns the live binding for the actor, or nil.
	Get(ctx context.Context, actor string) (*Binding, error)
	// Touch restarts the actor's binding TTL, and its place in the agent's
	// index when agent is set; a missing binding is no error.
	Touch(ctx context.Context, actor, agent string, ttl time.Duration) error
	// Delete removes the actor's binding when it belongs to subject
	// (ErrBoundToAnother otherwise) and, for a binding an approval sender
	// wrote, sender is that sender (ErrSenderBound otherwise); a missing
	// binding is no error.
	Delete(ctx context.Context, actor, subject, sender string) error
	// ActorsOfAgent returns the actors whose live bindings name the agent.
	ActorsOfAgent(ctx context.Context, agent string) ([]string, error)
	// DropGrants removes from the actor's binding of the agent every grant
	// mark keep refuses, keeping the binding's person, settings and expiry;
	// it returns how many it removed. A missing binding, or one of another
	// agent, is left alone (0).
	DropGrants(ctx context.Context, actor, agent string, keep func(api.AgentApprovalGrant) bool) (int, error)
}

// dropGrants removes the grant marks keep refuses from b, returning how
// many it removed; b is changed only when it names the agent.
func dropGrants(b *Binding, agent string, keep func(api.AgentApprovalGrant) bool) int {
	if b == nil || b.Agent != agent || b.Approval == nil || len(b.Approval.Grants) == 0 {
		return 0
	}
	kept := make([]api.AgentApprovalGrant, 0, len(b.Approval.Grants))
	for _, g := range b.Approval.Grants {
		if keep(g) {
			kept = append(kept, g)
		}
	}
	dropped := len(b.Approval.Grants) - len(kept)
	if dropped > 0 {
		approval := *b.Approval
		approval.Grants = kept
		b.Approval = &approval
	}
	return dropped
}

// MemoryStore is the in-process Store for a muster that runs without Valkey.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string]memoryEntry
	seals   map[string]memorySeal
	now     func() time.Time
}

type memorySeal struct {
	sender    string
	expiresAt time.Time
}

// seal returns the actor's live sender seal. Callers hold mu.
func (s *MemoryStore) seal(actor string) (string, bool) {
	seal, ok := s.seals[actor]
	if !ok {
		return "", false
	}
	if !s.now().Before(seal.expiresAt) {
		delete(s.seals, actor)
		return "", false
	}
	return seal.sender, true
}

type memoryEntry struct {
	binding   Binding
	expiresAt time.Time
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{entries: map[string]memoryEntry{}, seals: map[string]memorySeal{}, now: time.Now}
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
	var existing *Binding
	if e, ok := s.live(actor); ok {
		existing = &e.binding
	}
	seal, sealed := s.seal(actor)
	if err := checkReplace(existing, seal, sealed, b); err != nil {
		return err
	}
	s.entries[actor] = memoryEntry{binding: b, expiresAt: s.now().Add(ttl)}
	if b.ApprovalSender {
		s.seals[actor] = memorySeal{sender: b.Sender, expiresAt: s.now().Add(sealFactor * ttl)}
	}
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

// Touch implements Store.
func (s *MemoryStore) Touch(_ context.Context, actor, _ string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.live(actor); ok {
		e.expiresAt = s.now().Add(ttl)
		s.entries[actor] = e
		if seal, sealed := s.seal(actor); sealed && e.binding.ApprovalSender {
			s.seals[actor] = memorySeal{sender: seal, expiresAt: s.now().Add(sealFactor * ttl)}
		}
	}
	return nil
}

// ActorsOfAgent implements Store.
func (s *MemoryStore) ActorsOfAgent(_ context.Context, agent string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var actors []string
	for actor := range s.entries {
		if e, ok := s.live(actor); ok && agent != "" && e.binding.Agent == agent {
			actors = append(actors, actor)
		}
	}
	slices.Sort(actors)
	return actors, nil
}

// DropGrants implements Store.
func (s *MemoryStore) DropGrants(_ context.Context, actor, agent string, keep func(api.AgentApprovalGrant) bool) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.live(actor)
	if !ok {
		return 0, nil
	}
	dropped := dropGrants(&e.binding, agent, keep)
	if dropped > 0 {
		s.entries[actor] = e
	}
	return dropped, nil
}

// Delete implements Store.
func (s *MemoryStore) Delete(_ context.Context, actor, subject, sender string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.live(actor)
	if !ok {
		return nil
	}
	if err := checkDelete(&e.binding, subject, sender); err != nil {
		return err
	}
	delete(s.entries, actor)
	return nil
}
