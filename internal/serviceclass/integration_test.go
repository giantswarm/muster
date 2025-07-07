package serviceclass

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"muster/internal/config"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

// mockToolChecker implements ToolChecker for testing
type mockToolChecker struct {
	availableTools map[string]bool
}

func (m *mockToolChecker) IsToolAvailable(toolName string) bool {
	if m.availableTools == nil {
		return false
	}
	return m.availableTools[toolName]
}

func (m *mockToolChecker) GetAvailableTools() []string {
	var tools []string
	for tool, available := range m.availableTools {
		if available {
			tools = append(tools, tool)
		}
	}
	return tools
}

// mockToolCaller implements api.ToolCaller for testing
type mockToolCaller struct {
	calls []toolCall
}

type toolCall struct {
	toolName string
	args     map[string]interface{}
}

func (m *mockToolCaller) CallToolInternal(ctx context.Context, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
	m.calls = append(m.calls, toolCall{toolName: toolName, args: args})

	// Return different responses based on tool name
	switch toolName {
	case "api_kubernetes_connect":
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent(`{
					"connectionId": "k8s-test-connection-123",
					"status": "connected",
					"connected": true,
					"clusterName": "test-cluster",
					"context": "test-context"
				}`),
			},
			IsError: false,
		}, nil
	default:
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent(`{"status": "success", "tool": "` + toolName + `"}`),
			},
			IsError: false,
		}, nil
	}
}

// mockStorage implements a test-only Storage that doesn't load from system directories
type mockStorage struct {
	data map[string]map[string][]byte // entityType -> name -> data
	mu   sync.RWMutex
}

func newMockStorage() *mockStorage {
	return &mockStorage{
		data: make(map[string]map[string][]byte),
	}
}

func (m *mockStorage) Save(entityType string, name string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data[entityType] == nil {
		m.data[entityType] = make(map[string][]byte)
	}
	m.data[entityType][name] = data
	return nil
}

func (m *mockStorage) Load(entityType string, name string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.data[entityType] == nil {
		return nil, fmt.Errorf("entity %s/%s not found", entityType, name)
	}
	data, exists := m.data[entityType][name]
	if !exists {
		return nil, fmt.Errorf("entity %s/%s not found", entityType, name)
	}
	return data, nil
}

func (m *mockStorage) Delete(entityType string, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data[entityType] == nil {
		return fmt.Errorf("entity %s/%s not found", entityType, name)
	}
	if _, exists := m.data[entityType][name]; !exists {
		return fmt.Errorf("entity %s/%s not found", entityType, name)
	}
	delete(m.data[entityType], name)
	return nil
}

func (m *mockStorage) List(entityType string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.data[entityType] == nil {
		return []string{}, nil
	}
	var names []string
	for name := range m.data[entityType] {
		names = append(names, name)
	}
	return names, nil
}

// setupTestManager creates a ServiceClassManager and a mockToolChecker for testing
func setupTestManager(t *testing.T) (*ServiceClassManager, *mockToolChecker) {
	t.Helper()
	toolChecker := &mockToolChecker{
		availableTools: make(map[string]bool),
	}
	storage := config.NewStorage()
	manager, err := NewServiceClassManager(toolChecker, storage)
	require.NoError(t, err)
	return manager, toolChecker
}

// TestServiceClassManagerIntegration - removed as it was dependent on layered config system

// TestServiceClassMissingTools - removed as it was dependent on layered config system

// TestServiceClassAPIAdapter - removed as it was dependent on layered config system

// TestServiceClassErrorHandling - removed as it was dependent on layered config system
