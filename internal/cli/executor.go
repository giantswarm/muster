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

// OutputFormat represents the output format for CLI commands
type OutputFormat string

const (
	OutputFormatTable OutputFormat = "table"
	OutputFormatJSON  OutputFormat = "json"
	OutputFormatYAML  OutputFormat = "yaml"
)

// ExecutorOptions contains options for tool execution
type ExecutorOptions struct {
	Format OutputFormat
	Quiet  bool
}

// ToolExecutor provides high-level tool execution functionality
type ToolExecutor struct {
	client    *agent.Client
	options   ExecutorOptions
	formatter *TableFormatter
}

// NewToolExecutor creates a new tool executor
func NewToolExecutor(options ExecutorOptions) (*ToolExecutor, error) {
	// Check if server is running first
	if err := CheckServerRunning(); err != nil {
		return nil, err
	}

	cfg, err := config.LoadConfig()
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

// Connect establishes connection to the aggregator
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

// Close closes the connection
func (e *ToolExecutor) Close() error {
	return e.client.Close()
}

// Execute executes a tool and formats the output
func (e *ToolExecutor) Execute(ctx context.Context, toolName string, arguments map[string]interface{}) error {
	var s *spinner.Spinner
	if !e.options.Quiet {
		s = spinner.New(spinner.CharSets[14], 100*time.Millisecond)
		s.Suffix = " Executing command..."
		s.Start()
	}

	result, err := e.client.CallTool(ctx, toolName, arguments)

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

// ExecuteSimple executes a tool and returns the result as a string
func (e *ToolExecutor) ExecuteSimple(ctx context.Context, toolName string, args map[string]interface{}) (string, error) {
	return e.client.CallToolSimple(ctx, toolName, args)
}

// ExecuteJSON executes a tool and returns the result as parsed JSON
func (e *ToolExecutor) ExecuteJSON(ctx context.Context, toolName string, args map[string]interface{}) (interface{}, error) {
	return e.client.CallToolJSON(ctx, toolName, args)
}

// formatError formats error output
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

// formatOutput formats the tool output according to the specified format
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

// outputYAML converts JSON to YAML and prints it
func (e *ToolExecutor) outputYAML(jsonData string) error {
	var data interface{}
	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}

	yamlData, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to convert to YAML: %w", err)
	}

	fmt.Print(string(yamlData))
	return nil
}

// outputTable formats data as a professional table using the table formatter
func (e *ToolExecutor) outputTable(jsonData string) error {
	var data interface{}
	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		fmt.Println(jsonData) // Fallback to raw text if not JSON
		return nil
	}

	return e.formatter.FormatData(data)
}
