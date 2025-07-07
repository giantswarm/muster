package config

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestNewStorage(t *testing.T) {
	ds := NewStorage()
	if ds == nil {
		t.Fatal("NewStorage returned nil")
	}
}

func TestNewStorageWithPath(t *testing.T) {
	customPath := "/custom/config/path"
	ds := NewStorageWithPath(customPath)
	if ds == nil {
		t.Fatal("NewStorageWithPath returned nil")
	}
	if ds.configPath != customPath {
		t.Errorf("Expected configPath %s, got %s", customPath, ds.configPath)
	}
}

func TestStorage_Save(t *testing.T) {
	tempDir := t.TempDir()
	ds := NewStorageWithPath(tempDir)

	tests := []struct {
		name        string
		entityType  string
		itemName    string
		data        []byte
		wantErr     bool
		errContains string
	}{
		{
			name:       "save valid workflow",
			entityType: "workflows",
			itemName:   "test-workflow",
			data:       []byte("name: test-workflow\nsteps: []"),
			wantErr:    false,
		},
		{
			name:       "save valid serviceclass",
			entityType: "serviceclasses",
			itemName:   "test-service",
			data:       []byte("name: test-service\ntype: container"),
			wantErr:    false,
		},
		{
			name:        "empty entity type",
			entityType:  "",
			itemName:    "test",
			data:        []byte("data"),
			wantErr:     true,
			errContains: "entityType cannot be empty",
		},
		{
			name:        "empty name",
			entityType:  "workflows",
			itemName:    "",
			data:        []byte("data"),
			wantErr:     true,
			errContains: "name cannot be empty",
		},
		{
			name:       "sanitize filename",
			entityType: "workflows",
			itemName:   "test/workflow:with*problematic?chars",
			data:       []byte("name: test\nsteps: []"),
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ds.Save(tt.entityType, tt.itemName, tt.data)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Save() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("Save() error = %v, want error containing %s", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("Save() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Verify file was created in the config directory
			expectedDir := filepath.Join(tempDir, tt.entityType)
			sanitizedName := ds.sanitizeFilename(tt.itemName)
			expectedPath := filepath.Join(expectedDir, sanitizedName+".yaml")

			if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
				t.Errorf("Expected file %s was not created", expectedPath)
			}

			// Verify file content
			content, err := os.ReadFile(expectedPath)
			if err != nil {
				t.Errorf("Failed to read saved file: %v", err)
			}
			if !reflect.DeepEqual(content, tt.data) {
				t.Errorf("File content = %s, want %s", string(content), string(tt.data))
			}
		})
	}
}

func TestStorage_Load(t *testing.T) {
	tempDir := t.TempDir()
	ds := NewStorageWithPath(tempDir)

	// Create test files
	workflowDir := filepath.Join(tempDir, "workflows")
	if err := os.MkdirAll(workflowDir, 0755); err != nil {
		t.Fatalf("Failed to create workflow directory: %v", err)
	}

	// Create test file
	testContent := []byte("name: test-workflow\nsteps: []")
	testFilePath := filepath.Join(workflowDir, "test-workflow.yaml")
	if err := os.WriteFile(testFilePath, testContent, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	tests := []struct {
		name        string
		entityType  string
		itemName    string
		wantData    []byte
		wantErr     bool
		errContains string
	}{
		{
			name:       "load existing file",
			entityType: "workflows",
			itemName:   "test-workflow",
			wantData:   testContent,
			wantErr:    false,
		},
		{
			name:        "file not found",
			entityType:  "workflows",
			itemName:    "nonexistent",
			wantErr:     true,
			errContains: "not found",
		},
		{
			name:        "empty entity type",
			entityType:  "",
			itemName:    "test",
			wantErr:     true,
			errContains: "entityType cannot be empty",
		},
		{
			name:        "empty name",
			entityType:  "workflows",
			itemName:    "",
			wantErr:     true,
			errContains: "name cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := ds.Load(tt.entityType, tt.itemName)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Load() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("Load() error = %v, want error containing %s", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("Load() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !reflect.DeepEqual(data, tt.wantData) {
				t.Errorf("Load() data = %s, want %s", string(data), string(tt.wantData))
			}
		})
	}
}

func TestStorage_Delete(t *testing.T) {
	tempDir := t.TempDir()
	ds := NewStorageWithPath(tempDir)

	// Create test files
	workflowDir := filepath.Join(tempDir, "workflows")
	if err := os.MkdirAll(workflowDir, 0755); err != nil {
		t.Fatalf("Failed to create workflow directory: %v", err)
	}

	// Create file to delete
	testFilePath := filepath.Join(workflowDir, "test-workflow.yaml")
	if err := os.WriteFile(testFilePath, []byte("test data"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	tests := []struct {
		name        string
		entityType  string
		itemName    string
		wantErr     bool
		errContains string
		checkFile   string // File that should not exist after deletion
	}{
		{
			name:       "delete existing file",
			entityType: "workflows",
			itemName:   "test-workflow",
			wantErr:    false,
			checkFile:  testFilePath,
		},
		{
			name:        "file not found",
			entityType:  "workflows",
			itemName:    "nonexistent",
			wantErr:     true,
			errContains: "not found",
		},
		{
			name:        "empty entity type",
			entityType:  "",
			itemName:    "test",
			wantErr:     true,
			errContains: "entityType cannot be empty",
		},
		{
			name:        "empty name",
			entityType:  "workflows",
			itemName:    "",
			wantErr:     true,
			errContains: "name cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ds.Delete(tt.entityType, tt.itemName)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Delete() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("Delete() error = %v, want error containing %s", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("Delete() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Check that file was deleted
			if tt.checkFile != "" {
				if _, err := os.Stat(tt.checkFile); !os.IsNotExist(err) {
					t.Errorf("File %s should have been deleted but still exists", tt.checkFile)
				}
			}
		})
	}
}

func TestStorage_List(t *testing.T) {
	tempDir := t.TempDir()
	ds := NewStorageWithPath(tempDir)

	// Create test files
	workflowDir := filepath.Join(tempDir, "workflows")
	if err := os.MkdirAll(workflowDir, 0755); err != nil {
		t.Fatalf("Failed to create workflow directory: %v", err)
	}

	// Create test files with different extensions
	testFiles := []string{"workflow1.yaml", "workflow2.yaml", "workflow3.yml"}
	for _, file := range testFiles {
		filePath := filepath.Join(workflowDir, file)
		if err := os.WriteFile(filePath, []byte("test data"), 0644); err != nil {
			t.Fatalf("Failed to create test file %s: %v", file, err)
		}
	}

	tests := []struct {
		name        string
		entityType  string
		want        []string
		wantErr     bool
		errContains string
	}{
		{
			name:       "list workflows",
			entityType: "workflows",
			want:       []string{"workflow1", "workflow2", "workflow3"},
			wantErr:    false,
		},
		{
			name:       "list empty directory",
			entityType: "nonexistent",
			want:       []string{},
			wantErr:    false,
		},
		{
			name:        "empty entity type",
			entityType:  "",
			wantErr:     true,
			errContains: "entityType cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ds.List(tt.entityType)

			if tt.wantErr {
				if err == nil {
					t.Errorf("List() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("List() error = %v, want error containing %s", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("List() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Sort both slices for comparison
			sort.Strings(got)
			sort.Strings(tt.want)

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("List() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStorage_DefaultBehavior(t *testing.T) {
	// Save original function
	originalUserHomeDir := osUserHomeDir
	defer func() {
		osUserHomeDir = originalUserHomeDir
	}()

	tempDir := t.TempDir()
	osUserHomeDir = func() (string, error) {
		return tempDir, nil
	}

	// Test default storage behavior (should use ~/.config/muster)
	ds := NewStorage()

	// Create config directory structure
	configDir := filepath.Join(tempDir, userConfigDir, "workflows")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("Failed to create config directory: %v", err)
	}

	// Test save
	testData := []byte("name: test\nsteps: []")
	err := ds.Save("workflows", "test-workflow", testData)
	if err != nil {
		t.Errorf("Save() error = %v", err)
	}

	// Verify file was created in the correct location
	expectedPath := filepath.Join(configDir, "test-workflow.yaml")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("Expected file %s was not created", expectedPath)
	}

	// Test load
	data, err := ds.Load("workflows", "test-workflow")
	if err != nil {
		t.Errorf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(data, testData) {
		t.Errorf("Load() data = %s, want %s", string(data), string(testData))
	}

	// Test list
	names, err := ds.List("workflows")
	if err != nil {
		t.Errorf("List() error = %v", err)
	}
	if len(names) != 1 || names[0] != "test-workflow" {
		t.Errorf("List() = %v, want [test-workflow]", names)
	}

	// Test delete
	err = ds.Delete("workflows", "test-workflow")
	if err != nil {
		t.Errorf("Delete() error = %v", err)
	}
	if _, err := os.Stat(expectedPath); !os.IsNotExist(err) {
		t.Errorf("File %s should have been deleted but still exists", expectedPath)
	}
}

func TestStorage_sanitizeFilename(t *testing.T) {
	ds := NewStorage()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "clean filename",
			input: "clean-filename",
			want:  "clean-filename",
		},
		{
			name:  "filename with problematic characters",
			input: "test/workflow:with*problematic?chars<>|\"",
			want:  "test_workflow_with_problematic_chars",
		},
		{
			name:  "filename with spaces",
			input: "test workflow with spaces",
			want:  "test_workflow_with_spaces",
		},
		{
			name:  "filename with dots and leading/trailing spaces",
			input: " .test.workflow. ",
			want:  "test_workflow",
		},
		{
			name:  "empty after sanitization",
			input: ":::***",
			want:  "unnamed",
		},
		{
			name:  "consecutive underscores",
			input: "test___workflow___name",
			want:  "test_workflow_name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ds.sanitizeFilename(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeFilename() = %v, want %v", got, tt.want)
			}
		})
	}
}
