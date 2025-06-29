// Package logging provides a structured logging system for muster that supports both
// CLI and TUI execution modes with unified log handling and flexible output formatting.
//
// This package implements a dual-mode logging architecture that can operate in either
// CLI mode (direct output) or TUI mode (channel-based message passing), enabling
// consistent logging behavior across different user interface paradigms.
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
// ## Execution Modes
//   - **CLI Mode**: Direct logging to specified output writer (stdout/stderr)
//   - **TUI Mode**: Logging via buffered channel for consumption by terminal UI
//
// ## Structured Logging
// All log entries include:
//   - Timestamp with nanosecond precision
//   - Log level (Debug, Info, Warn, Error)
//   - Subsystem identifier for categorization
//   - Message content with optional formatting
//   - Optional error information
//   - Structured attributes using slog.Attr
//
// # Dual-Mode Operation
//
// ## CLI Mode
// When initialized for CLI mode:
//   - Logs are written directly to the specified output writer
//   - Uses structured text format via slog.TextHandler
//   - Respects configured log level filtering
//   - Suitable for command-line tools and automation
//
// ## TUI Mode
// When initialized for TUI mode:
//   - Logs are sent to a buffered channel for UI consumption
//   - TUI component reads from channel and handles display/filtering
//   - Fallback to stderr if channel is full or unavailable
//   - Enables rich terminal UI with interactive log viewing
//
// # Usage Examples
//
// ## CLI Mode Initialization
//
//	import "muster/pkg/logging"
//
//	// Initialize for CLI with Info level logging to stdout
//	logging.InitForCLI(logging.LevelInfo, os.Stdout)
//
//	// Log messages
//	logging.Info("Bootstrap", "Application starting up")
//	logging.Debug("Config", "Loaded configuration from %s", configPath)
//	logging.Warn("Service", "Service dependency not available")
//	logging.Error("Database", err, "Failed to connect to database")
//
// ## TUI Mode Initialization
//
//	import "muster/pkg/logging"
//
//	// Initialize for TUI with Debug level
//	logChannel := logging.InitForTUI(logging.LevelDebug)
//
//	// Start goroutine to consume log entries
//	go func() {
//	    for entry := range logChannel {
//	        // Process log entry in TUI
//	        displayLogEntry(entry)
//	    }
//	}()
//
//	// Log messages (same API as CLI mode)
//	logging.Info("TUI", "Terminal interface initialized")
//	logging.Debug("Input", "User pressed key: %c", key)
//
// ## Advanced Initialization
//
//	// Custom channel buffer size for TUI mode
//	logChannel := logging.Initcommon("tui", logging.LevelInfo, os.Stdout, 4096)
//
//	// CLI mode with custom writer
//	logFile, _ := os.OpenFile("app.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
//	logging.InitForCLI(logging.LevelDebug, logFile)
//
// # Log Entry Structure
//
// Each log entry contains comprehensive information:
//
//	type LogEntry struct {
//	    Timestamp  time.Time   // When the log entry was created
//	    Level      LogLevel    // Severity level
//	    Subsystem  string      // Component that generated the log
//	    Message    string      // Log message content
//	    Err        error       // Optional associated error
//	    Attributes []slog.Attr // Additional structured data
//	}
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
//   - **Capability**: Capability system operations
//   - **Workflow**: Workflow execution and management
//   - **Agent**: MCP agent and client operations
//   - **TUI**: Terminal user interface operations
//   - **API**: API layer operations and handler management
//
// # Integration with slog
//
// The logging system integrates with Go's standard slog package:
//   - Uses slog.Handler implementations for output formatting
//   - Converts custom LogLevel to slog.Level for compatibility
//   - Supports slog.Attr for structured logging attributes
//   - Provides fallback to global slog logger when needed
//
// # Error Handling and Reliability
//
// The logging system handles various failure scenarios:
//
// ## Channel Overflow (TUI Mode)
//   - Non-blocking channel sends with fallback to stderr
//   - Buffer size configuration to prevent overflow
//   - Critical error messages when log delivery fails
//
// ## Initialization Failures
//   - Graceful fallback to stderr for uninitialized logger
//   - Clear error messages when logging system is not ready
//   - Safe operation even with nil handlers or channels
//
// ## Mode Switching
//   - Clean shutdown of TUI channel with CloseTUIChannel()
//   - Prevention of further use after channel closure
//   - Safe concurrent access to logging state
//
// # Performance Characteristics
//
// ## CLI Mode
//   - Direct write to output with minimal overhead
//   - Level filtering at handler level for efficiency
//   - No memory allocation for filtered-out messages
//
// ## TUI Mode
//   - Buffered channel prevents UI blocking
//   - Configurable buffer size (default 2048 entries)
//   - Non-blocking sends with overflow handling
//   - Memory-efficient message passing
//
// # Thread Safety
//
// The logging system is fully thread-safe:
//   - Safe concurrent logging from multiple goroutines
//   - Protected access to shared logging state
//   - Channel operations designed for concurrent use
//   - No data races in mode switching or configuration
//
// # Cleanup and Shutdown
//
// Proper cleanup is essential for TUI mode:
//
//	// Clean shutdown in TUI mode
//	defer logging.CloseTUIChannel()
//
// This ensures:
//   - TUI log channel is properly closed
//   - No goroutine leaks from channel readers
//   - Clean application termination
//
// The logging package provides a robust foundation for muster's diagnostic
// and monitoring capabilities across both interactive and non-interactive
// execution modes.
package logging
