package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// ANSI color codes
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorGray   = "\033[90m"
)

// Logger provides formatted logging for the agent
type Logger struct {
	verbose     bool
	useColor    bool
	jsonRPCMode bool
	writer      io.Writer
}

// SetVerbose sets the verbose mode
func (l *Logger) SetVerbose(verbose bool) {
	l.verbose = verbose
}

// SetWriter sets a custom writer for the logger
func (l *Logger) SetWriter(w io.Writer) {
	l.writer = w
}

func NewDevNullLogger() *Logger {
	return &Logger{
		verbose:     false,
		useColor:    false,
		jsonRPCMode: false,
		writer:      io.Discard,
	}
}

// NewLogger creates a new logger
func NewLogger(verbose, useColor, jsonRPCMode bool) *Logger {
	return &Logger{
		verbose:     verbose,
		useColor:    useColor,
		jsonRPCMode: jsonRPCMode,
		writer:      os.Stdout, // Default to stdout
	}
}

// NewLoggerWithWriter creates a new logger with a custom writer
func NewLoggerWithWriter(verbose, useColor, jsonRPCMode bool, writer io.Writer) *Logger {
	return &Logger{
		verbose:     verbose,
		useColor:    useColor,
		jsonRPCMode: jsonRPCMode,
		writer:      writer,
	}
}

// Output writes user-facing output directly to stdout without timestamps
// This is for command results, formatted data, etc.
func (l *Logger) Output(format string, args ...interface{}) {
	fmt.Fprintf(os.Stdout, format, args...)
}

// OutputLine writes user-facing output with a newline
func (l *Logger) OutputLine(format string, args ...interface{}) {
	fmt.Fprintf(os.Stdout, format+"\n", args...)
}

// timestamp returns the current timestamp string
func (l *Logger) timestamp() string {
	return time.Now().Format("2006-01-02 15:04:05")
}

// colorize applies color to text if colors are enabled
func (l *Logger) colorize(text, colorCode string) string {
	if !l.useColor {
		return text
	}
	return fmt.Sprintf("%s%s%s", colorCode, text, colorReset)
}

// Info logs an informational message
func (l *Logger) Info(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(l.writer, "[%s] %s\n", l.timestamp(), msg)
}

// Debug logs a debug message (only in verbose mode)
func (l *Logger) Debug(format string, args ...interface{}) {
	if !l.verbose {
		return
	}
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(l.writer, "[%s] %s\n", l.timestamp(), l.colorize(msg, colorGray))
}

// Error logs an error message
func (l *Logger) Error(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(l.writer, "[%s] %s\n", l.timestamp(), l.colorize(msg, colorRed))
}

// Success logs a success message
func (l *Logger) Success(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(l.writer, "[%s] %s\n", l.timestamp(), l.colorize(msg, colorGreen))
}

// Request logs an outgoing request
func (l *Logger) Request(method string, params interface{}) {
	if !l.jsonRPCMode {
		// Simple mode - just log what we're doing
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

	// JSON-RPC mode - full protocol logging
	arrow := l.colorize("→", colorBlue)
	methodStr := l.colorize(fmt.Sprintf("REQUEST (%s)", method), colorBlue)

	fmt.Fprintf(l.writer, "[%s] %s %s:\n", l.timestamp(), arrow, methodStr)

	// Pretty print the params
	if params != nil {
		jsonStr := l.prettyJSON(params)
		fmt.Fprintln(l.writer, l.colorize(jsonStr, colorBlue))
	}
	fmt.Fprintln(l.writer)
}

// Response logs an incoming response
func (l *Logger) Response(method string, result interface{}) {
	if !l.jsonRPCMode {
		// Simple mode - log meaningful information
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

	// JSON-RPC mode - full protocol logging
	arrow := l.colorize("←", colorGreen)
	methodStr := l.colorize(fmt.Sprintf("RESPONSE (%s)", method), colorGreen)

	fmt.Fprintf(l.writer, "[%s] %s %s:\n", l.timestamp(), arrow, methodStr)

	// Pretty print the result
	if result != nil {
		jsonStr := l.prettyJSON(result)
		fmt.Fprintln(l.writer, l.colorize(jsonStr, colorGreen))
	}
	fmt.Fprintln(l.writer)
}

// Notification logs an incoming notification
func (l *Logger) Notification(method string, params interface{}) {
	// Skip keepalive notifications unless in verbose mode
	if method == "$/keepalive" && !l.verbose {
		return
	}

	if !l.jsonRPCMode {
		// Simple mode - just log the notification type
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

	// JSON-RPC mode - full protocol logging
	arrow := l.colorize("←", colorYellow)
	methodStr := l.colorize(fmt.Sprintf("NOTIFICATION (%s)", method), colorYellow)

	fmt.Fprintf(l.writer, "[%s] %s %s:\n", l.timestamp(), arrow, methodStr)

	// Pretty print the params
	if params != nil {
		jsonStr := l.prettyJSON(params)
		fmt.Fprintln(l.writer, l.colorize(jsonStr, colorYellow))
	}
	fmt.Fprintln(l.writer)
}

// prettyJSON formats JSON for display
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

	b, err := json.MarshalIndent(wrapper, "", "  ")
	if err != nil {
		// Fallback to direct marshaling
		b, err = json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Sprintf("%+v", v)
		}
	}
	return string(b)
}

// Write implements io.Writer for compatibility
func (l *Logger) Write(p []byte) (n int, err error) {
	l.Debug("%s", string(p))
	return len(p), nil
}

// countTools attempts to count the number of tools in a tools/list response
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

// countResources attempts to count the number of resources in a resources/list response
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

// countPrompts attempts to count the number of prompts in a prompts/list response
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
