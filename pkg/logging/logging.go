package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"
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

	if isTuiMode {
		return tuiLogChannel
	}
	return nil
}

// InitForTUI initializes the logging system for TUI mode.
func InitForTUI(filterLevel LogLevel) <-chan LogEntry {
	// For TUI, Initcommon will set up the channel. The filterLevel passed here
	// sets the minimum level for any *direct* output from defaultLogger in TUI mode,
	// which we now set to io.Discard. The TUI itself will filter from the channel.
	return Initcommon("tui", filterLevel, io.Discard, tuiChannelBufferSize)
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

// CloseTUIChannel closes the TUI log channel. Should be called on application shutdown.
func CloseTUIChannel() {
	if tuiLogChannel != nil {
		// Check if channel is already closed to prevent panic
		// This is a bit tricky. A select with a default is one way.
		// For simplicity, we assume it's called once correctly.
		// If TUI is robust, it stops reading when its main loop ends.
		close(tuiLogChannel)
		tuiLogChannel = nil // Prevent further use
	}
}
