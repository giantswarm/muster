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

func TestStorage_Save(t *testing.T) {
	// Save original functions
	originalUserHomeDir := osUserHomeDir
	originalGetwd := osGetwd
	defer func() {
		osUserHomeDir = originalUserHomeDir
		osGetwd = originalGetwd
	}()

	// Create temporary directories for testing
	tempDir := t.TempDir()
	userDir := filepath.Join(tempDir, "user")
	projectDir := filepath.Join(tempDir, "project")

	// Mock functions
	osUserHomeDir = func() (string, error) {
		return userDir, nil
	}
	osGetwd = func() (string, error) {
		return projectDir, nil
	}

	// Create project config directory to indicate we're in a project
	musterDir := filepath.Join(projectDir, ".muster")
	if err := os.MkdirAll(musterDir, 0755); err != nil {
		t.Fatalf("Failed to create project .muster directory: %v", err)
	}

	ds := NewStorage()

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

			// Verify file was created in project directory
			expectedDir := filepath.Join(projectDir, ".muster", tt.entityType)
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
	// Save original functions
	originalUserHomeDir := osUserHomeDir
	originalGetwd := osGetwd
	defer func() {
		osUserHomeDir = originalUserHomeDir
		osGetwd = originalGetwd
	}()

	// Create temporary directories for testing
	tempDir := t.TempDir()
	userDir := filepath.Join(tempDir, "user")
	projectDir := filepath.Join(tempDir, "project")

	// Mock functions
	osUserHomeDir = func() (string, error) {
		return userDir, nil
	}
	osGetwd = func() (string, error) {
		return projectDir, nil
	}

	ds := NewStorage()

	// Create test files
	userWorkflowDir := filepath.Join(userDir, ".config", "muster", "workflows")
	projectWorkflowDir := filepath.Join(projectDir, ".muster", "workflows")

	if err := os.MkdirAll(userWorkflowDir, 0755); err != nil {
		t.Fatalf("Failed to create user workflow directory: %v", err)
	}
	if err := os.MkdirAll(projectWorkflowDir, 0755); err != nil {
		t.Fatalf("Failed to create project workflow directory: %v", err)
	}

	// Create user file
	userContent := []byte("name: user-workflow\nsteps: []")
	userFilePath := filepath.Join(userWorkflowDir, "test-workflow.yaml")
	if err := os.WriteFile(userFilePath, userContent, 0644); err != nil {
		t.Fatalf("Failed to create user test file: %v", err)
	}

	// Create project file (should override user)
	projectContent := []byte("name: project-workflow\nsteps: []\nversion: 2")
	projectFilePath := filepath.Join(projectWorkflowDir, "test-workflow.yaml")
	if err := os.WriteFile(projectFilePath, projectContent, 0644); err != nil {
		t.Fatalf("Failed to create project test file: %v", err)
	}

	// Create user-only file
	userOnlyContent := []byte("name: user-only-workflow\nsteps: []")
	userOnlyFilePath := filepath.Join(userWorkflowDir, "user-only.yaml")
	if err := os.WriteFile(userOnlyFilePath, userOnlyContent, 0644); err != nil {
		t.Fatalf("Failed to create user-only test file: %v", err)
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
			name:       "load from project (overrides user)",
			entityType: "workflows",
			itemName:   "test-workflow",
			wantData:   projectContent,
			wantErr:    false,
		},
		{
			name:       "load from user (fallback)",
			entityType: "workflows",
			itemName:   "user-only",
			wantData:   userOnlyContent,
			wantErr:    false,
		},
		{
			name:        "file not found",
			entityType:  "workflows",
			itemName:    "nonexistent",
			wantErr:     true,
			errContains: "not found in user or project paths",
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
	// Save original functions
	originalUserHomeDir := osUserHomeDir
	originalGetwd := osGetwd
	defer func() {
		osUserHomeDir = originalUserHomeDir
		osGetwd = originalGetwd
	}()

	// Create temporary directories for testing
	tempDir := t.TempDir()
	userDir := filepath.Join(tempDir, "user")
	projectDir := filepath.Join(tempDir, "project")

	// Mock functions
	osUserHomeDir = func() (string, error) {
		return userDir, nil
	}
	osGetwd = func() (string, error) {
		return projectDir, nil
	}

	ds := NewStorage()

	// Create test files
	userWorkflowDir := filepath.Join(userDir, ".config", "muster", "workflows")
	projectWorkflowDir := filepath.Join(projectDir, ".muster", "workflows")

	if err := os.MkdirAll(userWorkflowDir, 0755); err != nil {
		t.Fatalf("Failed to create user workflow directory: %v", err)
	}
	if err := os.MkdirAll(projectWorkflowDir, 0755); err != nil {
		t.Fatalf("Failed to create project workflow directory: %v", err)
	}

	// Create files to delete
	userFilePath := filepath.Join(userWorkflowDir, "user-workflow.yaml")
	if err := os.WriteFile(userFilePath, []byte("user data"), 0644); err != nil {
		t.Fatalf("Failed to create user test file: %v", err)
	}

	projectFilePath := filepath.Join(projectWorkflowDir, "project-workflow.yaml")
	if err := os.WriteFile(projectFilePath, []byte("project data"), 0644); err != nil {
		t.Fatalf("Failed to create project test file: %v", err)
	}

	bothUserPath := filepath.Join(userWorkflowDir, "both-locations.yaml")
	bothProjectPath := filepath.Join(projectWorkflowDir, "both-locations.yaml")
	if err := os.WriteFile(bothUserPath, []byte("user data"), 0644); err != nil {
		t.Fatalf("Failed to create user both-locations file: %v", err)
	}
	if err := os.WriteFile(bothProjectPath, []byte("project data"), 0644); err != nil {
		t.Fatalf("Failed to create project both-locations file: %v", err)
	}

	tests := []struct {
		name        string
		entityType  string
		itemName    string
		wantErr     bool
		errContains string
		checkFiles  []string // Files that should not exist after deletion
	}{
		{
			name:       "delete from user directory",
			entityType: "workflows",
			itemName:   "user-workflow",
			wantErr:    false,
			checkFiles: []string{userFilePath},
		},
		{
			name:       "delete from project directory",
			entityType: "workflows",
			itemName:   "project-workflow",
			wantErr:    false,
			checkFiles: []string{projectFilePath},
		},
		{
			name:       "delete from both locations",
			entityType: "workflows",
			itemName:   "both-locations",
			wantErr:    false,
			checkFiles: []string{bothUserPath, bothProjectPath},
		},
		{
			name:        "file not found",
			entityType:  "workflows",
			itemName:    "nonexistent",
			wantErr:     true,
			errContains: "not found in user or project paths",
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

			// Check that files were deleted
			for _, filePath := range tt.checkFiles {
				if _, err := os.Stat(filePath); !os.IsNotExist(err) {
					t.Errorf("File %s should have been deleted but still exists", filePath)
				}
			}
		})
	}
}

func TestStorage_List(t *testing.T) {
	// Save original functions
	originalUserHomeDir := osUserHomeDir
	originalGetwd := osGetwd
	defer func() {
		osUserHomeDir = originalUserHomeDir
		osGetwd = originalGetwd
	}()

	// Create temporary directories for testing
	tempDir := t.TempDir()
	userDir := filepath.Join(tempDir, "user")
	projectDir := filepath.Join(tempDir, "project")

	// Mock functions
	osUserHomeDir = func() (string, error) {
		return userDir, nil
	}
	osGetwd = func() (string, error) {
		return projectDir, nil
	}

	ds := NewStorage()

	// Create test files
	userWorkflowDir := filepath.Join(userDir, ".config", "muster", "workflows")
	projectWorkflowDir := filepath.Join(projectDir, ".muster", "workflows")

	if err := os.MkdirAll(userWorkflowDir, 0755); err != nil {
		t.Fatalf("Failed to create user workflow directory: %v", err)
	}
	if err := os.MkdirAll(projectWorkflowDir, 0755); err != nil {
		t.Fatalf("Failed to create project workflow directory: %v", err)
	}

	// Create user files
	userFiles := []string{"user-only.yaml", "common.yaml", "user-with-yml.yml"}
	for _, file := range userFiles {
		filePath := filepath.Join(userWorkflowDir, file)
		if err := os.WriteFile(filePath, []byte("user data"), 0644); err != nil {
			t.Fatalf("Failed to create user file %s: %v", file, err)
		}
	}

	// Create project files (common.yaml should override user version)
	projectFiles := []string{"project-only.yaml", "common.yaml"}
	for _, file := range projectFiles {
		filePath := filepath.Join(projectWorkflowDir, file)
		if err := os.WriteFile(filePath, []byte("project data"), 0644); err != nil {
			t.Fatalf("Failed to create project file %s: %v", file, err)
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
			name:       "list workflows with override",
			entityType: "workflows",
			// common appears once (project overrides user), plus user-only, project-only, user-with-yml
			want:    []string{"user-only", "common", "user-with-yml", "project-only"},
			wantErr: false,
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

			// Handle nil vs empty slice comparison
			if got == nil && len(tt.want) == 0 {
				// Both are effectively empty, pass
			} else if len(got) == 0 && len(tt.want) == 0 {
				// Both are empty, pass
			} else {
				// Sort both slices for comparison
				sort.Strings(got)
				sort.Strings(tt.want)

				if !reflect.DeepEqual(got, tt.want) {
					t.Errorf("List() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestStorage_resolveEntityDir(t *testing.T) {
	// Save original functions
	originalUserHomeDir := osUserHomeDir
	originalGetwd := osGetwd
	defer func() {
		osUserHomeDir = originalUserHomeDir
		osGetwd = originalGetwd
	}()

	// Create temporary directories for testing
	tempDir := t.TempDir()
	userDir := filepath.Join(tempDir, "user")
	projectDir := filepath.Join(tempDir, "project")

	// Mock functions
	osUserHomeDir = func() (string, error) {
		return userDir, nil
	}

	ds := NewStorage()

	tests := []struct {
		name         string
		entityType   string
		setupProject bool   // Whether to create .muster directory
		wantContains string // What the resolved path should contain
	}{
		{
			name:         "prefer project when .muster exists",
			entityType:   "workflows",
			setupProject: true,
			wantContains: ".muster/workflows",
		},
		{
			name:         "fallback to user when no .muster",
			entityType:   "workflows",
			setupProject: false,
			wantContains: ".config/muster/workflows",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set current directory
			osGetwd = func() (string, error) {
				return projectDir, nil
			}

			if tt.setupProject {
				// Create .muster directory to indicate project context
				musterDir := filepath.Join(projectDir, ".muster")
				if err := os.MkdirAll(musterDir, 0755); err != nil {
					t.Fatalf("Failed to create .muster directory: %v", err)
				}
			} else {
				// Remove .muster directory if it exists
				musterDir := filepath.Join(projectDir, ".muster")
				os.RemoveAll(musterDir)
			}

			got, err := ds.resolveEntityDir(tt.entityType)
			if err != nil {
				t.Errorf("resolveEntityDir() error = %v", err)
				return
			}

			if !strings.Contains(got, tt.wantContains) {
				t.Errorf("resolveEntityDir() = %v, want path containing %s", got, tt.wantContains)
			}
		})
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
			name:  "normal filename",
			input: "normal-filename",
			want:  "normal-filename",
		},
		{
			name:  "filename with problematic characters",
			input: "test/file:with*problematic?chars\"<>|",
			want:  "test_file_with_problematic_chars",
		},
		{
			name:  "filename with spaces and dots",
			input: " . test . ",
			want:  "test",
		},
		{
			name:  "empty after sanitization",
			input: " . / : * ? \" < > | ",
			want:  "unnamed",
		},
		{
			name:  "windows path separators",
			input: "test\\file\\path",
			want:  "test_file_path",
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
