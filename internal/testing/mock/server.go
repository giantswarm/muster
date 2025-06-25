package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"muster/internal/template"

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
}

// NewServer creates a new mock MCP server with the given configuration
func NewServer(configName, scenarioPath string, debug bool) (*Server, error) {
	// Load the mock server configuration
	tools, err := loadServerConfig(configName, scenarioPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load mock server config: %w", err)
	}

	// Create the MCP server
	mcpServer := server.NewMCPServer(
		fmt.Sprintf("mock-%s", configName),
		"1.0.0",
		server.WithToolCapabilities(false),
		server.WithResourceCapabilities(false, false),
		server.WithPromptCapabilities(false),
	)

	mockServer := &Server{
		name:           configName,
		tools:          tools,
		toolHandlers:   make(map[string]*ToolHandler),
		templateEngine: template.New(),
		mcpServer:      mcpServer,
		debug:          debug,
	}

	// Initialize tool handlers and register tools
	for _, toolConfig := range tools {
		handler := NewToolHandler(toolConfig, mockServer.templateEngine, debug)
		mockServer.toolHandlers[toolConfig.Name] = handler

		// Register the tool with the MCP server
		tool := mcp.NewTool(toolConfig.Name, mcp.WithDescription(toolConfig.Description))
		mcpServer.AddTool(tool, mockServer.createToolHandler(toolConfig.Name))
	}

	if debug {
		fmt.Fprintf(os.Stderr, "🔧 Mock MCP server '%s' initialized with %d tools\n", configName, len(mockServer.toolHandlers))
		for toolName := range mockServer.toolHandlers {
			fmt.Fprintf(os.Stderr, "  • %s\n", toolName)
		}
	}

	return mockServer, nil
}

// NewServerFromFile creates a new mock MCP server from a configuration file
func NewServerFromFile(configPath string, debug bool) (*Server, error) {
	// Read the config file directly
	content, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read mock config file %s: %w", configPath, err)
	}

	// Parse the config structure that contains tools directly
	var configData struct {
		Tools []ToolConfig `yaml:"tools"`
	}
	if err := yaml.Unmarshal(content, &configData); err != nil {
		return nil, fmt.Errorf("failed to parse mock config file %s: %w", configPath, err)
	}

	// Extract name from file path for the server name
	name := filepath.Base(configPath)
	name = strings.TrimSuffix(name, filepath.Ext(name))

	// Create the MCP server
	mcpServer := server.NewMCPServer(
		fmt.Sprintf("mock-%s", name),
		"1.0.0",
		server.WithToolCapabilities(false),
		server.WithResourceCapabilities(false, false),
		server.WithPromptCapabilities(false),
	)

	mockServer := &Server{
		name:           name,
		tools:          configData.Tools,
		toolHandlers:   make(map[string]*ToolHandler),
		templateEngine: template.New(),
		mcpServer:      mcpServer,
		debug:          debug,
	}

	// Initialize tool handlers and register tools
	for _, toolConfig := range configData.Tools {
		handler := NewToolHandler(toolConfig, mockServer.templateEngine, debug)
		mockServer.toolHandlers[toolConfig.Name] = handler

		// Register the tool with the MCP server
		tool := mcp.NewTool(toolConfig.Name, mcp.WithDescription(toolConfig.Description))
		mcpServer.AddTool(tool, mockServer.createToolHandler(toolConfig.Name))
	}

	if debug {
		// Ensure debug output goes to stderr to not interfere with MCP protocol on stdout
		fmt.Fprintf(os.Stderr, "🔧 Mock MCP server '%s' initialized with %d tools from %s\n", name, len(mockServer.toolHandlers), configPath)
		for toolName := range mockServer.toolHandlers {
			fmt.Fprintf(os.Stderr, "  • %s\n", toolName)
		}
	}

	return mockServer, nil
}

// createToolHandler creates an MCP tool handler function for the given tool name
func (s *Server) createToolHandler(toolName string) func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		handler, exists := s.toolHandlers[toolName]
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

// Start starts the mock MCP server using stdio transport
func (s *Server) Start(ctx context.Context) error {
	if s.debug {
		fmt.Fprintf(os.Stderr, "🚀 Starting mock MCP server '%s' on stdio transport\n", s.name)
	}

	// Use the proper MCP library to serve stdio
	// This handles all the protocol details correctly
	return server.ServeStdio(s.mcpServer)
}

// loadServerConfig loads mock server configuration from test scenarios
func loadServerConfig(configName, scenarioPath string) ([]ToolConfig, error) {
	var tools []ToolConfig

	err := filepath.WalkDir(scenarioPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		var scenario TestScenario
		if err := yaml.Unmarshal(content, &scenario); err != nil {
			return nil
		}

		if scenario.PreConfiguration != nil {
			for _, mcpServer := range scenario.PreConfiguration.MCPServers {
				if mcpServer.Name == configName {
					// Check if this is a mock server (has tools in config)
					if toolsInterface, hasMockTools := mcpServer.Config["tools"]; hasMockTools {
						// Convert the config to tools array
						configBytes, err := yaml.Marshal(map[string]interface{}{"tools": toolsInterface})
						if err != nil {
							continue
						}
						var configData struct {
							Tools []ToolConfig `yaml:"tools"`
						}
						if err := yaml.Unmarshal(configBytes, &configData); err != nil {
							continue
						}
						tools = configData.Tools
						return filepath.SkipAll // Found it, stop searching
					}
				}
			}
		}

		return nil
	})

	if err != nil {
		return tools, fmt.Errorf("failed to search scenarios: %w", err)
	}

	if len(tools) == 0 {
		return tools, fmt.Errorf("mock server configuration '%s' not found in scenarios", configName)
	}

	return tools, nil
}
