package app

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	serv "muster/internal/services"
	"muster/pkg/logging"
)

// run executes the application in non-interactive command line mode.
// This mode is designed for automation, scripting, and headless environments
// where no user interaction is expected.
//
// Behavior:
//   - Starts all configured services through the orchestrator
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

	// Start all configured services
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
	services.Orchestrator.Stop()

	return nil
}
