package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/giantswarm/muster/pkg/logging"
)

// Storage provides generic storage functionality for dynamic entities
// using a single configuration directory approach
type Storage struct {
	mu         sync.RWMutex
	configPath string
}

// NewStorageWithPath creates a new Storage instance with a custom config path
func NewStorageWithPath(configPath string) *Storage {
	if configPath == "" {
		panic("Logic error: empty storage configPath")
	}

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
	targetDir := filepath.Join(ds.configPath, entityType)

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

	// Load from the single configuration directory
	filePath := filepath.Join(ds.configPath, entityType, ds.sanitizeFilename(name)+".yaml")
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

	// Delete from the single configuration directory
	filename := ds.sanitizeFilename(name) + ".yaml"
	filePath := filepath.Join(ds.configPath, entityType, filename)

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

	// List from the single configuration directory
	entityPath := filepath.Join(ds.configPath, entityType)
	names, err := ds.listFilesInDirectory(entityPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("failed to list %s: %w", entityType, err)
	}

	logging.Info("Storage", "Listed %d %s entities", len(names), entityType)
	return names, nil
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
