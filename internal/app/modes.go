package app

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	serv "github.com/giantswarm/muster/internal/services"

	"github.com/giantswarm/muster/pkg/logging"
)

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

	aggregatorFailed := false
	sigChan := make(chan os.Signal, 1)
	changeChan := services.Orchestrator.SubscribeToStateChanges()
	go func() {
		for change := range changeChan {
			if change.Name == "mcp-aggregator" && serv.ServiceState(change.NewState) == serv.StateFailed {
				logging.Info("CLI", "MCP Aggregator failed: %v", change)
				aggregatorFailed = true
				sigChan <- nil
				break
			}
		}
	}()

	// IMPORTANT: Startup order matters for capturing all state change events.
	//
	// The StateChangeBridge must subscribe to state changes BEFORE the orchestrator
	// starts, so it can capture all state transitions (unknown -> starting -> running).
	// The ReconcileManager must also be ready before the orchestrator starts so that
	// reconciliation requests triggered by state changes can be processed.
	//
	// Startup order:
	// 1. StateChangeBridge - subscribes to event channel (events buffered until processed)
	// 2. ReconcileManager - starts workers to process reconcile requests
	// 3. Orchestrator - starts services, fires state change events

	// Start the state change bridge first to capture all state change events
	// The bridge subscribes to the orchestrator's event channel (which is already created)
	// Events will be buffered in the channel until they can be processed
	if services.StateChangeBridge != nil {
		if err := services.StateChangeBridge.Start(ctx); err != nil {
			logging.Warn("CLI", "Failed to start state change bridge: %v", err)
			// Continue without state change bridge - not a critical failure
		} else {
			logging.Info("CLI", "State change bridge started - ready to capture state changes")
		}
	}

	// Start the reconciliation manager before the orchestrator so workers are ready
	// to process reconcile requests triggered by state changes during startup
	if services.ReconcileManager != nil {
		if err := services.ReconcileManager.Start(ctx); err != nil {
			logging.Warn("CLI", "Failed to start reconciliation manager: %v", err)
			// Continue without reconciliation - not a critical failure
		} else {
			logging.Info("CLI", "Reconciliation manager started - watching for configuration changes")
		}
	}

	// Start all configured services last - state change events will now be captured
	if err := services.Orchestrator.Start(ctx); err != nil {
		logging.Error("CLI", err, "Failed to start orchestrator")
		return err
	}

	logging.Info("CLI", "Services started. Press Ctrl+C to stop all services and exit.")

	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	if !aggregatorFailed {
		// Wait for interrupt signal or later service startup failure to gracefully shutdown
		<-sigChan
	}

	// Graceful shutdown sequence
	logging.Info("CLI", "\n--- Shutting down services ---")

	// Stop state change bridge first to prevent new reconciliation triggers during shutdown
	if services.StateChangeBridge != nil {
		if err := services.StateChangeBridge.Stop(); err != nil {
			logging.Error("CLI", err, "Error stopping state change bridge")
		}
	}

	// Stop reconciliation manager next to prevent new reconciliations during shutdown
	if services.ReconcileManager != nil {
		if err := services.ReconcileManager.Stop(); err != nil {
			logging.Error("CLI", err, "Error stopping reconciliation manager")
		}
	}

	services.Orchestrator.Stop()

	return nil
}
