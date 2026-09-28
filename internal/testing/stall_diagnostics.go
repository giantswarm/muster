package testing

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"time"
)

// stallNote is appended to the error of a step whose call never returned, so
// the failure line in the CI log says where the dumps are.
const stallNote = "; the last call to muster serve was still waiting for its response when its budget ran out" +
	" -- goroutine dumps of muster serve (SIGQUIT, at the end of the instance stderr)" +
	" and of the harness (harness_goroutines, and harness_goroutines_at_overrun if the call outlived its deadline) are in the report"

// harnessGoroutineDumpLimit caps the harness's goroutine dump. A run with
// fifty scenarios in flight has thousands of goroutines; the first megabytes
// hold the ones that matter, since a stalled scenario is usually among the
// last still running.
const harnessGoroutineDumpLimit = 8 << 20

// instanceDumpTimeout bounds how long captureStallDiagnostics waits for the
// signalled muster serve to print its goroutines and exit.
const instanceDumpTimeout = 5 * time.Second

// stalledCall reports whether a tool call never returned: it ended with
// context.DeadlineExceeded -- a wait_for_state budget, a step timeout or the
// client's own bound spent waiting for muster serve's response. A scenario
// that fails this way has no response to judge, and the log of a still-alive
// instance cannot show why the request never came back; the goroutines can.
func stalledCall(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
}

// captureStallDiagnostics records where the harness and the instance were
// when a step's call never returned: the harness's goroutines into the
// result, and muster serve's through SIGQUIT, on which the Go runtime prints
// every goroutine to stderr -- captured with the instance logs -- and exits.
// The scenario has failed by then; DestroyInstance follows as it would have.
// Reports whether the instance was stopped for the dump, so the exit is not
// read as a death mid-scenario.
func (r *testRunner) captureStallDiagnostics(instance *MusterInstance, result *TestScenarioResult, logger TestLogger) (instanceStopped bool) {
	result.HarnessGoroutines = harnessGoroutines()
	result.HarnessGoroutinesAtOverrun = takeOverrunDump(instance.Endpoint)
	manager, ok := r.instanceManager.(*musterInstanceManager)
	return ok && manager.dumpGoroutines(instance, logger)
}

// callOverrunGrace is how long a call to muster serve may run past its
// context's deadline before the harness records its goroutines. A call that
// honours its context returns within milliseconds of the deadline. One still
// running this much later is blocked somewhere that ignores the context --
// CI showed a poll with a 20s budget come back after 90s, its request never
// logged by muster serve (#1206) -- and only a dump taken while it is blocked
// shows where: the dump captureStallDiagnostics takes comes after the call
// has unwound, when the frame is gone.
var callOverrunGrace = 2 * time.Second

// overrunDumps holds, per muster serve endpoint, the harness goroutines
// recorded when a call to that endpoint overran its deadline; the first
// overrun of a scenario is kept.
var overrunDumps sync.Map

// recordOverrun stores the harness's goroutines for endpoint; a variable so
// tests can observe it firing.
var recordOverrun = func(endpoint string) {
	overrunDumps.LoadOrStore(endpoint, harnessGoroutines())
}

// watchCallOverrun records the harness's goroutines under endpoint if the
// call ctx bounds is still running callOverrunGrace after ctx's deadline. The
// returned stop ends the watch -- call it when the call returns -- and reports
// whether the watch was still pending, i.e. nothing was recorded.
func watchCallOverrun(ctx context.Context, endpoint string) (stop func() bool) {
	deadline, ok := ctx.Deadline()
	if !ok {
		return func() bool { return true }
	}
	timer := time.AfterFunc(time.Until(deadline)+callOverrunGrace, func() { recordOverrun(endpoint) })
	return timer.Stop
}

// takeOverrunDump returns and forgets the dump recorded for endpoint, or ""
// when no call to it overran. The runner also forgets it when the scenario's
// instance is destroyed, so a later instance on the same port starts clean.
func takeOverrunDump(endpoint string) string {
	dump, _ := overrunDumps.LoadAndDelete(endpoint)
	s, _ := dump.(string)
	return s
}

// harnessGoroutines returns the stacks of every goroutine of this process,
// truncated at harnessGoroutineDumpLimit.
func harnessGoroutines() string {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) || len(buf) >= harnessGoroutineDumpLimit {
			return string(buf[:n])
		}
		buf = make([]byte, 2*len(buf))
	}
}

// dumpGoroutines makes the instance's muster serve print every goroutine and
// exit (SIGQUIT; GOTRACEBACK=all in its environment covers all of them, not
// just the signalled one) and waits for the exit, so the instance logs the
// runner collects afterwards end with the dump. Reports whether the signal
// was delivered; a process that is already gone gets none, and its exit
// status then is a finding of its own.
func (m *musterInstanceManager) dumpGoroutines(instance *MusterInstance, logger TestLogger) bool {
	m.mu.RLock()
	proc := m.processes[instance.ID]
	m.mu.RUnlock()
	if proc == nil || proc.cmd == nil || proc.cmd.Process == nil {
		return false
	}
	select {
	case <-proc.exited:
		return false
	default:
	}
	if err := quitProcess(proc.cmd.Process.Pid); err != nil {
		if m.debug {
			logger.Debug("⚠️  Could not ask %s for a goroutine dump: %v\n", instance.ID, err)
		}
		return false
	}
	select {
	case <-proc.exited:
	case <-time.After(instanceDumpTimeout):
		if m.debug {
			logger.Debug("⚠️  %s did not exit within %s of SIGQUIT\n", instance.ID, instanceDumpTimeout)
		}
	}
	return true
}
