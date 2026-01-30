package api

import (
	"context"
	"testing"
)

// mockMCPServerManager implements MCPServerManagerHandler for testing.
type mockMCPServerManager struct {
	listMCPServersFn func() []MCPServerInfo
	getMCPServerFn   func(name string) (*MCPServerInfo, error)
	getToolsFn       func() []ToolMetadata
	executeToolFn    func(ctx context.Context, toolName string, args map[string]interface{}) (*CallToolResult, error)
}

func (m *mockMCPServerManager) ListMCPServers() []MCPServerInfo {
	if m.listMCPServersFn != nil {
		return m.listMCPServersFn()
	}
	return nil
}

func (m *mockMCPServerManager) GetMCPServer(name string) (*MCPServerInfo, error) {
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
		name     string
		setup    func()
		expected []string
	}{
		{
			name: "no manager registered returns nil",
			setup: func() {
				// Ensure no manager is registered
				handlerMutex.Lock()
				mcpServerManagerHandler = nil
				handlerMutex.Unlock()
			},
			expected: nil,
		},
		{
			name: "no servers returns nil",
			setup: func() {
				RegisterMCPServerManager(&mockMCPServerManager{
					listMCPServersFn: func() []MCPServerInfo {
						return []MCPServerInfo{}
					},
				})
			},
			expected: nil,
		},
		{
			name: "servers without forwardToken returns empty",
			setup: func() {
				RegisterMCPServerManager(&mockMCPServerManager{
					listMCPServersFn: func() []MCPServerInfo {
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
						}
					},
				})
			},
			expected: []string{},
		},
		{
			name: "servers with forwardToken returns audiences",
			setup: func() {
				RegisterMCPServerManager(&mockMCPServerManager{
					listMCPServersFn: func() []MCPServerInfo {
						return []MCPServerInfo{
							{
								Name: "server1",
								Auth: &MCPServerAuth{
									ForwardToken:      true,
									RequiredAudiences: []string{"dex-k8s-authenticator"},
								},
							},
						}
					},
				})
			},
			expected: []string{"dex-k8s-authenticator"},
		},
		{
			name: "multiple servers with forwardToken returns deduplicated sorted audiences",
			setup: func() {
				RegisterMCPServerManager(&mockMCPServerManager{
					listMCPServersFn: func() []MCPServerInfo {
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
						}
					},
				})
			},
			expected: []string{"audience-a", "audience-b", "audience-c"}, // Sorted and deduplicated
		},
		{
			name: "empty string audiences are filtered",
			setup: func() {
				RegisterMCPServerManager(&mockMCPServerManager{
					listMCPServersFn: func() []MCPServerInfo {
						return []MCPServerInfo{
							{
								Name: "server1",
								Auth: &MCPServerAuth{
									ForwardToken:      true,
									RequiredAudiences: []string{"valid-audience", "", "another-audience"},
								},
							},
						}
					},
				})
			},
			expected: []string{"another-audience", "valid-audience"}, // Sorted, empty strings filtered
		},
		{
			name: "server with forwardToken but no requiredAudiences returns empty",
			setup: func() {
				RegisterMCPServerManager(&mockMCPServerManager{
					listMCPServersFn: func() []MCPServerInfo {
						return []MCPServerInfo{
							{
								Name: "server1",
								Auth: &MCPServerAuth{
									ForwardToken:      true,
									RequiredAudiences: nil,
								},
							},
						}
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
			result := CollectRequiredAudiences()

			// Verify
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
