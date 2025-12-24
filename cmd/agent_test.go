package cmd

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestExtractAuthURLFromResult(t *testing.T) {
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
			name: "JSON with auth_url field",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: `{"status":"pending","auth_url":"https://dex.example.com/auth?client_id=muster"}`,
					},
				},
			},
			expected: "https://dex.example.com/auth?client_id=muster",
		},
		{
			name: "JSON with additional fields",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: `{"status":"pending","auth_url":"https://auth.example.com/oauth/authorize","message":"Please authenticate"}`,
					},
				},
			},
			expected: "https://auth.example.com/oauth/authorize",
		},
		{
			name: "plain text with https URL on its own line",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: "Please sign in to connect to the server.\nhttps://login.example.com/auth\nAfter authentication, you will be connected.",
					},
				},
			},
			expected: "https://login.example.com/auth",
		},
		{
			name: "plain text with http URL",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: "Authentication required:\nhttp://localhost:3000/callback\n",
					},
				},
			},
			expected: "http://localhost:3000/callback",
		},
		{
			name: "URL with whitespace padding",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: "Click link below:\n   https://auth.example.com/login   \nThen return here.",
					},
				},
			},
			expected: "https://auth.example.com/login",
		},
		{
			name: "invalid JSON falls back to line parsing",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: "{not valid json}\nhttps://fallback.example.com/auth",
					},
				},
			},
			expected: "https://fallback.example.com/auth",
		},
		{
			name: "JSON with empty auth_url falls back to line parsing",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: "{\"auth_url\":\"\"}\nhttps://fallback.example.com/auth",
					},
				},
			},
			expected: "https://fallback.example.com/auth",
		},
		{
			name: "no URL found",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: "No authentication URL available.\nPlease contact support.",
					},
				},
			},
			expected: "",
		},
		{
			name: "multiple content items - first with URL wins",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: `{"auth_url":"https://first.example.com/auth"}`,
					},
					mcp.TextContent{
						Type: "text",
						Text: `{"auth_url":"https://second.example.com/auth"}`,
					},
				},
			},
			expected: "https://first.example.com/auth",
		},
		{
			name: "first content has no URL, second does",
			result: &mcp.CallToolResult{
				Content: []mcp.Content{
					mcp.TextContent{
						Type: "text",
						Text: "Some message without URL",
					},
					mcp.TextContent{
						Type: "text",
						Text: `{"auth_url":"https://found.example.com/auth"}`,
					},
				},
			},
			expected: "https://found.example.com/auth",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractAuthURLFromResult(tt.result)
			if result != tt.expected {
				t.Errorf("extractAuthURLFromResult() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFindPendingAuthTools(t *testing.T) {
	tests := []struct {
		name     string
		tools    []mcp.Tool
		expected []string
	}{
		{
			name:     "empty tools list",
			tools:    []mcp.Tool{},
			expected: nil,
		},
		{
			name: "no authenticate tools",
			tools: []mcp.Tool{
				{Name: "call_tool"},
				{Name: "list_tools"},
			},
			expected: nil,
		},
		{
			name: "only authenticate_muster - should be excluded",
			tools: []mcp.Tool{
				{Name: "authenticate_muster"},
				{Name: "list_tools"},
			},
			expected: nil,
		},
		{
			name: "single remote auth tool",
			tools: []mcp.Tool{
				{Name: "authenticate_github"},
				{Name: "list_tools"},
			},
			expected: []string{"authenticate_github"},
		},
		{
			name: "multiple remote auth tools",
			tools: []mcp.Tool{
				{Name: "authenticate_github"},
				{Name: "authenticate_slack"},
				{Name: "authenticate_kubernetes"},
				{Name: "list_tools"},
			},
			expected: []string{"authenticate_github", "authenticate_slack", "authenticate_kubernetes"},
		},
		{
			name: "mixed with authenticate_muster",
			tools: []mcp.Tool{
				{Name: "authenticate_muster"},
				{Name: "authenticate_github"},
				{Name: "authenticate_gitlab"},
				{Name: "call_tool"},
			},
			expected: []string{"authenticate_github", "authenticate_gitlab"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := findPendingAuthTools(tt.tools)
			if len(result) != len(tt.expected) {
				t.Errorf("findPendingAuthTools() returned %d tools, want %d", len(result), len(tt.expected))
				return
			}
			for i, tool := range result {
				if tool != tt.expected[i] {
					t.Errorf("findPendingAuthTools()[%d] = %q, want %q", i, tool, tt.expected[i])
				}
			}
		})
	}
}
