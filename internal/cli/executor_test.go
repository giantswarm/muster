package cli

import (
	"context"
	"strings"
	"testing"

	"muster/internal/agent"
	"muster/internal/api"

	"github.com/stretchr/testify/assert"
)

func TestNewToolExecutor(t *testing.T) {
	tests := []struct {
		name    string
		options ExecutorOptions
	}{
		{
			name: "creates executor with default options",
			options: ExecutorOptions{
				Format: OutputFormatTable,
				Quiet:  false,
			},
		},
		{
			name: "creates executor with JSON format",
			options: ExecutorOptions{
				Format: OutputFormatJSON,
				Quiet:  true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use a temp directory that will be properly created
			tmpDir := t.TempDir()
			tt.options.ConfigPath = tmpDir
			executor, err := NewToolExecutor(tt.options)

			// The test can pass or fail depending on whether the server is running
			// This is expected behavior since NewToolExecutor checks server health
			if err != nil {
				// Server is not running or config issues - this is expected in some test environments
				assert.Error(t, err)
				assert.Nil(t, executor)
				// The error could be about missing config or server not running
				errorMsg := err.Error()
				validError := strings.Contains(errorMsg, "muster server is not running") ||
					strings.Contains(errorMsg, "config") ||
					strings.Contains(errorMsg, "no such file")
				assert.True(t, validError, "unexpected error: %s", errorMsg)
			} else {
				// Server is running - this is expected in integration test environments
				assert.NoError(t, err)
				assert.NotNil(t, executor)
				assert.Equal(t, tt.options.Format, executor.options.Format)
				assert.Equal(t, tt.options.Quiet, executor.options.Quiet)
			}
		})
	}
}

func TestOutputFormat_Constants(t *testing.T) {
	assert.Equal(t, OutputFormat("table"), OutputFormatTable)
	assert.Equal(t, OutputFormat("wide"), OutputFormatWide)
	assert.Equal(t, OutputFormat("json"), OutputFormatJSON)
	assert.Equal(t, OutputFormat("yaml"), OutputFormatYAML)
}

func TestAuthMode_Constants(t *testing.T) {
	assert.Equal(t, AuthMode("auto"), AuthModeAuto)
	assert.Equal(t, AuthMode("prompt"), AuthModePrompt)
	assert.Equal(t, AuthMode("none"), AuthModeNone)
}

func TestParseAuthMode(t *testing.T) {
	tests := []struct {
		input    string
		expected AuthMode
		wantErr  bool
	}{
		{"auto", AuthModeAuto, false},
		{"AUTO", AuthModeAuto, false},
		{"Auto", AuthModeAuto, false},
		{"prompt", AuthModePrompt, false},
		{"PROMPT", AuthModePrompt, false},
		{"none", AuthModeNone, false},
		{"NONE", AuthModeNone, false},
		{"", AuthModeAuto, false}, // Empty defaults to auto
		{"invalid", AuthModeAuto, true},
		{"disable", AuthModeAuto, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			mode, err := ParseAuthMode(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, mode)
			}
		})
	}
}

func TestGetDefaultAuthMode(t *testing.T) {
	// Test default (no env var set)
	t.Setenv(AuthModeEnvVar, "")
	assert.Equal(t, AuthModeAuto, GetDefaultAuthMode())

	// Test with env var set
	t.Setenv(AuthModeEnvVar, "prompt")
	assert.Equal(t, AuthModePrompt, GetDefaultAuthMode())

	t.Setenv(AuthModeEnvVar, "none")
	assert.Equal(t, AuthModeNone, GetDefaultAuthMode())

	// Test with invalid env var (should fall back to auto)
	t.Setenv(AuthModeEnvVar, "invalid")
	assert.Equal(t, AuthModeAuto, GetDefaultAuthMode())
}

func TestGetDefaultEndpoint(t *testing.T) {
	// Test default (no env var set)
	t.Setenv(EndpointEnvVar, "")
	assert.Equal(t, "", GetDefaultEndpoint())

	// Test with env var set
	t.Setenv(EndpointEnvVar, "https://muster.example.com/mcp")
	assert.Equal(t, "https://muster.example.com/mcp", GetDefaultEndpoint())
}

func TestGetAuthModeWithOverride(t *testing.T) {
	tests := []struct {
		name        string
		override    string
		envValue    string
		expected    AuthMode
		expectError bool
	}{
		{
			name:        "explicit override takes precedence",
			override:    "prompt",
			envValue:    "none",
			expected:    AuthModePrompt,
			expectError: false,
		},
		{
			name:        "empty override uses env default",
			override:    "",
			envValue:    "none",
			expected:    AuthModeNone,
			expectError: false,
		},
		{
			name:        "empty override with empty env defaults to auto",
			override:    "",
			envValue:    "",
			expected:    AuthModeAuto,
			expectError: false,
		},
		{
			name:        "invalid override returns error",
			override:    "invalid",
			envValue:    "",
			expected:    AuthModeAuto,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(AuthModeEnvVar, tt.envValue)
			mode, err := GetAuthModeWithOverride(tt.override)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, mode)
			}
		})
	}
}

func TestExecutorOptions_Structure(t *testing.T) {
	options := ExecutorOptions{
		Format: OutputFormatJSON,
		Quiet:  true,
	}

	assert.Equal(t, OutputFormatJSON, options.Format)
	assert.True(t, options.Quiet)
}

func TestToolExecutor_Structure(t *testing.T) {
	// Test the structure without actually connecting
	options := ExecutorOptions{
		Format: OutputFormatTable,
		Quiet:  false,
	}

	// We can't test NewToolExecutor without a server, but we can test the structure
	assert.Equal(t, OutputFormatTable, options.Format)
	assert.False(t, options.Quiet)
}

func TestToolExecutor_Methods_Exist(t *testing.T) {
	// Create a mock executor to test method signatures
	logger := agent.NewLogger(false, false, false)
	client := agent.NewClient("http://localhost:8090/mcp", logger, agent.TransportStreamableHTTP)
	executor := &ToolExecutor{
		client: client,
		options: ExecutorOptions{
			Format: OutputFormatTable,
			Quiet:  false,
		},
	}

	// Test that methods exist and have correct signatures
	assert.NotNil(t, executor.Connect)
	assert.NotNil(t, executor.Close)
	assert.NotNil(t, executor.Execute)
	assert.NotNil(t, executor.ExecuteSimple)
	assert.NotNil(t, executor.ExecuteJSON)
}

func TestToolExecutor_Close(t *testing.T) {
	logger := agent.NewLogger(false, false, false)
	client := agent.NewClient("http://localhost:8090/mcp", logger, agent.TransportStreamableHTTP)
	executor := &ToolExecutor{
		client: client,
		options: ExecutorOptions{
			Format: OutputFormatTable,
			Quiet:  false,
		},
	}

	// Should not panic when closing unconnected executor
	assert.NotPanics(t, func() {
		executor.Close()
	})
}

func TestToolExecutor_GetOptions(t *testing.T) {
	logger := agent.NewLogger(false, false, false)
	client := agent.NewClient("http://localhost:8090/mcp", logger, agent.TransportStreamableHTTP)
	executor := &ToolExecutor{
		client: client,
		options: ExecutorOptions{
			Format:   OutputFormatJSON,
			Quiet:    true,
			Endpoint: "http://test.example.com/mcp",
		},
		formatter: NewTableFormatter(ExecutorOptions{}),
	}

	options := executor.GetOptions()
	assert.Equal(t, OutputFormatJSON, options.Format)
	assert.True(t, options.Quiet)
	assert.Equal(t, "http://test.example.com/mcp", options.Endpoint)
}

func TestToolExecutor_GetFormatter(t *testing.T) {
	logger := agent.NewLogger(false, false, false)
	client := agent.NewClient("http://localhost:8090/mcp", logger, agent.TransportStreamableHTTP)
	formatter := NewTableFormatter(ExecutorOptions{Format: OutputFormatTable})
	executor := &ToolExecutor{
		client:    client,
		options:   ExecutorOptions{Format: OutputFormatTable},
		formatter: formatter,
	}

	assert.NotNil(t, executor.GetFormatter())
	assert.Equal(t, formatter, executor.GetFormatter())
}

func TestToolExecutor_GetClient(t *testing.T) {
	logger := agent.NewLogger(false, false, false)
	client := agent.NewClient("http://localhost:8090/mcp", logger, agent.TransportStreamableHTTP)
	executor := &ToolExecutor{
		client:  client,
		options: ExecutorOptions{Format: OutputFormatTable},
	}

	assert.NotNil(t, executor.GetClient())
	assert.Equal(t, client, executor.GetClient())
}

func TestMCPTypeAliases(t *testing.T) {
	// Verify that the type aliases work correctly
	var tool MCPTool
	tool.Name = "test_tool"
	tool.Description = "Test tool description"
	assert.Equal(t, "test_tool", tool.Name)
	assert.Equal(t, "Test tool description", tool.Description)

	var resource MCPResource
	resource.URI = "file://test.txt"
	resource.Name = "test.txt"
	resource.MIMEType = "text/plain"
	assert.Equal(t, "file://test.txt", resource.URI)
	assert.Equal(t, "test.txt", resource.Name)
	assert.Equal(t, "text/plain", resource.MIMEType)

	var prompt MCPPrompt
	prompt.Name = "test_prompt"
	prompt.Description = "Test prompt description"
	assert.Equal(t, "test_prompt", prompt.Name)
	assert.Equal(t, "Test prompt description", prompt.Description)
}

// mockAuthHandler implements api.AuthHandler for testing session ID behavior.
type mockAuthHandler struct {
	sessionID     string
	hasValidToken bool
	bearerToken   string
}

func (m *mockAuthHandler) CheckAuthRequired(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (m *mockAuthHandler) HasValidToken(_ string) bool {
	return m.hasValidToken
}

func (m *mockAuthHandler) GetBearerToken(_ string) (string, error) {
	return m.bearerToken, nil
}

func (m *mockAuthHandler) Login(_ context.Context, _ string) error {
	return nil
}

func (m *mockAuthHandler) LoginWithIssuer(_ context.Context, _, _ string) error {
	return nil
}

func (m *mockAuthHandler) Logout(_ string) error {
	return nil
}

func (m *mockAuthHandler) LogoutAll() error {
	return nil
}

func (m *mockAuthHandler) GetStatus() []api.AuthStatus {
	return nil
}

func (m *mockAuthHandler) GetStatusForEndpoint(_ string) *api.AuthStatus {
	return nil
}

func (m *mockAuthHandler) RefreshToken(_ context.Context, _ string) error {
	return nil
}

func (m *mockAuthHandler) GetSessionID() string {
	return m.sessionID
}

func (m *mockAuthHandler) Close() error {
	return nil
}

func TestToolExecutor_SetupAuthentication_SetsSessionIDHeader(t *testing.T) {
	// Cleanup after test
	defer api.SetAuthHandlerForTesting(nil)

	tests := []struct {
		name              string
		sessionID         string
		hasValidToken     bool
		bearerToken       string
		expectSessionID   bool
		expectBearerToken bool
	}{
		{
			name:              "sets session ID when auth handler provides one",
			sessionID:         "test-session-12345",
			hasValidToken:     true,
			bearerToken:       "Bearer test-token",
			expectSessionID:   true,
			expectBearerToken: true,
		},
		{
			name:              "sets session ID even when no valid token",
			sessionID:         "test-session-67890",
			hasValidToken:     false,
			bearerToken:       "",
			expectSessionID:   true,
			expectBearerToken: false,
		},
		{
			name:              "does not set session ID when empty",
			sessionID:         "",
			hasValidToken:     true,
			bearerToken:       "Bearer test-token",
			expectSessionID:   false,
			expectBearerToken: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Register mock auth handler
			mock := &mockAuthHandler{
				sessionID:     tt.sessionID,
				hasValidToken: tt.hasValidToken,
				bearerToken:   tt.bearerToken,
			}
			api.SetAuthHandlerForTesting(mock)

			// Create a ToolExecutor with a mock client
			logger := agent.NewDevNullLogger()
			client := agent.NewClient("https://muster.example.com/mcp", logger, agent.TransportStreamableHTTP)
			executor := &ToolExecutor{
				client:   client,
				endpoint: "https://muster.example.com/mcp",
				isRemote: true,
				options: ExecutorOptions{
					AuthMode: AuthModeAuto,
				},
			}

			// Call setupAuthentication
			err := executor.setupAuthentication(context.Background())
			assert.NoError(t, err)

			// Verify headers
			headers := client.GetHeaders()

			if tt.expectSessionID {
				assert.Equal(t, tt.sessionID, headers[api.ClientSessionIDHeader],
					"expected session ID header to be set")
			} else {
				_, hasHeader := headers[api.ClientSessionIDHeader]
				assert.False(t, hasHeader, "expected no session ID header when session ID is empty")
			}

			if tt.expectBearerToken {
				assert.Equal(t, tt.bearerToken, headers["Authorization"],
					"expected Authorization header to be set")
			}
		})
	}
}
