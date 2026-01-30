package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
)

// LogLevel defines the severity of the log entry.
type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
)

// String makes LogLevel satisfy the fmt.Stringer interface.
func (l LogLevel) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

func (l LogLevel) SlogLevel() slog.Level {
	switch l {
	case LevelDebug:
		return slog.LevelDebug
	case LevelInfo:
		return slog.LevelInfo
	case LevelWarn:
		return slog.LevelWarn
	case LevelError:
		return slog.LevelError
	default:
		return slog.LevelInfo // Default to INFO for unknown
	}
}

// LogEntry is the structured log entry passed to the TUI.
type LogEntry struct {
	Timestamp  time.Time
	Level      LogLevel
	Subsystem  string
	Message    string
	Err        error
	Attributes []slog.Attr // Using slog.Attr for flexibility
}

var (
	defaultLogger *slog.Logger
	tuiLogChannel chan LogEntry
	isTuiMode     bool
	// globalHandlerSlogLevel slog.Level // No longer needed with defaultLogger.Enabled()
)

const tuiChannelBufferSize = 2048

// Initcommon initializes the logger for either TUI or CLI mode.
// This should be called once at application startup.
func Initcommon(mode string, level LogLevel, output io.Writer, channelBufferSize int) <-chan LogEntry {
	opts := &slog.HandlerOptions{
		Level: level.SlogLevel(), // This sets the minimum level for the handler
	}

	var handler slog.Handler
	if mode == "tui" {
		isTuiMode = true
		if channelBufferSize <= 0 {
			channelBufferSize = tuiChannelBufferSize
		}
		tuiLogChannel = make(chan LogEntry, channelBufferSize)
		// For TUI, even if a handler is set up for defaultLogger,
		// logInternal will primarily send to tuiLogChannel.
		// A default handler can be useful for any direct slog calls during TUI init.
		handler = slog.NewTextHandler(io.Discard, opts) // TUI logs via channel; discard direct slog output from defaultLogger
	} else { // cli mode
		isTuiMode = false
		handler = slog.NewTextHandler(output, opts)
	}
	defaultLogger = slog.New(handler)
	slog.SetDefault(defaultLogger) // Set for any global slog calls if necessary

	// Initialize controller-runtime logger to prevent "log.SetLogger(...) was never called" warnings.
	// This bridges the Go slog logger to the logr interface used by controller-runtime.
	// See: https://github.com/go-logr/logr for slog integration details.
	initControllerRuntimeLogger(handler)

	if isTuiMode {
		return tuiLogChannel
	}
	return nil
}

// initControllerRuntimeLogger initializes the controller-runtime logger using the provided slog handler.
// This must be called before any controller-runtime operations (informers, caches, etc.) are used,
// otherwise controller-runtime will print warnings about the logger not being initialized and
// status sync operations may fail.
//
// The function creates a logr.Logger from the slog handler and sets it as the controller-runtime
// global logger via ctrl.SetLogger(). This ensures that controller-runtime logs are properly
// routed through the muster logging infrastructure.
//
// Note: In TUI mode, the handler is set to io.Discard, so controller-runtime logs will also be
// discarded. This is intentional as TUI mode uses a channel-based logging mechanism instead.
func initControllerRuntimeLogger(handler slog.Handler) {
	if handler == nil {
		return
	}

	// Create a logr.Logger from the slog handler
	// logr.FromSlogHandler is available in logr v1.3.0+
	logrLogger := logr.FromSlogHandler(handler)

	// Set the controller-runtime logger
	// This must be called before any controller operations to avoid warnings
	ctrl.SetLogger(logrLogger)
}

// InitForCLI initializes the logging system for CLI mode.
func InitForCLI(filterLevel LogLevel, output io.Writer) {
	Initcommon("cli", filterLevel, output, 0)
}

func logInternal(level LogLevel, subsystem string, err error, messageFmt string, args ...interface{}) {
	// For CLI mode, check if the level is enabled by the configured handler before proceeding.
	// For TUI mode, we always send to the channel; TUI will do its own filtering/display logic.
	if !isTuiMode {
		if defaultLogger == nil || !defaultLogger.Enabled(context.Background(), level.SlogLevel()) {
			return // Suppress log if not in TUI mode and level is not enabled for CLI
		}
	}

	msg := messageFmt
	if len(args) > 0 {
		msg = fmt.Sprintf(messageFmt, args...)
	}
	now := time.Now()

	if isTuiMode {
		if tuiLogChannel != nil {
			entry := LogEntry{
				Timestamp: now,
				Level:     level,
				Subsystem: subsystem,
				Message:   msg,
				Err:       err,
			}
			select {
			case tuiLogChannel <- entry:
				// Sent successfully
			default:
				// Channel full or closed, log to stderr as fallback for TUI log loss
				fmt.Fprintf(os.Stderr, "[LOGGING_CRITICAL] TUI log channel full/closed. Dropping: %s [%s] %s\n", now.Format(time.RFC3339), level, msg)
			}
		} else {
			fmt.Fprintf(os.Stderr, "[LOGGING_CRITICAL] TUI mode active but tuiLogChannel is nil. Log: %s [%s] %s\n", now.Format(time.RFC3339), level, msg)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  Error: %v\n", err)
			}
		}
		return // In TUI mode, primary path is the channel, even if defaultLogger is set.
	}

	// CLI mode logging (only reached if level was enabled)
	if defaultLogger == nil { // Should not happen if level was enabled, but as a safeguard.
		fmt.Fprintf(os.Stderr, "[LOGGING_ERROR] Logger not initialized. Log: %s [%s] %s\n", now.Format(time.RFC3339), level, msg)
		return
	}

	var slogAttrs []slog.Attr
	slogAttrs = append(slogAttrs, slog.String("subsystem", subsystem))
	if err != nil {
		slogAttrs = append(slogAttrs, slog.String("error", err.Error()))
	}

	defaultLogger.LogAttrs(context.Background(), level.SlogLevel(), msg, slogAttrs...)
}

// Debug logs a debug message.
func Debug(subsystem string, messageFmt string, args ...interface{}) {
	logInternal(LevelDebug, subsystem, nil, messageFmt, args...)
}

// Info logs an informational message.
func Info(subsystem string, messageFmt string, args ...interface{}) {
	logInternal(LevelInfo, subsystem, nil, messageFmt, args...)
}

// Warn logs a warning message.
func Warn(subsystem string, messageFmt string, args ...interface{}) {
	logInternal(LevelWarn, subsystem, nil, messageFmt, args...)
}

// Error logs an error message.
func Error(subsystem string, err error, messageFmt string, args ...interface{}) {
	logInternal(LevelError, subsystem, err, messageFmt, args...)
}

// TruncateSessionID returns a truncated session ID for secure logging.
// This prevents full session IDs from appearing in logs while still
// providing enough context for debugging correlation.
// Format: first 8 chars + "..." (e.g., "abc12345...")
func TruncateSessionID(sessionID string) string {
	if len(sessionID) <= 8 {
		return sessionID
	}
	return sessionID[:8] + "..."
}

// AuditEvent represents a structured audit log event for security-sensitive operations.
// These events can be collected by external audit systems for compliance monitoring.
type AuditEvent struct {
	// Action is the type of action being audited (e.g., "token_exchange", "auth_login")
	Action string
	// Outcome indicates whether the action succeeded or failed
	Outcome string // "success" or "failure"
	// SessionID is the truncated session identifier
	SessionID string
	// UserID is the truncated user identifier (from JWT sub claim)
	UserID string
	// Target is the target of the action (e.g., server name, endpoint)
	Target string
	// Details provides additional context-specific information
	Details string
	// Error contains the error message if Outcome is "failure"
	Error string
}

// Audit logs a structured audit event for security-sensitive operations.
// Audit events are always logged at INFO level and include a special [AUDIT] prefix
// to make them easily filterable by log aggregation systems.
//
// Example output:
// [AUDIT] action=token_exchange outcome=success session=abc12345... user=xyz789... target=mcp-kubernetes
func Audit(event AuditEvent) {
	// Pre-allocate with expected capacity for efficiency
	parts := make([]string, 0, 7)
	parts = append(parts, "action="+event.Action)
	parts = append(parts, "outcome="+event.Outcome)
	if event.SessionID != "" {
		parts = append(parts, "session="+event.SessionID)
	}
	if event.UserID != "" {
		parts = append(parts, "user="+event.UserID)
	}
	if event.Target != "" {
		parts = append(parts, "target="+event.Target)
	}
	if event.Details != "" {
		parts = append(parts, "details="+event.Details)
	}
	if event.Error != "" {
		parts = append(parts, "error="+event.Error)
	}

	logInternal(LevelInfo, "AUDIT", nil, "[AUDIT] %s", strings.Join(parts, " "))
}
