package agent

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// MCPServer wraps the agent functionality and exposes it via MCP
type MCPServer struct {
	client        *Client
	logger        *Logger
	mcpServer     *server.MCPServer
	notifyClients bool
}

// NewMCPServer creates a new MCP server that exposes agent functionality
// NewMCPServerWithTransport creates a new MCP server with specified transport
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

// Start starts the MCP server using stdio transport
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
