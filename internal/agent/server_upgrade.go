package agent

import (
	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/metatools"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterClientToolsOnServer registers all meta-tools from a connected client onto an MCP server.
// This is used to upgrade a pending auth server to a full server after authentication.
//
// The tools use the transport bridge pattern (Issue #344) where each handler forwards
// to the corresponding server meta-tool via the client.
func RegisterClientToolsOnServer(mcpServer *server.MCPServer, client *Client) {
	// Create a temporary MCPServer wrapper to access the forwarding handler method
	wrapper := &MCPServer{
		client:        client,
		logger:        client.logger,
		mcpServer:     mcpServer,
		notifyClients: true,
		authPoller:    newAuthPoller(client, client.logger),
	}

	// Register all the standard agent tools using the transport bridge pattern
	registerAgentTools(wrapper)
}

// registerAgentTools registers the aggregator's meta-tools on an MCPServer.
// All handlers use the transport bridge pattern and forward to the server's
// meta-tool of the same name, arguments passed through untouched.
//
// The definitions — names, descriptions, arguments — are the provider's own,
// so what an AI assistant sees through the local agent is exactly what the
// aggregator advertises: a new meta-tool, argument or description reaches the
// bridge without a hand-maintained copy that can fall behind (the copy this
// replaced still described filter_tools as returning full specifications).
func registerAgentTools(m *MCPServer) {
	for _, meta := range metatools.NewProvider().GetTools() {
		tool := mcp.Tool{
			Name:        meta.Name,
			Description: meta.Description,
			InputSchema: api.InputSchemaFromArgs(meta.Args),
		}
		m.mcpServer.AddTool(tool, m.forwardToServerMetaTool(meta.Name))
	}
}
