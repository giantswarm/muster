package mcpserver

import (
	"context"
	"muster/internal/config"
	"testing"
)

func TestNewAPIAdapter(t *testing.T) {
	storage := config.NewStorage()
	manager, err := NewMCPServerManager(storage)
	if err != nil {
		t.Fatalf("Failed to create MCP server manager: %v", err)
	}

	adapter := NewAdapter(manager)
	if adapter == nil {
		t.Fatal("NewAdapter returned nil")
	}

	if adapter.manager != manager {
		t.Error("Adapter manager not set correctly")
	}
}

func TestAdapterListMCPServers(t *testing.T) {
	storage := config.NewStorage()
	manager, err := NewMCPServerManager(storage)
	if err != nil {
		t.Fatalf("Failed to create MCP server manager: %v", err)
	}

	adapter := NewAdapter(manager)

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
	storage := config.NewStorage()
	manager, err := NewMCPServerManager(storage)
	if err != nil {
		t.Fatalf("Failed to create MCP server manager: %v", err)
	}

	adapter := NewAdapter(manager)

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
	storage := config.NewStorage()
	manager, err := NewMCPServerManager(storage)
	if err != nil {
		t.Fatalf("Failed to create MCP server manager: %v", err)
	}

	adapter := NewAdapter(manager)

	tools := adapter.GetTools()
	if tools == nil {
		t.Error("GetTools returned nil")
	}

	// Should have the expected tools (availability tool removed)
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
	storage := config.NewStorage()
	manager, err := NewMCPServerManager(storage)
	if err != nil {
		t.Fatalf("Failed to create MCP server manager: %v", err)
	}

	adapter := NewAdapter(manager)
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

func TestAdapterNilManager(t *testing.T) {
	adapter := NewAdapter(nil)

	// Test various methods with nil manager
	servers := adapter.ListMCPServers()
	if len(servers) != 0 {
		t.Error("Expected empty servers list with nil manager")
	}

	server, err := adapter.GetMCPServer("test")
	if err == nil {
		t.Error("Expected error with nil manager")
	}
	if server != nil {
		t.Error("Expected nil server with nil manager")
	}

	// Phase 1: LoadDefinitions and GetDefinitionsPath methods removed
	// These methods are no longer available as loading is transparent
}
