package mcpserver

import (
	"muster/internal/config"
	"testing"

	"muster/internal/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMCPServerManager(t *testing.T) {
	storage := config.NewStorage()
	manager, err := NewMCPServerManager(storage)
	require.NoError(t, err)
	assert.NotNil(t, manager)
	assert.NotNil(t, manager.loader)
	assert.NotNil(t, manager.definitions)
	assert.Empty(t, manager.definitions)
}

func TestMCPServerManager_validateDefinition(t *testing.T) {
	storage := config.NewStorage()
	manager, err := NewMCPServerManager(storage)
	require.NoError(t, err)

	tests := []struct {
		name    string
		def     *api.MCPServer
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid local command server",
			def: &api.MCPServer{
				Name:    "test-server",
				Type:    api.MCPServerTypeLocalCommand,
				Command: []string{"echo", "hello"},
			},
			wantErr: false,
		},

		{
			name: "empty name",
			def: &api.MCPServer{
				Type:    api.MCPServerTypeLocalCommand,
				Command: []string{"echo", "hello"},
			},
			wantErr: true,
			errMsg:  "field 'name': is required for mcpserver",
		},
		{
			name: "invalid type",
			def: &api.MCPServer{
				Name: "test-server",
				Type: "invalid-type",
			},
			wantErr: true,
			errMsg:  "field 'type': must be one of: localCommand",
		},
		{
			name: "local command without command",
			def: &api.MCPServer{
				Name: "test-server",
				Type: api.MCPServerTypeLocalCommand,
			},
			wantErr: true,
			errMsg:  "field 'command': is required for localCommand type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.validateDefinition(tt.def)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestMCPServerManager_GetDefinition(t *testing.T) {
	storage := config.NewStorage()
	manager, err := NewMCPServerManager(storage)
	require.NoError(t, err)

	// Test getting non-existent definition
	def, exists := manager.GetDefinition("non-existent")
	assert.False(t, exists)
	assert.Equal(t, api.MCPServer{}, def)

	// Add a definition directly to test getting it
	testDef := &api.MCPServer{
		Name:      "test-server",
		Type:      api.MCPServerTypeLocalCommand,
		Command:   []string{"echo", "hello"},
		AutoStart: true,
	}
	manager.definitions["test-server"] = testDef

	// Test getting existing definition
	def, exists = manager.GetDefinition("test-server")
	assert.True(t, exists)
	assert.Equal(t, *testDef, def)
}

func TestMCPServerManager_ListDefinitions(t *testing.T) {
	storage := config.NewStorage()
	manager, err := NewMCPServerManager(storage)
	require.NoError(t, err)

	// Test empty list
	defs := manager.ListDefinitions()
	assert.Empty(t, defs)

	// Add some definitions
	testDef1 := &api.MCPServer{
		Name:      "server1",
		Type:      api.MCPServerTypeLocalCommand,
		Command:   []string{"echo", "hello"},
		AutoStart: true,
	}
	testDef2 := &api.MCPServer{
		Name:      "server2",
		Type:      api.MCPServerTypeLocalCommand,
		Command:   []string{"echo", "world"},
		AutoStart: false,
	}

	manager.definitions["server1"] = testDef1
	manager.definitions["server2"] = testDef2

	// Test list with definitions
	defs = manager.ListDefinitions()
	assert.Len(t, defs, 2)

	// Check that both definitions are included
	names := make(map[string]bool)
	for _, def := range defs {
		names[def.Name] = true
	}
	assert.True(t, names["server1"])
	assert.True(t, names["server2"])
}

func TestMCPServerManager_ListAvailableDefinitions(t *testing.T) {
	storage := config.NewStorage()
	manager, err := NewMCPServerManager(storage)
	require.NoError(t, err)

	// Add some definitions
	testDef1 := &api.MCPServer{
		Name:      "server1",
		Type:      api.MCPServerTypeLocalCommand,
		Command:   []string{"echo", "hello"},
		AutoStart: true,
	}
	testDef2 := &api.MCPServer{
		Name:      "server2",
		Type:      api.MCPServerTypeLocalCommand,
		Command:   []string{"echo", "world"},
		AutoStart: false,
	}

	manager.definitions["server1"] = testDef1
	manager.definitions["server2"] = testDef2

	// For MCP servers, all definitions should be considered available
	availableDefs := manager.ListAvailableDefinitions()
	assert.Len(t, availableDefs, 2)

	// Should be the same length as ListDefinitions since no tool checking is done
	allDefs := manager.ListDefinitions()
	assert.Len(t, allDefs, len(availableDefs))

	// Check that both definitions are included by name
	availableNames := make(map[string]bool)
	for _, def := range availableDefs {
		availableNames[def.Name] = true
	}
	assert.True(t, availableNames["server1"])
	assert.True(t, availableNames["server2"])
}

func TestMCPServerManager_GetDefinitionsPath(t *testing.T) {
	storage := config.NewStorage()
	manager, err := NewMCPServerManager(storage)
	require.NoError(t, err)

	path := manager.GetDefinitionsPath()
	assert.NotEmpty(t, path)
	assert.Contains(t, path, "mcpservers")
	assert.Contains(t, path, "Configuration:")
}

func TestMCPServerManager_GetAllDefinitions(t *testing.T) {
	storage := config.NewStorage()
	manager, err := NewMCPServerManager(storage)
	require.NoError(t, err)

	// Test empty map
	allDefs := manager.GetAllDefinitions()
	assert.Empty(t, allDefs)

	// Add some definitions
	testDef1 := &api.MCPServer{
		Name:      "server1",
		Type:      api.MCPServerTypeLocalCommand,
		Command:   []string{"echo", "hello"},
		AutoStart: true,
	}
	testDef2 := &api.MCPServer{
		Name:      "server2",
		Type:      api.MCPServerTypeLocalCommand,
		Command:   []string{"echo", "world"},
		AutoStart: false,
	}

	manager.definitions["server1"] = testDef1
	manager.definitions["server2"] = testDef2

	// Test with definitions
	allDefs = manager.GetAllDefinitions()
	assert.Len(t, allDefs, 2)
	assert.Equal(t, testDef1, allDefs["server1"])
	assert.Equal(t, testDef2, allDefs["server2"])

	// Verify it's a copy (modifying returned map shouldn't affect original)
	delete(allDefs, "server1")
	assert.Len(t, manager.definitions, 2) // Original should still have both
}

func TestMCPServerManager_Get(t *testing.T) {
	storage := config.NewStorage()
	manager, err := NewMCPServerManager(storage)
	require.NoError(t, err)

	// Create a server
	def := api.MCPServer{
		Name:    "test-server",
		Type:    api.MCPServerTypeLocalCommand,
		Command: []string{"echo", "hello"},
	}

	err = manager.CreateMCPServer(def)
	assert.NoError(t, err)

	// Test getting existing server
	retrieved, exists := manager.GetDefinition("test-server")
	assert.True(t, exists)
	assert.Equal(t, def.Name, retrieved.Name)
	assert.Equal(t, def.Type, retrieved.Type)

	// Test getting non-existent server
	_, exists = manager.GetDefinition("non-existent")
	assert.False(t, exists)
}

func TestMCPServerManager_List(t *testing.T) {
	storage := config.NewStorage()
	manager, err := NewMCPServerManager(storage)
	require.NoError(t, err)

	// Test empty list
	servers := manager.ListDefinitions()
	assert.Len(t, servers, 0)

	def1 := api.MCPServer{
		Name:    "server-1",
		Type:    api.MCPServerTypeLocalCommand,
		Command: []string{"cmd1"},
	}

	def2 := api.MCPServer{
		Name:    "server-2",
		Type:    api.MCPServerTypeLocalCommand,
		Command: []string{"cmd2"},
	}

	manager.CreateMCPServer(def1)
	manager.CreateMCPServer(def2)

	// Test list with servers
	servers = manager.ListDefinitions()
	assert.Len(t, servers, 2)

	// Verify servers are in the list
	names := make(map[string]bool)
	for _, server := range servers {
		names[server.Name] = true
	}

	assert.True(t, names["server-1"])
	assert.True(t, names["server-2"])
}
