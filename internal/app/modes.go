package app

import (
	"context"
	"muster/pkg/logging"
	"os"
	"os/signal"
	"syscall"
)

// runCLIMode executes the non-interactive command line mode
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

// runTUIMode executes the interactive terminal UI mode
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
