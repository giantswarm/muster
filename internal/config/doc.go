// Package config provides configuration management for muster.
//
// This package implements a simple configuration system that loads configuration
// from a single directory. The default configuration directory is ~/.config/muster,
// but users can specify a custom directory using the --config-path flag in commands.
//
// # Configuration Directory
//
// Configuration is loaded from a single directory containing:
//   - config.yaml (main configuration file)
//   - subdirectories for entity definitions (workflows/, capabilities/, serviceclasses/, mcpservers/)
//
// Default location: ~/.config/muster
// Custom location: Specified via --config-path flag
//
// # Entity Storage System
//
// The Storage system provides generic YAML-based persistence for entity definitions
// including workflows, capabilities, serviceclasses, and mcpservers. This unified
// storage system allows users to create, modify, and manage entities through both
// API operations and direct file manipulation.
//
// ## Storage Locations
//
// Entities are stored in YAML files in type-specific subdirectories within the
// configuration directory:
//   - Default: ~/.config/muster/{entityType}/
//   - Custom: {customConfigPath}/{entityType}/
//
// Where {entityType} is one of: workflows, capabilities, serviceclasses, mcpservers
//
// ## Supported Operations
//
// The Storage interface provides CRUD operations:
//   - Save: Store entity data as YAML file
//   - Load: Retrieve entity data from file
//   - Delete: Remove entity file
//   - List: Get all available entity names
//
// ## File Format
//
// All entities are stored as YAML files with .yaml extension.
// Filenames are automatically sanitized to ensure filesystem compatibility.
//
// ## Usage Example
//
//	// Create storage instance (uses default ~/.config/muster)
//	storage := config.NewStorage()
//
//	// Create storage instance with custom path
//	storage := config.NewStorageWithPath("/custom/config/path")
//
//	// Save a workflow
//	workflowYAML := []byte(`name: "my-workflow"
//	description: "Example workflow"
//	steps: []`)
//	err := storage.Save("workflows", "my-workflow", workflowYAML)
//
//	// Load the workflow
//	data, err := storage.Load("workflows", "my-workflow")
//
//	// List all workflows
//	names, err := storage.List("workflows")
//
//	// Delete the workflow
//	err = storage.Delete("workflows", "my-workflow")
//
// # Configuration Structure
//
// The configuration file uses YAML format with the following main sections:
//
//	aggregator:
//	  port: 8090                          # Port for the aggregator service (default: 8090)
//	  host: "localhost"                   # Host to bind to (default: localhost)
//	  transport: "streamable-http"        # Transport to use (default: streamable-http)
//	  enabled: true                       # Whether the aggregator is enabled (default: true)
//	  musterPrefix: "x"                   # Pre-prefix for all tools (default: "x")
//
// # Configuration API
//
// The configuration can be accessed and modified at runtime through the Configuration API.
// The API adapter (ConfigAdapter) is located in the app package rather than here to avoid
// circular import dependencies, as the adapter needs to import the api package for registration,
// while the api package imports this package for type definitions.
//
// # Usage Examples
//
//	// Load configuration from default location
//	cfg, err := config.LoadConfig(config.GetDefaultConfigPathOrPanic())
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Access aggregator configuration
//	fmt.Printf("Aggregator running on %s:%d\n", cfg.Aggregator.Host, cfg.Aggregator.Port)
package config
