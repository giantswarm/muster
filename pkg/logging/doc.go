// Package logging provides a structured logging system for muster with unified
// log handling and flexible output formatting.
//
// This package implements a logging system built on Go's standard slog package,
// providing consistent logging behavior with structured output and level filtering.
//
// # Architecture
//
// The logging system is built around these core concepts:
//
// ## Log Levels
//   - **Debug**: Detailed information for debugging and development
//   - **Info**: General informational messages about application operation
//   - **Warn**: Warning messages that indicate potential issues
//   - **Error**: Error messages for failures and exceptional conditions
//
// ## Structured Logging
// All log entries include:
//   - Timestamp with nanosecond precision
//   - Log level (Debug, Info, Warn, Error)
//   - Subsystem identifier for categorization
//   - Message content with optional formatting
//   - Optional error information
//
// # Usage Examples
//
// ## Initialization
//
//	import "github.com/giantswarm/muster/pkg/logging"
//
//	// Initialize with Info level logging to stdout
//	logging.InitForCLI(logging.LevelInfo, os.Stdout)
//
//	// Log messages
//	logging.Info("Bootstrap", "Application starting up")
//	logging.Debug("Config", "Loaded configuration from %s", configPath)
//	logging.Warn("Service", "Service dependency not available")
//	logging.Error("Database", err, "Failed to connect to database")
//
// ## Custom Output Writer
//
//	// CLI mode with custom writer
//	logFile, _ := os.OpenFile("app.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
//	logging.InitForCLI(logging.LevelDebug, logFile)
//
// # Subsystem Organization
//
// Logs are organized by subsystem to enable filtering and categorization:
//
//   - **Bootstrap**: Application initialization and startup
//   - **Config**: Configuration loading and validation
//   - **Orchestrator**: Service lifecycle management
//   - **Aggregator**: MCP tool aggregation and management
//   - **ServiceClass**: ServiceClass definition and instance management
//   - **Workflow**: Workflow execution and management
//   - **Agent**: MCP agent and client operations
//   - **API**: API layer operations and handler management
//
// # Integration with slog
//
// The logging system integrates with Go's standard slog package:
//   - Uses slog.Handler implementations for output formatting
//   - Converts custom LogLevel to slog.Level for compatibility
//   - Provides fallback to global slog logger when needed
//
// # Controller-Runtime Integration
//
// The logging system automatically initializes the controller-runtime logger
// when InitForCLI is called. This ensures that Kubernetes controller-runtime
// operations (informers, caches, etc.) log through the muster logging
// infrastructure without warnings about uninitialized loggers.
//
// # Performance Characteristics
//
//   - Direct write to output with minimal overhead
//   - Level filtering at handler level for efficiency
//   - No memory allocation for filtered-out messages
//
// # Thread Safety
//
// The logging system is fully thread-safe:
//   - Safe concurrent logging from multiple goroutines
//   - Protected access to shared logging state
//   - No data races in configuration
//
// # Audit Logging
//
// The package provides specialized audit logging for security-sensitive operations:
//
//	logging.Audit(logging.AuditEvent{
//	    Action:    "token_exchange",
//	    Outcome:   "success",
//	    SessionID: logging.TruncateSessionID(sessionID),
//	    Target:    "mcp-kubernetes",
//	})
//
// Audit events are logged at INFO level with an [AUDIT] prefix for easy filtering
// by log aggregation systems.
//
// The logging package provides a robust foundation for muster's diagnostic
// and monitoring capabilities.
package logging
