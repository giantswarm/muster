package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"muster/pkg/logging"
)

// Storage provides generic storage functionality for dynamic entities
// with context-aware path resolution that prefers project over user paths
type Storage struct {
	mu         sync.RWMutex
	configPath string // Optional custom config path - when set, disables layered loading
}

// NewStorage creates a new Storage instance
func NewStorage() *Storage {
	return &Storage{}
}

// NewStorageWithPath creates a new Storage instance with a custom config path
func NewStorageWithPath(configPath string) *Storage {
	return &Storage{
		configPath: configPath,
	}
}

// Save stores data for the given entity type and name
// entityType: subdirectory name (workflows, serviceclasses, mcpservers, capabilities)
// name: filename without extension
// data: file content to write
func (ds *Storage) Save(entityType string, name string, data []byte) error {
	if entityType == "" {
		return fmt.Errorf("entityType cannot be empty")
	}
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}

	ds.mu.Lock()
	defer ds.mu.Unlock()

	// Resolve the target directory
	targetDir, err := ds.resolveEntityDir(entityType)
	if err != nil {
		return fmt.Errorf("failed to resolve directory for entity type %s: %w", entityType, err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", targetDir, err)
	}

	// Create file path with .yaml extension
	filename := ds.sanitizeFilename(name) + ".yaml"
	filePath := filepath.Join(targetDir, filename)

	// Write file
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write file %s: %w", filePath, err)
	}

	logging.Info("Storage", "Saved %s/%s to %s", entityType, name, filePath)
	return nil
}

// Load retrieves data for the given entity type and name
// Returns the file content, or an error if not found
func (ds *Storage) Load(entityType string, name string) ([]byte, error) {
	if entityType == "" {
		return nil, fmt.Errorf("entityType cannot be empty")
	}
	if name == "" {
		return nil, fmt.Errorf("name cannot be empty")
	}

	ds.mu.RLock()
	defer ds.mu.RUnlock()

	// Use custom config path if provided
	if ds.configPath != "" {
		filePath := filepath.Join(ds.configPath, entityType, ds.sanitizeFilename(name)+".yaml")
		data, err := os.ReadFile(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("entity %s/%s not found in custom path", entityType, name)
			}
			return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
		}
		logging.Info("Storage", "Loaded %s/%s from custom path: %s", entityType, name, filePath)
		return data, nil
	}

	// Try both user and project paths
	userDir, projectDir, err := GetConfigurationPaths()
	if err != nil {
		return nil, fmt.Errorf("failed to get configuration paths: %w", err)
	}

	// Check project path first (preferred)
	projectPath := filepath.Join(projectDir, entityType, ds.sanitizeFilename(name)+".yaml")
	if data, err := os.ReadFile(projectPath); err == nil {
		logging.Info("Storage", "Loaded %s/%s from project path: %s", entityType, name, projectPath)
		return data, nil
	}

	// Fallback to user path
	userPath := filepath.Join(userDir, entityType, ds.sanitizeFilename(name)+".yaml")
	data, err := os.ReadFile(userPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("entity %s/%s not found in user or project paths", entityType, name)
		}
		return nil, fmt.Errorf("failed to read file %s: %w", userPath, err)
	}

	logging.Info("Storage", "Loaded %s/%s from user path: %s", entityType, name, userPath)
	return data, nil
}

// Delete removes the file for the given entity type and name
func (ds *Storage) Delete(entityType string, name string) error {
	if entityType == "" {
		return fmt.Errorf("entityType cannot be empty")
	}
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}

	ds.mu.Lock()
	defer ds.mu.Unlock()

	// Use custom config path if provided
	if ds.configPath != "" {
		filename := ds.sanitizeFilename(name) + ".yaml"
		filePath := filepath.Join(ds.configPath, entityType, filename)

		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			return fmt.Errorf("entity %s/%s not found in custom path", entityType, name)
		}

		if err := os.Remove(filePath); err != nil {
			return fmt.Errorf("failed to delete file %s: %w", filePath, err)
		}

		logging.Info("Storage", "Deleted %s/%s from custom path: %s", entityType, name, filePath)
		return nil
	}

	// Try both user and project paths
	userDir, projectDir, err := GetConfigurationPaths()
	if err != nil {
		return fmt.Errorf("failed to get configuration paths: %w", err)
	}

	filename := ds.sanitizeFilename(name) + ".yaml"
	deleted := false

	// Try to delete from project path
	projectPath := filepath.Join(projectDir, entityType, filename)
	if _, err := os.Stat(projectPath); err == nil {
		if err := os.Remove(projectPath); err != nil {
			return fmt.Errorf("failed to delete file %s: %w", projectPath, err)
		}
		logging.Info("Storage", "Deleted %s/%s from project path: %s", entityType, name, projectPath)
		deleted = true
	}

	// Try to delete from user path
	userPath := filepath.Join(userDir, entityType, filename)
	if _, err := os.Stat(userPath); err == nil {
		if err := os.Remove(userPath); err != nil {
			return fmt.Errorf("failed to delete file %s: %w", userPath, err)
		}
		logging.Info("Storage", "Deleted %s/%s from user path: %s", entityType, name, userPath)
		deleted = true
	}

	if !deleted {
		return fmt.Errorf("entity %s/%s not found in user or project paths", entityType, name)
	}

	return nil
}

// List returns all available names for the given entity type
// Returns names from both user and project directories, with project overriding user
func (ds *Storage) List(entityType string) ([]string, error) {
	if entityType == "" {
		return nil, fmt.Errorf("entityType cannot be empty")
	}

	ds.mu.RLock()
	defer ds.mu.RUnlock()

	// Use custom config path if provided
	if ds.configPath != "" {
		customPath := filepath.Join(ds.configPath, entityType)
		names, err := ds.listFilesInDirectory(customPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to list custom %s: %w", entityType, err)
		}
		logging.Info("Storage", "Listed %d %s entities from custom path", len(names), entityType)
		return names, nil
	}

	userDir, projectDir, err := GetConfigurationPaths()
	if err != nil {
		return nil, fmt.Errorf("failed to get configuration paths: %w", err)
	}

	nameMap := make(map[string]bool)
	var names []string

	// Load from user directory first
	userPath := filepath.Join(userDir, entityType)
	userNames, err := ds.listFilesInDirectory(userPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to list user %s: %w", entityType, err)
	}

	for _, name := range userNames {
		names = append(names, name)
		nameMap[name] = true
	}

	// Load from project directory (overrides user)
	projectPath := filepath.Join(projectDir, entityType)
	projectNames, err := ds.listFilesInDirectory(projectPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to list project %s: %w", entityType, err)
	}

	// Add project names, they override user names with same name
	for _, name := range projectNames {
		if !nameMap[name] {
			names = append(names, name)
		}
		nameMap[name] = true
	}

	logging.Info("Storage", "Listed %d unique %s entities (%d user, %d project)",
		len(names), entityType, len(userNames), len(projectNames))

	return names, nil
}

// resolveEntityDir determines the target directory for saving based on context
// Prefers project directory if .muster exists in current directory
func (ds *Storage) resolveEntityDir(entityType string) (string, error) {
	// Use custom config path if provided
	if ds.configPath != "" {
		return filepath.Join(ds.configPath, entityType), nil
	}

	userDir, projectDir, err := GetConfigurationPaths()
	if err != nil {
		return "", err
	}

	// Check if project config directory exists (indicating we're in a project)
	// projectDir includes .muster, so check if .muster directory exists
	if _, err := os.Stat(projectDir); err == nil {
		// Use project directory
		return filepath.Join(projectDir, entityType), nil
	}

	// Fallback to user directory
	return filepath.Join(userDir, entityType), nil
}

// listFilesInDirectory lists all .yaml files in a directory and returns their base names
func (ds *Storage) listFilesInDirectory(dirPath string) ([]string, error) {
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return nil, nil // Directory doesn't exist, return empty
	}

	// Load .yaml files
	yamlPattern := filepath.Join(dirPath, "*.yaml")
	yamlFiles, err := filepath.Glob(yamlPattern)
	if err != nil {
		return nil, fmt.Errorf("failed to glob yaml files: %w", err)
	}

	// Load .yml files
	ymlPattern := filepath.Join(dirPath, "*.yml")
	ymlFiles, err := filepath.Glob(ymlPattern)
	if err != nil {
		return nil, fmt.Errorf("failed to glob yml files: %w", err)
	}

	// Combine and extract names
	var names []string
	allFiles := append(yamlFiles, ymlFiles...)
	for _, filePath := range allFiles {
		basename := filepath.Base(filePath)
		name := strings.TrimSuffix(basename, filepath.Ext(basename))
		names = append(names, name)
	}

	return names, nil
}

// sanitizeFilename ensures the filename is safe for filesystem operations
func (ds *Storage) sanitizeFilename(name string) string {
	// Replace problematic characters with underscores
	sanitized := strings.ReplaceAll(name, "/", "_")
	sanitized = strings.ReplaceAll(sanitized, "\\", "_")
	sanitized = strings.ReplaceAll(sanitized, ":", "_")
	sanitized = strings.ReplaceAll(sanitized, "*", "_")
	sanitized = strings.ReplaceAll(sanitized, "?", "_")
	sanitized = strings.ReplaceAll(sanitized, "\"", "_")
	sanitized = strings.ReplaceAll(sanitized, "<", "_")
	sanitized = strings.ReplaceAll(sanitized, ">", "_")
	sanitized = strings.ReplaceAll(sanitized, "|", "_")

	// Remove leading/trailing spaces and dots
	sanitized = strings.Trim(sanitized, " .")

	// Replace spaces with underscores
	sanitized = strings.ReplaceAll(sanitized, " ", "_")

	// Collapse multiple consecutive underscores to single underscore
	for strings.Contains(sanitized, "__") {
		sanitized = strings.ReplaceAll(sanitized, "__", "_")
	}

	// Remove leading/trailing underscores
	sanitized = strings.Trim(sanitized, "_")

	// Ensure name is not empty after sanitization
	if sanitized == "" {
		sanitized = "unnamed"
	}

	return sanitized
}
