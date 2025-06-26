package app

import (
	"context"
	"muster/pkg/logging"
	"os"
	"os/signal"
	"syscall"
)

// runCLIMode executes the application in non-interactive command line mode.
// This mode is designed for automation, scripting, and headless environments
// where no user interaction is expected.
//
// Behavior:
//   - Starts all configured services through the orchestrator
//   - Logs service startup progress to stdout
//   - Blocks waiting for interrupt signals (SIGINT, SIGTERM)
//   - Performs graceful shutdown when signaled
//   - Suitable for systemd services, Docker containers, and CI/CD pipelines
//
// The function handles service lifecycle management and ensures proper cleanup
// on shutdown. All logging output is directed to standard streams for easy
// capture and processing by external tools.
//
// Signal Handling:
//   - SIGINT (Ctrl+C): Triggers graceful shutdown
//   - SIGTERM: Triggers graceful shutdown (common in container environments)
//
// Returns an error if service startup fails or if the orchestrator encounters
// a critical error during operation.
func runCLIMode(ctx context.Context, config *Config, services *Services) error {
	logging.Info("CLI", "Running in no-TUI mode.")
	logging.Info("CLI", "--- Setting up orchestrator for service management ---")

	// Start all configured services
	if err := services.Orchestrator.Start(ctx); err != nil {
		logging.Error("CLI", err, "Failed to start orchestrator")
		return err
	}

	logging.Info("CLI", "Services started. Press Ctrl+C to stop all services and exit.")

	// Wait for interrupt signal to gracefully shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	// Graceful shutdown sequence
	logging.Info("CLI", "\n--- Shutting down services ---")
	services.Orchestrator.Stop()

	return nil
}

// runTUIMode executes the application in interactive terminal UI mode.
// This mode provides a rich terminal interface for real-time monitoring,
// service management, and interactive control of the muster environment.
//
// Current Status:
// The TUI implementation has been temporarily removed from the codebase.
// This function currently logs a notification message and returns immediately.
//
// Planned Features (when TUI is reimplemented):
//   - Real-time service status monitoring
//   - Interactive service start/stop/restart controls
//   - Live log streaming with filtering capabilities
//   - Configuration management through UI forms
//   - Resource usage monitoring and alerts
//   - Workflow execution tracking and control
//
// The TUI mode will switch logging to a channel-based system to integrate
// log messages into the terminal interface without interfering with the UI.
//
// Context Handling:
// The function respects context cancellation for graceful shutdown when
// the TUI is active.
func runTUIMode(ctx context.Context, config *Config, services *Services) error {
	logging.Info("CLI", "Starting TUI mode...")

	// Switch logging to channel-based system for TUI integration
	/*
		logLevel := logging.LevelInfo
		if config.Debug {
			logLevel = logging.LevelDebug
		}
		logChan := logging.InitForTUI(logLevel)
		defer logging.CloseTUIChannel()
	*/
	logging.Info("TUI-Lifecycle", "TUI has been removed.")

	return nil
}
