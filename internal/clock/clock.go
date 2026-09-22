// Package clock is the source of the current time for muster's timers that
// measure elapsed time: the reconnect backoff of a remote MCPServer, the
// orchestrator's retry and health-probe ticks, the aggregator's capability
// poll and the age of its core catalogue.
//
// In production Now is time.Now. When MUSTER_TEST_CLOCK names a Unix socket,
// the process serves a control endpoint on it (StartControl) through which
// the integration test harness advances the clock: Now jumps forward by the
// offset and every Ticker whose next tick has become due fires at once. A
// scenario about a two-minute backoff cap or a five-minute catalogue age then
// runs on the production schedule in milliseconds, instead of on intervals
// shortened through environment knobs. The offset is process-wide and only
// ever grows, as wall time does.
package clock

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// offset is how far the clock has been advanced past the system time, in
// nanoseconds. Zero unless the control endpoint moved it.
var offset atomic.Int64

// Now returns the current time as muster sees it: the system time plus the
// offset the clock has been advanced by.
func Now() time.Time {
	return time.Now().Add(Offset())
}

// Offset returns how far the clock has been advanced past the system time.
func Offset() time.Duration {
	return time.Duration(offset.Load())
}

// Since returns the time elapsed since t on this clock.
func Since(t time.Time) time.Duration {
	return Now().Sub(t)
}

// Until returns the duration until t on this clock.
func Until(t time.Time) time.Duration {
	return t.Sub(Now())
}

// Advance moves the clock forward by d and returns the total offset. Every
// Ticker is woken, so a tick that has become due fires at once. A negative d
// is refused: the clock only moves forward.
func Advance(d time.Duration) (time.Duration, error) {
	if d < 0 {
		return Offset(), fmt.Errorf("clock: cannot move backwards by %s", -d)
	}
	total := time.Duration(offset.Add(int64(d)))
	tickers.wakeAll()
	return total, nil
}

// Ticker delivers ticks at intervals of d on this clock, like time.Ticker,
// and also when Advance moves the clock past its next tick. A receiver that
// has not taken the previous tick is not sent a second one.
type Ticker struct {
	// C is the channel on which the ticks are delivered.
	C <-chan time.Time

	c        chan time.Time
	d        time.Duration
	mu       sync.Mutex
	next     time.Time
	wake     chan struct{}
	stop     chan struct{}
	stopOnce sync.Once
}

// NewTicker returns a Ticker whose first tick is due d from now.
func NewTicker(d time.Duration) *Ticker {
	if d <= 0 {
		panic("clock: non-positive interval for NewTicker")
	}
	c := make(chan time.Time, 1)
	t := &Ticker{
		C:    c,
		c:    c,
		d:    d,
		next: Now().Add(d),
		wake: make(chan struct{}, 1),
		stop: make(chan struct{}),
	}
	tickers.add(t)
	go t.run()
	return t
}

// Stop turns off the ticker. No more ticks are sent; C is not closed.
func (t *Ticker) Stop() {
	t.stopOnce.Do(func() {
		tickers.remove(t)
		close(t.stop)
	})
}

// run waits for the next tick to become due -- by the system time passing or
// by Advance -- and sends it.
func (t *Ticker) run() {
	for {
		t.mu.Lock()
		wait := Until(t.next)
		t.mu.Unlock()
		timer := time.NewTimer(wait)
		select {
		case <-t.stop:
			timer.Stop()
			return
		case <-t.wake:
			timer.Stop()
		case <-timer.C:
		}
		// A timer that fired together with Stop (a wait that had become
		// negative through an Advance after Stop) must not tick: the select
		// above picks either ready case.
		select {
		case <-t.stop:
			return
		default:
		}
		t.tickIfDue()
	}
}

// tickIfDue sends a tick when the clock has reached the next one and puts
// the following tick d after now: a clock advanced by many intervals yields
// one tick, not one per interval skipped.
func (t *Ticker) tickIfDue() {
	now := Now()
	t.mu.Lock()
	if now.Before(t.next) {
		t.mu.Unlock()
		return
	}
	t.next = now.Add(t.d)
	t.mu.Unlock()
	select {
	case t.c <- now:
	default:
	}
}

// tickerSet is the set of live tickers Advance wakes.
type tickerSet struct {
	mu sync.Mutex
	m  map[*Ticker]struct{}
}

var tickers = tickerSet{m: make(map[*Ticker]struct{})}

func (s *tickerSet) add(t *Ticker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[t] = struct{}{}
}

func (s *tickerSet) remove(t *Ticker) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, t)
}

// wakeAll asks every live ticker to check whether its next tick is due.
func (s *tickerSet) wakeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for t := range s.m {
		select {
		case t.wake <- struct{}{}:
		default:
		}
	}
}
