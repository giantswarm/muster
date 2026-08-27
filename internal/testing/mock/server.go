package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/giantswarm/muster/internal/template"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"gopkg.in/yaml.v3"
)

// Server represents a mock MCP server for testing
type Server struct {
	name           string
	tools          []ToolConfig // Direct array of tools instead of config struct
	toolHandlers   map[string]*ToolHandler
	templateEngine *template.Engine
	mcpServer      *server.MCPServer
	debug          bool
	mu             sync.RWMutex

	// clientProtocolVersion and clientName record the initialize request the
	// connecting client sent, which echo_handshake tools report back. mcp-go
	// echoes any revision from mcp.ValidProtocolVersions straight back to the
	// client, so the response alone cannot tell an asked-for revision apart
	// from one the server chose; the request is the only place the caller's
	// own ask is observable.
	clientProtocolVersion string
	clientName            string
}

// NewServerFromFile creates a new mock MCP server from a configuration file
func NewServerFromFile(configPath string, debug bool) (*Server, error) {
	// Read the config file directly
	content, err := os.ReadFile(configPath) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("failed to read mock config file %s: %w", configPath, err)
	}

	// Parse the config structure that contains tools directly
	var configData struct {
		Tools           []ToolConfig     `yaml:"tools"`
		Resources       []ResourceConfig `yaml:"resources"`
		ProtocolVersion string           `yaml:"protocol_version"`
	}
	if err := yaml.Unmarshal(content, &configData); err != nil {
		return nil, fmt.Errorf("failed to parse mock config file %s: %w", configPath, err)
	}

	// Extract name from file path for the server name
	name := filepath.Base(configPath)
	name = strings.TrimSuffix(name, filepath.Ext(name))

	hooks := &server.Hooks{}
	mockServer := &Server{
		name:           name,
		tools:          configData.Tools,
		toolHandlers:   make(map[string]*ToolHandler),
		templateEngine: template.New(),
		debug:          debug,
	}
	hooks.AddBeforeInitialize(func(_ context.Context, _ any, req *mcp.InitializeRequest) {
		mockServer.mu.Lock()
		mockServer.clientProtocolVersion = req.Params.ProtocolVersion
		mockServer.clientName = req.Params.ClientInfo.Name
		mockServer.mu.Unlock()
	})
	if configData.ProtocolVersion != "" {
		// protocol_version replaces the revision mcp-go would answer initialize
		// with, so the mock can stand in for a backend that supports only an
		// older revision. mcp-go otherwise echoes whatever the client asked for,
		// which no down-negotiating backend does. mcp-go builds the result, runs
		// this hook, then serialises it, so the overwrite here is what the
		// client sees.
		hooks.AddAfterInitialize(func(_ context.Context, _ any, _ *mcp.InitializeRequest, result *mcp.InitializeResult) {
			result.ProtocolVersion = configData.ProtocolVersion
		})
	}

	mcpServer := server.NewMCPServer(
		fmt.Sprintf("mock-%s", name),
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(false, len(configData.Resources) > 0),
		server.WithPromptCapabilities(false),
		server.WithHooks(hooks),
	)
	mockServer.mcpServer = mcpServer

	// Initialize tool handlers and register tools
	for _, toolConfig := range configData.Tools {
		handler := NewToolHandler(toolConfig, mockServer.templateEngine, debug)
		mockServer.toolHandlers[toolConfig.Name] = handler

		// Register the tool with the MCP server, propagating any input_schema
		// from the YAML config so downstream consumers (e.g. the aggregator's
		// family-grouping collision check) see the same schema operators
		// declared in pre_configuration.
		mcpServer.AddTool(toolWithSchema(toolConfig), mockServer.createToolHandler(toolConfig.Name))
	}

	// Register static resources. Unlike tools these have no handler config --
	// a read returns the configured text verbatim.
	for _, resourceConfig := range configData.Resources {
		mimeType := resourceConfig.MIMEType
		if mimeType == "" {
			mimeType = "text/plain"
		}
		resource := mcp.NewResource(
			resourceConfig.URI,
			resourceConfig.Name,
			mcp.WithResourceDescription(resourceConfig.Description),
			mcp.WithMIMEType(mimeType),
		)
		uri, text := resourceConfig.URI, resourceConfig.Text
		mcpServer.AddResource(resource, func(_ context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return []mcp.ResourceContents{mcp.TextResourceContents{
				URI:      uri,
				MIMEType: mimeType,
				Text:     text,
			}}, nil
		})
	}

	if debug {
		// Ensure debug output goes to stderr to not interfere with MCP protocol on stdout
		fmt.Fprintf(os.Stderr, "🔧 Mock MCP server '%s' initialized with %d tools and %d resources from %s\n", name, len(mockServer.toolHandlers), len(configData.Resources), configPath)
		for toolName := range mockServer.toolHandlers {
			fmt.Fprintf(os.Stderr, "  • %s\n", toolName)
		}
	}

	return mockServer, nil
}

// toolWithSchema builds an mcp.Tool from a ToolConfig, attaching the raw
// JSON-Schema declared in the YAML config when present. Without this, the
// mock server publishes tools with no input schema and downstream consumers
// (notably the aggregator's family-grouping logic) cannot see the schema
// operators wrote in the scenario fixture.
//
// mcp.NewToolWithRawSchema and mcp.NewTool produce mutually exclusive Tool
// shapes (the former is documented as incompatible with ToolOption); we
// pick one or the other based on whether a schema was declared.
func toolWithSchema(toolConfig ToolConfig) mcp.Tool {
	if len(toolConfig.InputSchema) > 0 {
		if raw, err := json.Marshal(toolConfig.InputSchema); err == nil {
			return mcp.NewToolWithRawSchema(toolConfig.Name, toolConfig.Description, raw)
		}
	}
	return mcp.NewTool(toolConfig.Name, mcp.WithDescription(toolConfig.Description))
}

// createToolHandler creates an MCP tool handler function for the given tool name
func (s *Server) createToolHandler(toolName string) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		s.mu.RLock()
		handler, exists := s.toolHandlers[toolName]
		s.mu.RUnlock()
		if !exists {
			return mcp.NewToolResultError(fmt.Sprintf("tool %s not found", toolName)), nil
		}

		// Convert MCP arguments to the format expected by our mock tool handler
		args := request.GetArguments()

		// Handle the tool call
		result, err := handler.HandleCall(args)
		if err != nil {
			return nil, err
		}

		// echo_handshake tools report the initialize request muster's outbound
		// client sent, so scenarios can pin the negotiated protocol version.
		if handler.config.EchoHandshake {
			s.mu.RLock()
			version, clientName := s.clientProtocolVersion, s.clientName
			s.mu.RUnlock()
			echo := map[string]interface{}{
				"response":                result,
				"client_protocol_version": version,
				"client_name":             clientName,
			}
			jsonBytes, mErr := json.Marshal(echo)
			if mErr != nil {
				return nil, fmt.Errorf("failed to marshal echo_handshake result for tool %s: %w", toolName, mErr)
			}
			return mcp.NewToolResultText(string(jsonBytes)), nil
		}

		// Convert result to MCP format
		if result != nil {
			// Check if result is a map or slice - if so, JSON marshal it
			switch result.(type) {
			case map[string]interface{}, []interface{}, map[interface{}]interface{}:
				// JSON marshal structured data
				if jsonBytes, err := json.Marshal(result); err == nil {
					return mcp.NewToolResultText(string(jsonBytes)), nil
				}
				// Fallback to string representation if JSON marshaling fails
				resultStr := fmt.Sprintf("%v", result)
				return mcp.NewToolResultText(resultStr), nil
			default:
				// For primitive types, convert to string
				resultStr := fmt.Sprintf("%v", result)
				return mcp.NewToolResultText(resultStr), nil
			}
		}

		return mcp.NewToolResultText(""), nil
	}
}

// AddDynamicTool adds a tool to the running MCP server at runtime.
// The mcp-go library automatically sends a notifications/tools/list_changed
// notification to all connected clients.
func (s *Server) AddDynamicTool(toolConfig ToolConfig) {
	handler := NewToolHandler(toolConfig, s.templateEngine, s.debug)

	s.mu.Lock()
	s.toolHandlers[toolConfig.Name] = handler
	s.mu.Unlock()

	s.mcpServer.AddTool(toolWithSchema(toolConfig), s.createToolHandler(toolConfig.Name))

	if s.debug {
		fmt.Fprintf(os.Stderr, "Dynamically added tool '%s' to mock server '%s'\n", toolConfig.Name, s.name)
	}
}

// RemoveDynamicTool removes a tool from the running MCP server at runtime.
// The mcp-go library automatically sends a notifications/tools/list_changed
// notification to all connected clients.
func (s *Server) RemoveDynamicTool(toolName string) {
	s.mu.Lock()
	delete(s.toolHandlers, toolName)
	s.mu.Unlock()

	s.mcpServer.DeleteTools(toolName)

	if s.debug {
		fmt.Fprintf(os.Stderr, "Dynamically removed tool '%s' from mock server '%s'\n", toolName, s.name)
	}
}

// Start starts the mock MCP server using stdio transport
func (s *Server) Start(ctx context.Context) error {
	if s.debug {
		fmt.Fprintf(os.Stderr, "🚀 Starting mock MCP server '%s' on stdio transport\n", s.name)
	}

	// Use the proper MCP library to serve stdio
	// This handles all the protocol details correctly
	return server.ServeStdio(s.mcpServer)
}
