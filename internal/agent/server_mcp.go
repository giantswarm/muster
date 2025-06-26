package agent

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// MCPServer wraps the agent functionality and exposes it as MCP tools for AI assistants.
// It acts as a bridge between AI assistants and the muster aggregator, enabling
// programmatic access to all MCP capabilities through the standard MCP protocol.
//
// The server exposes comprehensive tool operations including:
//   - Listing and describing tools, resources, and prompts
//   - Executing tools with argument validation
//   - Retrieving resource contents and prompt templates
//   - Advanced filtering and search capabilities
//   - Core tool identification and categorization
//
// Key features:
//   - Stdio transport for AI assistant integration
//   - JSON-formatted responses for structured data consumption
//   - Error handling with detailed error messages
//   - Optional client notification support
//   - Tool availability caching and refresh
type MCPServer struct {
	client        *Client
	logger        *Logger
	mcpServer     *server.MCPServer
	notifyClients bool
}

// NewMCPServer creates a new MCP server that exposes agent functionality as MCP tools.
// This enables AI assistants to interact with muster through the standard MCP protocol
// using stdio transport.
//
// Parameters:
//   - client: MCP client for aggregator communication
//   - logger: Logger instance for structured logging
//   - notifyClients: Whether to enable client notifications for tool changes
//
// The server is initialized with:
//   - Complete tool registry for agent operations
//   - Stdio transport for AI assistant integration
//   - Tool, resource, and prompt capabilities
//   - Optional notification support for dynamic updates
//
// Exposed tools include: list_tools, describe_tool, call_tool, get_resource,
// get_prompt, filter_tools, list_core_tools, and more.
//
// Example:
//
//	client := agent.NewClient("http://localhost:8090/sse", logger, agent.TransportSSE)
//	server, err := agent.NewMCPServer(client, logger, false)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	if err := server.Start(ctx); err != nil {
//	    log.Fatal(err)
//	}
func NewMCPServer(client *Client, logger *Logger, notifyClients bool) (*MCPServer, error) {
	// Create MCP server
	mcpServer := server.NewMCPServer(
		"muster-agent",
		"1.0.0",
		server.WithToolCapabilities(notifyClients),
		server.WithResourceCapabilities(false, false),
		server.WithPromptCapabilities(false),
	)

	ms := &MCPServer{
		client:        client,
		logger:        logger,
		mcpServer:     mcpServer,
		notifyClients: notifyClients,
	}

	// Register all tools
	ms.registerTools()

	return ms, nil
}

// Start starts the MCP server using stdio transport for AI assistant integration.
// This method blocks until the server is terminated, handling MCP protocol
// communication over stdin/stdout. It's designed to be used as the main
// entry point when running as an MCP server for AI assistants.
//
// The server will continue running until the context is cancelled or
// the stdio connection is closed by the client.
func (m *MCPServer) Start(ctx context.Context) error {
	// Start the stdio server
	return server.ServeStdio(m.mcpServer)
}

// registerTools registers all MCP tools
func (m *MCPServer) registerTools() {
	// List tools
	listToolsTool := mcp.NewTool("list_tools",
		mcp.WithDescription("List all available tools from connected MCP servers"),
	)
	m.mcpServer.AddTool(listToolsTool, m.handleListTools)

	// List resources
	listResourcesTool := mcp.NewTool("list_resources",
		mcp.WithDescription("List all available resources from connected MCP servers"),
	)
	m.mcpServer.AddTool(listResourcesTool, m.handleListResources)

	// List prompts
	listPromptsTool := mcp.NewTool("list_prompts",
		mcp.WithDescription("List all available prompts from connected MCP servers"),
	)
	m.mcpServer.AddTool(listPromptsTool, m.handleListPrompts)

	// Describe tool
	describeToolTool := mcp.NewTool("describe_tool",
		mcp.WithDescription("Get detailed information about a specific tool"),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Name of the tool to describe"),
		),
	)
	m.mcpServer.AddTool(describeToolTool, m.handleDescribeTool)

	// Describe resource
	describeResourceTool := mcp.NewTool("describe_resource",
		mcp.WithDescription("Get detailed information about a specific resource"),
		mcp.WithString("uri",
			mcp.Required(),
			mcp.Description("URI of the resource to describe"),
		),
	)
	m.mcpServer.AddTool(describeResourceTool, m.handleDescribeResource)

	// Describe prompt
	describePromptTool := mcp.NewTool("describe_prompt",
		mcp.WithDescription("Get detailed information about a specific prompt"),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Name of the prompt to describe"),
		),
	)
	m.mcpServer.AddTool(describePromptTool, m.handleDescribePrompt)

	// Call tool
	callToolTool := mcp.NewTool("call_tool",
		mcp.WithDescription("Execute a tool with the given arguments"),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Name of the tool to call"),
		),
		mcp.WithObject("arguments",
			mcp.Description("Arguments to pass to the tool (as JSON object)"),
		),
	)
	m.mcpServer.AddTool(callToolTool, m.handleCallTool)

	// Get resource
	getResourceTool := mcp.NewTool("get_resource",
		mcp.WithDescription("Retrieve the contents of a resource"),
		mcp.WithString("uri",
			mcp.Required(),
			mcp.Description("URI of the resource to retrieve"),
		),
	)
	m.mcpServer.AddTool(getResourceTool, m.handleGetResource)

	// Get prompt
	getPromptTool := mcp.NewTool("get_prompt",
		mcp.WithDescription("Get a prompt with the given arguments"),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Name of the prompt to get"),
		),
		mcp.WithObject("arguments",
			mcp.Description("Arguments to pass to the prompt (as JSON object with string values)"),
		),
	)
	m.mcpServer.AddTool(getPromptTool, m.handleGetPrompt)

	// List core tools
	listCoreToolsTool := mcp.NewTool("list_core_tools",
		mcp.WithDescription("List core muster tools (built-in functionality separate from external MCP servers)"),
	)
	m.mcpServer.AddTool(listCoreToolsTool, m.handleListCoreTools)

	// Filter tools
	filterToolsTool := mcp.NewTool("filter_tools",
		mcp.WithDescription("Filter available tools based on name patterns or descriptions"),
		mcp.WithString("pattern",
			mcp.Description("Pattern to match against tool names (supports wildcards like *)"),
		),
		mcp.WithString("description_filter",
			mcp.Description("Filter by description content (case-insensitive substring match)"),
		),
		mcp.WithBoolean("case_sensitive",
			mcp.Description("Whether pattern matching should be case-sensitive (default: false)"),
		),
	)
	m.mcpServer.AddTool(filterToolsTool, m.handleFilterTools)
}
