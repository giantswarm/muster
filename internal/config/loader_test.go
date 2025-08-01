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

	// LoadConfig should return default config when no config file exists
	expectedConfig := GetDefaultConfigWithRoles()
	loadedConfig, err := LoadConfig(tempDir)
	assert.NoError(t, err)

	// Compare the loaded config with the default config
	assert.True(t, reflect.DeepEqual(expectedConfig.Aggregator, loadedConfig.Aggregator), "Aggregator should match default")
}

func TestLoadConfig_WithUserConfig(t *testing.T) {
	tempDir := t.TempDir()

	// Create the user config directory
	userConfDir := filepath.Join(tempDir, userConfigDir)
	err := os.MkdirAll(userConfDir, 0755)
	assert.NoError(t, err)

	// Create a user config file with custom settings
	userConfig := MusterConfig{
		Aggregator: AggregatorConfig{
			Port: 9090,
			Host: "0.0.0.0",
		},
	}
	createTempConfigFile(t, userConfDir, configFileName, userConfig)

	loadedConfig, err := LoadConfig(userConfDir)
	assert.NoError(t, err)

	// Check that the custom settings were loaded
	assert.Equal(t, 9090, loadedConfig.Aggregator.Port)
	assert.Equal(t, "0.0.0.0", loadedConfig.Aggregator.Host)
}

func TestLoadConfigFromPath(t *testing.T) {
	tempDir := t.TempDir()

	// Create a custom config file
	customConfig := MusterConfig{
		Aggregator: AggregatorConfig{
			Port: 8888,
			Host: "custom-host",
		},
	}
	createTempConfigFile(t, tempDir, configFileName, customConfig)

	// Load config from the custom path
	loadedConfig, err := LoadConfig(tempDir)
	assert.NoError(t, err)

	// Check that the custom settings were loaded
	assert.Equal(t, 8888, loadedConfig.Aggregator.Port)
	assert.Equal(t, "custom-host", loadedConfig.Aggregator.Host)
}

func TestLoadConfigFromPath_NonExistentPath(t *testing.T) {
	tempDir := t.TempDir()
	nonExistentPath := filepath.Join(tempDir, "non-existent")

	// Loading from non-existent path should return default config
	expectedConfig := GetDefaultConfigWithRoles()
	loadedConfig, err := LoadConfig(nonExistentPath)
	assert.NoError(t, err)

	// Should match default config
	assert.True(t, reflect.DeepEqual(expectedConfig.Aggregator, loadedConfig.Aggregator), "Aggregator should match default")
}

func TestLoadConfigFromPath_NoConfigFile(t *testing.T) {
	tempDir := t.TempDir()

	// Directory exists but no config.yaml file
	loadedConfig, err := LoadConfig(tempDir)
	assert.NoError(t, err)

	// Should return default config
	expectedConfig := GetDefaultConfigWithRoles()
	assert.True(t, reflect.DeepEqual(expectedConfig.Aggregator, loadedConfig.Aggregator), "Aggregator should match default")
}

func TestLoadConfigFromPath_InvalidYAML(t *testing.T) {
	tempDir := t.TempDir()

	// Create an invalid YAML file
	invalidYAMLPath := filepath.Join(tempDir, configFileName)
	err := os.WriteFile(invalidYAMLPath, []byte("invalid: yaml: content: ["), 0644)
	assert.NoError(t, err)

	// Should return an error for invalid YAML
	_, err = LoadConfig(tempDir)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error loading config")
}
