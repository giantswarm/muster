package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

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

var defaultLogger *slog.Logger

// initControllerRuntimeLogger initializes the controller-runtime logger using the provided slog handler.
// This must be called before any controller-runtime operations (informers, caches, etc.) are used,
// otherwise controller-runtime will print warnings about the logger not being initialized and
// status sync operations may fail.
//
// The function creates a logr.Logger from the slog handler and sets it as the controller-runtime
// global logger via ctrl.SetLogger(). This ensures that controller-runtime logs are properly
// routed through the muster logging infrastructure.
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
// This should be called once at application startup to configure structured logging.
//
// Args:
//   - filterLevel: minimum log level to output (Debug, Info, Warn, Error)
//   - output: writer for log output (typically os.Stdout or os.Stderr)
func InitForCLI(filterLevel LogLevel, output io.Writer) {
	opts := &slog.HandlerOptions{
		Level: filterLevel.SlogLevel(),
	}

	handler := slog.NewTextHandler(output, opts)
	defaultLogger = slog.New(handler)
	slog.SetDefault(defaultLogger)

	// Initialize controller-runtime logger to prevent "log.SetLogger(...) was never called" warnings.
	// This bridges the Go slog logger to the logr interface used by controller-runtime.
	// See: https://github.com/go-logr/logr for slog integration details.
	initControllerRuntimeLogger(handler)
}

func logInternal(level LogLevel, subsystem string, err error, messageFmt string, args ...interface{}) {
	// Check if the level is enabled by the configured handler before proceeding.
	if defaultLogger == nil || !defaultLogger.Enabled(context.Background(), level.SlogLevel()) {
		return
	}

	msg := messageFmt
	if len(args) > 0 {
		msg = fmt.Sprintf(messageFmt, args...)
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

// TruncateIdentifier returns a truncated identifier (user subject, session ID, etc.)
// for secure logging. This prevents full identifiers from appearing in logs while
// still providing enough context for debugging correlation.
// Format: first 8 chars + "..." (e.g., "abc12345...")
func TruncateIdentifier(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8] + "..."
}

// AuditEvent represents a structured audit log event for security-sensitive operations.
// These events can be collected by external audit systems for compliance monitoring.
type AuditEvent struct {
	// Action is the type of action being audited (e.g., "token_exchange", "auth_login")
	Action string
	// Outcome indicates whether the action succeeded or failed
	Outcome string // "success" or "failure"
	// Subject is the truncated user subject (sub claim from JWT)
	Subject string
	// UserID is the truncated user identifier (from JWT sub claim or extracted user ID)
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
// [AUDIT] action=token_exchange outcome=success subject=abc12345... user=xyz789... target=mcp-kubernetes
func Audit(event AuditEvent) {
	// Pre-allocate with expected capacity for efficiency
	parts := make([]string, 0, 7)
	parts = append(parts, "action="+event.Action)
	parts = append(parts, "outcome="+event.Outcome)
	if event.Subject != "" {
		parts = append(parts, "subject="+event.Subject)
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
