// Package mock provides mock MCP server functionality for testing muster components.
//
// This package contains the implementation of mock MCP servers that can be used
// in test scenarios to simulate external MCP servers with predefined behaviors.
//
// The mock MCP server functionality allows test scenarios to define:
// - Mock tools with configurable responses
// - Conditional responses based on input parameters
// - Simulated delays and error conditions
// - Template-based response generation
//
// Key Components:
//
// Server: The main mock MCP server that implements the MCP protocol over stdio.
// It can be configured with a set of tools and their expected behaviors.
//
// ToolHandler: Handles individual tool calls with configurable responses based
// on input parameters and conditions.
//
// ToolConfig & ToolResponse: Configuration structures that define how mock tools
// should behave, including input schemas, response conditions, and template-based
// response generation.
//
// Usage:
//
// Mock MCP servers can be used in two ways:
//
//  1. Embedded in test scenarios via YAML configuration:
//     The test framework automatically starts mock MCP servers based on scenario
//     pre-configuration that includes MCP servers with "tools" in their config.
//
//  2. Standalone mock server mode:
//     Use `muster test --mock-mcp-server --mock-config=path/to/config.yaml` to
//     run a standalone mock MCP server for manual testing or external integration.
//
// Configuration Format:
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
// The responses support Go template syntax and can reference input parameters.
// Conditional responses allow different behaviors based on input values.
//
// Integration:
//
// This package is designed to work seamlessly with the muster testing framework
// and supports the same MCP protocol implementation used by real MCP servers.
package mock
