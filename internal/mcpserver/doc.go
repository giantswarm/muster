// Package mcpserver provides MCP (Model Context Protocol) server management functionality.
//
// This package enables the registration, configuration, and lifecycle management of MCP servers
// within the muster ecosystem. It supports both static configuration through YAML files and
// dynamic management through API operations.
//
// # MCP Server Types
//
// Currently supported MCP server types:
//   - **Local**: Execute MCP servers as local processes with configurable command lines
//   - **Remote**: Connect to external MCP servers via HTTP, SSE, or WebSocket transports
//
// # Configuration Structure
//
// MCP servers are defined using the MCPServer CRD or YAML configuration files. Each server
// definition includes metadata, type-specific configuration, and operational settings.
//
// Basic configuration structure:
//
//	apiVersion: muster.giantswarm.io/v1alpha1
//	kind: MCPServer
//	metadata:
//	  name: example-server
//	  namespace: default
//	spec:
//	  description: Example MCP server for demonstration
//	  toolPrefix: example
//	  type: local
//	  local:
//	    autoStart: true
//	    command:
//	      - "npx"
//	      - "@modelcontextprotocol/server-filesystem"
//	      - "/workspace"
//	    env:
//	      DEBUG: "1"
//
// For remote servers:
//
//	spec:
//	  type: remote
//	  remote:
//	    endpoint: "https://api.example.com/mcp"
//	    transport: "http"
//	    timeout: 30
//
// # Static Configuration
//
// MCP servers can be defined in YAML files for static configuration. These files are
// typically placed in the muster configuration directory and loaded at startup.
//
// Static configuration example:
//
//	# Local server example
//	---
//	apiVersion: muster.giantswarm.io/v1alpha1
//	kind: MCPServer
//	metadata:
//	  name: filesystem-tools
//	spec:
//	  description: File system operations
//	  toolPrefix: fs
//	  type: local
//	  local:
//	    autoStart: true
//	    command: ["npx", "@modelcontextprotocol/server-filesystem", "/workspace"]
//
//	# Remote server example
//	---
//	apiVersion: muster.giantswarm.io/v1alpha1
//	kind: MCPServer
//	metadata:
//	  name: remote-api
//	spec:
//	  description: Remote API server
//	  toolPrefix: api
//	  type: remote
//	  remote:
//	    endpoint: "https://api.example.com/mcp"
//	    transport: "http"
//	    timeout: 30
//
// # Dynamic Management
//
// MCP servers can also be created, updated, and deleted dynamically using the API tools
// provided by this package. The API adapter exposes the following tools:
//
//   - mcpserver_list: List all configured MCP servers
//   - mcpserver_get: Get details of a specific MCP server
//   - mcpserver_create: Create a new MCP server definition
//   - mcpserver_update: Update an existing MCP server
//   - mcpserver_delete: Remove an MCP server definition
//   - mcpserver_validate: Validate a server configuration without creating it
//
// # Tool Integration
//
// Created MCP servers are automatically registered with the aggregator, making their tools
// available through the unified muster tool interface. Tool names are automatically prefixed
// using the configured toolPrefix to avoid naming conflicts.
//
// # Architecture
//
// The mcpserver package follows the standard muster architectural patterns:
//
//   - **API Adapter**: Implements the MCPServerManagerHandler interface and exposes
//     management tools through the ToolProvider interface
//   - **Unified Client**: Uses the muster unified client for both Kubernetes CRD
//     operations and filesystem-based configuration management
//   - **Service Integration**: MCP servers are managed as services by the orchestrator,
//     handling lifecycle, health monitoring, and dependency management
//
// # Error Handling
//
// The package provides comprehensive error handling with specific error types for
// common scenarios such as server not found, invalid configuration, and lifecycle
// management failures. All errors include contextual information for debugging.
//
// # Thread Safety
//
// All operations in this package are thread-safe and can be called concurrently.
// The underlying unified client handles synchronization and ensures data consistency.
package mcpserver
