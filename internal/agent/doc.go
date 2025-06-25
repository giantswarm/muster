// Package agent provides an MCP (Model Context Protocol) client implementation
// that can be used to debug and test the muster aggregator's SSE server.
//
// The agent connects to the aggregator endpoint, performs the MCP handshake,
// lists available tools, and waits for notifications about tool changes.
// When tools are added or removed, it automatically fetches the updated list
// and displays the differences.
//
// The package also provides an interactive REPL (Read-Eval-Print Loop) mode
// that allows users to explore and execute MCP tools, view resources, and
// interact with prompts in real-time.
//
// Additionally, the package includes an MCP server mode that exposes
// all REPL functionality via MCP tools. This allows AI assistants to interact
// with muster through the MCP protocol using stdio transport.
//
// This package is primarily used by the `muster agent` command for debugging
// purposes, but can also be used programmatically to test MCP server implementations.
//
// Example usage (normal mode):
//
//	logger := agent.NewLogger(true, true, false)  // verbose=true, color=true, jsonRPC=false
//	client := agent.NewClient("http://localhost:8090/sse", logger)
//
//	ctx := context.Background()
//	if err := client.Run(ctx); err != nil {
//	    log.Fatal(err)
//	}
//
// Example usage (REPL mode):
//
//	logger := agent.NewLogger(true, true, false)
//	client := agent.NewClient("http://localhost:8090/sse", logger)
//	repl := agent.NewREPL(client, logger)
//
//	ctx := context.Background()
//	if err := repl.Run(ctx); err != nil {
//	    log.Fatal(err)
//	}
//
// Example usage (MCP server mode):
//
//	logger := agent.NewLogger(true, true, false)
//	server, err := agent.NewMCPServer("http://localhost:8090/sse", logger, false)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	ctx := context.Background()
//	if err := server.Start(ctx); err != nil {
//	    log.Fatal(err)
//	}
//
// The MCP server mode is typically used via the `muster agent --mcp-server` command
// and configured in AI assistant MCP settings.
//
// MCP Tools exposed by the server mode:
//   - list_tools: List all available tools from connected MCP servers
//   - list_resources: List all available resources from connected MCP servers
//   - list_prompts: List all available prompts from connected MCP servers
//   - describe_tool: Get detailed information about a specific tool
//   - describe_resource: Get detailed information about a specific resource
//   - describe_prompt: Get detailed information about a specific prompt
//   - call_tool: Execute a tool with the given arguments
//   - get_resource: Retrieve the contents of a resource
//   - get_prompt: Get a prompt with the given arguments
package agent
