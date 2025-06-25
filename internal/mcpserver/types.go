package mcpserver

import (
	"context"
	"io"
)

// MCPClient interface defines what mcpserver needs from an MCP client
// This breaks the import cycle with aggregator package
type MCPClient interface {
	Initialize(ctx context.Context) error
	Close() error
	GetStderr() (io.Reader, bool)
}

// McpDiscreteStatusUpdate is used to report discrete status changes from a running MCP process.
// It focuses on the state, not verbose logs.
type McpDiscreteStatusUpdate struct {
	Name          string // The unique label of the MCP server instance
	ProcessStatus string // A string indicating the process status, e.g., "ProcessInitializing", "ProcessStarting", "ProcessRunning", "ProcessExitedWithError"
	ProcessErr    error  // The actual Go error object if the process failed or exited with an error
}

// McpUpdateFunc is a callback function type for receiving McpDiscreteStatusUpdate messages.
type McpUpdateFunc func(update McpDiscreteStatusUpdate)
