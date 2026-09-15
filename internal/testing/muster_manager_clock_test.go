package testing

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/giantswarm/muster/internal/clock"
)

func TestInstanceIntervals(t *testing.T) {
	require.Equal(t, IntervalsShort, instanceIntervals(nil))
	require.Equal(t, IntervalsShort, instanceIntervals(&MusterPreConfiguration{}))
	require.Equal(t, IntervalsProduction, instanceIntervals(&MusterPreConfiguration{Intervals: "Production"}))
}

func TestValidateIntervalsConfig(t *testing.T) {
	require.NoError(t, validateIntervalsConfig(nil))
	require.NoError(t, validateIntervalsConfig(&MusterPreConfiguration{}))
	require.NoError(t, validateIntervalsConfig(&MusterPreConfiguration{Intervals: "short"}))
	require.NoError(t, validateIntervalsConfig(&MusterPreConfiguration{Intervals: "production"}))
	require.ErrorContains(t, validateIntervalsConfig(&MusterPreConfiguration{Intervals: "fast"}), "pre_configuration.intervals must be")
}

// TestTimingEnv: both schedules give the process a clock control socket and
// the shortened resync; only the short one shortens the timers the clock
// reaches.
func TestTimingEnv(t *testing.T) {
	timing := instanceTiming{intervals: IntervalsShort, clockSocket: "/tmp/x/clock-1.sock"}
	short := strings.Join(timingEnv(timing), "\n")
	require.Contains(t, short, clock.EnvControlSocket+"=/tmp/x/clock-1.sock")
	require.Contains(t, short, "MUSTER_RECONCILER_RESYNC_INTERVAL=2s")
	for _, knob := range []string{"MUSTER_MCPSERVER_INITIAL_BACKOFF=1s", "MUSTER_MCPSERVER_MAX_BACKOFF=3s", "MUSTER_ORCHESTRATOR_RETRY_INTERVAL=1s", "MUSTER_ORCHESTRATOR_HEALTH_CHECK_INTERVAL=1s", "MUSTER_CORE_CATALOGUE_MAX_AGE=3s"} {
		require.Contains(t, short, knob)
	}

	timing.intervals = IntervalsProduction
	production := strings.Join(timingEnv(timing), "\n")
	require.Contains(t, production, clock.EnvControlSocket+"=/tmp/x/clock-1.sock")
	require.Contains(t, production, "MUSTER_RECONCILER_RESYNC_INTERVAL=2s")
	for _, knob := range []string{"MUSTER_MCPSERVER_INITIAL_BACKOFF", "MUSTER_MCPSERVER_MAX_BACKOFF", "MUSTER_ORCHESTRATOR_RETRY_INTERVAL", "MUSTER_ORCHESTRATOR_HEALTH_CHECK_INTERVAL", "MUSTER_CORE_CATALOGUE_MAX_AGE"} {
		require.NotContains(t, production, knob, "production intervals leave the clock-reachable timers on their defaults")
	}
}

func TestClockSocketPathIsShort(t *testing.T) {
	path := clockSocketPath("/tmp/muster-test-1234567890", 30042)
	require.Equal(t, filepath.Join("/tmp/muster-test-1234567890", "clock-30042.sock"), path)
	require.Less(t, len(path), 100, "a Unix socket path must stay under the platform limit")
}

func TestTokenCapable(t *testing.T) {
	require.False(t, (&MCPServerOAuthConfig{}).tokenCapable())
	require.True(t, (&MCPServerOAuthConfig{Required: true}).tokenCapable())
	require.True(t, (&MCPServerOAuthConfig{MockOAuthServerRef: "idp"}).tokenCapable())
	require.True(t, (&MCPServerOAuthConfig{TrustIssuerRef: "idp"}).tokenCapable())
}

// TestFaultToolsRefuseWithoutAnInstance: the argument checks and the
// no-instance guard of the fault and clock tools.
func TestFaultToolsRefuseWithoutAnInstance(t *testing.T) {
	h := &TestToolsHandler{logger: NewStdoutLogger(false, false)}
	ctx := context.Background()

	_, err := h.HandleTestTool(ctx, TestToolAdvanceClock, map[string]interface{}{})
	require.ErrorContains(t, err, "duration argument is required")
	_, err = h.HandleTestTool(ctx, TestToolAdvanceClock, map[string]interface{}{"duration": "soon"})
	require.ErrorContains(t, err, "invalid duration")
	_, err = h.HandleTestTool(ctx, TestToolAdvanceClock, map[string]interface{}{"duration": "-1m"})
	require.ErrorContains(t, err, "must be positive")
	_, err = h.HandleTestTool(ctx, TestToolAdvanceClock, map[string]interface{}{"duration": "1m"})
	require.ErrorContains(t, err, "not available")

	_, err = h.HandleTestTool(ctx, TestToolRedeployMockServer, map[string]interface{}{})
	require.ErrorContains(t, err, "server argument is required")
	_, err = h.HandleTestTool(ctx, TestToolRedeployMockServer, map[string]interface{}{"server": "x"})
	require.ErrorContains(t, err, "not available")

	_, err = h.HandleTestTool(ctx, TestToolSetMockServerAuth, map[string]interface{}{"required": true})
	require.ErrorContains(t, err, "server argument is required")
	_, err = h.HandleTestTool(ctx, TestToolSetMockServerAuth, map[string]interface{}{"server": "x"})
	require.ErrorContains(t, err, "required argument is required")
	_, err = h.HandleTestTool(ctx, TestToolSetMockServerAuth, map[string]interface{}{"server": "x", "required": true})
	require.ErrorContains(t, err, "not available")

	for _, name := range []string{TestToolAdvanceClock, TestToolRedeployMockServer, TestToolSetMockServerAuth} {
		require.True(t, IsTestTool(name), name)
	}
}

// TestAdvanceClockWithoutAServingProcess: an instance whose muster serve does
// not serve the control socket -- a release before the clock -- is reported
// as such, and no mock clock is moved.
func TestAdvanceClockWithoutAServingProcess(t *testing.T) {
	m := newStorageTestManager(t)
	inst := &MusterInstance{ID: "inst", ClockSocketPath: filepath.Join(t.TempDir(), "clock.sock")}
	h := NewTestToolsHandler(m, inst, false, m.logger)
	_, err := h.HandleTestTool(context.Background(), TestToolAdvanceClock, map[string]interface{}{"duration": "1m"})
	require.ErrorContains(t, err, "exposes no controllable clock")
	require.Zero(t, inst.ClockOffset)

	inst.ClockSocketPath = ""
	_, err = h.HandleTestTool(context.Background(), TestToolAdvanceClock, map[string]interface{}{"duration": "1m"})
	require.ErrorContains(t, err, "no clock control socket")
}

// TestSetMockServerAuthNamesTheMissingValidator: a plain mock cannot be
// flipped and the error says what the scenario has to configure.
func TestSetMockServerAuthNamesTheMissingValidator(t *testing.T) {
	m := newStorageTestManager(t)
	inst := &MusterInstance{ID: "inst"}
	h := NewTestToolsHandler(m, inst, false, m.logger)
	_, err := h.HandleTestTool(context.Background(), TestToolSetMockServerAuth, map[string]interface{}{"server": "ghost", "required": true})
	require.ErrorContains(t, err, "not found")
	_, err = h.HandleTestTool(context.Background(), TestToolRedeployMockServer, map[string]interface{}{"server": "ghost"})
	require.ErrorContains(t, err, "not found")
}
