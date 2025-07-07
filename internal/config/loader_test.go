package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
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

	// Mock osUserHomeDir to point to a temp directory without config
	originalOsUserHomeDir := osUserHomeDir
	defer func() {
		osUserHomeDir = originalOsUserHomeDir
	}()

	osUserHomeDir = func() (string, error) { return tempDir, nil }

	// LoadConfig should return default config when no config file exists
	expectedConfig := GetDefaultConfigWithRoles()
	loadedConfig, err := LoadConfig()
	assert.NoError(t, err)

	// Compare the loaded config with the default config
	assert.True(t, reflect.DeepEqual(expectedConfig.GlobalSettings, loadedConfig.GlobalSettings), "GlobalSettings should match default")
	assert.True(t, reflect.DeepEqual(expectedConfig.Aggregator, loadedConfig.Aggregator), "Aggregator should match default")
}

func TestLoadConfig_WithUserConfig(t *testing.T) {
	tempDir := t.TempDir()

	// Mock osUserHomeDir to point to our temp directory
	originalOsUserHomeDir := osUserHomeDir
	defer func() {
		osUserHomeDir = originalOsUserHomeDir
	}()

	osUserHomeDir = func() (string, error) { return tempDir, nil }

	// Create the user config directory
	userConfDir := filepath.Join(tempDir, userConfigDir)
	err := os.MkdirAll(userConfDir, 0755)
	assert.NoError(t, err)

	// Create a user config file with custom settings
	userConfig := MusterConfig{
		GlobalSettings: GlobalSettings{
			DefaultContainerRuntime: "podman",
		},
		Aggregator: AggregatorConfig{
			Port: 9090,
			Host: "0.0.0.0",
		},
	}
	createTempConfigFile(t, userConfDir, configFileName, userConfig)

	loadedConfig, err := LoadConfig()
	assert.NoError(t, err)

	// Check that the custom settings were loaded
	assert.Equal(t, "podman", loadedConfig.GlobalSettings.DefaultContainerRuntime)
	assert.Equal(t, 9090, loadedConfig.Aggregator.Port)
	assert.Equal(t, "0.0.0.0", loadedConfig.Aggregator.Host)
}

func TestLoadConfigFromPath(t *testing.T) {
	tempDir := t.TempDir()

	// Create a custom config file
	customConfig := MusterConfig{
		GlobalSettings: GlobalSettings{
			DefaultContainerRuntime: "cri-o",
		},
		Aggregator: AggregatorConfig{
			Port: 8888,
			Host: "custom-host",
		},
	}
	createTempConfigFile(t, tempDir, configFileName, customConfig)

	// Load config from the custom path
	loadedConfig, err := LoadConfigFromPath(tempDir)
	assert.NoError(t, err)

	// Check that the custom settings were loaded
	assert.Equal(t, "cri-o", loadedConfig.GlobalSettings.DefaultContainerRuntime)
	assert.Equal(t, 8888, loadedConfig.Aggregator.Port)
	assert.Equal(t, "custom-host", loadedConfig.Aggregator.Host)
}

func TestLoadConfigFromPath_NonExistentPath(t *testing.T) {
	tempDir := t.TempDir()
	nonExistentPath := filepath.Join(tempDir, "non-existent")

	// Loading from non-existent path should return default config
	expectedConfig := GetDefaultConfigWithRoles()
	loadedConfig, err := LoadConfigFromPath(nonExistentPath)
	assert.NoError(t, err)

	// Should match default config
	assert.True(t, reflect.DeepEqual(expectedConfig.GlobalSettings, loadedConfig.GlobalSettings), "GlobalSettings should match default")
	assert.True(t, reflect.DeepEqual(expectedConfig.Aggregator, loadedConfig.Aggregator), "Aggregator should match default")
}

func TestLoadConfigFromPath_NoConfigFile(t *testing.T) {
	tempDir := t.TempDir()

	// Directory exists but no config.yaml file
	loadedConfig, err := LoadConfigFromPath(tempDir)
	assert.NoError(t, err)

	// Should return default config
	expectedConfig := GetDefaultConfigWithRoles()
	assert.True(t, reflect.DeepEqual(expectedConfig.GlobalSettings, loadedConfig.GlobalSettings), "GlobalSettings should match default")
	assert.True(t, reflect.DeepEqual(expectedConfig.Aggregator, loadedConfig.Aggregator), "Aggregator should match default")
}

func TestLoadConfigFromPath_InvalidYAML(t *testing.T) {
	tempDir := t.TempDir()

	// Create an invalid YAML file
	invalidYAMLPath := filepath.Join(tempDir, configFileName)
	err := os.WriteFile(invalidYAMLPath, []byte("invalid: yaml: content: ["), 0644)
	assert.NoError(t, err)

	// Should return an error for invalid YAML
	_, err = LoadConfigFromPath(tempDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error loading config")
}

func TestGetUserConfigDir(t *testing.T) {
	// Mock osUserHomeDir
	originalOsUserHomeDir := osUserHomeDir
	defer func() {
		osUserHomeDir = originalOsUserHomeDir
	}()

	testHome := "/test/home"
	osUserHomeDir = func() (string, error) { return testHome, nil }

	configDir, err := GetUserConfigDir()
	assert.NoError(t, err)
	assert.Equal(t, filepath.Join(testHome, userConfigDir), configDir)
}
