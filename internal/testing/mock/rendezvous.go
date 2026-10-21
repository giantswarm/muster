package mock

import (
	"fmt"
	"sync"
	"time"
)

// rendezvousTimeout bounds how long a call waits for its partners. It is a
// failure bound -- a loop that never runs calls side by side fails with it --
// not a way to order them.
const rendezvousTimeout = 20 * time.Second

// rendezvous is a cyclic barrier over a tool's calls: every call waits until
// size calls are waiting together, then all of them go on. It also counts the
// most calls of the tool in flight at once, so a scenario can assert both that
// calls ran concurrently and how many did.
type rendezvous struct {
	size int

	mu          sync.Mutex
	arrived     int
	release     chan struct{}
	inFlight    int
	maxInFlight int
}

func newRendezvous(size int) *rendezvous {
	return &rendezvous{size: size}
}

// enter counts a call in flight and returns the function that counts it out.
func (r *rendezvous) enter() func() {
	r.mu.Lock()
	r.inFlight++
	r.maxInFlight = max(r.maxInFlight, r.inFlight)
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		r.inFlight--
		r.mu.Unlock()
	}
}

// wait blocks until size calls wait together, or fails after the timeout.
func (r *rendezvous) wait(timeout time.Duration) error {
	r.mu.Lock()
	if r.release == nil {
		r.release = make(chan struct{})
	}
	release := r.release
	r.arrived++
	if r.arrived == r.size {
		close(release)
		r.release = nil
		r.arrived = 0
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-release:
		return nil
	case <-timer.C:
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	select {
	case <-release: // released while the timer fired
		return nil
	default:
	}
	r.arrived--
	return fmt.Errorf("rendezvous: waited %s for %d calls in flight at once, the most seen was %d", timeout, r.size, r.maxInFlight)
}

// max is the most calls in flight at once so far.
func (r *rendezvous) max() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.maxInFlight
}
