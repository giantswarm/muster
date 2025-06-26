// Package mcpserver provides Model Context Protocol (MCP) server management for muster.
//
// This package handles the lifecycle of MCP servers, which provide AI assistants
// with structured access to Kubernetes clusters and monitoring services. It supports
// both local process execution and containerized deployments through a unified
// management interface.
//
// # Overview
//
// MCP servers act as bridges between AI assistants (like Claude in Cursor) and
// backend services. They expose tools and resources through a standardized protocol,
// allowing AI assistants to query and interact with systems in a controlled manner.
//
// The package provides comprehensive MCP server definition management, allowing
// users to create, update, and delete server configurations through both API
// operations and direct YAML file manipulation.
//
// # Server Types
//
// The package supports two deployment models:
//
// ## Local Command
//   - Runs MCP servers as local processes
//   - Direct execution of binaries or scripts
//   - Environment variable configuration
//   - Process lifecycle management
//   - Standard input/output handling
//
// ## Container
//   - Runs MCP servers in Docker containers
//   - Isolated execution environment
//   - Volume and port mapping support
//   - Automatic image management
//   - Container health monitoring
//
// # Core Components
//
// ## MCPServerManager
// The central component for managing MCP server definitions:
//   - **Definition Loading**: Load server configurations from YAML files
//   - **Validation**: Comprehensive validation of server definitions
//   - **CRUD Operations**: Create, read, update, and delete server definitions
//   - **Availability Checking**: Verify server configuration completeness
//   - **Storage Integration**: Unified storage for definition persistence
//
// ## Definition Management
//   - **YAML-Based Storage**: Server definitions stored as YAML files
//   - **User and Project Scope**: Support for both user and project configurations
//   - **Validation**: Type-specific validation for different server types
//   - **Hot Reloading**: Dynamic loading of definition changes
//
// ## Process Management
//   - Start/stop MCP server processes
//   - Monitor process health
//   - Capture and log output
//   - Handle graceful shutdown
//   - Environment variable injection
//
// ## Container Management
//   - Pull required images
//   - Create and start containers
//   - Map ports and volumes
//   - Clean up on termination
//   - Container health monitoring
//
// ## Proxy Server
//   - HTTP proxy for MCP protocol
//   - SSE (Server-Sent Events) support
//   - Request forwarding to MCP servers
//   - Connection pooling and management
//   - Protocol translation
//
// # Configuration
//
// MCP servers are configured through YAML definitions with:
//   - **Name**: Unique identifier for the server
//   - **Type**: "localCommand" or "container"
//   - **Command/Image**: Execution details based on type
//   - **Environment**: Variables to set for the server
//   - **Dependencies**: Required services or conditions
//   - **Metadata**: Additional descriptive information
//
// ## Local Command Configuration
//
//	name: "kubernetes"
//	type: "localCommand"
//	command: ["npx", "mcp-server-kubernetes"]
//	env:
//	  KUBECONFIG: "/home/user/.kube/config"
//	  NAMESPACE_FILTER: "default,kube-system"
//
// ## Container Configuration
//
//	name: "prometheus"
//	type: "container"
//	image: "ghcr.io/org/mcp-prometheus:latest"
//	ports: ["8002:8000"]
//	env:
//	  PROMETHEUS_URL: "http://localhost:9090"
//	volumes: ["/host/data:/container/data"]
//
// # Definition Storage
//
// MCP server definitions are stored as YAML files in:
//   - User directory: ~/.config/muster/mcpservers/
//   - Project directory: .muster/mcpservers/
//
// Project definitions take precedence over user definitions with the same name.
//
// # Health Monitoring
//
// Server health is determined by:
//   - Process/container running status
//   - Proxy server availability
//   - MCP protocol responsiveness
//   - Tool availability checks
//   - Connection health monitoring
//
// # Usage Examples
//
// ## Manager Initialization
//
//	storage := config.NewStorage()
//	manager, err := mcpserver.NewMCPServerManager(storage)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Load definitions from YAML files
//	if err := manager.LoadDefinitions(); err != nil {
//	    log.Printf("Failed to load MCP server definitions: %v", err)
//	}
//
// ## Creating MCP Server Definitions
//
//	// Local command MCP server
//	localServer := api.MCPServer{
//	    Name:    "kubernetes",
//	    Type:    api.MCPServerTypeLocalCommand,
//	    Command: []string{"npx", "mcp-server-kubernetes"},
//	    Env: map[string]string{
//	        "KUBECONFIG": "/home/user/.kube/config",
//	    },
//	}
//
//	if err := manager.CreateMCPServer(localServer); err != nil {
//	    log.Fatal(err)
//	}
//
//	// Container-based MCP server
//	containerServer := api.MCPServer{
//	    Name:    "prometheus",
//	    Type:    api.MCPServerTypeContainer,
//	    Image:   "ghcr.io/org/mcp-prometheus:latest",
//	    Ports:   []string{"8002:8000"},
//	    Env: map[string]string{
//	        "PROMETHEUS_URL": "http://localhost:9090",
//	    },
//	}
//
//	if err := manager.CreateMCPServer(containerServer); err != nil {
//	    log.Fatal(err)
//	}
//
// ## Querying MCP Server Definitions
//
//	// List all definitions
//	servers := manager.ListDefinitions()
//	for _, server := range servers {
//	    fmt.Printf("Server: %s (type: %s)\n", server.Name, server.Type)
//	}
//
//	// Get specific definition
//	server, exists := manager.GetDefinition("kubernetes")
//	if exists {
//	    fmt.Printf("Found server: %s\n", server.Name)
//	}
//
//	// List only available definitions
//	available := manager.ListAvailableDefinitions()
//	fmt.Printf("Available servers: %d\n", len(available))
//
// ## Updating and Deleting Definitions
//
//	// Update existing server
//	server.Env["NEW_VAR"] = "new_value"
//	if err := manager.UpdateMCPServer("kubernetes", server); err != nil {
//	    log.Fatal(err)
//	}
//
//	// Delete server definition
//	if err := manager.DeleteMCPServer("kubernetes"); err != nil {
//	    log.Fatal(err)
//	}
//
// # Integration with AI Assistants
//
// MCP servers are accessed by AI assistants through:
//
//  1. **SSE endpoint**: http://localhost:{proxyPort}/sse
//  2. **JSON-RPC protocol** over SSE
//  3. **Tool discovery and invocation**
//  4. **Structured responses**
//
// Example Cursor configuration:
//
//	{
//	  "mcpServers": {
//	    "kubernetes": {
//	      "command": "curl",
//	      "args": ["-N", "http://localhost:8001/sse"]
//	    }
//	  }
//	}
//
// # Validation
//
// The package provides comprehensive validation for MCP server definitions:
//
//   - **Name validation**: Ensures unique and valid identifiers
//   - **Type validation**: Verifies server type is supported
//   - **Command validation**: For local command servers, ensures command is specified
//   - **Image validation**: For container servers, ensures image is specified
//   - **Environment validation**: Validates environment variable format
//   - **Cross-type validation**: Prevents invalid combinations of fields
//
// # Error Handling
//
// The package handles various error scenarios:
//   - Binary not found for local command servers
//   - Port already in use
//   - Container runtime issues
//   - Network connectivity problems
//   - Protocol errors
//   - Definition validation failures
//   - YAML parsing errors
//   - Storage operation failures
//
// # API Integration
//
// The package integrates with muster's API layer through:
//   - **MCPServerManagerHandler**: API interface for server management
//   - **Registration pattern**: Proper API layer registration
//   - **Tool provider interface**: Exposing management tools
//   - **Event integration**: Server lifecycle event broadcasting
//
// # Thread Safety
//
// All operations are thread-safe:
//   - Multiple MCP servers can be managed concurrently
//   - Concurrent definition operations
//   - Thread-safe access to server registry
//   - Protected storage operations
//
// # Performance Considerations
//
//   - **Efficient loading**: Lazy loading of definitions when needed
//   - **Caching**: In-memory caching of loaded definitions
//   - **Validation**: Early validation to prevent runtime errors
//   - **Resource cleanup**: Proper cleanup of server resources
//
// This package provides the foundation for muster's MCP server ecosystem,
// enabling seamless integration with AI assistants and other external tools
// through the standardized MCP protocol.
package mcpserver
