package config

import (
	"fmt"
	"os"
	"path/filepath"

	"muster/pkg/logging"

	"gopkg.in/yaml.v3"
)

// For mocking in tests
var osUserHomeDir = os.UserHomeDir

const (
	userConfigDir  = ".config/muster"
	configFileName = "config.yaml"
)

func GetDefaultConfigPathOrPanic() string {
	userConfigDir, err := GetUserConfigDir()
	if err != nil {
		panic(fmt.Errorf("could not determine user config directory: %w", err))
	}
	return userConfigDir
}

// LoadConfig loads the muster configuration from the default directory (~/.config/muster).
func LoadConfig() (MusterConfig, error) {
	return LoadConfigFromPath(GetDefaultConfigPathOrPanic())
}

// LoadConfigFromPath loads configuration from a single specified directory.
// The directory should contain config.yaml and subdirectories for other configuration types.
func LoadConfigFromPath(configPath string) (MusterConfig, error) {
	// Validate that the directory exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// If directory doesn't exist, return default config (create directories as needed)
		logging.Info("ConfigLoader", "Configuration directory %s does not exist, using defaults", configPath)
		return GetDefaultConfigWithRoles(), nil
	}

	// Start with default configuration
	config := GetDefaultConfigWithRoles()

	// Load main config.yaml from the specified path
	configFilePath := filepath.Join(configPath, configFileName)
	if _, err := os.Stat(configFilePath); err == nil {
		fileConfig, err := loadConfigFromFile(configFilePath)
		if err != nil {
			return MusterConfig{}, fmt.Errorf("error loading config from %s: %w", configFilePath, err)
		}
		config = mergeConfigs(config, fileConfig)
		logging.Info("ConfigLoader", "Loaded configuration from %s", configFilePath)
	} else {
		logging.Info("ConfigLoader", "No config.yaml found at %s, using defaults", configFilePath)
	}

	return config, nil
}

// GetUserConfigDir returns the user configuration directory path
func GetUserConfigDir() (string, error) {
	homeDir, err := osUserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, userConfigDir), nil
}

// loadConfigFromFile loads an MusterConfig from a YAML file.
func loadConfigFromFile(filePath string) (MusterConfig, error) {
	var config MusterConfig
	data, err := os.ReadFile(filePath)
	if err != nil {
		return MusterConfig{}, err
	}
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return MusterConfig{}, err
	}
	return config, nil
}

// mergeConfigs merges 'overlay' config into 'base' config.
func mergeConfigs(base, overlay MusterConfig) MusterConfig {
	mergedConfig := base

	// Merge Aggregator settings
	if overlay.Aggregator.Port != 0 {
		mergedConfig.Aggregator.Port = overlay.Aggregator.Port
	}
	if overlay.Aggregator.Host != "" {
		mergedConfig.Aggregator.Host = overlay.Aggregator.Host
	}
	// Merge Enabled field - only if explicitly set in overlay
	mergedConfig.Aggregator.Enabled = overlay.Aggregator.Enabled

	// Merge namespace
	if overlay.Namespace != "" {
		mergedConfig.Namespace = overlay.Namespace
	}

	return mergedConfig
}
