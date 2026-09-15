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
