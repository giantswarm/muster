// Package cli provides sophisticated command-line interface utilities for the muster application.
//
// This package offers a comprehensive CLI experience with intelligent data formatting,
// robust server connectivity, and professional output presentation. It serves as the
// primary interface layer between user commands and the muster aggregator system.
//
// # Core Components
//
// ToolExecutor provides high-level tool execution with multiple output formats:
//   - MCP (Message Control Protocol) client integration with the aggregator
//   - Multiple output formats: professional tables, JSON, and YAML
//   - Progress indicators with spinners for long-running operations
//   - Server connectivity validation and health checking
//   - Both formatted and programmatic execution modes
//
// TableFormatter offers intelligent table creation and optimization:
//   - Auto-detection of muster resource types (services, workflows, capabilities, etc.)
//   - Smart column selection and optimization based on data structure
//   - Specialized formatting rules for different resource types
//   - Professional styling with consistent iconography and color coding
//   - Handles both simple arrays and complex nested object structures
//
// TableBuilder provides cell-level formatting and styling utilities:
//   - Status indicators with color-coded icons (✅ healthy, ❌ unhealthy, etc.)
//   - Resource-specific formatting (tool names, metadata, descriptions)
//   - Consistent styling for names, IDs, types, and other common fields
//   - Smart truncation and summarization of complex data
//   - Icon assignment based on resource types
//
// Common utilities handle server connectivity and message formatting:
//   - Auto-detection of aggregator endpoints from configuration
//   - Server health checks with appropriate error messages
//   - Consistent formatting for success (✓), error, and warning (⚠) messages
//   - Configuration-aware endpoint resolution
//
// # Output Formats
//
// The package supports three output formats to accommodate different use cases:
//   - Table: Professional tables with optimized columns, styling, and summaries
//   - JSON: Raw JSON output for programmatic consumption
//   - YAML: Human-readable YAML format converted from JSON responses
//
// # Resource Type Intelligence
//
// The formatter automatically detects and optimizes display for muster resource types:
//   - Services: Health status, state, service type, and metadata
//   - ServiceClasses: Availability, required tools, and descriptions
//   - MCP Servers: Server state, type, and configuration details
//   - Workflows: Steps, input args, and execution details
//   - Capabilities: Availability status and capability types
//
// # Usage Patterns
//
// The package is designed for use by muster CLI commands that need to:
//   - Execute tools through the aggregator with professional output
//   - Display complex data structures in user-friendly formats
//   - Provide consistent styling and iconography across all commands
//   - Validate server connectivity before attempting operations
//   - Support both interactive and programmatic usage scenarios
//
// # Integration
//
// All CLI commands should use this package for consistent user experience.
// The package handles the complexity of MCP communication, data formatting,
// and output presentation, allowing commands to focus on their core logic.
package cli
