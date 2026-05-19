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
	logging.Info("CLI", "--- Starting muster ---")

	// Startup order:
	// 1. Aggregator — serves muster's /mcp + OAuth endpoints; agentgateway
	//    federates to it so it must be up first.
	// 2. agentgateway subprocess (filesystem mode only).
	// 3. ReconcileManager — workers reconcile MCPServer CRDs into aggregator
	//    upstream registrations via api.GetAggregator().RegisterUpstream.
	if services.AggregatorManager != nil {
		if err := services.AggregatorManager.Start(ctx); err != nil {
			return fmt.Errorf("start aggregator: %w", err)
		}
		logging.Info("CLI", "Aggregator manager started")
	}

	if services.AgentgatewayConfigDir != "" {
		var readinessPort uint16
		if services.AggregatorManager != nil {
			_, _, readinessPort = services.AggregatorManager.AgentgatewayManagementPorts()
		}
		mgr, err := startAgentgatewaySubprocess(ctx, services.AgentgatewayConfigDir, readinessPort)
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

	logging.Info("CLI", "Services started. Press Ctrl+C to stop all services and exit.")

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	// Graceful shutdown: reconciler first so it stops queueing
	// (de)registrations, then the agentgateway subprocess (drops in-flight
	// upstream connections), then the aggregator (drains client-facing
	// requests).
	logging.Info("CLI", "\n--- Shutting down services ---")

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

	if services.AggregatorManager != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), aggregatorShutdownTimeout)
		if err := services.AggregatorManager.Stop(shutCtx); err != nil {
			logging.Error("CLI", err, "Error stopping aggregator")
		}
		cancel()
	}

	return nil
}
