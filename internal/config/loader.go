package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"muster/pkg/logging"

	"gopkg.in/yaml.v3"
)

const (
	userConfigDir  = ".config/muster"
	configFileName = "config.yaml"
)

func GetDefaultConfigPathOrPanic() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		panic(fmt.Errorf("could not determine user config directory: %w", err))
	}

	return filepath.Join(homeDir, userConfigDir)
}

// LoadConfig loads configuration from a single specified directory.
// The directory should contain config.yaml and subdirectories for other configuration types.
func LoadConfig(configPath string) (MusterConfig, error) {
	// Load main config.yaml from the specified path
	configFilePath := filepath.Join(configPath, configFileName)
	config := GetDefaultConfigWithRoles() // Start with default config

	// Start with default config
	data, err := os.ReadFile(configFilePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			logging.Info("ConfigLoader", "No config.yaml found at %s, using defaults", configFilePath)
			return config, nil
		}
		logging.Info("ConfigLoader", "Error loading config.yaml from %s: %s", configFilePath, err)
		return MusterConfig{}, err
	}
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		// config malformed
		return MusterConfig{}, fmt.Errorf("error loading config from %s: %w", configFilePath, err)
	}
	logging.Info("ConfigLoader", "Loaded configuration from %s", configFilePath)
	return config, nil
}
