// Package agent provides comprehensive MCP (Model Context Protocol) client and server
// implementations for debugging, testing, and integrating with the muster aggregator.

package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// ANSI color codes for terminal output formatting
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorGray   = "\033[90m"
)

// Logger provides structured logging for the agent with multiple output modes.
// It supports different logging levels, output formats, and destinations to
// accommodate various use cases from interactive debugging to automated testing.
//
// Key features:
//   - Multiple output modes: simple, verbose, JSON-RPC protocol debugging
//   - Color-coded output for better readability in terminals
//   - Configurable output destinations (stdout, files, custom writers)
//   - MCP protocol-aware formatting for requests, responses, and notifications
//   - User-facing output separation from system logging
//
// Logging modes:
//   - Simple mode: User-friendly status messages without technical details
//   - Verbose mode: Detailed operation tracking and debug information
//   - JSON-RPC mode: Complete protocol debugging with full message content
type Logger struct {
	verbose     bool      // Enable verbose debug output
	useColor    bool      // Use ANSI color codes in output
	jsonRPCMode bool      // Enable full JSON-RPC protocol logging
	writer      io.Writer // Destination for log output (default: stdout)
}

// SetVerbose enables or disables verbose logging mode.
// When verbose mode is enabled, the logger will output debug messages
// and more detailed information about operations, including protocol
// details and internal state changes.
//
// Args:
//   - verbose: Whether to enable verbose output
//
// This is useful for debugging and development scenarios where you need
// detailed insight into the agent's operation.
func (l *Logger) SetVerbose(verbose bool) {
	l.verbose = verbose
}

// SetWriter sets a custom writer for the logger output.
// By default, the logger writes to stdout, but this can be changed
// to write to files, buffers, or other destinations for testing
// or log aggregation purposes.
//
// Args:
//   - w: The io.Writer to use for log output
//
// Example:
//
//	var buffer bytes.Buffer
//	logger.SetWriter(&buffer)
//	// Now all log output goes to the buffer instead of stdout
func (l *Logger) SetWriter(w io.Writer) {
	l.writer = w
}

// NewDevNullLogger creates a logger that discards all output.
// This is useful for testing scenarios and automated scripts where
// logging output should be suppressed completely to avoid noise
// in test output or production logs.
//
// Returns:
//   - Logger instance that discards all output
//
// Example:
//
//	logger := agent.NewDevNullLogger()
//	client := agent.NewClient(endpoint, logger, transport)
//	// All logging output will be discarded
func NewDevNullLogger() *Logger {
	return &Logger{
		verbose:     false,
		useColor:    false,
		jsonRPCMode: false,
		writer:      io.Discard,
	}
}

// NewLogger creates a new logger with the specified configuration.
// This is the primary constructor for logger instances with customizable
// behavior for different use cases.
//
// Args:
//   - verbose: Enable detailed debug output and operation tracking
//   - useColor: Use ANSI color codes for enhanced terminal readability
//   - jsonRPCMode: Enable complete JSON-RPC protocol message logging
//
// Returns:
//   - Configured logger instance writing to stdout by default
//
// Example:
//
//	// Interactive debugging with colors and full protocol logging
//	logger := agent.NewLogger(true, true, true)
//
//	// Production mode with minimal output
//	logger := agent.NewLogger(false, false, false)
func NewLogger(verbose, useColor, jsonRPCMode bool) *Logger {
	return &Logger{
		verbose:     verbose,
		useColor:    useColor,
		jsonRPCMode: jsonRPCMode,
		writer:      os.Stdout, // Default to stdout
	}
}

// Output writes user-facing output directly to stdout without timestamps.
// This method is used for command results, formatted data, and other content
// that should be displayed to users without logging metadata.
//
// Unlike other logging methods, Output always writes to stdout regardless
// of the configured writer, ensuring user-facing content is properly displayed.
//
// Args:
//   - format: Printf-style format string (or plain text if no args provided)
//   - args: Arguments for the format string
//
// This method is primarily used by command implementations to display
// results and formatted data to the user.
func (l *Logger) Output(format string, args ...interface{}) {
	if len(args) == 0 {
		// No args - print format string literally to avoid treating % as format specifiers
		// (important for URLs with percent-encoding like %2F, %3A)
		fmt.Fprint(os.Stdout, format)
	} else {
		fmt.Fprintf(os.Stdout, format, args...)
	}
}

// OutputLine writes user-facing output with a newline.
// This is a convenience wrapper around Output that automatically adds a newline.
//
// Args:
//   - format: Printf-style format string (or plain text if no args provided)
//   - args: Arguments for the format string
func (l *Logger) OutputLine(format string, args ...interface{}) {
	if len(args) == 0 {
		// No args - print format string literally to avoid treating % as format specifiers
		// (important for URLs with percent-encoding like %2F, %3A)
		fmt.Fprintln(os.Stdout, format)
	} else {
		fmt.Fprintf(os.Stdout, format+"\n", args...)
	}
}

// timestamp returns the current timestamp string in a consistent format.
// Used internally by logging methods to provide temporal context for log entries.
func (l *Logger) timestamp() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

// colorize applies ANSI color codes to text if colors are enabled.
// This method handles color formatting consistently across all logging methods.
//
// Args:
//   - text: The text to colorize
//   - colorCode: ANSI color code constant
//
// Returns:
//   - Colorized text if colors are enabled, otherwise original text
func (l *Logger) colorize(text, colorCode string) string {
	if !l.useColor {
		return text
	}
	return fmt.Sprintf("%s%s%s", colorCode, text, colorReset)
}

// Info logs an informational message with timestamp.
// This is used for general status updates and operational information
// that should be visible in normal operation.
//
// Args:
//   - format: Printf-style format string
//   - args: Arguments for the format string
//
// Output format: [timestamp] message
func (l *Logger) Info(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(l.writer, "[%s] %s\n", l.timestamp(), msg)
}

// Debug logs a debug message that is only shown in verbose mode.
// This is used for detailed operation tracking and troubleshooting
// information that would be too noisy for normal operation.
//
// Args:
//   - format: Printf-style format string
//   - args: Arguments for the format string
//
// The message is colored gray when colors are enabled and only
// appears when verbose mode is active.
func (l *Logger) Debug(format string, args ...interface{}) {
	if !l.verbose {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(l.writer, "[%s] %s\n", l.timestamp(), l.colorize(msg, colorGray))
}

// Error logs an error message with timestamp and red coloring.
// This is used for error conditions, failures, and other problems
// that need immediate attention.
//
// Args:
//   - format: Printf-style format string
//   - args: Arguments for the format string
//
// Output format: [timestamp] message (in red when colors enabled)
func (l *Logger) Error(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(l.writer, "[%s] %s\n", l.timestamp(), l.colorize(msg, colorRed))
}

// Success logs a success message with timestamp and green coloring.
// This is used for successful operations, completed tasks, and
// positive status updates.
//
// Args:
//   - format: Printf-style format string
//   - args: Arguments for the format string
//
// Output format: [timestamp] message (in green when colors enabled)
func (l *Logger) Success(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(l.writer, "[%s] %s\n", l.timestamp(), l.colorize(msg, colorGreen))
}

// Request logs an outgoing MCP request with appropriate formatting.
// The behavior depends on the logging mode: simple mode shows user-friendly
// messages, while JSON-RPC mode shows complete protocol details.
//
// Args:
//   - method: The MCP method name (e.g., "tools/list", "initialize")
//   - params: The request args (logged in JSON-RPC mode only)
//
// In simple mode, this maps method names to user-friendly messages.
// In JSON-RPC mode, this shows complete request details with proper formatting.
func (l *Logger) Request(method string, params interface{}) {
	if !l.jsonRPCMode {
		// Simple mode - just log what we're doing with user-friendly messages
		switch method {
		case "initialize":
			l.Info("Initializing MCP session...")
		case "tools/list":
			l.Info("Listing available tools...")
		case "resources/list":
			l.Info("Listing available resources...")
		case "prompts/list":
			l.Info("Listing available prompts...")
		default:
			l.Info("Sending request: %s", method)
		}
		return
	}

	// JSON-RPC mode - full protocol logging with color coding
	arrow := l.colorize("→", colorBlue)
	methodStr := l.colorize(fmt.Sprintf("REQUEST (%s)", method), colorBlue)

	fmt.Fprintf(l.writer, "[%s] %s %s:\n", l.timestamp(), arrow, methodStr)

	// Pretty print the params if available
	if params != nil {
		jsonStr := l.prettyJSON(params)
		fmt.Fprintln(l.writer, l.colorize(jsonStr, colorBlue))
	}
	fmt.Fprintln(l.writer)
}

// Response logs an incoming MCP response with appropriate formatting.
// The behavior depends on the logging mode: simple mode shows user-friendly
// summaries, while JSON-RPC mode shows complete response details.
//
// Args:
//   - method: The MCP method name this response corresponds to
//   - result: The response result (logged in JSON-RPC mode only)
//
// In simple mode, this extracts meaningful information like counts and
// status. In JSON-RPC mode, this shows complete response details.
func (l *Logger) Response(method string, result interface{}) {
	if !l.jsonRPCMode {
		// Simple mode - log meaningful information with user-friendly messages
		switch method {
		case "initialize":
			// Extract protocol version if possible
			if initResult, ok := result.(map[string]interface{}); ok {
				if protocolVersion, exists := initResult["protocolVersion"]; exists {
					l.Success("Session initialized successfully (protocol: %v)", protocolVersion)
				} else {
					l.Success("Session initialized successfully")
				}
			} else {
				l.Success("Session initialized successfully")
			}
		case "tools/list":
			// Try to count tools
			toolCount := l.countTools(result)
			if toolCount >= 0 {
				l.Success("Found %d tools", toolCount)
			} else {
				l.Success("Retrieved tool list")
			}
		case "resources/list":
			// Try to count resources
			resourceCount := l.countResources(result)
			if resourceCount >= 0 {
				l.Success("Found %d resources", resourceCount)
			} else {
				l.Success("Retrieved resource list")
			}
		case "prompts/list":
			// Try to count prompts
			promptCount := l.countPrompts(result)
			if promptCount >= 0 {
				l.Success("Found %d prompts", promptCount)
			} else {
				l.Success("Retrieved prompt list")
			}
		default:
			l.Success("Received response for: %s", method)
		}
		return
	}

	// JSON-RPC mode - full protocol logging with color coding
	arrow := l.colorize("←", colorGreen)
	methodStr := l.colorize(fmt.Sprintf("RESPONSE (%s)", method), colorGreen)

	fmt.Fprintf(l.writer, "[%s] %s %s:\n", l.timestamp(), arrow, methodStr)

	// Pretty print the result if available
	if result != nil {
		jsonStr := l.prettyJSON(result)
		fmt.Fprintln(l.writer, l.colorize(jsonStr, colorGreen))
	}
	fmt.Fprintln(l.writer)
}

// Notification logs an incoming MCP notification with appropriate formatting.
// Notifications are typically sent by the server to indicate capability changes
// or other events that the client should be aware of.
//
// Args:
//   - method: The notification method name (e.g., "notifications/tools/list_changed")
//   - params: The notification args (logged in JSON-RPC mode only)
//
// Some notifications like keepalive are filtered in simple mode unless
// verbose output is enabled. JSON-RPC mode shows all notification details.
func (l *Logger) Notification(method string, params interface{}) {
	// Skip keepalive notifications unless in verbose mode to reduce noise
	if method == "$/keepalive" && !l.verbose {
		return
	}

	if !l.jsonRPCMode {
		// Simple mode - just log the notification type with user-friendly messages
		switch method {
		case "notifications/tools/list_changed":
			l.Info("Tools list changed! Fetching updated list...")
		case "notifications/resources/list_changed":
			l.Info("Resources list changed! Fetching updated list...")
		case "notifications/prompts/list_changed":
			l.Info("Prompts list changed! Fetching updated list...")
		default:
			if l.verbose {
				l.Debug("Received notification: %s", method)
			}
		}
		return
	}

	// JSON-RPC mode - full protocol logging with color coding
	arrow := l.colorize("←", colorYellow)
	methodStr := l.colorize(fmt.Sprintf("NOTIFICATION (%s)", method), colorYellow)

	fmt.Fprintf(l.writer, "[%s] %s %s:\n", l.timestamp(), arrow, methodStr)

	// Pretty print the params if available
	if params != nil {
		jsonStr := l.prettyJSON(params)
		fmt.Fprintln(l.writer, l.colorize(jsonStr, colorYellow))
	}
	fmt.Fprintln(l.writer)
}

// prettyJSON formats JSON for display with proper indentation.
// This method handles the complexity of JSON-RPC message formatting
// and provides fallback formatting for complex structures.
//
// Args:
//   - v: The value to format as JSON
//
// Returns:
//   - Formatted JSON string with proper indentation
func (l *Logger) prettyJSON(v interface{}) string {
	// Create a wrapper that includes the full JSON-RPC structure if needed
	wrapper := make(map[string]interface{})

	switch val := v.(type) {
	case map[string]interface{}:
		// Already a map, use as-is
		wrapper = val
	default:
		// Wrap in a simple structure
		wrapper["jsonrpc"] = "2.0"

		// Try to detect the type and structure appropriately
		switch vt := v.(type) {
		case struct {
			ProtocolVersion string      `json:"protocolVersion"`
			Capabilities    interface{} `json:"capabilities"`
			ClientInfo      interface{} `json:"clientInfo"`
		}:
			// Initialize request params
			wrapper["method"] = "initialize"
			wrapper["params"] = v
			wrapper["id"] = 1
		default:
			// Generic wrapping
			wrapper["result"] = v
			_ = vt // avoid unused variable warning
		}
	}

	// Use the consolidated PrettyJSON for the actual formatting
	jsonData, err := json.MarshalIndent(wrapper, "", "    ")
	if err != nil {
		return ""
	}
	return string(jsonData)
}

// Write implements io.Writer for compatibility with other systems.
// This allows the logger to be used as a writer destination for
// other components that expect an io.Writer interface.
//
// All writes are treated as debug messages and subject to verbose mode filtering.
func (l *Logger) Write(p []byte) (n int, err error) {
	l.Debug("%s", string(p))
	return len(p), nil
}

// countTools attempts to count the number of tools in a tools/list response.
// This is used by the simple logging mode to provide meaningful summary
// information instead of raw protocol details.
//
// Args:
//   - result: The response result from a tools/list operation
//
// Returns:
//   - Number of tools found, or -1 if count cannot be determined
func (l *Logger) countTools(result interface{}) int {
	// Try to extract tools array from various response structures
	switch v := result.(type) {
	case map[string]interface{}:
		if tools, ok := v["tools"]; ok {
			if toolsArray, ok := tools.([]interface{}); ok {
				return len(toolsArray)
			}
		}
	}

	// Try type assertion for the specific ListToolsResult type
	type toolsResult struct {
		Tools []interface{} `json:"tools"`
	}

	if jsonBytes, err := json.Marshal(result); err == nil {
		var tr toolsResult
		if err := json.Unmarshal(jsonBytes, &tr); err == nil && tr.Tools != nil {
			return len(tr.Tools)
		}
	}

	return -1 // Indicate we couldn't count
}

// countResources attempts to count the number of resources in a resources/list response.
// This is used by the simple logging mode to provide meaningful summary
// information instead of raw protocol details.
//
// Args:
//   - result: The response result from a resources/list operation
//
// Returns:
//   - Number of resources found, or -1 if count cannot be determined
func (l *Logger) countResources(result interface{}) int {
	// Try to extract resources array from various response structures
	switch v := result.(type) {
	case map[string]interface{}:
		if resources, ok := v["resources"]; ok {
			if resourcesArray, ok := resources.([]interface{}); ok {
				return len(resourcesArray)
			}
		}
	}

	// Try type assertion for the specific ListResourcesResult type
	type resourcesResult struct {
		Resources []interface{} `json:"resources"`
	}

	if jsonBytes, err := json.Marshal(result); err == nil {
		var rr resourcesResult
		if err := json.Unmarshal(jsonBytes, &rr); err == nil && rr.Resources != nil {
			return len(rr.Resources)
		}
	}

	return -1 // Indicate we couldn't count
}

// countPrompts attempts to count the number of prompts in a prompts/list response.
// This is used by the simple logging mode to provide meaningful summary
// information instead of raw protocol details.
//
// Args:
//   - result: The response result from a prompts/list operation
//
// Returns:
//   - Number of prompts found, or -1 if count cannot be determined
func (l *Logger) countPrompts(result interface{}) int {
	// Try to extract prompts array from various response structures
	switch v := result.(type) {
	case map[string]interface{}:
		if prompts, ok := v["prompts"]; ok {
			if promptsArray, ok := prompts.([]interface{}); ok {
				return len(promptsArray)
			}
		}
	}

	// Try type assertion for the specific ListPromptsResult type
	type promptsResult struct {
		Prompts []interface{} `json:"prompts"`
	}

	if jsonBytes, err := json.Marshal(result); err == nil {
		var pr promptsResult
		if err := json.Unmarshal(jsonBytes, &pr); err == nil && pr.Prompts != nil {
			return len(pr.Prompts)
		}
	}

	return -1 // Indicate we couldn't count
}
