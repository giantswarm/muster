package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/giantswarm/muster/pkg/logging"
)

// agentgatewayShutdownTimeout bounds graceful agentgateway shutdown with a
// fresh context so a cancelled parent doesn't preempt the drain.
const agentgatewayShutdownTimeout = 15 * time.Second

// aggregatorShutdownTimeout bounds graceful aggregator shutdown with a fresh
// context so a cancelled parent doesn't preempt the drain.
const aggregatorShutdownTimeout = 10 * time.Second

// run executes the application in non-interactive command line mode.
// This mode is designed for automation, scripting, and headless environments
// where no user interaction is expected.
//
// Behavior:
//   - Starts all configured services through the orchestrator
//   - Starts the reconciliation manager for automatic change detection
//   - Logs service startup progress to stdout
//   - Blocks waiting for interrupt signals (SIGINT, SIGTERM)
//   - Performs graceful shutdown when signaled
//   - Suitable for systemd services, run in a Kubernetes cluster
//
// The function handles service lifecycle management and ensures proper cleanup
// on shutdown. All logging output is directed to standard streams for easy
// capture and processing by external tools.
//
// Signal Handling:
//   - SIGINT (Ctrl+C): Triggers graceful shutdown
//   - SIGTERM: Triggers graceful shutdown
//
// Returns an error if service startup fails or if the orchestrator encounters
// a critical error during operation.
func runOrchestrator(ctx context.Context, services *Services) error {
	logging.Info("CLI", "--- Setting up orchestrator for service management ---")

	// Startup order:
	// 1. StateChangeBridge — subscribes to the orchestrator's event channel
	//    before any state transitions fire.
	// 2. Aggregator — serves muster's /mcp + OAuth endpoints; agentgateway
	//    federates to it so it must be up first.
	// 3. agentgateway subprocess (filesystem mode only).
	// 4. ReconcileManager — workers ready before the orchestrator fires events.
	// 5. Orchestrator — runs remaining managed services.
	if services.StateChangeBridge != nil {
		if err := services.StateChangeBridge.Start(ctx); err != nil {
			logging.Warn("CLI", "Failed to start state change bridge: %v", err)
		} else {
			logging.Info("CLI", "State change bridge started - ready to capture state changes")
		}
	}

	if services.Aggregator != nil {
		if err := services.Aggregator.Start(ctx); err != nil {
			return fmt.Errorf("start aggregator: %w", err)
		}
		logging.Info("CLI", "Aggregator started directly (out of orchestrator service registry)")
	}

	if services.AgentgatewayConfigDir != "" {
		mgr, err := startAgentgatewaySubprocess(ctx, services.AgentgatewayConfigDir)
		if err != nil {
			return fmt.Errorf("start agentgateway subprocess: %w", err)
		}
		services.AgentgatewayManager = mgr
	}

	if services.ReconcileManager != nil {
		if err := services.ReconcileManager.Start(ctx); err != nil {
			logging.Warn("CLI", "Failed to start reconciliation manager: %v", err)
		} else {
			logging.Info("CLI", "Reconciliation manager started - watching for configuration changes")
		}
	}

	if err := services.Orchestrator.Start(ctx); err != nil {
		logging.Error("CLI", err, "Failed to start orchestrator")
		return err
	}

	logging.Info("CLI", "Services started. Press Ctrl+C to stop all services and exit.")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	// Graceful shutdown sequence (reverse of startup, with aggregator stopping
	// before the orchestrator so its API consumers wind down cleanly).
	logging.Info("CLI", "\n--- Shutting down services ---")

	if services.StateChangeBridge != nil {
		if err := services.StateChangeBridge.Stop(); err != nil {
			logging.Error("CLI", err, "Error stopping state change bridge")
		}
	}

	if services.ReconcileManager != nil {
		if err := services.ReconcileManager.Stop(); err != nil {
			logging.Error("CLI", err, "Error stopping reconciliation manager")
		}
	}

	if services.AgentgatewayManager != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), agentgatewayShutdownTimeout)
		if err := services.AgentgatewayManager.Stop(shutCtx); err != nil {
			logging.Error("CLI", err, "Error stopping agentgateway subprocess")
		}
		cancel()
	}

	if services.Aggregator != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), aggregatorShutdownTimeout)
		if err := services.Aggregator.Stop(shutCtx); err != nil {
			logging.Error("CLI", err, "Error stopping aggregator")
		}
		cancel()
	}

	_ = services.Orchestrator.Stop()

	return nil
}
