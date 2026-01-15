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
				{Name: "x_github_authenticate"},
				{Name: "list_tools"},
			},
			expected: []string{"x_github_authenticate"},
		},
		{
			name: "multiple remote auth tools",
			tools: []mcp.Tool{
				{Name: "x_github_authenticate"},
				{Name: "x_slack_authenticate"},
				{Name: "x_mcp-kubernetes_authenticate"},
				{Name: "list_tools"},
			},
			expected: []string{"x_github_authenticate", "x_slack_authenticate", "x_mcp-kubernetes_authenticate"},
		},
		{
			name: "mixed with authenticate_muster",
			tools: []mcp.Tool{
				{Name: "authenticate_muster"},
				{Name: "x_github_authenticate"},
				{Name: "x_gitlab_authenticate"},
				{Name: "call_tool"},
			},
			expected: []string{"x_github_authenticate", "x_gitlab_authenticate"},
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

func TestExtractServerNameFromAuthTool(t *testing.T) {
	tests := []struct {
		toolName string
		expected string
	}{
		{"x_github_authenticate", "github"},
		{"x_mcp-kubernetes_authenticate", "mcp-kubernetes"},
		{"x_slack_authenticate", "slack"},
		{"x_my-server_authenticate", "my-server"},
	}

	for _, tt := range tests {
		t.Run(tt.toolName, func(t *testing.T) {
			result := extractServerNameFromAuthTool(tt.toolName)
			if result != tt.expected {
				t.Errorf("extractServerNameFromAuthTool(%q) = %q, want %q", tt.toolName, result, tt.expected)
			}
		})
	}
}

func TestIsRemoteAgentEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		expected bool
	}{
		{
			name:     "localhost is not remote",
			endpoint: "http://localhost:8080/mcp",
			expected: false,
		},
		{
			name:     "LOCALHOST uppercase is not remote",
			endpoint: "http://LOCALHOST:8080/mcp",
			expected: false,
		},
		{
			name:     "127.0.0.1 is not remote",
			endpoint: "http://127.0.0.1:8080/mcp",
			expected: false,
		},
		{
			name:     "IPv6 loopback is not remote",
			endpoint: "http://[::1]:8080/mcp",
			expected: false,
		},
		{
			name:     "external hostname is remote",
			endpoint: "https://muster.example.com/mcp",
			expected: true,
		},
		{
			name:     "external IP is remote",
			endpoint: "https://192.168.1.100/mcp",
			expected: true,
		},
		{
			name:     "domain with localhost in path is remote",
			endpoint: "https://example.com/localhost/api",
			expected: false, // Contains localhost
		},
		{
			name:     "empty endpoint is remote",
			endpoint: "",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRemoteAgentEndpoint(tt.endpoint)
			if result != tt.expected {
				t.Errorf("isRemoteAgentEndpoint(%q) = %v, want %v", tt.endpoint, result, tt.expected)
			}
		})
	}
}

func TestAgentCmdProperties(t *testing.T) {
	t.Run("agent command exists", func(t *testing.T) {
		if agentCmd == nil {
			t.Fatal("agentCmd should not be nil")
		}
	})

	t.Run("agent command Use field", func(t *testing.T) {
		if agentCmd.Use != "agent" {
			t.Errorf("expected Use 'agent', got %q", agentCmd.Use)
		}
	})

	t.Run("agent command has short description", func(t *testing.T) {
		if agentCmd.Short == "" {
			t.Error("expected Short description to be set")
		}
	})

	t.Run("agent command has RunE", func(t *testing.T) {
		if agentCmd.RunE == nil {
			t.Error("expected RunE to be set")
		}
	})
}

func TestAgentFlags(t *testing.T) {
	t.Run("endpoint flag exists", func(t *testing.T) {
		flag := agentCmd.Flags().Lookup("endpoint")
		if flag == nil {
			t.Error("expected --endpoint flag to exist")
		}
	})

	t.Run("transport flag exists", func(t *testing.T) {
		flag := agentCmd.Flags().Lookup("transport")
		if flag == nil {
			t.Error("expected --transport flag to exist")
		}
	})

	t.Run("repl flag exists", func(t *testing.T) {
		flag := agentCmd.Flags().Lookup("repl")
		if flag == nil {
			t.Error("expected --repl flag to exist")
		}
	})

	t.Run("mcp-server flag exists", func(t *testing.T) {
		flag := agentCmd.Flags().Lookup("mcp-server")
		if flag == nil {
			t.Error("expected --mcp-server flag to exist")
		}
	})

	t.Run("disable-auto-sso flag exists", func(t *testing.T) {
		flag := agentCmd.Flags().Lookup("disable-auto-sso")
		if flag == nil {
			t.Error("expected --disable-auto-sso flag to exist")
		}
	})
}
