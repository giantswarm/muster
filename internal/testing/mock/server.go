package mock

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"unsafe"

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

	mcpServer := server.NewMCPServer(
		fmt.Sprintf("mock-%s", name),
		"1.0.0",
		server.WithToolCapabilities(true),
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

	tool := mcp.NewTool(toolConfig.Name, mcp.WithDescription(toolConfig.Description))
	s.mcpServer.AddTool(tool, s.createToolHandler(toolConfig.Name))

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

// AddDynamicToolSilently adds a tool to the server so that ListTools
// returns it, but WITHOUT sending a notifications/tools/list_changed
// notification. This simulates a silent server redeployment where the
// tool list changes without the connected client being notified.
//
// It bypasses the mcp-go library's AddTool (which always sends a
// notification) by writing directly to the internal tools map via
// reflect + unsafe. This is acceptable because this code is used
// exclusively in test scenarios.
func (s *Server) AddDynamicToolSilently(toolConfig ToolConfig) {
	handler := NewToolHandler(toolConfig, s.templateEngine, s.debug)

	s.mu.Lock()
	s.toolHandlers[toolConfig.Name] = handler
	s.mu.Unlock()

	tool := mcp.NewTool(toolConfig.Name, mcp.WithDescription(toolConfig.Description))
	st := server.ServerTool{
		Tool:    tool,
		Handler: s.createToolHandler(toolConfig.Name),
	}

	injectToolSilently(s.mcpServer, toolConfig.Name, st)

	if s.debug {
		fmt.Fprintf(os.Stderr, "Silently added tool '%s' to mock server '%s' (no notification)\n", toolConfig.Name, s.name)
	}
}

// injectToolSilently writes a ServerTool directly into the MCPServer's
// unexported tools map, acquiring the toolsMu lock but skipping the
// notification that AddTool/AddTools normally sends. This is test-only
// code that uses reflect + unsafe to access unexported struct fields.
func injectToolSilently(srv *server.MCPServer, name string, st server.ServerTool) {
	v := reflect.ValueOf(srv).Elem()

	muField := v.FieldByName("toolsMu")
	//nolint:gosec // Test-only code: accessing unexported field via unsafe.
	mu := (*sync.RWMutex)(unsafe.Pointer(muField.UnsafeAddr()))

	toolsField := v.FieldByName("tools")
	//nolint:gosec // Test-only code: accessing unexported field via unsafe.
	toolsPtr := (*map[string]server.ServerTool)(unsafe.Pointer(toolsField.UnsafeAddr()))

	mu.Lock()
	(*toolsPtr)[name] = st
	mu.Unlock()
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
