package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/giantswarm/muster/internal/agent"
	agentoauth "github.com/giantswarm/muster/internal/agent/oauth"
	"github.com/giantswarm/muster/internal/api"
	"github.com/giantswarm/muster/internal/config"
	"github.com/giantswarm/muster/internal/metatools"
	pkgoauth "github.com/giantswarm/muster/pkg/oauth"

	"github.com/briandowns/spinner"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/mark3labs/mcp-go/mcp"
	"gopkg.in/yaml.v3"
)

// Re-export mcp types for convenience so cmd package doesn't need to import mcp directly
type (
	// MCPTool is an alias for mcp.Tool for use in cmd package
	MCPTool = mcp.Tool
	// MCPResource is an alias for mcp.Resource for use in cmd package
	MCPResource = mcp.Resource
	// MCPPrompt is an alias for mcp.Prompt for use in cmd package
	MCPPrompt = mcp.Prompt
)

// OutputFormat represents the supported output formats for CLI commands.
// This allows users to choose how they want to receive command results.
type OutputFormat string

const (
	// OutputFormatTable formats output as a kubectl-style plain table
	OutputFormatTable OutputFormat = "table"
	// OutputFormatWide formats output as a table with additional columns
	OutputFormatWide OutputFormat = "wide"
	// OutputFormatJSON formats output as raw JSON data
	OutputFormatJSON OutputFormat = "json"
	// OutputFormatYAML formats output as YAML data converted from JSON
	OutputFormatYAML OutputFormat = "yaml"
)

// ValidOutputFormats contains all valid output format values.
var ValidOutputFormats = []OutputFormat{
	OutputFormatTable,
	OutputFormatWide,
	OutputFormatJSON,
	OutputFormatYAML,
}

// ValidateOutputFormat validates that the given format string is a supported output format.
// Returns nil if valid, or an error with a helpful message listing valid formats.
func ValidateOutputFormat(format string) error {
	switch OutputFormat(format) {
	case OutputFormatTable, OutputFormatWide, OutputFormatJSON, OutputFormatYAML:
		return nil
	default:
		return fmt.Errorf("unsupported output format: %q (valid: table, wide, json, yaml)", format)
	}
}

// AuthMode represents authentication behavior for CLI commands.
type AuthMode string

const (
	// AuthModeAuto automatically triggers OAuth browser login when authentication is required.
	// This is the default behavior.
	AuthModeAuto AuthMode = "auto"
	// AuthModePrompt prompts the user before triggering authentication.
	AuthModePrompt AuthMode = "prompt"
	// AuthModeNone fails immediately on 401 without attempting authentication.
	AuthModeNone AuthMode = "none"
)

// AuthModeEnvVar is the environment variable name for setting the default auth mode.
const AuthModeEnvVar = "MUSTER_AUTH_MODE"

// EndpointEnvVar is the environment variable name for setting the default endpoint.
const EndpointEnvVar = "MUSTER_ENDPOINT"

// ParseAuthMode parses a string into an AuthMode, with validation.
func ParseAuthMode(s string) (AuthMode, error) {
	switch strings.ToLower(s) {
	case "auto", "":
		return AuthModeAuto, nil
	case "prompt":
		return AuthModePrompt, nil
	case "none":
		return AuthModeNone, nil
	default:
		return AuthModeAuto, fmt.Errorf("invalid auth mode %q: must be one of 'auto', 'prompt', or 'none'", s)
	}
}

// GetDefaultAuthMode returns the default auth mode from environment or "auto".
func GetDefaultAuthMode() AuthMode {
	if envMode := os.Getenv(AuthModeEnvVar); envMode != "" {
		mode, err := ParseAuthMode(envMode)
		if err == nil {
			return mode
		}
		// Invalid env value, fall through to default
	}
	return AuthModeAuto
}

// GetDefaultEndpoint returns the endpoint from environment variable if set.
func GetDefaultEndpoint() string {
	return os.Getenv(EndpointEnvVar)
}

// GetAuthModeWithOverride returns the auth mode from the provided override string,
// falling back to the environment variable default if the override is empty.
// This consolidates the common pattern used across CLI commands.
//
// Note: ParseAuthMode already handles empty string as "auto", so this function
// adds environment variable lookup as an intermediate step.
func GetAuthModeWithOverride(override string) (AuthMode, error) {
	if override != "" {
		return ParseAuthMode(override)
	}
	return GetDefaultAuthMode(), nil
}

// ExecutorOptions contains configuration options for tool execution.
// These options control how commands are executed and how output is formatted.
type ExecutorOptions struct {
	// Format specifies the desired output format (table, json, yaml)
	Format OutputFormat
	// NoHeaders suppresses the header row in table output
	NoHeaders bool
	// Quiet suppresses progress indicators and non-essential output
	Quiet bool
	// Debug enables verbose logging of MCP protocol messages and initialization
	Debug bool
	// ConfigPath specifies a custom configuration directory path
	ConfigPath string
	// Endpoint overrides the aggregator endpoint URL for remote connections
	Endpoint string
	// Context specifies a named context to use for endpoint resolution
	Context string
	// AuthMode controls authentication behavior (auto, prompt, none)
	AuthMode AuthMode
}

// ToolExecutor provides high-level tool execution functionality with formatted output.
// It handles the connection to the muster aggregator, executes tools, and formats
// the results according to the specified output format. This is the main interface
// for CLI commands that need to interact with muster services.
type ToolExecutor struct {
	// client is the MCP client for communicating with the aggregator
	client *agent.Client
	// options contains execution configuration
	options ExecutorOptions
	// formatter handles table formatting when output format is table
	formatter *TableFormatter
	// endpoint is the resolved endpoint URL
	endpoint string
	// isRemote indicates if this is a remote (non-localhost) connection
	isRemote bool
}

// NewToolExecutor creates a new tool executor with the specified options.
// It establishes the connection configuration and initializes the MCP client
// for communication with the muster aggregator server.
//
// Args:
//   - options: Configuration options for execution and output formatting
//
// Returns:
//   - *ToolExecutor: Configured tool executor ready for use
//   - error: Configuration or connection setup error
func NewToolExecutor(options ExecutorOptions) (*ToolExecutor, error) {
	// Use DevNullLogger by default to suppress MCP protocol messages
	// Only enable verbose logging when Debug mode is explicitly requested
	var logger *agent.Logger
	if options.Debug {
		logger = agent.NewLogger(true, true, false)
	} else {
		logger = agent.NewDevNullLogger()
	}

	var endpoint string
	var transport agent.TransportType
	var isRemote bool

	// Resolve endpoint using the precedence order:
	// 1. --endpoint flag
	// 2. --context flag
	// 3. MUSTER_CONTEXT environment variable
	// 4. current-context from contexts.yaml
	// 5. config-based fallback
	resolvedEndpoint, err := ResolveEndpoint(options.Endpoint, options.Context)
	if err != nil {
		return nil, err
	}

	if resolvedEndpoint != "" {
		endpoint = resolvedEndpoint
		isRemote = IsRemoteEndpoint(endpoint)
		// Infer transport from URL path
		if strings.HasSuffix(endpoint, "/sse") {
			transport = agent.TransportSSE
		} else {
			transport = agent.TransportStreamableHTTP
		}
	} else {
		// Fall back to config-based endpoint resolution
		if options.ConfigPath == "" {
			return nil, fmt.Errorf("Logic error: empty tool executor ConfigPath")
		}

		cfg, err := config.LoadConfig(options.ConfigPath)
		if err != nil {
			return nil, err
		}

		transport = agent.TransportType(cfg.Aggregator.Transport)
		switch transport {
		case agent.TransportStreamableHTTP, agent.TransportSSE:
			// Supported transports
		default:
			return nil, fmt.Errorf("unsupported transport: %s", cfg.Aggregator.Transport)
		}

		endpoint = GetAggregatorEndpoint(&cfg)
		isRemote = IsRemoteEndpoint(endpoint)
	}

	// Check if server is running first (for local servers only)
	// Remote servers may require auth which we handle during Connect
	if !isRemote {
		if err := CheckServerRunning(endpoint); err != nil {
			return nil, err
		}
	}

	client := agent.NewClient(endpoint, logger, transport)

	// Handle MCP notifications silently unless debug mode is enabled
	go func() {
		for notification := range client.NotificationChan {
			if options.Debug {
				logger.Debug("MCP Notification: %s", notification.Method)
			}
		}
	}()

	return &ToolExecutor{
		client:    client,
		options:   options,
		formatter: NewTableFormatter(options),
		endpoint:  endpoint,
		isRemote:  isRemote,
	}, nil
}

// GetClient returns the underlying agent client for advanced use cases like streaming.
func (e *ToolExecutor) GetClient() *agent.Client {
	return e.client
}

// Connect establishes a connection to the muster aggregator server.
// It shows a progress spinner unless quiet mode is enabled, and handles
// connection errors with appropriate user feedback. For remote servers,
// it uses mcp-go's OAuth transport for automatic token injection and
// typed 401 error handling.
//
// Args:
//   - ctx: Context for connection timeout and cancellation
//
// Returns:
//   - error: Connection error, if any
func (e *ToolExecutor) Connect(ctx context.Context) error {
	if e.isRemote && e.options.AuthMode != AuthModeNone {
		if err := e.setupAuthentication(ctx); err != nil {
			return err
		}
	}

	if e.options.Quiet {
		return e.connectWithAuthHandling(ctx)
	}

	s := spinner.New(spinner.CharSets[14], 100*time.Millisecond)
	s.Suffix = " Connecting to muster server..."
	s.Start()
	defer s.Stop()

	err := e.connectWithAuthHandling(ctx)
	if err != nil {
		s.FinalMSG = text.FgRed.Sprint("Failed to connect to muster server") + "\n"
	}
	return err
}

// connectWithAuthHandling connects and handles OAuthAuthorizationRequiredError
// by triggering the auth flow and retrying.
func (e *ToolExecutor) connectWithAuthHandling(ctx context.Context) error {
	err := e.client.Connect(ctx)
	if err == nil {
		return nil
	}

	if pkgoauth.IsOAuthUnauthorizedError(err) {
		return e.handleAuthError(ctx, err)
	}

	return err
}

// setupAuthentication configures the mcp-go OAuth transport for remote connections.
// It creates an AgentTokenStore backed by the file-based token store and sets up
// the OAuth config on the client. The transport automatically injects bearer tokens
// and returns typed errors on 401.
func (e *ToolExecutor) setupAuthentication(ctx context.Context) error {
	authHandler := api.GetAuthHandler()
	if authHandler == nil {
		adapter, err := NewAuthAdapter()
		if err != nil {
			return nil
		}
		adapter.Register()
		authHandler = api.GetAuthHandler()
		if authHandler == nil {
			return nil
		}
	}

	if sessionID := authHandler.GetSessionID(); sessionID != "" {
		e.client.SetHeader(api.ClientSessionIDHeader, sessionID)
	}

	oauthCfg, agentStore, err := agentoauth.SetupOAuthConfig(e.endpoint)
	if err != nil {
		slog.Debug("Could not set up OAuth transport, proceeding without it",
			"endpoint", e.endpoint, "error", err)
		return nil
	}
	e.client.SetOAuthConfig(*oauthCfg, agentStore)

	return nil
}

// triggerAuthentication handles authentication based on the configured AuthMode.
// After Login() completes, the token is stored in the file-based token store
// and the mcp-go transport will pick it up automatically on the next connection.
func (e *ToolExecutor) triggerAuthentication(ctx context.Context, authHandler api.AuthHandler) error {
	switch e.options.AuthMode {
	case AuthModeAuto:
		if !e.options.Quiet {
			fmt.Println("Authentication required. Opening browser...")
		}
		if err := authHandler.Login(ctx, e.endpoint); err != nil {
			return &AuthFailedError{Endpoint: e.endpoint, Reason: err}
		}
		return nil

	case AuthModePrompt:
		if !e.options.Quiet {
			fmt.Printf("Authentication required for %s\n", e.endpoint)
			fmt.Print("Open browser to authenticate? [Y/n]: ")

			var response string
			fmt.Scanln(&response)
			response = strings.TrimSpace(strings.ToLower(response))
			if response != "" && response != "y" && response != "yes" {
				return &AuthRequiredError{Endpoint: e.endpoint}
			}
		}
		if err := authHandler.Login(ctx, e.endpoint); err != nil {
			return &AuthFailedError{Endpoint: e.endpoint, Reason: err}
		}
		return nil

	case AuthModeNone:
		return &AuthRequiredError{Endpoint: e.endpoint}

	default:
		if !e.options.Quiet {
			fmt.Println("Authentication required. Opening browser...")
		}
		if err := authHandler.Login(ctx, e.endpoint); err != nil {
			return &AuthFailedError{Endpoint: e.endpoint, Reason: err}
		}
		return nil
	}
}

// handleAuthError handles OAuthAuthorizationRequiredError during connection.
// It clears invalid tokens and triggers authentication, then retries.
func (e *ToolExecutor) handleAuthError(ctx context.Context, originalErr error) error {
	if e.options.AuthMode == AuthModeNone {
		return &AuthRequiredError{Endpoint: e.endpoint}
	}

	authHandler := api.GetAuthHandler()
	if authHandler == nil {
		return &AuthRequiredError{Endpoint: e.endpoint}
	}

	_ = authHandler.Logout(e.endpoint)

	if err := e.triggerAuthentication(ctx, authHandler); err != nil {
		return err
	}

	return e.client.Connect(ctx)
}

// Close gracefully closes the connection to the aggregator server.
// This should be called when the executor is no longer needed to free resources.
//
// Returns:
//   - error: Error during connection cleanup, if any
func (e *ToolExecutor) Close() error {
	return e.client.Close()
}

// Execute executes a tool with the given args and formats the output.
// This is the main method for running muster tools through the CLI interface.
// It handles progress indication, error formatting, and output formatting
// according to the configured options.
//
// Args:
//   - ctx: Context for execution timeout and cancellation
//   - toolName: Name of the tool to execute
//   - args: Tool args as key-value pairs
//
// Returns:
//   - error: Execution or formatting error, if any
func (e *ToolExecutor) Execute(ctx context.Context, toolName string, args map[string]interface{}) error {
	var s *spinner.Spinner
	if !e.options.Quiet {
		s = spinner.New(spinner.CharSets[14], 100*time.Millisecond)
		s.Suffix = " Executing command..."
		s.Start()
	}

	result, err := e.client.CallTool(ctx, toolName, args)

	if s != nil {
		s.Stop()
	}

	if err != nil {
		if s != nil {
			fmt.Fprintf(os.Stderr, "%s\n", text.FgRed.Sprint("❌ Command failed"))
		}
		return fmt.Errorf("failed to execute tool %s: %w", toolName, err)
	}

	if result.IsError {
		if s != nil {
			fmt.Fprintf(os.Stderr, "%s\n", text.FgRed.Sprint("❌ Command returned error"))
		}
		return e.formatError(result)
	}

	return e.formatOutput(result)
}

// ExecuteJSON executes a tool and returns the result as parsed JSON.
// This method is useful when you need to work with structured data
// programmatically rather than displaying it to users.
//
// Args:
//   - ctx: Context for execution timeout and cancellation
//   - toolName: Name of the tool to execute
//   - args: Tool args as key-value pairs
//
// Returns:
//   - interface{}: Parsed JSON result as Go data structures
//   - error: Execution or JSON parsing error, if any
func (e *ToolExecutor) ExecuteJSON(ctx context.Context, toolName string, args map[string]interface{}) (interface{}, error) {
	return e.client.CallToolJSON(ctx, toolName, args)
}

// formatError formats and displays error output from tool execution.
// It extracts error messages from the MCP result and presents them
// in a user-friendly format. The error is returned so cobra can handle
// the exit code, but not printed directly to avoid duplicate error messages.
//
// Args:
//   - result: MCP call result containing error information
//
// Returns:
//   - error: Formatted error for propagation up the call stack
func (e *ToolExecutor) formatError(result *mcp.CallToolResult) error {
	var errorMsgs []string
	for _, content := range result.Content {
		if textContent, ok := mcp.AsTextContent(content); ok {
			errorMsgs = append(errorMsgs, textContent.Text)
		}
	}

	errorMsg := strings.Join(errorMsgs, "\n")
	// Don't print here - cobra will print the returned error
	return fmt.Errorf("%s", errorMsg)
}

// formatOutput formats the tool output according to the specified format.
// It handles conversion between different output formats and delegates
// to appropriate formatting functions based on the configured output format.
//
// Args:
//   - result: MCP call result containing the data to format
//
// Returns:
//   - error: Formatting error, if any
func (e *ToolExecutor) formatOutput(result *mcp.CallToolResult) error {
	if len(result.Content) == 0 {
		if !e.options.Quiet {
			fmt.Println("No results")
		}
		return nil
	}

	content := result.Content[0]
	textContent, ok := mcp.AsTextContent(content)
	if !ok {
		return fmt.Errorf("content is not text")
	}

	switch e.options.Format {
	case OutputFormatJSON:
		fmt.Println(textContent.Text)
		return nil
	case OutputFormatYAML:
		return e.outputYAML(textContent.Text)
	case OutputFormatTable, OutputFormatWide:
		return e.outputTable(textContent.Text)
	default:
		return fmt.Errorf("unsupported output format: %s", e.options.Format)
	}
}

// outputYAML converts JSON data to YAML format and prints it.
// This provides a more readable alternative to JSON for configuration
// and structured data display. For responses that already contain a 'yaml'
// field, it outputs that directly instead of converting the entire response.
//
// Args:
//   - jsonData: JSON data as a string
//
// Returns:
//   - error: JSON parsing or YAML conversion error, if any
func (e *ToolExecutor) outputYAML(jsonData string) error {
	var data interface{}
	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		return fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Check if this is a response that already contains YAML content
	if dataMap, ok := data.(map[string]interface{}); ok {
		// If there's a 'yaml' field, output that directly (common for workflows)
		if yamlContent, exists := dataMap["yaml"]; exists {
			if yamlStr, ok := yamlContent.(string); ok {
				fmt.Print(yamlStr)
				return nil
			}
		}
	}

	// Fallback to converting entire response to YAML
	yamlData, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to convert to YAML: %w", err)
	}

	fmt.Print(string(yamlData))
	return nil
}

// outputTable formats data as a professional table using the table formatter.
// This provides the most user-friendly display format with proper styling,
// icons, and optimized column layouts.
//
// Args:
//   - jsonData: JSON data as a string to be formatted as a table
//
// Returns:
//   - error: JSON parsing or table formatting error, if any
func (e *ToolExecutor) outputTable(jsonData string) error {
	var data interface{}
	if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
		fmt.Println(jsonData) // Fallback to raw text if not JSON
		return nil
	}

	return e.formatter.FormatData(data)
}

// ListMCPTools returns all MCP tools by calling the list_tools meta-tool.
// This method retrieves the actual tools (core_*, x_*, workflow_*) rather than
// the meta-tools exposed by the MCP native tools/list protocol.
//
// Args:
//   - ctx: Context for execution timeout and cancellation
//
// Returns:
//   - []mcp.Tool: Slice of all available tools from the server
//   - error: Connection or retrieval error, if any
func (e *ToolExecutor) ListMCPTools(ctx context.Context) ([]mcp.Tool, error) {
	// Call the list_tools meta-tool to get actual tools
	result, err := e.client.CallTool(ctx, metatools.ToolListTools, map[string]interface{}{})
	if err != nil {
		return nil, fmt.Errorf("failed to call %s: %w", metatools.ToolListTools, err)
	}

	if result.IsError {
		var errorMsgs []string
		for _, content := range result.Content {
			if textContent, ok := mcp.AsTextContent(content); ok {
				errorMsgs = append(errorMsgs, textContent.Text)
			}
		}
		return nil, fmt.Errorf("%s failed: %s", metatools.ToolListTools, strings.Join(errorMsgs, "; "))
	}

	// Parse the JSON response from list_tools
	for _, content := range result.Content {
		if textContent, ok := mcp.AsTextContent(content); ok {
			var response metatools.ListToolsResponse

			if err := json.Unmarshal([]byte(textContent.Text), &response); err != nil {
				return nil, fmt.Errorf("failed to parse %s response: %w", metatools.ToolListTools, err)
			}

			// Convert to mcp.Tool format
			tools := make([]mcp.Tool, len(response.Tools))
			for i, t := range response.Tools {
				tools[i] = mcp.Tool{
					Name:        t.Name,
					Description: t.Description,
				}
			}

			return tools, nil
		}
	}

	return nil, fmt.Errorf("no content in %s response", metatools.ToolListTools)
}

// ListMCPResources returns all MCP resources using native protocol.
// This method retrieves resources directly from the MCP server without going through
// the tool execution interface.
//
// Args:
//   - ctx: Context for execution timeout and cancellation
//
// Returns:
//   - []mcp.Resource: Slice of all available resources from the server
//   - error: Connection or retrieval error, if any
func (e *ToolExecutor) ListMCPResources(ctx context.Context) ([]mcp.Resource, error) {
	return e.client.ListResourcesFromServer(ctx)
}

// ListMCPPrompts returns all MCP prompts using native protocol.
// This method retrieves prompts directly from the MCP server without going through
// the tool execution interface.
//
// Args:
//   - ctx: Context for execution timeout and cancellation
//
// Returns:
//   - []mcp.Prompt: Slice of all available prompts from the server
//   - error: Connection or retrieval error, if any
func (e *ToolExecutor) ListMCPPrompts(ctx context.Context) ([]mcp.Prompt, error) {
	return e.client.ListPromptsFromServer(ctx)
}

// GetMCPTool returns detailed info for a specific tool by calling describe_tool meta-tool.
// This method retrieves the tool information from the actual tools (core_*, x_*, workflow_*)
// rather than the meta-tools exposed by the MCP native protocol.
//
// Args:
//   - ctx: Context for execution timeout and cancellation
//   - name: The exact name of the tool to find
//
// Returns:
//   - *mcp.Tool: Pointer to the found tool, or nil if not found
//   - error: Connection or retrieval error, if any
func (e *ToolExecutor) GetMCPTool(ctx context.Context, name string) (*mcp.Tool, error) {
	// Call the describe_tool meta-tool to get tool details
	result, err := e.client.CallTool(ctx, metatools.ToolDescribeTool, map[string]interface{}{
		"name": name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to call %s: %w", metatools.ToolDescribeTool, err)
	}

	if result.IsError {
		// Tool not found - extract error message for context
		for _, content := range result.Content {
			if textContent, ok := mcp.AsTextContent(content); ok {
				// Check if this is a "not found" error (return nil, nil)
				if strings.Contains(textContent.Text, "not found") {
					return nil, nil
				}
				// Otherwise return the actual error
				return nil, fmt.Errorf("%s: %s", metatools.ToolDescribeTool, textContent.Text)
			}
		}
		return nil, nil
	}

	// Parse the JSON response from describe_tool
	for _, content := range result.Content {
		if textContent, ok := mcp.AsTextContent(content); ok {
			var toolInfo struct {
				Name        string              `json:"name"`
				Description string              `json:"description"`
				InputSchema mcp.ToolInputSchema `json:"inputSchema"`
			}

			if err := json.Unmarshal([]byte(textContent.Text), &toolInfo); err != nil {
				return nil, fmt.Errorf("failed to parse %s response: %w", metatools.ToolDescribeTool, err)
			}

			return &mcp.Tool{
				Name:        toolInfo.Name,
				Description: toolInfo.Description,
				InputSchema: toolInfo.InputSchema,
			}, nil
		}
	}

	return nil, nil
}

// GetMCPResource returns detailed info for a specific resource.
// This method retrieves the resource list and finds the specified resource by URI.
//
// Args:
//   - ctx: Context for execution timeout and cancellation
//   - uri: The exact URI of the resource to find
//
// Returns:
//   - *mcp.Resource: Pointer to the found resource, or nil if not found
//   - error: Connection or retrieval error, if any
func (e *ToolExecutor) GetMCPResource(ctx context.Context, uri string) (*mcp.Resource, error) {
	// First refresh the cache
	_, err := e.client.ListResourcesFromServer(ctx)
	if err != nil {
		return nil, err
	}

	resource := e.client.GetResourceByURI(uri)
	return resource, nil
}

// GetMCPPrompt returns detailed info for a specific prompt.
// This method retrieves the prompt list and finds the specified prompt by name.
//
// Args:
//   - ctx: Context for execution timeout and cancellation
//   - name: The exact name of the prompt to find
//
// Returns:
//   - *mcp.Prompt: Pointer to the found prompt, or nil if not found
//   - error: Connection or retrieval error, if any
func (e *ToolExecutor) GetMCPPrompt(ctx context.Context, name string) (*mcp.Prompt, error) {
	// First refresh the cache
	_, err := e.client.ListPromptsFromServer(ctx)
	if err != nil {
		return nil, err
	}

	prompt := e.client.GetPromptByName(name)
	return prompt, nil
}

// GetOptions returns the executor options.
// This allows callers to check the configured output format and other settings.
func (e *ToolExecutor) GetOptions() ExecutorOptions {
	return e.options
}
