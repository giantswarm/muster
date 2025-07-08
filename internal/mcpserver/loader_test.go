package mcpserver

import (
	"os"
	"path/filepath"
	"testing"

	"muster/internal/api"
)

func TestLoadDefinitionFromFile(t *testing.T) {
	// Create a test file
	content := `
name: test-server
type: localCommand
command: ["test-cmd", "--version"]
env:
  TEST_VAR: test-value
`

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test-server.yaml")

	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Test loading
	def, err := LoadDefinitionFromFile(testFile)
	if err != nil {
		t.Fatalf("Failed to load MCP server: %v", err)
	}

	if def.Name != "test-server" {
		t.Errorf("Expected name 'test-server', got %s", def.Name)
	}

	if def.Type != api.MCPServerTypeLocalCommand {
		t.Errorf("Expected type %s, got %s", api.MCPServerTypeLocalCommand, def.Type)
	}
}

func TestLoadDefinitionFromFileInvalidType(t *testing.T) {
	content := `
name: test-invalid-server
type: invalid-type
command: ["test-cmd"]
`

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test-invalid.yaml")

	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	def, err := LoadDefinitionFromFile(testFile)
	if err != nil {
		t.Fatalf("Failed to load MCP server: %v", err)
	}

	// The definition loads but validation would happen later
	if def.Type != "invalid-type" {
		t.Errorf("Expected type 'invalid-type', got %s", def.Type)
	}
}

func TestLoadDefinitionFromFileIncomplete(t *testing.T) {
	content := `
name: test-incomplete-server
type: local-command
# missing command
`

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test-incomplete.yaml")

	err := os.WriteFile(testFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	def, err := LoadDefinitionFromFile(testFile)
	if err != nil {
		t.Fatalf("Failed to load MCP server: %v", err)
	}

	// The definition loads but validation would happen later
	if len(def.Command) != 0 {
		t.Errorf("Expected empty command, got %v", def.Command)
	}
}

func TestLoadDefinitions(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test files
	testFiles := map[string]string{
		"server1.yaml": `
name: server1
type: local-command
command: ["cmd1"]
`,
		"server2.yaml": `
name: server2
type: local-command
command: ["cmd2"]
`,
	}

	for filename, content := range testFiles {
		path := filepath.Join(tmpDir, filename)
		err := os.WriteFile(path, []byte(content), 0644)
		if err != nil {
			t.Fatalf("Failed to create test file %s: %v", filename, err)
		}
	}

	// Test loading from directory
	servers, err := LoadDefinitions(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load servers from directory: %v", err)
	}

	if len(servers) != 2 {
		t.Errorf("Expected 2 servers, got %d", len(servers))
	}

	// Verify server names
	foundNames := make(map[string]bool)
	for _, server := range servers {
		foundNames[server.Name] = true
	}

	for expectedName := range testFiles {
		expectedServerName := expectedName[:len(expectedName)-5] // remove .yaml
		if !foundNames[expectedServerName] {
			t.Errorf("Expected to find server %s", expectedServerName)
		}
	}
}

func TestLoadDefinitionsEmptyDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	// Test loading from empty directory
	servers, err := LoadDefinitions(tmpDir)
	if err != nil {
		t.Fatalf("Failed to load from empty directory: %v", err)
	}

	if len(servers) != 0 {
		t.Errorf("Expected 0 servers from empty directory, got %d", len(servers))
	}
}

func TestLoadDefinitionsNonExistentDirectory(t *testing.T) {
	// Test loading from non-existent directory
	servers, err := LoadDefinitions("/non/existent/path")
	if err != nil {
		t.Fatalf("Expected no error for non-existent directory, got: %v", err)
	}

	if len(servers) != 0 {
		t.Errorf("Expected 0 servers from non-existent directory, got %d", len(servers))
	}
}

func TestSaveDefinitionToFile(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test-save.yaml")

	def := &api.MCPServer{
		Name:    "test-server",
		Type:    api.MCPServerTypeLocalCommand,
		Command: []string{"test", "command"},
		Env: map[string]string{
			"TEST_ENV": "test-value",
		},
	}

	err := SaveDefinitionToFile(def, testFile)
	if err != nil {
		t.Fatalf("Failed to save definition: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Fatal("File was not created")
	}

	// Load it back and verify
	loaded, err := LoadDefinitionFromFile(testFile)
	if err != nil {
		t.Fatalf("Failed to load saved definition: %v", err)
	}

	if loaded.Name != def.Name {
		t.Errorf("Expected name %s, got %s", def.Name, loaded.Name)
	}

	if loaded.Type != def.Type {
		t.Errorf("Expected type %s, got %s", def.Type, loaded.Type)
	}
}
