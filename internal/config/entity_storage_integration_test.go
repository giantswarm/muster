package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// EntityStorageIntegrationTest provides comprehensive testing for the unified entity storage system
func TestEntityStorageIntegration(t *testing.T) {
	// Test all entity types
	entityTypes := []string{"capabilities", "serviceclasses", "mcpservers", "workflows"}

	for _, entityType := range entityTypes {
		t.Run(entityType, func(t *testing.T) {
			testEntityStorageScenarios(t, entityType)
		})
	}
}

func testEntityStorageScenarios(t *testing.T, entityType string) {
	t.Run("UserConfig_Storage", func(t *testing.T) {
		testUserConfigStorage(t, entityType)
	})

	t.Run("ProjectConfig_Storage", func(t *testing.T) {
		testProjectConfigStorage(t, entityType)
	})

	t.Run("Precedence_ProjectOverridesUser", func(t *testing.T) {
		testProjectOverridesUser(t, entityType)
	})

	t.Run("CRUD_Operations", func(t *testing.T) {
		testCRUDOperations(t, entityType)
	})

	t.Run("Loading_MultipleFiles", func(t *testing.T) {
		testLoadingMultipleFiles(t, entityType)
	})
}

func testUserConfigStorage(t *testing.T, entityType string) {
	// Setup temporary directories
	tempHomeDir := t.TempDir()
	tempWorkDir := t.TempDir()

	// Setup mock environment (no .muster directory - should use user config)
	originalGetwd := osGetwd
	originalUserHomeDir := osUserHomeDir
	defer func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHomeDir
	}()

	osGetwd = func() (string, error) {
		return tempWorkDir, nil
	}
	osUserHomeDir = func() (string, error) {
		return tempHomeDir, nil
	}

	// Create storage instance
	storage := NewStorage()

	// Create test entity data
	testData := generateTestEntityData(entityType, "test-user-entity")

	// Save entity - should go to user config since no .muster directory
	err := storage.Save(entityType, "test-user-entity", testData)
	require.NoError(t, err)

	// Verify file was saved to user config directory
	expectedPath := filepath.Join(tempHomeDir, ".config/muster", entityType, "test-user-entity.yaml")
	assert.FileExists(t, expectedPath)

	// Verify file was NOT saved to project directory
	projectPath := filepath.Join(tempWorkDir, ".muster", entityType, "test-user-entity.yaml")
	assert.NoFileExists(t, projectPath)

	// Test loading
	loadedData, err := storage.Load(entityType, "test-user-entity")
	require.NoError(t, err)
	assert.Equal(t, testData, loadedData)

	// Test listing
	names, err := storage.List(entityType)
	require.NoError(t, err)
	assert.Contains(t, names, "test-user-entity")
}

func testProjectConfigStorage(t *testing.T, entityType string) {
	// Setup temporary directories
	tempHomeDir := t.TempDir()
	tempWorkDir := t.TempDir()

	// Create .muster directory to indicate project context
	musterDir := filepath.Join(tempWorkDir, ".muster")
	require.NoError(t, os.MkdirAll(musterDir, 0755))

	// Setup mock environment
	originalGetwd := osGetwd
	originalUserHomeDir := osUserHomeDir
	defer func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHomeDir
	}()

	osGetwd = func() (string, error) {
		return tempWorkDir, nil
	}
	osUserHomeDir = func() (string, error) {
		return tempHomeDir, nil
	}

	// Create storage instance
	storage := NewStorage()

	// Create test entity data
	testData := generateTestEntityData(entityType, "test-project-entity")

	// Save entity - should go to project config since .muster directory exists
	err := storage.Save(entityType, "test-project-entity", testData)
	require.NoError(t, err)

	// Verify file was saved to project config directory
	expectedPath := filepath.Join(tempWorkDir, ".muster", entityType, "test-project-entity.yaml")
	assert.FileExists(t, expectedPath)

	// Verify file was NOT saved to user directory
	userPath := filepath.Join(tempHomeDir, ".config/muster", entityType, "test-project-entity.yaml")
	assert.NoFileExists(t, userPath)

	// Test loading
	loadedData, err := storage.Load(entityType, "test-project-entity")
	require.NoError(t, err)
	assert.Equal(t, testData, loadedData)

	// Test listing
	names, err := storage.List(entityType)
	require.NoError(t, err)
	assert.Contains(t, names, "test-project-entity")
}

func testProjectOverridesUser(t *testing.T, entityType string) {
	// Setup temporary directories
	tempHomeDir := t.TempDir()
	tempWorkDir := t.TempDir()

	// Create .muster directory for project context
	musterDir := filepath.Join(tempWorkDir, ".muster")
	require.NoError(t, os.MkdirAll(musterDir, 0755))

	// Setup mock environment
	originalGetwd := osGetwd
	originalUserHomeDir := osUserHomeDir
	defer func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHomeDir
	}()

	osGetwd = func() (string, error) {
		return tempWorkDir, nil
	}
	osUserHomeDir = func() (string, error) {
		return tempHomeDir, nil
	}

	// Create both user and project directories
	userDir := filepath.Join(tempHomeDir, ".config/muster", entityType)
	projectDir := filepath.Join(tempWorkDir, ".muster", entityType)
	require.NoError(t, os.MkdirAll(userDir, 0755))
	require.NoError(t, os.MkdirAll(projectDir, 0755))

	// Create same-named entity in both locations with different content
	userData := generateTestEntityData(entityType, "shared-entity")
	projectData := generateTestEntityData(entityType, "shared-entity-project-version")

	// Write user config file
	userPath := filepath.Join(userDir, "shared-entity.yaml")
	require.NoError(t, os.WriteFile(userPath, userData, 0644))

	// Write project config file (should override)
	projectPath := filepath.Join(projectDir, "shared-entity.yaml")
	require.NoError(t, os.WriteFile(projectPath, projectData, 0644))

	// Create storage and test loading - should return project version
	storage := NewStorage()
	loadedData, err := storage.Load(entityType, "shared-entity")
	require.NoError(t, err)
	assert.Equal(t, projectData, loadedData)

	// Test that listing only shows one instance
	names, err := storage.List(entityType)
	require.NoError(t, err)

	count := 0
	for _, name := range names {
		if name == "shared-entity" {
			count++
		}
	}
	assert.Equal(t, 1, count, "Should only list shared-entity once despite being in both locations")
}

func testCRUDOperations(t *testing.T, entityType string) {
	// Setup temporary directories with project context
	tempHomeDir := t.TempDir()
	tempWorkDir := t.TempDir()

	musterDir := filepath.Join(tempWorkDir, ".muster")
	require.NoError(t, os.MkdirAll(musterDir, 0755))

	// Setup mock environment
	originalGetwd := osGetwd
	originalUserHomeDir := osUserHomeDir
	defer func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHomeDir
	}()

	osGetwd = func() (string, error) {
		return tempWorkDir, nil
	}
	osUserHomeDir = func() (string, error) {
		return tempHomeDir, nil
	}

	storage := NewStorage()

	// CREATE
	testData := generateTestEntityData(entityType, "crud-test-entity")
	err := storage.Save(entityType, "crud-test-entity", testData)
	require.NoError(t, err)

	// READ
	loadedData, err := storage.Load(entityType, "crud-test-entity")
	require.NoError(t, err)
	assert.Equal(t, testData, loadedData)

	// UPDATE
	updatedData := generateTestEntityData(entityType, "crud-test-entity-updated")
	err = storage.Save(entityType, "crud-test-entity", updatedData)
	require.NoError(t, err)

	// Verify update
	loadedUpdatedData, err := storage.Load(entityType, "crud-test-entity")
	require.NoError(t, err)
	assert.Equal(t, updatedData, loadedUpdatedData)

	// DELETE
	err = storage.Delete(entityType, "crud-test-entity")
	require.NoError(t, err)

	// Verify deletion
	_, err = storage.Load(entityType, "crud-test-entity")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func testLoadingMultipleFiles(t *testing.T, entityType string) {
	// Setup temporary directories with project context
	tempHomeDir := t.TempDir()
	tempWorkDir := t.TempDir()

	musterDir := filepath.Join(tempWorkDir, ".muster")
	require.NoError(t, os.MkdirAll(musterDir, 0755))

	// Setup mock environment
	originalGetwd := osGetwd
	originalUserHomeDir := osUserHomeDir
	defer func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHomeDir
	}()

	osGetwd = func() (string, error) {
		return tempWorkDir, nil
	}
	osUserHomeDir = func() (string, error) {
		return tempHomeDir, nil
	}

	storage := NewStorage()

	// Create multiple entities
	entities := []string{"entity1", "entity2", "entity3"}

	for _, entityName := range entities {
		testData := generateTestEntityData(entityType, entityName)
		err := storage.Save(entityType, entityName, testData)
		require.NoError(t, err)
	}

	// Test listing returns all entities
	names, err := storage.List(entityType)
	require.NoError(t, err)

	for _, expectedName := range entities {
		assert.Contains(t, names, expectedName)
	}

	// Test loading each entity
	for _, entityName := range entities {
		data, err := storage.Load(entityType, entityName)
		require.NoError(t, err)
		assert.NotEmpty(t, data)
	}
}

// generateTestEntityData creates valid test data for different entity types
func generateTestEntityData(entityType, name string) []byte {
	switch entityType {
	case "capabilities":
		data := map[string]interface{}{
			"name":        name,
			"type":        "test_capability",
			"description": "Test capability for integration testing",
			"operations": map[string]interface{}{
				"test_operation": map[string]interface{}{
					"description": "Test operation",
					"requires":    []string{"test_tool"},
				},
			},
		}
		result, _ := yaml.Marshal(data)
		return result

	case "serviceclasses":
		data := map[string]interface{}{
			"name":        name,
			"type":        "test_service",
			"version":     "1.0.0",
			"description": "Test service class for integration testing",
			"serviceConfig": map[string]interface{}{
				"serviceType": "TestService",
				"lifecycleTools": map[string]interface{}{
					"start": map[string]interface{}{
						"tool": "test_start_tool",
					},
					"stop": map[string]interface{}{
						"tool": "test_stop_tool",
					},
				},
			},
		}
		result, _ := yaml.Marshal(data)
		return result

	case "mcpservers":
		data := map[string]interface{}{
			"name":    name,
			"type":    "localCommand",
			"command": []string{"echo", "test"},
			"enabled": true,
		}
		result, _ := yaml.Marshal(data)
		return result

	case "workflows":
		data := map[string]interface{}{
			"name":        name,
			"description": "Test workflow for integration testing",
			"steps": []map[string]interface{}{
				{
					"id":   "test_step",
					"tool": "test_tool",
					"args": map[string]interface{}{
						"param": "value",
					},
				},
			},
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		}
		result, _ := yaml.Marshal(data)
		return result

	default:
		panic("Unknown entity type: " + entityType)
	}
}

// TestEntityStorageConsistency tests that all entity types behave consistently
func TestEntityStorageConsistency(t *testing.T) {
	entityTypes := []string{"capabilities", "serviceclasses", "mcpservers", "workflows"}

	// Setup temporary directories with project context
	tempHomeDir := t.TempDir()
	tempWorkDir := t.TempDir()

	musterDir := filepath.Join(tempWorkDir, ".muster")
	require.NoError(t, os.MkdirAll(musterDir, 0755))

	// Setup mock environment
	originalGetwd := osGetwd
	originalUserHomeDir := osUserHomeDir
	defer func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHomeDir
	}()

	osGetwd = func() (string, error) {
		return tempWorkDir, nil
	}
	osUserHomeDir = func() (string, error) {
		return tempHomeDir, nil
	}

	storage := NewStorage()

	// Test that all entity types can be created, loaded, and deleted consistently
	for _, entityType := range entityTypes {
		t.Run("Consistency_"+entityType, func(t *testing.T) {
			entityName := "consistency-test-" + entityType
			testData := generateTestEntityData(entityType, entityName)

			// Save
			err := storage.Save(entityType, entityName, testData)
			require.NoError(t, err, "Failed to save %s", entityType)

			// Verify file location (should be in project config)
			expectedPath := filepath.Join(tempWorkDir, ".muster", entityType, entityName+".yaml")
			assert.FileExists(t, expectedPath, "File not found in expected project location for %s", entityType)

			// Load
			loadedData, err := storage.Load(entityType, entityName)
			require.NoError(t, err, "Failed to load %s", entityType)
			assert.Equal(t, testData, loadedData, "Loaded data doesn't match saved data for %s", entityType)

			// List
			names, err := storage.List(entityType)
			require.NoError(t, err, "Failed to list %s", entityType)
			assert.Contains(t, names, entityName, "Entity not found in list for %s", entityType)

			// Delete
			err = storage.Delete(entityType, entityName)
			require.NoError(t, err, "Failed to delete %s", entityType)

			// Verify deletion
			_, err = storage.Load(entityType, entityName)
			assert.Error(t, err, "Entity should be deleted for %s", entityType)
			assert.Contains(t, err.Error(), "not found", "Expected 'not found' error for %s", entityType)
		})
	}
}

// TestFilenameHandling tests that problematic filenames are handled correctly
func TestFilenameHandling(t *testing.T) {
	// Setup temporary directories with project context
	tempHomeDir := t.TempDir()
	tempWorkDir := t.TempDir()

	musterDir := filepath.Join(tempWorkDir, ".muster")
	require.NoError(t, os.MkdirAll(musterDir, 0755))

	// Setup mock environment
	originalGetwd := osGetwd
	originalUserHomeDir := osUserHomeDir
	defer func() {
		osGetwd = originalGetwd
		osUserHomeDir = originalUserHomeDir
	}()

	osGetwd = func() (string, error) {
		return tempWorkDir, nil
	}
	osUserHomeDir = func() (string, error) {
		return tempHomeDir, nil
	}

	storage := NewStorage()

	// Test problematic filenames that should be sanitized
	testCases := []struct {
		originalName      string
		expectedSanitized string
	}{
		{"name with spaces", "name_with_spaces"},
		{"name/with/slashes", "name_with_slashes"},
		{"name:with:colons", "name_with_colons"},
		{"name*with*stars", "name_with_stars"},
		{"name?with?questions", "name_with_questions"},
		{"name\"with\"quotes", "name_with_quotes"},
		{"name<with>brackets", "name_with_brackets"},
		{"name|with|pipes", "name_with_pipes"},
		{"name\\with\\backslashes", "name_with_backslashes"},
		{"  leading-trailing-spaces  ", "leading-trailing-spaces"},
		{"..dots..", "dots"},
		{"multiple___underscores", "multiple_underscores"},
	}

	for _, tc := range testCases {
		t.Run("Filename_"+tc.originalName, func(t *testing.T) {
			testData := generateTestEntityData("workflows", tc.originalName)

			// Should save without error (filename gets sanitized)
			err := storage.Save("workflows", tc.originalName, testData)
			require.NoError(t, err)

			// Should be able to load back using original name
			loadedData, err := storage.Load("workflows", tc.originalName)
			require.NoError(t, err)
			assert.Equal(t, testData, loadedData)

			// List should return the sanitized name (since that's what's on disk)
			names, err := storage.List("workflows")
			require.NoError(t, err)
			assert.Contains(t, names, tc.expectedSanitized, "Sanitized name should appear in listing")
			assert.NotContains(t, names, tc.originalName, "Original name should not appear in listing if it was sanitized")

			// Should be able to delete using original name
			err = storage.Delete("workflows", tc.originalName)
			require.NoError(t, err)

			// Verify deletion - list should no longer contain the sanitized name
			names, err = storage.List("workflows")
			require.NoError(t, err)
			assert.NotContains(t, names, tc.expectedSanitized, "Sanitized name should be gone after deletion")
		})
	}
}
