package testing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestStalledCall(t *testing.T) {
	wrapped := fmt.Errorf("tool call x failed: %w", fmt.Errorf("transport error: %w", context.DeadlineExceeded))
	if !stalledCall(wrapped) {
		t.Error("a wrapped context.DeadlineExceeded is a stalled call")
	}
	for _, err := range []error{nil, errors.New("not authenticated"), context.Canceled} {
		if stalledCall(err) {
			t.Errorf("%v is not a stalled call", err)
		}
	}
}

// TestWaitForStateReportsAStalledPoll: a poll that never returns ends the
// wait with the stall reported, instead of being read as "state not yet
// achieved" and polled again behind the call that never came back.
func TestWaitForStateReportsAStalledPoll(t *testing.T) {
	runner := &testRunner{logger: NewSilentLogger(false, false)}
	expected := TestExpectation{Success: true, Contains: []string{"never"}, WaitForState: 30 * time.Second}
	calls := 0
	client := &pollingStubClient{onCall: func() (interface{}, error) {
		calls++
		return nil, fmt.Errorf("tool call failed: %w", context.DeadlineExceeded)
	}}

	start := time.Now()
	met, stalled := runner.validateExpectationsWithClient(
		context.Background(), expected, mcpResultOf(t, map[string]interface{}{"state": "pending"}, false), nil, client,
		"core_mcpserver_get", nil, runner.logger,
	)
	if met || !stalled {
		t.Fatalf("got met=%v stalled=%v, want the stall reported", met, stalled)
	}
	if calls != 1 {
		t.Errorf("polled %d times after the call never returned, want 1", calls)
	}
	if took := time.Since(start); took > expected.WaitForState/2 {
		t.Errorf("the wait ran on for %s after the stalled poll", took)
	}
}

func TestHarnessGoroutinesListsThisTest(t *testing.T) {
	dump := harnessGoroutines()
	if !strings.Contains(dump, "TestHarnessGoroutinesListsThisTest") {
		t.Errorf("the dump does not show the calling goroutine:\n%s", dump)
	}
}

func TestSplitGoroutineDump(t *testing.T) {
	stderr := "time=1 level=INFO msg=a\nSIGQUIT: quit\nPC=0x1 m=0 sigcode=0\n\ngoroutine 1 [running]:\n"
	log, dump := splitGoroutineDump(stderr)
	if log != "time=1 level=INFO msg=a\n" || !strings.HasPrefix(dump, "SIGQUIT: quit") {
		t.Errorf("split into %q / %q", log, dump)
	}
	if log, dump := splitGoroutineDump("plain log"); log != "plain log" || dump != "" {
		t.Errorf("a log without a dump split into %q / %q", log, dump)
	}
}

// TestWatchCallOverrunRecordsABlockedCall: a call still running past its
// deadline plus the grace gets the harness's goroutines recorded while it is
// blocked, so the dump shows the blocked frame (#1206).
func TestWatchCallOverrunRecordsABlockedCall(t *testing.T) {
	defer func(grace time.Duration) { callOverrunGrace = grace }(callOverrunGrace)
	callOverrunGrace = 10 * time.Millisecond
	endpoint := "http://localhost:1/overrun"
	defer overrunDumps.Delete(endpoint)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer watchCallOverrun(ctx, endpoint)()
		blockedIgnoringContext(release)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := overrunDumps.Load(endpoint); ok || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	<-done

	dump := takeOverrunDump(endpoint)
	if !strings.Contains(dump, "blockedIgnoringContext") {
		t.Fatalf("the overrun dump does not show the blocked call:\n%s", dump)
	}
	if again := takeOverrunDump(endpoint); again != "" {
		t.Error("takeOverrunDump did not forget the dump")
	}
}

// TestWatchCallOverrunIgnoresACallThatReturns: a call that returns before its
// deadline records nothing, nor does a context without a deadline.
func TestWatchCallOverrunIgnoresACallThatReturns(t *testing.T) {
	defer func(grace time.Duration) { callOverrunGrace = grace }(callOverrunGrace)
	callOverrunGrace = 10 * time.Millisecond
	endpoint := "http://localhost:1/returns"
	defer overrunDumps.Delete(endpoint)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	watchCallOverrun(ctx, endpoint)()
	watchCallOverrun(context.Background(), endpoint)()
	time.Sleep(60 * time.Millisecond)

	if dump := takeOverrunDump(endpoint); dump != "" {
		t.Errorf("a call that returned in time got a dump:\n%s", dump)
	}
}

//go:noinline
func blockedIgnoringContext(release <-chan struct{}) {
	<-release
}
