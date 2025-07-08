package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"muster/pkg/logging"

	"gopkg.in/yaml.v3"
)

// For mocking in tests
var osUserHomeDir = os.UserHomeDir

const (
	userConfigDir  = ".config/muster"
	configFileName = "config.yaml"
)

// LoadedFile represents a configuration file that was loaded
type LoadedFile struct {
	Path   string // Full path to the file
	Source string // Always "config" now (no more user/project distinction)
	Name   string // Base filename without extension
}

// ConfigurationLoader provides configuration loading from a single directory.
type ConfigurationLoader struct {
	configDir string
}

// LoadConfig loads the muster configuration from the default directory (~/.config/muster).
func LoadConfig() (MusterConfig, error) {
	userConfigDir, err := GetUserConfigDir()
	if err != nil {
		return MusterConfig{}, fmt.Errorf("could not determine user config directory: %w", err)
	}

	return LoadConfigFromPath(userConfigDir)
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

// NewConfigurationLoader creates a new configuration loader for the default user directory.
func NewConfigurationLoader() (*ConfigurationLoader, error) {
	userDir, err := GetUserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to determine user config directory: %w", err)
	}

	return &ConfigurationLoader{
		configDir: userDir,
	}, nil
}

// NewConfigurationLoaderFromPath creates a new configuration loader for a specific directory.
func NewConfigurationLoaderFromPath(configPath string) (*ConfigurationLoader, error) {
	// Validate that the directory exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("configuration directory does not exist: %s", configPath)
	}

	return &ConfigurationLoader{
		configDir: configPath,
	}, nil
}

// LoadYAMLFiles loads YAML files from the configuration directory.
func (cl *ConfigurationLoader) LoadYAMLFiles(subDir string) ([]LoadedFile, error) {
	fullPath := filepath.Join(cl.configDir, subDir)
	files, err := cl.loadFilesFromDirectory(fullPath, "config")
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load %s: %w", subDir, err)
	}

	logging.Info("ConfigurationLoader", "Loaded %d files from %s", len(files), subDir)
	return files, nil
}

// loadFilesFromDirectory loads all YAML files from a directory
func (cl *ConfigurationLoader) loadFilesFromDirectory(dirPath, source string) ([]LoadedFile, error) {
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return nil, nil // Directory doesn't exist, return empty
	}

	var allFiles []string

	// Load .yaml files
	yamlPattern := filepath.Join(dirPath, "*.yaml")
	yamlFiles, err := filepath.Glob(yamlPattern)
	if err != nil {
		return nil, fmt.Errorf("failed to glob yaml files: %w", err)
	}
	allFiles = append(allFiles, yamlFiles...)

	// Load .yml files
	ymlPattern := filepath.Join(dirPath, "*.yml")
	ymlFiles, err := filepath.Glob(ymlPattern)
	if err != nil {
		return nil, fmt.Errorf("failed to glob yml files: %w", err)
	}
	allFiles = append(allFiles, ymlFiles...)

	// Convert to LoadedFile structs
	var result []LoadedFile
	for _, filePath := range allFiles {
		basename := filepath.Base(filePath)
		name := strings.TrimSuffix(basename, filepath.Ext(basename))

		result = append(result, LoadedFile{
			Path:   filePath,
			Source: source,
			Name:   name,
		})
	}

	return result, nil
}

// LoadAndParseYAML is a generic utility for loading and parsing YAML files into any type
// with comprehensive error collection and graceful degradation.
func LoadAndParseYAML[T any](subDir string, validator func(T) error) ([]T, *ConfigurationErrorCollection, error) {
	loader, err := NewConfigurationLoader()
	if err != nil {
		return nil, nil, err
	}

	files, err := loader.LoadYAMLFiles(subDir)
	if err != nil {
		return nil, nil, err
	}

	return parseYAMLFiles[T](files, subDir, validator)
}

// LoadAndParseYAMLFromPath is a generic utility for loading and parsing YAML files from a specific directory
// with comprehensive error collection and graceful degradation.
func LoadAndParseYAMLFromPath[T any](configPath, subDir string, validator func(T) error) ([]T, *ConfigurationErrorCollection, error) {
	loader, err := NewConfigurationLoaderFromPath(configPath)
	if err != nil {
		return nil, nil, err
	}

	files, err := loader.LoadYAMLFiles(subDir)
	if err != nil {
		return nil, nil, err
	}

	return parseYAMLFiles[T](files, subDir, validator)
}

// LoadAndParseYAMLWithConfig is a utility that chooses between default and custom directory loading
// based on whether a custom config path is provided. If configPath is empty, uses default directory.
func LoadAndParseYAMLWithConfig[T any](configPath, subDir string, validator func(T) error) ([]T, *ConfigurationErrorCollection, error) {
	if configPath != "" {
		return LoadAndParseYAMLFromPath[T](configPath, subDir, validator)
	}
	return LoadAndParseYAML[T](subDir, validator)
}

// parseYAMLFiles is a helper function that parses loaded YAML files
func parseYAMLFiles[T any](files []LoadedFile, subDir string, validator func(T) error) ([]T, *ConfigurationErrorCollection, error) {
	var results []T
	errorCollection := NewConfigurationErrorCollection()

	for _, file := range files {
		var item T

		// Read file
		data, err := os.ReadFile(file.Path)
		if err != nil {
			configError := NewConfigurationErrorWithDetails(
				file.Path, file.Name, file.Source, subDir, "io",
				fmt.Sprintf("Failed to read file: %v", err),
				"File system error occurred while reading configuration file",
			)
			errorCollection.Add(configError)
			continue
		}

		// Parse YAML
		if err := yaml.Unmarshal(data, &item); err != nil {
			details := fmt.Sprintf("YAML parsing failed: %v", err)
			if yamlErr, ok := err.(*yaml.TypeError); ok {
				details = fmt.Sprintf("YAML type error: %s", strings.Join(yamlErr.Errors, ", "))
			}

			configError := NewConfigurationErrorWithDetails(
				file.Path, file.Name, file.Source, subDir, "parse",
				"Invalid YAML format",
				details,
			)
			errorCollection.Add(configError)
			continue
		}

		// Validate if validator provided
		if validator != nil {
			if err := validator(item); err != nil {
				configError := NewConfigurationErrorWithDetails(
					file.Path, file.Name, file.Source, subDir, "validation",
					fmt.Sprintf("Validation failed: %v", err),
					"Configuration content does not meet requirements",
				)
				errorCollection.Add(configError)
				continue
			}
		}

		results = append(results, item)
		logging.Info("ConfigurationLoader", "Successfully loaded configuration: %s from %s", file.Name, subDir)
	}

	// Log summary of results
	totalFiles := len(files)
	successCount := len(results)
	errorCount := errorCollection.Count()

	if errorCount > 0 {
		logging.Warn("ConfigurationLoader", "Loaded %d/%d %s configurations (%d errors)",
			successCount, totalFiles, subDir, errorCount)
	} else if totalFiles > 0 {
		logging.Info("ConfigurationLoader", "Successfully loaded all %d %s configurations",
			successCount, subDir)
	}

	return results, errorCollection, nil
}

// GetUserConfigDir returns the user configuration directory path
func GetUserConfigDir() (string, error) {
	homeDir, err := osUserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, userConfigDir), nil
}

// GetConfigurationPaths returns the configuration directory path (single directory now)
func GetConfigurationPaths() (configDir string, err error) {
	configDir, err = GetUserConfigDir()
	if err != nil {
		return "", fmt.Errorf("failed to determine config directory: %w", err)
	}
	return configDir, nil
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

	return mergedConfig
}
