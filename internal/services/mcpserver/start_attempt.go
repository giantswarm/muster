package mcpserver

import (
	"context"
	"errors"
	"fmt"
)

// errStopRequested is the cause a Stop cancels the connect of a Start in
// flight with.
var errStopRequested = errors.New("stop requested while the MCP server was starting")

// startAttempt is a Start in flight. err is written before done closes.
type startAttempt struct {
	cancel context.CancelCauseFunc
	done   chan struct{}
	err    error
}

// runStart runs start unless a Start is already in flight, in which case it
// waits for that one and returns its result: a service connects one client at
// a time, so concurrent starts never open a second client over the first.
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
	connectCtx, cancel := context.WithCancelCause(ctx)
	attempt := &startAttempt{cancel: cancel, done: make(chan struct{})}
	s.startInFlight = attempt
	s.startMutex.Unlock()

	attempt.err = start(ctx, connectCtx)

	cancel(nil)
	s.startMutex.Lock()
	s.startInFlight = nil
	s.startMutex.Unlock()
	close(attempt.done)
	return attempt.err
}

// abortStart cancels the connect of a Start in flight and waits until that
// Start returned, so the Stop that follows closes the client the Start may
// have opened and settles the state after it.
func (s *Service) abortStart(ctx context.Context) error {
	s.startMutex.Lock()
	inFlight := s.startInFlight
	s.startMutex.Unlock()
	if inFlight == nil {
		return nil
	}
	inFlight.cancel(errStopRequested)
	select {
	case <-inFlight.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("stop of MCP server %s: the start in flight did not return: %w", s.GetName(), ctx.Err())
	}
}
