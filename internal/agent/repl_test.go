package agent

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestNewREPL(t *testing.T) {
	logger := NewLogger(false, false, false)
	client := NewClient("http://localhost:8090/sse", logger, TransportStreamableHTTP)

	repl := NewREPL(client, logger)

	if repl == nil {
		t.Fatal("NewREPL returned nil")
	}

	if repl.client != client {
		t.Error("REPL client does not match provided client")
	}

	if repl.logger != logger {
		t.Error("REPL logger does not match provided logger")
	}

	if repl.notificationChan == nil {
		t.Error("REPL notification channel is nil")
	}

	if repl.stopChan == nil {
		t.Error("REPL stop channel is nil")
	}
}

func TestREPLHelp(t *testing.T) {
	logger := NewLogger(false, false, false)
	client := NewClient("http://localhost:8090/sse", logger, TransportStreamableHTTP)
	repl := NewREPL(client, logger)

	// Test help command using the command pattern
	err := repl.executeCommand("help")
	if err != nil {
		t.Errorf("help command returned error: %v", err)
	}
}

func TestREPLCreateCompleter(t *testing.T) {
	logger := NewLogger(false, false, false)
	client := NewClient("http://localhost:8090/sse", logger, TransportStreamableHTTP)
	repl := NewREPL(client, logger)

	// Add some test data
	client.mu.Lock()
	client.toolCache = []mcp.Tool{
		{Name: "test_tool1", Description: "Test tool 1"},
		{Name: "test_tool2", Description: "Test tool 2"},
	}
	client.resourceCache = []mcp.Resource{
		{URI: "test://resource1", Name: "Resource 1"},
		{URI: "test://resource2", Name: "Resource 2"},
	}
	client.promptCache = []mcp.Prompt{
		{Name: "test_prompt1", Description: "Test prompt 1"},
		{Name: "test_prompt2", Description: "Test prompt 2"},
	}
	client.mu.Unlock()

	// Create completer
	completer := repl.createCompleter()

	// Verify completer is not nil
	if completer == nil {
		t.Fatal("createCompleter returned nil")
	}

	// The completer should have the basic commands
	// We can't easily test the exact structure, but we can verify it's created
}

func TestREPLExecuteCommand(t *testing.T) {
	logger := NewLogger(false, false, false)
	client := NewClient("http://localhost:8090/sse", logger, TransportStreamableHTTP)
	repl := NewREPL(client, logger)

	tests := []struct {
		name    string
		input   string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "help command",
			input:   "help",
			wantErr: false,
		},
		{
			name:    "question mark help",
			input:   "?",
			wantErr: false,
		},
		{
			name:    "unknown command",
			input:   "unknown",
			wantErr: true,
			errMsg:  "unknown command",
		},
		{
			name:    "list without target",
			input:   "list",
			wantErr: true,
			errMsg:  "usage: list",
		},
		{
			name:    "describe without target",
			input:   "describe",
			wantErr: true,
			errMsg:  "usage: describe",
		},
		{
			name:    "notifications without setting",
			input:   "notifications",
			wantErr: true,
			errMsg:  "usage: notifications",
		},
		{
			name:    "exit command",
			input:   "exit",
			wantErr: true,
			errMsg:  "exit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repl.executeCommand(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("executeCommand(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if err != nil && tt.errMsg != "" && err.Error() != tt.errMsg {
				if !contains(err.Error(), tt.errMsg) {
					t.Errorf("executeCommand(%q) error = %v, want error containing %q", tt.input, err, tt.errMsg)
				}
			}
		})
	}
}

func TestREPLHandleNotifications(t *testing.T) {
	logger := NewLogger(false, false, false)
	client := NewClient("http://localhost:8090/sse", logger, TransportStreamableHTTP)
	repl := NewREPL(client, logger)

	tests := []struct {
		name    string
		setting string
		wantErr bool
	}{
		{
			name:    "enable notifications",
			setting: "on",
			wantErr: false,
		},
		{
			name:    "disable notifications",
			setting: "off",
			wantErr: false,
		},
		{
			name:    "invalid setting",
			setting: "invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repl.executeCommand("notifications " + tt.setting)
			if (err != nil) != tt.wantErr {
				t.Errorf("notifications command error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestREPLCallTool(t *testing.T) {
	logger := NewLogger(false, false, false)
	client := NewClient("http://localhost:8090/sse", logger, TransportStreamableHTTP)
	repl := NewREPL(client, logger)

	// Simulate tool in cache
	client.mu.Lock()
	client.toolCache = []mcp.Tool{
		{
			Name:        "test_tool",
			Description: "Test tool for unit tests",
			InputSchema: mcp.ToolInputSchema{
				Type: "object",
				Properties: map[string]any{
					"param1": map[string]any{"type": "string"},
				},
			},
		},
	}
	client.mu.Unlock()

	// Test with non-existent tool (will get "client not connected" since no real connection)
	err := repl.executeCommand("call nonexistent {\"param1\": \"value\"}")
	if err != nil {
		t.Errorf("Command should handle error gracefully, got: %v", err)
	}

	// Test with invalid JSON (this should be handled by the command)
	err = repl.executeCommand("call test_tool invalid json")
	if err != nil {
		t.Errorf("Command should handle invalid JSON gracefully, got: %v", err)
	}
}

func TestREPLGetResource(t *testing.T) {
	logger := NewLogger(false, false, false)
	client := NewClient("http://localhost:8090/sse", logger, TransportStreamableHTTP)
	repl := NewREPL(client, logger)

	// Simulate resource in cache
	client.mu.Lock()
	client.resourceCache = []mcp.Resource{
		{
			URI:         "test://resource",
			Name:        "Test Resource",
			Description: "Test resource for unit tests",
			MIMEType:    "text/plain",
		},
	}
	client.mu.Unlock()

	// Test with non-existent resource (will get connection error since no real connection)
	err := repl.executeCommand("get nonexistent://resource")
	if err != nil {
		t.Errorf("Command should handle error gracefully, got: %v", err)
	}
}

func TestREPLGetPrompt(t *testing.T) {
	logger := NewLogger(false, false, false)
	client := NewClient("http://localhost:8090/sse", logger, TransportStreamableHTTP)
	repl := NewREPL(client, logger)

	// Simulate prompt in cache
	client.mu.Lock()
	client.promptCache = []mcp.Prompt{
		{
			Name:        "test_prompt",
			Description: "Test prompt for unit tests",
			Arguments: []mcp.PromptArgument{
				{
					Name:     "arg1",
					Required: true,
				},
			},
		},
	}
	client.mu.Unlock()

	// Test with non-existent prompt (will get connection error since no real connection)
	err := repl.executeCommand("prompt nonexistent {\"arg1\": \"value\"}")
	if err != nil {
		t.Errorf("Command should handle error gracefully, got: %v", err)
	}

	// Test with existing prompt (will get connection error since no real connection)
	err = repl.executeCommand("prompt test_prompt {}")
	if err != nil {
		t.Errorf("Command should handle error gracefully, got: %v", err)
	}

	// Test with invalid JSON (this should be handled by the command)
	err = repl.executeCommand("prompt test_prompt invalid json")
	if err != nil {
		t.Errorf("Command should handle invalid JSON gracefully, got: %v", err)
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
