package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
	// For direct use in tests if needed, or mocking
)

// Helper function to create a temporary config file
func createTempConfigFile(t *testing.T, dir string, filename string, content MusterConfig) string {
	t.Helper()
	tempFilePath := filepath.Join(dir, filename)
	data, err := yaml.Marshal(&content)
	assert.NoError(t, err)
	err = os.WriteFile(tempFilePath, data, 0644)
	assert.NoError(t, err)
	return tempFilePath
}

func TestLoadConfig_DefaultOnly(t *testing.T) {
	tempDir := t.TempDir()

	// Mock paths to prevent loading any existing config files
	originalGetUserConfigPath := getUserConfigPath
	originalGetProjectConfigPath := getProjectConfigPath
	defer func() {
		getUserConfigPath = originalGetUserConfigPath
		getProjectConfigPath = originalGetProjectConfigPath
	}()

	// Point to non-existent files in temp directory
	getUserConfigPath = func() (string, error) {
		return filepath.Join(tempDir, "non-existent-user-config.yaml"), nil
	}
	getProjectConfigPath = func() (string, error) {
		return filepath.Join(tempDir, "non-existent-project-config.yaml"), nil
	}

	tc := GetDefaultConfigWithRoles()

	loadedConfig, err := LoadConfig()
	assert.NoError(t, err)

	// DeepEqual might be too strict if order changes in slices, but for default config it should be stable.
	assert.True(t, reflect.DeepEqual(tc.GlobalSettings, loadedConfig.GlobalSettings), "GlobalSettings should match default")
	// MCPServers are now managed by MCPServerManager, not loaded via config system
	// Port forwards and clusters have been removed as part of the generic orchestrator refactoring
}

func TestLoadConfig_UserOverride(t *testing.T) {
	tempDir := t.TempDir()

	// Mock user config dir
	originalGetUserConfigPath := getUserConfigPath
	originalOsUserHomeDir := osUserHomeDir // Mock our package-level variable
	defer func() {
		getUserConfigPath = originalGetUserConfigPath
		osUserHomeDir = originalOsUserHomeDir // Restore
	}()

	osUserHomeDir = func() (string, error) { return tempDir, nil } // Assign to our var
	getUserConfigPath = func() (string, error) {
		// This can now also use the mocked osUserHomeDir if needed, or be self-contained
		// For this test, it directly returns the temp path based on the mocked home dir.
		return filepath.Join(tempDir, userConfigDir, configFileName), nil
	}

	// Create a user config file
	userConfDir := filepath.Join(tempDir, userConfigDir)
	err := os.MkdirAll(userConfDir, 0755)
	assert.NoError(t, err)

	userOverride := MusterConfig{
		GlobalSettings: GlobalSettings{
			DefaultContainerRuntime: "podman",
		},
		// MCPServers removed - now managed by MCPServerManager via directory-based config
	}
	createTempConfigFile(t, userConfDir, configFileName, userOverride)

	loadedConfig, err := LoadConfig()
	assert.NoError(t, err)

	// Check global settings override
	assert.Equal(t, "podman", loadedConfig.GlobalSettings.DefaultContainerRuntime)

	// MCPServers are now managed by MCPServerManager, not tested here
}

func TestLoadConfig_ProjectOverride(t *testing.T) {
	tempDir := t.TempDir()

	originalGetProjectConfigPath := getProjectConfigPath
	originalOsGetwd := osGetwd // Mock our package-level variable
	defer func() {
		getProjectConfigPath = originalGetProjectConfigPath
		osGetwd = originalOsGetwd // Restore
	}()

	osGetwd = func() (string, error) { return tempDir, nil } // Assign to our var
	getProjectConfigPath = func() (string, error) {
		// Similar to user config, this can use mocked osGetwd or be self-contained.
		return filepath.Join(tempDir, projectConfigDir, configFileName), nil
	}

	projectConfDir := filepath.Join(tempDir, projectConfigDir)
	err := os.MkdirAll(projectConfDir, 0755)
	assert.NoError(t, err)

	projectOverride := MusterConfig{
		GlobalSettings: GlobalSettings{DefaultContainerRuntime: "cri-o"},
		// MCPServers removed - now managed by MCPServerManager via directory-based config
	}
	createTempConfigFile(t, projectConfDir, configFileName, projectOverride)

	loadedConfig, err := LoadConfig()
	assert.NoError(t, err)
	assert.Equal(t, "cri-o", loadedConfig.GlobalSettings.DefaultContainerRuntime)

	// MCPServers are now managed by MCPServerManager, not tested here
}

func TestLoadConfig_ContextResolution(t *testing.T) {
	// This test is no longer relevant since PortForwards functionality was removed
	t.Skip("PortForwards functionality removed as part of generic orchestrator refactoring")
}

func TestResolveKubeContextPlaceholders(t *testing.T) {
	// This test is no longer relevant since PortForwards functionality was removed
	t.Skip("PortForwards functionality removed as part of generic orchestrator refactoring")
}

// TODO: Add more tests:
// - Test with user config but no project config (covered by TestLoadConfig_UserOverride if project path doesn't exist).
// - Test with project config but no user config (covered by TestLoadConfig_ProjectOverride if user path doesn't exist).
// - Test with both user and project config, ensuring project takes precedence for same-name items.
// - Test with empty config files.
// - Test with malformed YAML in config files.
// - Test merge strategy for GlobalSettings more thoroughly if more fields are added.

// </rewritten_file>
