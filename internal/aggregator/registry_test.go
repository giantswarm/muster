package aggregator

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
)

func TestNameTracker_AlwaysPrefixing(t *testing.T) {
	tests := []struct {
		name     string
		servers  map[string]*ServerInfo
		expected map[string]string // tool/prompt name -> expected exposed name
	}{
		{
			name: "All tools get prefixed",
			servers: map[string]*ServerInfo{
				"serverA": {
					Name:      "serverA",
					Connected: true,
					Tools: []mcp.Tool{
						{Name: "read_file"},
						{Name: "write_file"},
					},
				},
				"serverB": {
					Name:      "serverB",
					Connected: true,
					Tools: []mcp.Tool{
						{Name: "search"},
						{Name: "analyze"},
					},
				},
			},
			expected: map[string]string{
				"serverA.read_file":  "x_serverA_read_file",
				"serverA.write_file": "x_serverA_write_file",
				"serverB.search":     "x_serverB_search",
				"serverB.analyze":    "x_serverB_analyze",
			},
		},
		{
			name: "Tools with same names get prefixed",
			servers: map[string]*ServerInfo{
				"serverA": {
					Name:      "serverA",
					Connected: true,
					Tools: []mcp.Tool{
						{Name: "read_file"},
						{Name: "search"}, // same as serverB
					},
				},
				"serverB": {
					Name:      "serverB",
					Connected: true,
					Tools: []mcp.Tool{
						{Name: "search"}, // same as serverA
						{Name: "analyze"},
					},
				},
			},
			expected: map[string]string{
				"serverA.read_file": "x_serverA_read_file",
				"serverA.search":    "x_serverA_search",
				"serverB.search":    "x_serverB_search",
				"serverB.analyze":   "x_serverB_analyze",
			},
		},
		{
			name: "Multiple servers with same tool",
			servers: map[string]*ServerInfo{
				"serverA": {
					Name:      "serverA",
					Connected: true,
					Tools: []mcp.Tool{
						{Name: "common_tool"},
					},
				},
				"serverB": {
					Name:      "serverB",
					Connected: true,
					Tools: []mcp.Tool{
						{Name: "common_tool"},
					},
				},
				"serverC": {
					Name:      "serverC",
					Connected: true,
					Tools: []mcp.Tool{
						{Name: "common_tool"},
						{Name: "unique_tool"},
					},
				},
			},
			expected: map[string]string{
				"serverA.common_tool": "x_serverA_common_tool",
				"serverB.common_tool": "x_serverB_common_tool",
				"serverC.common_tool": "x_serverC_common_tool",
				"serverC.unique_tool": "x_serverC_unique_tool",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := NewNameTracker("x")

			// Set server prefixes
			for serverName := range tt.servers {
				tracker.SetServerPrefix(serverName, serverName)
			}

			for key, expectedName := range tt.expected {
				parts := splitKey(key)
				serverName := parts[0]
				toolName := parts[1]

				actualName := tracker.GetExposedToolName(serverName, toolName)
				assert.Equal(t, expectedName, actualName,
					"Tool %s on server %s should be exposed as %s, but got %s",
					toolName, serverName, expectedName, actualName)
			}
		})
	}
}

func TestNameTracker_ResolveName(t *testing.T) {
	tracker := NewNameTracker("x")

	// Set up server prefixes
	tracker.SetServerPrefix("serverA", "serverA")
	tracker.SetServerPrefix("serverB", "serverB")
	tracker.SetServerPrefix("serverC", "serverC")

	// Register some names to test resolution
	// Tools
	tracker.GetExposedToolName("serverA", "unique_tool")
	tracker.GetExposedToolName("serverA", "shared_tool")
	tracker.GetExposedToolName("serverB", "shared_tool")

	// Prompts
	tracker.GetExposedPromptName("serverA", "unique_prompt")
	tracker.GetExposedPromptName("serverB", "shared_prompt")
	tracker.GetExposedPromptName("serverC", "shared_prompt")

	tests := []struct {
		exposedName      string
		expectedServer   string
		expectedOriginal string
		expectedItemType string
		expectError      bool
	}{
		// Tools - all prefixed
		{"x_serverA_unique_tool", "serverA", "unique_tool", "tool", false},
		{"x_serverA_shared_tool", "serverA", "shared_tool", "tool", false},
		{"x_serverB_shared_tool", "serverB", "shared_tool", "tool", false},
		// Prompts - all prefixed
		{"x_serverA_unique_prompt", "serverA", "unique_prompt", "prompt", false},
		{"x_serverB_shared_prompt", "serverB", "shared_prompt", "prompt", false},
		{"x_serverC_shared_prompt", "serverC", "shared_prompt", "prompt", false},
		// Non-existent name
		{"non_existent", "", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.exposedName, func(t *testing.T) {
			serverName, originalName, itemType, err := tracker.ResolveName(tt.exposedName)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedServer, serverName)
				assert.Equal(t, tt.expectedOriginal, originalName)
				assert.Equal(t, tt.expectedItemType, itemType)
			}
		})
	}
}

// Helper function to split "server.tool" into ["server", "tool"]
func splitKey(key string) []string {
	for i := 0; i < len(key); i++ {
		if key[i] == '.' {
			return []string{key[:i], key[i+1:]}
		}
	}
	return []string{key}
}
