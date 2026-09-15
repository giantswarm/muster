package testing

import (
	"context"
	"fmt"
	"time"

	"github.com/giantswarm/muster/v5/internal/clock"
)

// RestartInstance stops the instance's muster serve process and starts it
// again on the same configuration directory, ports and environment. The
// Valkey stand-in and every mock MCP and OAuth server keep running with their
// state, so what the new process finds is what a pod finds after a rollout:
// the stores of the old life, backends that never went away. The captured
// output of both lives is one log for instance_logs. Returns once the new
// process is ready by the same standard CreateInstance applies.
func (m *musterInstanceManager) RestartInstance(ctx context.Context, instance *MusterInstance, logger TestLogger) error {
	if logger == nil {
		logger = m.logger
	}

	m.mu.RLock()
	old := m.processes[instance.ID]
	m.mu.RUnlock()
	if old == nil {
		return fmt.Errorf("instance %s has no running muster serve process to restart", instance.ID)
	}

	if m.debug {
		logger.Debug("🔁 Restarting muster instance %s (PID %d)\n", instance.ID, old.cmd.Process.Pid)
	}
	if err := m.gracefulShutdown(old, instance.ID, logger); err != nil {
		return fmt.Errorf("failed to stop muster instance %s for restart: %w", instance.ID, err)
	}
	old.logCapture.close()
	prior := old.logCapture.getLogs()

	// The new process must outlive the step that restarted it: ctx is the
	// step's context, and a step with a timeout cancels it as soon as the
	// step returns, which would kill the process right after readiness. The
	// process ends the way the first one did, through DestroyInstance.
	timing := instanceTiming{intervals: instance.Intervals, clockSocket: instance.ClockSocketPath}
	proc, err := m.startMusterProcess(context.WithoutCancel(ctx), instance.ConfigPath, instance.Port, instance.MetricsPort, timing, logger)
	if err != nil {
		m.mu.Lock()
		delete(m.processes, instance.ID)
		m.mu.Unlock()
		instance.Logs = prior
		return fmt.Errorf("failed to start muster instance %s again: %w", instance.ID, err)
	}
	proc.logCapture.setPrior(prior)

	m.mu.Lock()
	m.processes[instance.ID] = proc
	m.mu.Unlock()
	instance.Process = proc.cmd.Process
	instance.StartTime = time.Now()
	instance.Logs = nil

	if m.debug {
		logger.Debug("🔁 muster instance %s started again (PID %d), waiting for readiness\n", instance.ID, proc.cmd.Process.Pid)
	}
	if err := m.WaitForReady(ctx, instance, logger); err != nil {
		return err
	}
	// The new process starts on the system time; the scenario's clock had
	// moved on. Advance it by the same offset, so time never runs backwards
	// across a restart (a pod restart does not turn the wall clock back).
	if instance.ClockOffset > 0 {
		if _, err := clock.RemoteAdvance(instance.ClockSocketPath, instance.ClockOffset); err != nil {
			return fmt.Errorf("failed to re-apply the clock offset of %s to the restarted instance: %w", instance.ClockOffset, err)
		}
	}
	return nil
}
