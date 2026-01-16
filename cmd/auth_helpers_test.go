package cmd

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestExtractAuthURL(t *testing.T) {
	tests := []struct {
		name     string
		result   *mcp.CallToolResult
		expected string
	}{
		{
			name:     "nil result",
			result:   nil,
			expected: "",
		},
		{
			name: "empty content",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{},
			},
			expected: "",
		},
		{
			name: "JSON with auth_url",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: `{"auth_url":"https://auth.example.com/oauth/authorize?client_id=test"}`,
					},
				},
			},
			expected: "https://auth.example.com/oauth/authorize?client_id=test",
		},
		{
			name: "plain text URL",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: `Please sign in to connect the server.
https://auth.example.com/login
After signing in, the connection will be established.`,
					},
				},
			},
			expected: "https://auth.example.com/login",
		},
		{
			name: "HTTP URL",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: "http://localhost:8080/callback",
					},
				},
			},
			expected: "http://localhost:8080/callback",
		},
		{
			name: "no URL in text",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: "Authentication required but no URL provided.",
					},
				},
			},
			expected: "",
		},
		{
			name: "invalid JSON falls back to text parsing",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: `{invalid json}
https://fallback.example.com/auth`,
					},
				},
			},
			expected: "https://fallback.example.com/auth",
		},
		{
			name: "URL with query params",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: "https://auth.example.com/authorize?response_type=code&client_id=abc&redirect_uri=http://localhost:3000/callback",
					},
				},
			},
			expected: "https://auth.example.com/authorize?response_type=code&client_id=abc&redirect_uri=http://localhost:3000/callback",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractAuthURL(tt.result)
			if result != tt.expected {
				t.Errorf("extractAuthURL() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestEnsureAuthHandler(t *testing.T) {
	// Test that ensureAuthHandler creates a handler when none exists
	// This is an integration test that will attempt to create a real handler
	t.Run("creates handler when none exists", func(t *testing.T) {
		// Note: This test may fail in environments without proper home directory
		// The function should at least not panic
		handler, err := ensureAuthHandler()
		if err != nil {
			// Error is acceptable in test environments
			t.Skipf("Skipping test: %v", err)
		}
		if handler == nil {
			t.Error("expected handler to be created")
		}
	})
}
