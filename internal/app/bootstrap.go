package app

import (
	"context"
	"fmt"
	"io"
	"os"

	"muster/internal/config"
	"muster/pkg/logging"
)

// Application represents the main application structure that bootstraps and runs muster.
// It encapsulates all necessary configuration and services required for the application's
// lifecycle, including initialization, service management, and execution mode handling.
//
// The Application follows a two-phase initialization pattern:
//  1. Bootstrap phase: Load configuration, initialize logging, setup services
//  2. Execution phase: Run the orchestrator
//
// Example usage:
//
//	cfg := app.NewConfig(false, true, false, "")  // TUI mode, debug enabled
//	app, err := app.NewApplication(cfg)
//	if err != nil {
//	    return fmt.Errorf("failed to create application: %w", err)
//	}
//	return app.Run(ctx)
type Application struct {
	config   *Config
	services *Services
}

// NewApplication creates and initializes a new application instance with the provided configuration.
// This function performs the complete bootstrap sequence:
//
//  1. Configures logging based on debug settings
//  2. Loads muster configuration (layered or single-path)
//  3. Initializes all required services and API handlers
//  4. Sets up service dependencies and registrations
//
// Configuration Loading Behavior:
//   - If cfg.ConfigPath is set: loads from the specified directory only
//   - If cfg.ConfigPath is empty: uses layered loading (defaults + user + project)
//
// The function returns an error if any critical initialization step fails,
// including configuration loading or service initialization failures.
//
// Example:
//
//	cfg := app.NewConfig(true, false, false, "/custom/config")  // CLI mode, custom config
//	app, err := app.NewApplication(cfg)
//	if err != nil {
//	    log.Fatalf("Bootstrap failed: %v", err)
//	}
func NewApplication(cfg *Config) (*Application, error) {
	// Configure logging based on debug flag
	appLogLevel := logging.LevelInfo
	if cfg.Debug {
		appLogLevel = logging.LevelDebug
	}

	// Initialize logging for CLI output (will be replaced for TUI mode)
	var logOutput io.Writer = os.Stdout
	if cfg.Silent {
		// If silent mode is enabled, suppress all output
		logOutput = io.Discard
	}
	logging.InitForCLI(appLogLevel, logOutput)

	// Load environment configuration
	var musterCfg config.MusterConfig
	var err error

	if cfg.ConfigPath != "" {
		// Use single directory configuration loading
		musterCfg, err = config.LoadConfigFromPath(cfg.ConfigPath)
		if err != nil {
			logging.Error("Bootstrap", err, "Failed to load muster configuration from path: %s", cfg.ConfigPath)
			return nil, fmt.Errorf("failed to load muster configuration from path %s: %w", cfg.ConfigPath, err)
		}
		logging.Info("Bootstrap", "Loaded configuration from custom path: %s", cfg.ConfigPath)
	} else {
		// Use layered configuration loading (default behavior)
		musterCfg, err = config.LoadConfig()
		if err != nil {
			logging.Error("Bootstrap", err, "Failed to load muster configuration")
			return nil, fmt.Errorf("failed to load muster configuration: %w", err)
		}
		logging.Info("Bootstrap", "Loaded configuration using layered approach")
	}

	cfg.MusterConfig = &musterCfg

	// Initialize services
	services, err := InitializeServices(cfg)
	if err != nil {
		logging.Error("Bootstrap", err, "Failed to initialize services")
		return nil, fmt.Errorf("failed to initialize services: %w", err)
	}

	return &Application{
		config:   cfg,
		services: services,
	}, nil
}

// Run executes the application
//
// Handles graceful shutdown via context cancellation and system signals.
// The method blocks until the application is terminated or encounters an error.
//
// Returns an error if the selected execution mode fails to start or encounters
// a runtime error during execution.
func (a *Application) Run(ctx context.Context) error {
	return runOrchestrator(ctx, a.services)
}
