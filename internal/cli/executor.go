package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"muster/internal/agent"
	"muster/internal/config"

	"github.com/briandowns/spinner"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/mark3labs/mcp-go/mcp"
	"gopkg.in/yaml.v3"
)

// OutputFormat represents the supported output formats for CLI commands.
// This allows users to choose how they want to receive command results.
type OutputFormat string

const (
	// OutputFormatTable formats output as a professional table with styling and icons
	OutputFormatTable OutputFormat = "table"
	// OutputFormatJSON formats output as raw JSON data
	OutputFormatJSON OutputFormat = "json"
	// OutputFormatYAML formats output as YAML data converted from JSON
	OutputFormatYAML OutputFormat = "yaml"
)

// ExecutorOptions contains configuration options for tool execution.
// These options control how commands are executed and how output is formatted.
type ExecutorOptions struct {
	// Format specifies the desired output format (table, json, yaml)
	Format OutputFormat
	// Quiet suppresses progress indicators and non-essential output
	Quiet bool
	// ConfigPath specifies a custom configuration directory path
	ConfigPath string
}

// ToolExecutor provides high-level tool execution functionality with formatted output.
// It handles the connection to the muster aggregator, executes tools, and formats
// the results according to the specified output format. This is the main interface
// for CLI commands that need to interact with muster services.
type ToolExecutor struct {
	// client is the MCP client for communicating with the aggregator
	client *agent.Client
	// options contains execution configuration
	options ExecutorOptions
	// formatter handles table formatting when output format is table
	formatter *TableFormatter
}

// NewToolExecutor creates a new tool executor with the specified options.
// It establishes the connection configuration and initializes the MCP client
// for communication with the muster aggregator server.
//
// Args:
//   - options: Configuration options for execution and output formatting
//
// Returns:
//   - *ToolExecutor: Configured tool executor ready for use
//   - error: Configuration or connection setup error
func NewToolExecutor(options ExecutorOptions) (*ToolExecutor, error) {
	// Check if server is running first
	if err := CheckServerRunning(); err != nil {
		return nil, err
	}

	var cfg config.MusterConfig
	var err error

	if options.ConfigPath == "" {
		panic("Logic error: empty tool executor ConfigPath")
	}

	cfg, err = config.LoadConfigFromPath(options.ConfigPath)
	if err != nil {
		return nil, err
	}

	logger := agent.NewLogger(false, false, false)

	transport := agent.TransportType(cfg.Aggregator.Transport)
	var endpoint string

	if transport == agent.TransportStreamableHTTP {
		endpoint = fmt.Sprintf("http://%s:%d/mcp", cfg.Aggregator.Host, cfg.Aggregator.Port)
	} else if transport == agent.TransportSSE {
		endpoint = fmt.Sprintf("http://%s:%d/sse", cfg.Aggregator.Host, cfg.Aggregator.Port)
	} else {
		return nil, fmt.Errorf("unsupported transport: %s", cfg.Aggregator.Transport)
	}

	client := agent.NewClient(endpoint, logger, transport)

	go func() {
		for notification := range client.NotificationChan {
			fmt.Println(notification)
		}
	}()
	return &ToolExecutor{
		client:    client,
		options:   options,
		formatter: NewTableFormatter(options),
	}, nil
}

// Connect establishes a connection to the muster aggregator server.
// It shows a progress spinner unless quiet mode is enabled, and handles
// connection errors with appropriate user feedback.
//
// Args:
//   - ctx: Context for connection timeout and cancellation
//
// Returns:
//   - error: Connection error, if any
func (e *ToolExecutor) Connect(ctx context.Context) error {
	if e.options.Quiet {
		return e.client.Connect(ctx)
	}

	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Connecting to muster server..."
	s.Start()
	defer s.Stop()

	err := e.client.Connect(ctx)
	if err != nil {
		s.FinalMSG = text.FgRed.Sprint("❌ Failed to connect to muster server") + "\n"
		return err
	}

	// Remove the success message - connection success is implied by command working
	return nil
}

// Close gracefully closes the connection to the aggregator server.
// This should be called when the executor is no longer needed to free resources.
//
// Returns:
//   - error: Error during connection cleanup, if any
func (e *ToolExecutor) Close() error {
	return e.client.Close()
}

// Execute executes a tool with the given args and formats the output.
// This is the main method for running muster tools through the CLI interface.
// It handles progress indication, error formatting, and output formatting
// according to the configured options.
//
// Args:
//   - ctx: Context for execution timeout and cancellation
//   - toolName: Name of the tool to execute
//   - args: Tool args as key-value pairs
//
// Returns:
//   - error: Execution or formatting error, if any
func (e *ToolExecutor) Execute(ctx context.Context, toolName string, args map[string]interface{}) error {
	var s *spinner.Spinner
	if !e.options.Quiet {
		s = spinner.New(spinner.CharSets[14], 100*time.Millisecond)
		s.Suffix = " Executing command..."
		s.Start()
	}

	result, err := e.client.CallTool(ctx, toolName, args)

	if s != nil {
		s.Stop()
	}

	if err != nil {
		if s != nil {
			fmt.Fprintf(os.Stderr, "%s\n", text.FgRed.Sprint("❌ Command failed"))
		}
		return fmt.Errorf("failed to execute tool %s: %w", toolName, err)
	}

	if result.IsError {
		if s != nil {
			fmt.Fprintf(os.Stderr, "%s\n", text.FgRed.Sprint("❌ Command returned error"))
		}
		return e.formatError(result)
	}

	return e.formatOutput(result)
}

// ExecuteSimple executes a tool and returns the raw result as a string.
// This method bypasses output formatting and is useful for programmatic
// access to tool results or when custom formatting is needed.
//
// Args:
//   - ctx: Context for execution timeout and cancellation
//   - toolName: Name of the tool to execute
//   - args: Tool args as key-value pairs
//
// Returns:
//   - string: Raw tool output as string
//   - error: Execution error, if any
func (e *ToolExecutor) ExecuteSimple(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	return e.client.CallToolSimple(ctx, toolName, args)
}

// ExecuteJSON executes a tool and returns the result as parsed JSON.
// This method is useful when you need to work with structured data
// programmatically rather than displaying it to users.
//
// Args:
//   - ctx: Context for execution timeout and cancellation
//   - toolName: Name of the tool to execute
//   - args: Tool args as key-value pairs
//
// Returns:
//   - interface{}: Parsed JSON result as Go data structures
//   - error: Execution or JSON parsing error, if any
func (e *ToolExecutor) ExecuteJSON(ctx context.Context, toolName string, args map[string]interface{}) (interface{}, error) {
	return e.client.CallToolJSON(ctx, toolName, args)
}

// formatError formats and displays error output from tool execution.
// It extracts error messages from the MCP result and presents them
// in a user-friendly format.
//
// Args:
//   - result: MCP call result containing error information
//
// Returns:
//   - error: Formatted error for propagation up the call stack
func (e *ToolExecutor) formatError(result *mcp.CallToolResult) error {
	var errorMsgs []string
	for _, content := range result.Content {
		if textContent, ok := mcp.AsTextContent(content); ok {
			errorMsgs = append(errorMsgs, textContent.Text)
		}
	}

	errorMsg := strings.Join(errorMsgs, "\n")
	fmt.Fprintf(os.Stderr, "Error: %s\n", errorMsg)
	return fmt.Errorf("%s", errorMsg)
}

// formatOutput formats the tool output according to the specified format.
// It handles conversion between different output formats and delegates
// to appropriate formatting functions based on the configured output format.
//
// Args:
//   - result: MCP call result containing the data to format
//
// Returns:
//   - error: Formatting error, if any
func (e *ToolExecutor) formatOutput(result *mcp.CallToolResult) error {
	if len(result.Content) == 0 {
		if !e.options.Quiet {
			fmt.Println("No results")
		}
		return nil
	}

	content := result.Content[0]
	textContent, ok := mcp.AsTextContent(content)
	if !ok {
		return fmt.Errorf("content is not text")
	}

	switch e.options.Format {
	case OutputFormatJSON:
		fmt.Println(textContent.Text)
		return nil
	case OutputFormatYAML:
		return e.outputYAML(textContent.Text)
	case OutputFormatTable:
		return e.outputTable(textContent.Text)
	default:
		return fmt.Errorf("unsupported output format: %s", e.options.Format)
	}
}

// outputYAML converts JSON data to YAML format and prints it.
// This provides a more readable alternative to JSON for configuration
// and structured data display. For responses that already contain a 'yaml'
// field, it outputs that directly instead of converting the entire response.
//
// Args:
//   - jsonData: JSON data as a string
//
// Returns:
//   - error: JSON parsing or YAML conversion error, if any
func (e *ToolExecutor) outputYAML(jsonData string) error {
	var data interface{}
	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Check if this is a response that already contains YAML content
	if dataMap, ok := data.(map[string]interface{}); ok {
		// If there's a 'yaml' field, output that directly (common for workflows)
		if yamlContent, exists := dataMap["yaml"]; exists {
			if yamlStr, ok := yamlContent.(string); ok {
				fmt.Print(yamlStr)
				return nil
			}
		}
	}

	// Fallback to converting entire response to YAML
	yamlData, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to convert to YAML: %w", err)
	}

	fmt.Print(string(yamlData))
	return nil
}

// outputTable formats data as a professional table using the table formatter.
// This provides the most user-friendly display format with proper styling,
// icons, and optimized column layouts.
//
// Args:
//   - jsonData: JSON data as a string to be formatted as a table
//
// Returns:
//   - error: JSON parsing or table formatting error, if any
func (e *ToolExecutor) outputTable(jsonData string) error {
	var data interface{}
	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		fmt.Println(jsonData) // Fallback to raw text if not JSON
		return nil
	}

	return e.formatter.FormatData(data)
}
