// Package app provides the application bootstrap and lifecycle management for muster.
//
// This package implements a clean separation of concerns for the muster application:
//
// # Architecture
//
// The package is organized into several key components:
//
// 1. **Bootstrap (bootstrap.go)**: Handles application initialization, including:
//
//   - Loading configuration from multiple sources
//
//   - Capturing initial Kubernetes context
//
//   - Initializing all required services
//
//   - Determining which mode (CLI/TUI) to run
//
//     2. **Configuration (config.go)**: Defines the application configuration structure
//     that holds all runtime settings including cluster names, UI preferences,
//     and loaded environment configuration.
//
// 3. **Services (services.go)**: Manages service initialization and registration:
//   - Creates the orchestrator with proper configuration
//   - Initializes all API layers
//   - Sets up the MCP aggregator service when needed
//   - Provides a clean service registry for dependency injection
//
// 4. **Modes (modes.go)**: Implements the two execution modes:
//   - CLI Mode: Non-interactive mode for scripting and automation
//   - TUI Mode: Interactive terminal UI for monitoring and control
//
// # Usage
//
// The typical usage pattern is:
//
//	cfg := app.NewConfig(managementCluster, workloadCluster, noTUI, debug)
//	application, err := app.NewApplication(cfg)
//	if err != nil {
//	    return err
//	}
//	return application.Run(ctx)
//
// # Benefits
//
// This structure provides several benefits:
//
// - **Testability**: Each component can be tested in isolation
// - **Clarity**: Clear separation between initialization, configuration, and execution
// - **Flexibility**: Easy to add new modes or modify existing behavior
// - **Maintainability**: Changes to one component don't affect others
//
// # Dependencies
//
// The app package depends on:
// - internal/config: For loading environment configuration
// - internal/orchestrator: For managing services
// - internal/api: For API layer initialization
// - internal/adapters: For adapting APIs to service interfaces
// - internal/tui: For the terminal UI implementation
// - internal/aggregator: For MCP tool aggregation and dynamic service management
package app
