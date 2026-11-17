package mcpserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/giantswarm/muster/v5/internal/services"
)

// startAttempt is a Start (or the start half of a Restart) in flight. err is
// written before done closes.
type startAttempt struct {
	cancel context.CancelCauseFunc
	done   chan struct{}
	err    error
}

// runStart runs start unless an attempt is already in flight, in which case
// it waits for that one and returns its result: a service connects one client
// at a time, so concurrent starts never open a second client over the first.
//
// start runs under lifecycleMutex, so it never interleaves with a stop. An
// attempt that begins while a Stop is in progress has its connectCtx
// cancelled with services.ErrStopRequested before start runs: of a Start and
// a Stop that overlap, the Stop wins.
func (s *Service) runStart(ctx context.Context, start func(ctx, connectCtx context.Context) error) error {
	s.startMutex.Lock()
	if inFlight := s.startInFlight; inFlight != nil {
		s.startMutex.Unlock()
		select {
		case <-inFlight.done:
			return inFlight.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if s.removed {
		s.startMutex.Unlock()
		return fmt.Errorf("start of MCP server %s: %w", s.GetName(), services.ErrServiceRemoved)
	}
	connectCtx, cancel := context.WithCancelCause(ctx)
	if s.stopsInFlight > 0 {
		cancel(services.ErrStopRequested)
	}
	attempt := &startAttempt{cancel: cancel, done: make(chan struct{})}
	s.startInFlight = attempt
	s.startMutex.Unlock()

	defer func() {
		cancel(nil)
		s.startMutex.Lock()
		s.startInFlight = nil
		s.startMutex.Unlock()
		close(attempt.done)
	}()

	s.lifecycleMutex.Lock()
	defer s.lifecycleMutex.Unlock()
	if err := s.stopRequested(connectCtx); err != nil {
		attempt.err = err
	} else {
		attempt.err = start(ctx, connectCtx)
	}
	return attempt.err
}

// stopRequested returns the error a start attempt ends with once a Stop
// cancelled its connectCtx, nil while none did.
func (s *Service) stopRequested(connectCtx context.Context) error {
	if !errors.Is(context.Cause(connectCtx), services.ErrStopRequested) {
		return nil
	}
	s.startMutex.Lock()
	removed := s.removed
	s.startMutex.Unlock()
	if removed {
		return fmt.Errorf("start of MCP server %s: %w", s.GetName(), services.ErrServiceRemoved)
	}
	return fmt.Errorf("start of MCP server %s: %w", s.GetName(), services.ErrStopRequested)
}

// Remove stops the service for good: like Stop, and every start attempt
// that has not connected yet or begins later returns
// services.ErrServiceRemoved.
func (s *Service) Remove(ctx context.Context) error {
	unlock, err := s.lockForStop(ctx, true)
	if err != nil {
		return err
	}
	defer unlock()
	s.stop()
	return nil
}

// lockForStop cancels the connect of the attempt in flight, waits until that
// attempt returned and takes lifecycleMutex; every attempt that begins before
// the returned unlock is called is cancelled before it connects. With remove,
// the service is marked removed first, even when ctx ends before the attempt
// in flight returned. It gives up when ctx ends before the attempt in flight
// returned.
func (s *Service) lockForStop(ctx context.Context, remove bool) (unlock func(), err error) {
	s.startMutex.Lock()
	s.removed = s.removed || remove
	s.stopsInFlight++
	inFlight := s.startInFlight
	s.startMutex.Unlock()
	endStop := func() {
		s.startMutex.Lock()
		s.stopsInFlight--
		s.startMutex.Unlock()
	}

	if inFlight != nil {
		inFlight.cancel(services.ErrStopRequested)
		select {
		case <-inFlight.done:
		case <-ctx.Done():
			endStop()
			return nil, fmt.Errorf("stop of MCP server %s: the start in flight did not return: %w", s.GetName(), ctx.Err())
		}
	}
	s.lifecycleMutex.Lock()
	return func() {
		s.lifecycleMutex.Unlock()
		endStop()
	}, nil
}
