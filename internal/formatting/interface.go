// Package formatting provides unified output formatting for both CLI and agent components.
//
// This package consolidates formatting logic that was previously scattered across
// the agent and CLI packages, providing consistent output formatting with support
// for multiple output formats (console, JSON, YAML, table).
package formatting

import (
	"github.com/mark3labs/mcp-go/mcp"
)

// OutputFormat represents the desired output format
type OutputFormat string

const (
	FormatConsole OutputFormat = "console" // Simple console output
	FormatJSON    OutputFormat = "json"    // JSON output
	FormatYAML    OutputFormat = "yaml"    // YAML output
	FormatTable   OutputFormat = "table"   // Rich table output
)

// Options configures the formatter behavior
type Options struct {
	Format OutputFormat
	Quiet  bool // Suppress decorative elements
	Color  bool // Enable colored output
}

// Formatter provides unified formatting for MCP resources
type Formatter interface {
	// Tool formatting
	FormatToolsList(tools []mcp.Tool) string
	FormatToolDetail(tool mcp.Tool) string
	FindTool(tools []mcp.Tool, name string) *mcp.Tool

	// Resource formatting
	FormatResourcesList(resources []mcp.Resource) string
	FormatResourceDetail(resource mcp.Resource) string
	FindResource(resources []mcp.Resource, uri string) *mcp.Resource

	// Prompt formatting
	FormatPromptsList(prompts []mcp.Prompt) string
	FormatPromptDetail(prompt mcp.Prompt) string
	FindPrompt(prompts []mcp.Prompt, name string) *mcp.Prompt

	// Generic data formatting (for CLI tool results)
	FormatData(data interface{}) error

	// Configuration
	SetOptions(options Options)
	GetOptions() Options
}

// Factory creates formatters for different output formats
type Factory interface {
	CreateFormatter(options Options) Formatter
}

// NewFactory creates a new formatter factory
func NewFactory() Factory {
	return &factory{}
}

// factory implements the Factory interface
type factory struct{}

// CreateFormatter creates the appropriate formatter based on options
func (f *factory) CreateFormatter(options Options) Formatter {
	switch options.Format {
	case FormatJSON:
		return NewJSONFormatter(options)
	case FormatYAML:
		return NewYAMLFormatter(options)
	case FormatTable:
		return NewTableFormatter(options)
	case FormatConsole:
		fallthrough
	default:
		return NewConsoleFormatter(options)
	}
}
