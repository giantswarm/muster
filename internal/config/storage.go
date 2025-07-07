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
// using a single configuration directory approach
type Storage struct {
	mu         sync.RWMutex
	configPath string // Optional custom config path - when set, uses this path; otherwise uses default ~/.config/muster
}

// NewStorage creates a new Storage instance using the default configuration directory
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

	// Get the configuration directory
	configDir, err := ds.getConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get configuration directory: %w", err)
	}

	// Load from the single configuration directory
	filePath := filepath.Join(configDir, entityType, ds.sanitizeFilename(name)+".yaml")
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("entity %s/%s not found", entityType, name)
		}
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	logging.Info("Storage", "Loaded %s/%s from %s", entityType, name, filePath)
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

	// Get the configuration directory
	configDir, err := ds.getConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get configuration directory: %w", err)
	}

	// Delete from the single configuration directory
	filename := ds.sanitizeFilename(name) + ".yaml"
	filePath := filepath.Join(configDir, entityType, filename)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return fmt.Errorf("entity %s/%s not found", entityType, name)
	}

	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("failed to delete file %s: %w", filePath, err)
	}

	logging.Info("Storage", "Deleted %s/%s from %s", entityType, name, filePath)
	return nil
}

// List returns all available names for the given entity type
func (ds *Storage) List(entityType string) ([]string, error) {
	if entityType == "" {
		return nil, fmt.Errorf("entityType cannot be empty")
	}

	ds.mu.RLock()
	defer ds.mu.RUnlock()

	// Get the configuration directory
	configDir, err := ds.getConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get configuration directory: %w", err)
	}

	// List from the single configuration directory
	entityPath := filepath.Join(configDir, entityType)
	names, err := ds.listFilesInDirectory(entityPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to list %s: %w", entityType, err)
	}

	logging.Info("Storage", "Listed %d %s entities", len(names), entityType)
	return names, nil
}

// getConfigDir returns the configuration directory to use
func (ds *Storage) getConfigDir() (string, error) {
	if ds.configPath != "" {
		return ds.configPath, nil
	}

	return GetUserConfigDir()
}

// resolveEntityDir determines the target directory for saving
func (ds *Storage) resolveEntityDir(entityType string) (string, error) {
	configDir, err := ds.getConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(configDir, entityType), nil
}

// listFilesInDirectory lists all .yaml files in a directory and returns their base names
func (ds *Storage) listFilesInDirectory(dirPath string) ([]string, error) {
	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		return []string{}, nil // Directory doesn't exist, return empty slice
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
	sanitized = strings.ReplaceAll(sanitized, ".", "_")

	// Remove leading/trailing spaces and underscores
	sanitized = strings.Trim(sanitized, " _")

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
