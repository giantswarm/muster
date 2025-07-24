// Package mcpserver provides MCP (Model Context Protocol) server management functionality.
//
// This package enables the registration, configuration, and lifecycle management of MCP servers
// within the muster ecosystem. It supports both static configuration through YAML files and
// dynamic management through API operations.
//
// # MCP Server Types
//
// Currently supported MCP server types:
//   - **Stdio**: Execute MCP servers as local processes with configurable command lines
//   - **Streamable-HTTP**: Connect to external MCP servers via HTTP transport
//   - **SSE**: Connect to external MCP servers via Server-Sent Events transport
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
//	  type: stdio
//	  autoStart: true
//	  command: npx
//	  args:
//	    - "@modelcontextprotocol/server-filesystem"
//	    - "/workspace"
//	  env:
//	    DEBUG: "1"
//
// For remote servers:
//
//	spec:
//	  type: streamable-http
//	  url: "https://api.example.com/mcp"
//	  timeout: 30
//	  headers:
//	    Authorization: "Bearer token"
//
// # Static Configuration
//
// MCP servers can be defined in YAML files for static configuration. These files are
// typically placed in the muster configuration directory and loaded at startup.
//
// Static configuration example:
//
//	# Stdio server example
//	---
//	apiVersion: muster.giantswarm.io/v1alpha1
//	kind: MCPServer
//	metadata:
//	  name: filesystem-tools
//	spec:
//	  description: File system operations
//	  toolPrefix: fs
//	  type: stdio
//	  autoStart: true
//	  command: npx
//	  args: ["@modelcontextprotocol/server-filesystem", "/workspace"]
//
//	# Remote server example
//	---
//	apiVersion: muster.giantswarm.io/v1alpha1
//	kind: MCPServer
//	metadata:
//	  name: remote-api
//	spec:
//	  description: Remote API tools
//	  toolPrefix: api
//	  type: streamable-http
//	  url: "https://api.example.com/mcp"
//	  timeout: 60
//	  headers:
//	    Authorization: "Bearer token"
//
// # Dynamic Management
//
// MCP servers can also be created, updated, and deleted dynamically through the muster API.
// This enables runtime configuration changes and programmatic server management.
//
// API operations include:
//   - Create: Register new MCP server definitions
//   - Update: Modify existing server configurations
//   - Delete: Remove server definitions
//   - List: Retrieve all configured servers
//   - Get: Fetch specific server details
//   - Validate: Check server configuration validity
//
// # Lifecycle Management
//
// The package handles the complete lifecycle of MCP servers:
//   - **Registration**: Adding server definitions to the system
//   - **Validation**: Ensuring configuration correctness
//   - **Instantiation**: Creating service instances
//   - **Startup**: Initializing server processes or connections
//   - **Monitoring**: Tracking server health and status
//   - **Shutdown**: Graceful termination of server instances
//
// # Integration Points
//
// This package integrates with several muster components:
//   - **Aggregator**: Registers tools provided by MCP servers
//   - **Service Registry**: Manages server lifecycle as services
//   - **Configuration System**: Loads static server definitions
//   - **API Layer**: Exposes management operations as tools
//   - **Orchestrator**: Coordinates server startup and dependencies
package mcpserver
