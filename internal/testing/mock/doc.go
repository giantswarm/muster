// Package mock provides mock MCP server functionality for testing muster components.
//
// This package contains the implementation of mock MCP servers that can be used
// in test scenarios to simulate external MCP servers with predefined behaviors.
//
// The mock MCP server functionality allows test scenarios to define:
// - Mock tools with configurable responses
// - Conditional responses based on input args
// - Simulated delays and error conditions
// - Template-based response generation
//
// # Key Components
//
// Server: The main mock MCP server that implements the MCP protocol.
// It can be configured with a set of tools and their expected behaviors.
// By default, it uses stdio transport for subprocess communication.
//
// HTTPServer: Wraps a mock MCP server with HTTP transport capabilities.
// Supports both SSE (Server-Sent Events) and Streamable HTTP transports.
// Use this for testing URL-based MCP server configurations.
//
// ToolHandler: Handles individual tool calls with configurable responses based
// on input args and conditions.
//
// ToolConfig & ToolResponse: Configuration structures that define how mock tools
// should behave, including input schemas, response conditions, and template-based
// response generation.
//
// # Supported Transports
//
// The mock package supports three transport types:
//
//   - stdio: Standard input/output for subprocess communication (default)
//   - streamable-http: HTTP-based streaming protocol
//   - sse: Server-Sent Events protocol
//
// # Usage
//
// Mock MCP servers can be used in three ways:
//
//  1. Embedded stdio mock in test scenarios:
//     The test framework automatically starts stdio mock MCP servers based on
//     scenario pre-configuration that includes MCP servers with "tools" in config:
//
//     pre_configuration:
//     mcp_servers:
//     - name: "my-mock"
//     config:
//     tools:
//     - name: "my_tool"
//     ...
//
//  2. Embedded HTTP mock in test scenarios:
//     For URL-based transports, add a "type" field to the config:
//
//     pre_configuration:
//     mcp_servers:
//     - name: "my-http-mock"
//     config:
//     type: "streamable-http"  # or "sse"
//     tools:
//     - name: "my_tool"
//     ...
//
//  3. Standalone mock server mode:
//     Use `muster test --mock-mcp-server --mock-config=path/to/config.yaml` to
//     run a standalone mock MCP server for manual testing or external integration.
//
// # Configuration Format
//
// Mock MCP servers are configured using YAML files that define the tools and
// their expected behaviors:
//
//	tools:
//	  - name: example-tool
//	    description: "An example mock tool"
//	    input_schema:
//	      type: object
//	      properties:
//	        param1:
//	          type: string
//	          default: "default-value"
//	    responses:
//	      - condition:
//	          param1: "special"
//	        response: "Special response for param1=special"
//	      - response: "Default response: {{.param1}}"
//
// The responses support Go template syntax and can reference input args.
// Conditional responses allow different behaviors based on input values.
//
// # Integration
//
// This package is designed to work seamlessly with the muster testing framework
// and supports the same MCP protocol implementation used by real MCP servers.
// The HTTPServer automatically allocates ports and provides endpoint URLs for
// muster to connect to during test scenario execution.
package mock
