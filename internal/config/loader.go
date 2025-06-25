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
var osGetwd = os.Getwd

// GetOsGetwd returns the current osGetwd function (for testing)
func GetOsGetwd() func() (string, error) {
	return osGetwd
}

// SetOsGetwd sets the osGetwd function (for testing)
func SetOsGetwd(fn func() (string, error)) {
	osGetwd = fn
}

// GetOsUserHomeDir returns the current osUserHomeDir function (for testing)
func GetOsUserHomeDir() func() (string, error) {
	return osUserHomeDir
}

// SetOsUserHomeDir sets the osUserHomeDir function (for testing)
func SetOsUserHomeDir(fn func() (string, error)) {
	osUserHomeDir = fn
}

const (
	userConfigDir    = ".config/muster"
	projectConfigDir = ".muster"
	configFileName   = "config.yaml"
)

// LoadedFile represents a configuration file that was loaded
type LoadedFile struct {
	Path   string // Full path to the file
	Source string // "user" or "project"
	Name   string // Base filename without extension
}

// ConfigurationLoader provides common layered loading for all configuration types.
// This utility ensures NO DIFFERENCE between packages in how they handle configuration loading.
type ConfigurationLoader struct {
	userConfigDir    string
	projectConfigDir string
}

// LoadConfig loads the muster configuration by layering default, user, and project settings.
func LoadConfig() (MusterConfig, error) {
	// 1. Start with the default configuration
	config := GetDefaultConfigWithRoles()

	// 2. Determine user-specific configuration path
	userConfigPath, err := getUserConfigPath()
	if err != nil {
		// Log this error but don't fail; user config is optional
		fmt.Fprintf(os.Stderr, "Warning: Could not determine user config path: %v\n", err)
	} else {
		if _, err := os.Stat(userConfigPath); !os.IsNotExist(err) {
			userConfig, err := loadConfigFromFile(userConfigPath)
			if err != nil {
				return MusterConfig{}, fmt.Errorf("error loading user config from %s: %w", userConfigPath, err)
			}
			config = mergeConfigs(config, userConfig)
		}
	}

	// 3. Determine project-specific configuration path
	projectConfigPath, err := getProjectConfigPath()
	if err != nil {
		// Log this error but don't fail; project config is optional
		fmt.Fprintf(os.Stderr, "Warning: Could not determine project config path: %v\n", err)
	} else {
		if _, err := os.Stat(projectConfigPath); !os.IsNotExist(err) {
			projectConfig, err := loadConfigFromFile(projectConfigPath)
			if err != nil {
				return MusterConfig{}, fmt.Errorf("error loading project config from %s: %w", projectConfigPath, err)
			}
			config = mergeConfigs(config, projectConfig)
		}
	}

	return config, nil
}

// LoadConfigFromPath loads configuration from a single specified directory.
// This bypasses the layered configuration system and loads everything from the given path.
// The directory should contain config.yaml and subdirectories for other configuration types.
func LoadConfigFromPath(configPath string) (MusterConfig, error) {
	// Validate that the directory exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return MusterConfig{}, fmt.Errorf("configuration directory does not exist: %s", configPath)
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
	}

	return config, nil
}

// NewConfigurationLoader creates a new configuration loader
func NewConfigurationLoader() (*ConfigurationLoader, error) {
	userDir, err := GetUserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to determine user config directory: %w", err)
	}

	projectDir, err := getProjectConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to determine project config directory: %w", err)
	}

	return &ConfigurationLoader{
		userConfigDir:    userDir,
		projectConfigDir: projectDir,
	}, nil
}

// NewConfigurationLoaderFromPath creates a new configuration loader for a single directory.
// This disables layered loading and loads all configuration from the specified path.
func NewConfigurationLoaderFromPath(configPath string) (*ConfigurationLoader, error) {
	// Validate that the directory exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("configuration directory does not exist: %s", configPath)
	}

	return &ConfigurationLoader{
		userConfigDir:    "",         // Disable user config
		projectConfigDir: configPath, // Use specified path as project config
	}, nil
}

// LoadYAMLFiles loads YAML files from both user and project directories with layered override.
// Project files override user files with the same base name.
// Returns slice of file paths in order: user files first, then project files (for override behavior)
func (cl *ConfigurationLoader) LoadYAMLFiles(subDir string) ([]LoadedFile, error) {
	var allFiles []LoadedFile
	nameMap := make(map[string]bool) // Track file names for override detection

	// 1. Load from user directory first
	userPath := filepath.Join(cl.userConfigDir, subDir)
	userFiles, err := cl.loadFilesFromDirectory(userPath, "user")
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load user %s: %w", subDir, err)
	}

	// Add user files to result
	for _, file := range userFiles {
		allFiles = append(allFiles, file)
		nameMap[file.Name] = true
	}

	// 2. Load from project directory (overrides user)
	projectPath := filepath.Join(cl.projectConfigDir, subDir)
	projectFiles, err := cl.loadFilesFromDirectory(projectPath, "project")
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to load project %s: %w", subDir, err)
	}

	// Handle project overrides
	for _, projectFile := range projectFiles {
		if nameMap[projectFile.Name] {
			// Remove user file with same name
			for i, userFile := range allFiles {
				if userFile.Name == projectFile.Name && userFile.Source == "user" {
					allFiles = append(allFiles[:i], allFiles[i+1:]...)
					logging.Info("ConfigurationLoader", "Project %s overriding user %s in %s", projectFile.Name, userFile.Name, subDir)
					break
				}
			}
		}
		allFiles = append(allFiles, projectFile)
		nameMap[projectFile.Name] = true
	}

	logging.Info("ConfigurationLoader", "Loaded %d files from %s (%d user, %d project)",
		len(allFiles), subDir, len(userFiles), len(projectFiles))

	return allFiles, nil
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

	var results []T
	errorCollection := NewConfigurationErrorCollection()

	for _, file := range files {
		var item T

		// Read file
		data, err := os.ReadFile(file.Path)
		if err != nil {
			suggestions := []string{
				"Check file permissions",
				"Verify the file exists and is readable",
			}
			configError := NewConfigurationErrorWithDetails(
				file.Path, file.Name, file.Source, subDir, "io",
				fmt.Sprintf("Failed to read file: %v", err),
				"File system error occurred while reading configuration file",
				suggestions,
			)
			errorCollection.Add(configError)
			continue
		}

		// Parse YAML
		if err := yaml.Unmarshal(data, &item); err != nil {
			suggestions := []string{
				"Check YAML syntax and indentation",
				"Verify all strings are properly quoted",
				"Ensure no tabs are used (use spaces for indentation)",
				"Validate YAML structure with a YAML validator",
			}

			details := fmt.Sprintf("YAML parsing failed: %v", err)
			if yamlErr, ok := err.(*yaml.TypeError); ok {
				details = fmt.Sprintf("YAML type error: %s", strings.Join(yamlErr.Errors, ", "))
			}

			configError := NewConfigurationErrorWithDetails(
				file.Path, file.Name, file.Source, subDir, "parse",
				"Invalid YAML format",
				details,
				suggestions,
			)
			errorCollection.Add(configError)
			continue
		}

		// Validate if validator provided
		if validator != nil {
			if err := validator(item); err != nil {
				suggestions := getValidationSuggestions(subDir, err)
				configError := NewConfigurationErrorWithDetails(
					file.Path, file.Name, file.Source, subDir, "validation",
					fmt.Sprintf("Validation failed: %v", err),
					"Configuration content does not meet requirements",
					suggestions,
				)
				errorCollection.Add(configError)
				continue
			}
		}

		results = append(results, item)
		logging.Info("ConfigurationLoader", "Successfully loaded %s configuration: %s from %s", file.Source, file.Name, subDir)
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

	var results []T
	errorCollection := NewConfigurationErrorCollection()

	for _, file := range files {
		var item T

		// Read file
		data, err := os.ReadFile(file.Path)
		if err != nil {
			suggestions := []string{
				"Check file permissions",
				"Verify the file exists and is readable",
			}
			configError := NewConfigurationErrorWithDetails(
				file.Path, file.Name, file.Source, subDir, "io",
				fmt.Sprintf("Failed to read file: %v", err),
				"File system error occurred while reading configuration file",
				suggestions,
			)
			errorCollection.Add(configError)
			continue
		}

		// Parse YAML
		if err := yaml.Unmarshal(data, &item); err != nil {
			suggestions := []string{
				"Check YAML syntax and indentation",
				"Verify all strings are properly quoted",
				"Ensure no tabs are used (use spaces for indentation)",
				"Validate YAML structure with a YAML validator",
			}

			details := fmt.Sprintf("YAML parsing failed: %v", err)
			if yamlErr, ok := err.(*yaml.TypeError); ok {
				details = fmt.Sprintf("YAML type error: %s", strings.Join(yamlErr.Errors, ", "))
			}

			configError := NewConfigurationErrorWithDetails(
				file.Path, file.Name, file.Source, subDir, "parse",
				"Invalid YAML format",
				details,
				suggestions,
			)
			errorCollection.Add(configError)
			continue
		}

		// Validate if validator provided
		if validator != nil {
			if err := validator(item); err != nil {
				suggestions := getValidationSuggestions(subDir, err)
				configError := NewConfigurationErrorWithDetails(
					file.Path, file.Name, file.Source, subDir, "validation",
					fmt.Sprintf("Validation failed: %v", err),
					"Configuration content does not meet requirements",
					suggestions,
				)
				errorCollection.Add(configError)
				continue
			}
		}

		results = append(results, item)
		logging.Info("ConfigurationLoader", "Successfully loaded %s configuration: %s from %s", file.Source, file.Name, subDir)
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

// LoadAndParseYAMLWithConfig is a utility that chooses between layered and single-directory loading
// based on whether a custom config path is provided. If configPath is empty, uses layered loading.
func LoadAndParseYAMLWithConfig[T any](configPath, subDir string, validator func(T) error) ([]T, *ConfigurationErrorCollection, error) {
	if configPath != "" {
		return LoadAndParseYAMLFromPath[T](configPath, subDir, validator)
	}
	return LoadAndParseYAML[T](subDir, validator)
}

// getValidationSuggestions returns context-aware suggestions based on the configuration type and error
func getValidationSuggestions(subDir string, validationErr error) []string {
	errorMsg := strings.ToLower(validationErr.Error())
	var suggestions []string

	// Common suggestions based on configuration type
	switch subDir {
	case "serviceclasses":
		suggestions = append(suggestions, []string{
			"Ensure 'name', 'type', and 'version' fields are provided",
			"Verify serviceConfig.lifecycleTools.start.tool is specified",
			"Check that serviceConfig.lifecycleTools.stop.tool is specified",
			"Review serviceclass examples in .muster/serviceclasses/",
		}...)
	case "capabilities":
		suggestions = append(suggestions, []string{
			"Ensure 'name' and 'type' fields are provided",
			"Add at least one operation to the operations map",
			"Verify all operations have valid tool requirements",
			"Review capability examples in .muster/capabilities/",
		}...)
	case "workflows":
		suggestions = append(suggestions, []string{
			"Ensure 'name' field is provided",
			"Add at least one step to the steps array",
			"Verify each step has 'id' and 'tool' fields",
			"Check inputSchema format if present",
			"Review workflow examples in .muster/workflows/",
		}...)
	}

	// Error-specific suggestions
	if strings.Contains(errorMsg, "name") && strings.Contains(errorMsg, "empty") {
		suggestions = append(suggestions, "Set a unique, descriptive name for this configuration")
	}
	if strings.Contains(errorMsg, "type") && strings.Contains(errorMsg, "empty") {
		suggestions = append(suggestions, "Specify the configuration type (must not be empty)")
	}
	if strings.Contains(errorMsg, "version") && strings.Contains(errorMsg, "empty") {
		suggestions = append(suggestions, "Add a version string (e.g., '1.0.0')")
	}
	if strings.Contains(errorMsg, "tool") && strings.Contains(errorMsg, "required") {
		suggestions = append(suggestions, "Specify the MCP tool name to execute")
	}
	if strings.Contains(errorMsg, "step") && strings.Contains(errorMsg, "least one") {
		suggestions = append(suggestions, "Add workflow steps with tool calls")
	}
	if strings.Contains(errorMsg, "operation") && strings.Contains(errorMsg, "least one") {
		suggestions = append(suggestions, "Define operations that this capability provides")
	}

	// Add generic suggestions if none were added
	if len(suggestions) == 0 {
		suggestions = []string{
			"Check the configuration file format and required fields",
			"Review documentation for this configuration type",
			"Compare with working examples in .muster/",
		}
	}

	return suggestions
}

// Path helper functions

var getUserConfigPath = func() (string, error) {
	homeDir, err := osUserHomeDir() // Use mockable variable
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, userConfigDir, configFileName), nil
}

var getProjectConfigPath = func() (string, error) {
	wd, err := osGetwd() // Use mockable variable
	if err != nil {
		return "", err
	}
	return filepath.Join(wd, projectConfigDir, configFileName), nil
}

// GetUserConfigDir returns the user configuration directory path
func GetUserConfigDir() (string, error) {
	homeDir, err := osUserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, userConfigDir), nil
}

// getProjectConfigDir returns the project configuration directory path
func getProjectConfigDir() (string, error) {
	wd, err := osGetwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(wd, projectConfigDir), nil
}

// GetConfigurationPaths returns both user and project configuration directory paths
func GetConfigurationPaths() (userDir, projectDir string, err error) {
	userDir, err = GetUserConfigDir()
	if err != nil {
		return "", "", fmt.Errorf("failed to determine user config directory: %w", err)
	}

	projectDir, err = getProjectConfigDir()
	if err != nil {
		return "", "", fmt.Errorf("failed to determine project config directory: %w", err)
	}

	return userDir, projectDir, nil
}

// Config file loading functions

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

	// Merge GlobalSettings (overlay overrides base)
	if overlay.GlobalSettings.DefaultContainerRuntime != "" {
		mergedConfig.GlobalSettings.DefaultContainerRuntime = overlay.GlobalSettings.DefaultContainerRuntime
	}
	// Add merging for other GlobalSettings fields here if any

	// Note: MCPServers are no longer merged here - they are loaded from mcpservers/ directories

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
