package approval

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/giantswarm/muster/v5/internal/api"
)

// Pending tracks the approval requests this replica made, to report how many
// still wait and which ran out unanswered. Valkey drops a request when it
// expires without telling anyone, so expiry is only observable by looking
// before it happens: Sweep asks the store about each tracked request and
// counts as expired the ones it last saw pending that have passed their
// expiry. Requests decided or used on another replica leave the store's
// pending state and are dropped here without being counted (the replica that
// decided or used them counted them).
type Pending struct {
	mu      sync.Mutex
	entries map[string]*pendingEntry
}

type pendingEntry struct {
	expiresAt time.Time
}

// NewPending returns an empty tracker.
func NewPending() *Pending {
	return &Pending{entries: map[string]*pendingEntry{}}
}

// Track notes a request this replica made.
func (p *Pending) Track(request *api.ApprovalRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.entries[request.ID]; !ok {
		p.entries[request.ID] = &pendingEntry{expiresAt: request.ExpiresAt}
	}
}

// Len is the number of requests still waiting, as of the last Sweep.
func (p *Pending) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

func (p *Pending) ids() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.entries))
	for id := range p.entries {
		out = append(out, id)
	}
	return out
}

// Done stops tracking a request this replica saw decided or used.
func (p *Pending) Done(id string) { p.drop(id) }

func (p *Pending) drop(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.entries, id)
}

// Sweep reconciles the tracked requests with store at now: a request still
// pending in the store stays; one past its expiry was never decided and is
// counted expired; one decided, used or gone before its expiry is dropped
// uncounted. A store that cannot be reached keeps every entry (an expired
// one is counted: nobody can decide it any more).
func (p *Pending) Sweep(ctx context.Context, store api.ApprovalStore, now time.Time, m *Metrics) {
	for _, id := range p.ids() {
		p.mu.Lock()
		entry, ok := p.entries[id]
		p.mu.Unlock()
		if !ok {
			continue
		}
		if !now.Before(entry.expiresAt) {
			p.drop(id)
			m.Decided(ctx, OutcomeExpired)
			continue
		}
		if store == nil {
			continue
		}
		request, err := store.Get(ctx, id)
		switch {
		case err != nil && !errorIsNotFound(err):
			// Unknown: keep.
		case err != nil, request.Status != api.ApprovalStatusPending:
			p.drop(id)
		}
	}
}

func errorIsNotFound(err error) bool { return errors.Is(err, api.ErrApprovalNotFound) }
