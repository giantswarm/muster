package mcpserver

import (
	"context"
	"testing"
)

func TestNewAPIAdapter(t *testing.T) {
	adapter, err := NewAdapter()
	if err != nil {
		t.Fatalf("Failed to create adapter: %v", err)
	}
	if adapter == nil {
		t.Fatal("NewAdapter returned nil")
	}

	// Verify the adapter has a client
	if adapter.client == nil {
		t.Error("Adapter client not initialized")
	}
}

func TestAdapterListMCPServers(t *testing.T) {
	adapter, err := NewAdapter()
	if err != nil {
		t.Fatalf("Failed to create adapter: %v", err)
	}

	servers := adapter.ListMCPServers()
	if servers == nil {
		t.Error("ListMCPServers returned nil")
	}

	// Should return empty list initially
	if len(servers) != 0 {
		t.Errorf("Expected 0 servers, got %d", len(servers))
	}
}

func TestAdapterGetMCPServer(t *testing.T) {
	adapter, err := NewAdapter()
	if err != nil {
		t.Fatalf("Failed to create adapter: %v", err)
	}

	// Test getting non-existent server
	server, err := adapter.GetMCPServer("nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent server")
	}
	if server != nil {
		t.Error("Expected nil server for non-existent server")
	}
}

func TestAdapterGetTools(t *testing.T) {
	adapter, err := NewAdapter()
	if err != nil {
		t.Fatalf("Failed to create adapter: %v", err)
	}

	tools := adapter.GetTools()
	if tools == nil {
		t.Error("GetTools returned nil")
	}

	// Should have the expected tools
	expectedTools := []string{
		"mcpserver_list",
		"mcpserver_get",
		"mcpserver_validate",
		"mcpserver_create",
		"mcpserver_update",
		"mcpserver_delete",
	}

	if len(tools) != len(expectedTools) {
		t.Errorf("Expected %d tools, got %d", len(expectedTools), len(tools))
	}

	for i, expectedTool := range expectedTools {
		if i >= len(tools) || tools[i].Name != expectedTool {
			t.Errorf("Expected tool %s, got %s", expectedTool, tools[i].Name)
		}
	}
}

func TestAdapterExecuteTool(t *testing.T) {
	adapter, err := NewAdapter()
	if err != nil {
		t.Fatalf("Failed to create adapter: %v", err)
	}

	ctx := context.Background()

	// Test mcpserver_list tool
	result, err := adapter.ExecuteTool(ctx, "mcpserver_list", nil)
	if err != nil {
		t.Errorf("ExecuteTool failed for mcpserver_list: %v", err)
	}
	if result == nil {
		t.Error("ExecuteTool returned nil result")
	}
	if result.IsError {
		t.Error("ExecuteTool returned error result")
	}

	// Test unknown tool
	result, err = adapter.ExecuteTool(ctx, "unknown_tool", nil)
	if err == nil {
		t.Error("Expected error for unknown tool")
	}
}

func TestAdapterErrorHandling(t *testing.T) {
	adapter, err := NewAdapter()
	if err != nil {
		t.Fatalf("Failed to create adapter: %v", err)
	}

	ctx := context.Background()

	// Test error handling for various scenarios
	tests := []struct {
		name     string
		toolName string
		args     map[string]interface{}
		wantErr  bool
	}{
		{
			name:     "unknown tool",
			toolName: "unknown_tool",
			args:     nil,
			wantErr:  true,
		},
		{
			name:     "get non-existent server",
			toolName: "mcpserver_get",
			args:     map[string]interface{}{"name": "nonexistent"},
			wantErr:  false, // Should return empty result, not error
		},
		{
			name:     "create with invalid args",
			toolName: "mcpserver_create",
			args:     map[string]interface{}{"invalid": "data"},
			wantErr:  false, // Validation happens at runtime, not at tool execution level
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := adapter.ExecuteTool(ctx, tt.toolName, tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExecuteTool() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err == nil && result == nil {
				t.Error("ExecuteTool() returned nil result without error")
			}
		})
	}
}
