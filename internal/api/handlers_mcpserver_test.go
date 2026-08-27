package api

import (
	"context"
	"errors"
	"testing"
)

// errListFailed stands in for an apiserver read failure.
var errListFailed = errors.New("list failed")

// mockMCPServerManager implements MCPServerManagerHandler for testing.
type mockMCPServerManager struct {
	listMCPServersFn func() ([]MCPServerInfo, error)
	getMCPServerFn   func(name string) (*MCPServerInfo, error)
	getToolsFn       func() []ToolMetadata
	executeToolFn    func(ctx context.Context, toolName string, args map[string]interface{}) (*CallToolResult, error)
}

func (m *mockMCPServerManager) ListMCPServers(context.Context) ([]MCPServerInfo, error) {
	if m.listMCPServersFn != nil {
		return m.listMCPServersFn()
	}
	return nil, nil
}

func (m *mockMCPServerManager) GetMCPServer(_ context.Context, name string) (*MCPServerInfo, error) {
	if m.getMCPServerFn != nil {
		return m.getMCPServerFn(name)
	}
	return nil, nil
}

func (m *mockMCPServerManager) GetTools() []ToolMetadata {
	if m.getToolsFn != nil {
		return m.getToolsFn()
	}
	return nil
}

func (m *mockMCPServerManager) ExecuteTool(ctx context.Context, toolName string, args map[string]interface{}) (*CallToolResult, error) {
	if m.executeToolFn != nil {
		return m.executeToolFn(ctx, toolName, args)
	}
	return nil, nil
}

func TestCollectRequiredAudiences(t *testing.T) {
	tests := []struct {
		name        string
		setup       func()
		expected    []string
		expectedErr error
	}{
		{
			name: "no manager registered reports the set is unknown",
			setup: func() {
				// Ensure no manager is registered
				handlerMutex.Lock()
				mcpServerManagerHandler = nil
				handlerMutex.Unlock()
			},
			expectedErr: ErrNoMCPServerManager,
		},
		{
			name: "a failed list is an error, not an empty set",
			setup: func() {
				RegisterMCPServerManager(&mockMCPServerManager{
					listMCPServersFn: func() ([]MCPServerInfo, error) {
						return nil, errListFailed
					},
				})
			},
			expectedErr: errListFailed,
		},
		{
			name: "no servers returns nil",
			setup: func() {
				RegisterMCPServerManager(&mockMCPServerManager{
					listMCPServersFn: func() ([]MCPServerInfo, error) {
						return []MCPServerInfo{}, nil
					},
				})
			},
			expected: nil,
		},
		{
			name: "servers without forwardToken returns empty",
			setup: func() {
				RegisterMCPServerManager(&mockMCPServerManager{
					listMCPServersFn: func() ([]MCPServerInfo, error) {
						return []MCPServerInfo{
							{
								Name: "server1",
								Auth: &MCPServerAuth{
									ForwardToken:      false,
									RequiredAudiences: []string{"audience1"},
								},
							},
							{
								Name: "server2",
								Auth: nil, // No auth config
							},
						}, nil
					},
				})
			},
			expected: []string{},
		},
		{
			name: "servers with forwardToken returns audiences",
			setup: func() {
				RegisterMCPServerManager(&mockMCPServerManager{
					listMCPServersFn: func() ([]MCPServerInfo, error) {
						return []MCPServerInfo{
							{
								Name: "server1",
								Auth: &MCPServerAuth{
									ForwardToken:      true,
									RequiredAudiences: []string{"dex-k8s-authenticator"},
								},
							},
						}, nil
					},
				})
			},
			expected: []string{"dex-k8s-authenticator"},
		},
		{
			name: "multiple servers with forwardToken returns deduplicated sorted audiences",
			setup: func() {
				RegisterMCPServerManager(&mockMCPServerManager{
					listMCPServersFn: func() ([]MCPServerInfo, error) {
						return []MCPServerInfo{
							{
								Name: "server1",
								Auth: &MCPServerAuth{
									ForwardToken:      true,
									RequiredAudiences: []string{"audience-b", "audience-a"},
								},
							},
							{
								Name: "server2",
								Auth: &MCPServerAuth{
									ForwardToken:      true,
									RequiredAudiences: []string{"audience-a", "audience-c"}, // audience-a is duplicate
								},
							},
							{
								Name: "server3",
								Auth: &MCPServerAuth{
									ForwardToken:      false, // Should be ignored
									RequiredAudiences: []string{"ignored-audience"},
								},
							},
						}, nil
					},
				})
			},
			expected: []string{"audience-a", "audience-b", "audience-c"}, // Sorted and deduplicated
		},
		{
			name: "empty string audiences are filtered",
			setup: func() {
				RegisterMCPServerManager(&mockMCPServerManager{
					listMCPServersFn: func() ([]MCPServerInfo, error) {
						return []MCPServerInfo{
							{
								Name: "server1",
								Auth: &MCPServerAuth{
									ForwardToken:      true,
									RequiredAudiences: []string{"valid-audience", "", "another-audience"},
								},
							},
						}, nil
					},
				})
			},
			expected: []string{"another-audience", "valid-audience"}, // Sorted, empty strings filtered
		},
		{
			name: "server with forwardToken but no requiredAudiences returns empty",
			setup: func() {
				RegisterMCPServerManager(&mockMCPServerManager{
					listMCPServersFn: func() ([]MCPServerInfo, error) {
						return []MCPServerInfo{
							{
								Name: "server1",
								Auth: &MCPServerAuth{
									ForwardToken:      true,
									RequiredAudiences: nil,
								},
							},
						}, nil
					},
				})
			},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test state
			tt.setup()

			// Execute
			result, err := CollectRequiredAudiences(t.Context())

			// Verify
			if tt.expectedErr != nil {
				if !errors.Is(err, tt.expectedErr) {
					t.Errorf("expected error %v, got %v", tt.expectedErr, err)
				}
				if result != nil {
					t.Errorf("expected no audiences alongside the error, got %v", result)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}

			if len(result) != len(tt.expected) {
				t.Errorf("expected %d audiences, got %d: %v", len(tt.expected), len(result), result)
				return
			}

			for i, audience := range result {
				if audience != tt.expected[i] {
					t.Errorf("at index %d: expected %q, got %q", i, tt.expected[i], audience)
				}
			}
		})
	}

	// Cleanup
	handlerMutex.Lock()
	mcpServerManagerHandler = nil
	handlerMutex.Unlock()
}

// TestCollectRequiredAudiencesWithInvalidAudiences verifies that invalid audiences
// are filtered out when collecting from MCPServers. Validation is delegated to
// dex.ValidateAudience() from mcp-oauth which enforces:
// - Non-empty strings
// - Only alphanumeric, hyphen, and underscore characters
// - Maximum length of 256 characters
func TestCollectRequiredAudiencesWithInvalidAudiences(t *testing.T) {
	// Test that invalid audiences (spaces, special chars, etc.) are filtered out
	RegisterMCPServerManager(&mockMCPServerManager{
		listMCPServersFn: func() ([]MCPServerInfo, error) {
			return []MCPServerInfo{
				{
					Name: "server1",
					Auth: &MCPServerAuth{
						ForwardToken: true,
						RequiredAudiences: []string{
							"valid-audience",
							"invalid audience",  // contains space - invalid
							"another\taudience", // contains tab - invalid
							"valid_audience_2",  // underscores are valid
							"newline\naudience", // contains newline - invalid
							"special@char",      // contains @ - invalid (dex.ValidateAudience rejects)
							"valid-123",         // numbers are valid
							"",                  // empty - invalid
							"exclaim!tion",      // contains ! - invalid
						},
					},
				},
			}, nil
		},
	})

	result, err := CollectRequiredAudiences(t.Context())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only audiences matching [a-zA-Z0-9_-] should be included
	expected := []string{"valid-123", "valid-audience", "valid_audience_2"}
	if len(result) != len(expected) {
		t.Errorf("expected %d audiences, got %d: %v", len(expected), len(result), result)
		return
	}

	for i, audience := range result {
		if audience != expected[i] {
			t.Errorf("at index %d: expected %q, got %q", i, expected[i], audience)
		}
	}

	// Cleanup
	handlerMutex.Lock()
	mcpServerManagerHandler = nil
	handlerMutex.Unlock()
}
