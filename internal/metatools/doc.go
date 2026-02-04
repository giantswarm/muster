// Package metatools provides server-side meta-tool handlers for the MCP aggregator.
//
// This package implements the meta-tools that enable AI assistants to discover
// and interact with tools, resources, and prompts available through the muster
// aggregator. Meta-tools provide an indirection layer that allows AI assistants
// to dynamically discover and call tools without hard-coding specific tool names.
//
// # Architecture
//
// The metatools package follows the API Service Locator Pattern established
// in the muster architecture. It retrieves the aggregator handler through
// api.GetAggregator() and does not directly depend on other packages.
//
// Key components:
//   - Provider: Main entry point that registers meta-tools with the aggregator
//   - Handlers: Implementation of individual meta-tool operations
//   - Formatters: Response formatting utilities for JSON output
//   - Adapter: API layer integration following the service locator pattern
//
// # Available Meta-Tools
//
// Discovery tools:
//   - list_tools: List all available tools from connected MCP servers
//   - describe_tool: Get detailed information about a specific tool
//   - list_core_tools: List core muster tools (built-in functionality)
//   - filter_tools: Filter tools based on name patterns or descriptions
//
// Execution tools:
//   - call_tool: Execute any tool by name with arguments
//
// Resource tools:
//   - list_resources: List all available resources
//   - describe_resource: Get detailed information about a specific resource
//   - get_resource: Retrieve resource contents
//
// Prompt tools:
//   - list_prompts: List all available prompts
//   - describe_prompt: Get detailed information about a specific prompt
//   - get_prompt: Execute a prompt with arguments
//
// # Session Awareness
//
// Meta-tools are session-aware and use the context session ID for tool
// visibility. This ensures that tools requiring authentication are only
// visible to authenticated sessions.
//
// # Response Format
//
// The call_tool meta-tool preserves the full CallToolResult structure
// (IsError, Content types) as structured JSON, rather than flattening to
// text. This maintains BDD test validation fidelity and enables proper
// response unwrapping by clients.
//
// # Usage
//
// The metatools package is initialized and registered during application
// startup via the API adapter:
//
//	// In app initialization
//	adapter := metatools.NewAdapter()
//	adapter.Register()
//
// Once registered, meta-tools are available through the aggregator's
// tool interface and can be called like any other tool.
package metatools
