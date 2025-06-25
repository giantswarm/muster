package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Test type for configuration loading
type TestConfig struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"`
	Description string `yaml:"description"`
}

func TestConfigurationLoader_LayeredLoading(t *testing.T) {
	// Create temporary directories for testing
	tempDir := t.TempDir()
	userDir := filepath.Join(tempDir, "user", ".config", "muster")
	projectDir := filepath.Join(tempDir, "project", ".muster")

	// Create directory structure
	require.NoError(t, os.MkdirAll(filepath.Join(userDir, "serviceclasses"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, "serviceclasses"), 0755))

	// Create test files
	userService := TestConfig{
		Name:        "user-service",
		Type:        "test",
		Description: "User service",
	}
	projectService := TestConfig{
		Name:        "project-service",
		Type:        "test",
		Description: "Project service",
	}
	overrideService := TestConfig{
		Name:        "shared-service",
		Type:        "override",
		Description: "Project override",
	}
	userSharedService := TestConfig{
		Name:        "shared-service",
		Type:        "original",
		Description: "User original",
	}

	// Write user files
	writeYAMLFile(t, filepath.Join(userDir, "serviceclasses", "user-service.yaml"), userService)
	writeYAMLFile(t, filepath.Join(userDir, "serviceclasses", "shared-service.yaml"), userSharedService)

	// Write project files (including override)
	writeYAMLFile(t, filepath.Join(projectDir, "serviceclasses", "project-service.yaml"), projectService)
	writeYAMLFile(t, filepath.Join(projectDir, "serviceclasses", "shared-service.yaml"), overrideService)

	// Mock the directory functions
	originalUserHomeDir := osUserHomeDir
	originalGetwd := osGetwd
	defer func() {
		osUserHomeDir = originalUserHomeDir
		osGetwd = originalGetwd
	}()

	osUserHomeDir = func() (string, error) {
		return filepath.Join(tempDir, "user"), nil
	}
	osGetwd = func() (string, error) {
		return filepath.Join(tempDir, "project"), nil
	}

	// Test ConfigurationLoader
	loader, err := NewConfigurationLoader()
	require.NoError(t, err)

	files, err := loader.LoadYAMLFiles("serviceclasses")
	require.NoError(t, err)

	// Should have 3 files: user-service (user), project-service (project), shared-service (project override)
	assert.Len(t, files, 3)

	// Verify correct files are loaded
	fileMap := make(map[string]LoadedFile)
	for _, file := range files {
		fileMap[file.Name] = file
	}

	// Check user-service exists and is from user
	userFile, exists := fileMap["user-service"]
	assert.True(t, exists)
	assert.Equal(t, "user", userFile.Source)

	// Check project-service exists and is from project
	projectFile, exists := fileMap["project-service"]
	assert.True(t, exists)
	assert.Equal(t, "project", projectFile.Source)

	// Check shared-service exists and is from project (override)
	sharedFile, exists := fileMap["shared-service"]
	assert.True(t, exists)
	assert.Equal(t, "project", sharedFile.Source)
}

func TestLoadAndParseYAML(t *testing.T) {
	// Create temporary directories for testing
	tempDir := t.TempDir()
	userDir := filepath.Join(tempDir, "user", ".config", "muster")
	projectDir := filepath.Join(tempDir, "project", ".muster")

	// Create directory structure
	require.NoError(t, os.MkdirAll(filepath.Join(userDir, "workflows"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(projectDir, "workflows"), 0755))

	// Create test configurations
	userWorkflow := TestConfig{
		Name:        "user-workflow",
		Type:        "automation",
		Description: "User automation workflow",
	}
	projectWorkflow := TestConfig{
		Name:        "project-workflow",
		Type:        "automation",
		Description: "Project automation workflow",
	}

	// Write files
	writeYAMLFile(t, filepath.Join(userDir, "workflows", "user-workflow.yaml"), userWorkflow)
	writeYAMLFile(t, filepath.Join(projectDir, "workflows", "project-workflow.yml"), projectWorkflow) // Test .yml extension

	// Mock directory functions
	originalUserHomeDir := osUserHomeDir
	originalGetwd := osGetwd
	defer func() {
		osUserHomeDir = originalUserHomeDir
		osGetwd = originalGetwd
	}()

	osUserHomeDir = func() (string, error) {
		return filepath.Join(tempDir, "user"), nil
	}
	osGetwd = func() (string, error) {
		return filepath.Join(tempDir, "project"), nil
	}

	// Test LoadAndParseYAML with validator
	validator := func(config TestConfig) error {
		if config.Name == "" {
			return assert.AnError
		}
		return nil
	}

	configs, errorCollection, err := LoadAndParseYAML[TestConfig]("workflows", validator)
	require.NoError(t, err)
	require.False(t, errorCollection.HasErrors(), "Expected no validation errors but got: %s", errorCollection.GetSummary())

	// Should have 2 configs
	assert.Len(t, configs, 2)

	// Check configs are parsed correctly
	configMap := make(map[string]TestConfig)
	for _, config := range configs {
		configMap[config.Name] = config
	}

	userConfig, exists := configMap["user-workflow"]
	assert.True(t, exists)
	assert.Equal(t, "automation", userConfig.Type)
	assert.Equal(t, "User automation workflow", userConfig.Description)

	projectConfig, exists := configMap["project-workflow"]
	assert.True(t, exists)
	assert.Equal(t, "automation", projectConfig.Type)
	assert.Equal(t, "Project automation workflow", projectConfig.Description)
}

func TestConfigurationLoader_MissingDirectories(t *testing.T) {
	// Create temporary directories for testing
	tempDir := t.TempDir()

	// Mock directory functions to point to non-existent directories
	originalUserHomeDir := osUserHomeDir
	originalGetwd := osGetwd
	defer func() {
		osUserHomeDir = originalUserHomeDir
		osGetwd = originalGetwd
	}()

	osUserHomeDir = func() (string, error) {
		return filepath.Join(tempDir, "nonexistent-user"), nil
	}
	osGetwd = func() (string, error) {
		return filepath.Join(tempDir, "nonexistent-project"), nil
	}

	// Test that loader works with missing directories
	loader, err := NewConfigurationLoader()
	require.NoError(t, err)

	files, err := loader.LoadYAMLFiles("serviceclasses")
	require.NoError(t, err)
	assert.Len(t, files, 0) // Should return empty slice, not error

	// Test LoadAndParseYAML with missing directories
	configs, errorCollection, err := LoadAndParseYAML[TestConfig]("capabilities", nil)
	require.NoError(t, err)
	require.False(t, errorCollection.HasErrors(), "Expected no validation errors but got: %s", errorCollection.GetSummary())
	assert.Len(t, configs, 0)
}

func TestConfigurationLoader_BothExtensions(t *testing.T) {
	// Create temporary directories for testing
	tempDir := t.TempDir()
	userDir := filepath.Join(tempDir, "user", ".config", "muster")

	// Create directory structure
	require.NoError(t, os.MkdirAll(filepath.Join(userDir, "capabilities"), 0755))

	// Create test files with both extensions
	yamlConfig := TestConfig{Name: "yaml-config", Type: "test", Description: "YAML file"}
	ymlConfig := TestConfig{Name: "yml-config", Type: "test", Description: "YML file"}

	writeYAMLFile(t, filepath.Join(userDir, "capabilities", "test.yaml"), yamlConfig)
	writeYAMLFile(t, filepath.Join(userDir, "capabilities", "test2.yml"), ymlConfig)

	// Mock directory functions
	originalUserHomeDir := osUserHomeDir
	originalGetwd := osGetwd
	defer func() {
		osUserHomeDir = originalUserHomeDir
		osGetwd = originalGetwd
	}()

	osUserHomeDir = func() (string, error) {
		return filepath.Join(tempDir, "user"), nil
	}
	osGetwd = func() (string, error) {
		return tempDir, nil // No project directory
	}

	// Test that both extensions are loaded
	loader, err := NewConfigurationLoader()
	require.NoError(t, err)

	files, err := loader.LoadYAMLFiles("capabilities")
	require.NoError(t, err)
	assert.Len(t, files, 2)

	// Check both files are present
	names := make([]string, len(files))
	for i, file := range files {
		names[i] = file.Name
	}
	assert.Contains(t, names, "test")
	assert.Contains(t, names, "test2")
}

func TestGetConfigurationPaths(t *testing.T) {
	tempDir := t.TempDir()

	// Mock directory functions
	originalUserHomeDir := osUserHomeDir
	originalGetwd := osGetwd
	defer func() {
		osUserHomeDir = originalUserHomeDir
		osGetwd = originalGetwd
	}()

	osUserHomeDir = func() (string, error) {
		return filepath.Join(tempDir, "home"), nil
	}
	osGetwd = func() (string, error) {
		return filepath.Join(tempDir, "project"), nil
	}

	userDir, projectDir, err := GetConfigurationPaths()
	require.NoError(t, err)

	expectedUserDir := filepath.Join(tempDir, "home", ".config", "muster")
	expectedProjectDir := filepath.Join(tempDir, "project", ".muster")

	assert.Equal(t, expectedUserDir, userDir)
	assert.Equal(t, expectedProjectDir, projectDir)
}

// Helper function to write YAML files
func writeYAMLFile(t *testing.T, path string, config TestConfig) {
	data, err := yaml.Marshal(config)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0644))
}
