package context

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

const (
	// contextsFileName is the name of the contexts configuration file.
	contextsFileName = "contexts.yaml"
	// userConfigDir is the subdirectory under home for muster configuration.
	userConfigDir = ".config/muster"
)

// Storage provides thread-safe access to the contexts configuration file.
// It handles loading, saving, and manipulating the contexts.yaml file.
type Storage struct {
	mu         sync.RWMutex
	configPath string
}

// NewStorage creates a new Storage instance using the default config path.
// The default path is ~/.config/muster/contexts.yaml.
func NewStorage() (*Storage, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("failed to determine home directory: %w", err)
	}

	configPath := filepath.Join(homeDir, userConfigDir)
	return &Storage{
		configPath: configPath,
	}, nil
}

// NewStorageWithPath creates a new Storage instance with a custom config path.
// This is useful for testing or when using a non-default configuration directory.
func NewStorageWithPath(configPath string) *Storage {
	return &Storage{
		configPath: configPath,
	}
}

// getContextsFilePath returns the full path to the contexts.yaml file.
func (s *Storage) getContextsFilePath() string {
	return filepath.Join(s.configPath, contextsFileName)
}

// Load reads and parses the contexts configuration file.
// If the file doesn't exist, an empty ContextConfig is returned.
func (s *Storage) Load() (*ContextConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.loadLocked()
}

// loadLocked performs the actual load without acquiring locks.
// This is used internally when the caller already holds a lock.
func (s *Storage) loadLocked() (*ContextConfig, error) {
	filePath := s.getContextsFilePath()

	data, err := os.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Return empty config if file doesn't exist
			return &ContextConfig{}, nil
		}
		return nil, fmt.Errorf("failed to read contexts file: %w", err)
	}

	var config ContextConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse contexts file: %w", err)
	}

	return &config, nil
}

// Save writes the contexts configuration to the file.
// It creates the configuration directory if it doesn't exist.
func (s *Storage) Save(config *ContextConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.saveLocked(config)
}

// saveLocked performs the actual save without acquiring locks.
// This is used internally when the caller already holds a lock.
func (s *Storage) saveLocked(config *ContextConfig) error {
	// Ensure directory exists
	if err := os.MkdirAll(s.configPath, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal contexts config: %w", err)
	}

	filePath := s.getContextsFilePath()
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write contexts file: %w", err)
	}

	return nil
}

// GetCurrentContext returns the currently selected context.
// If no current context is set or the context doesn't exist, returns nil.
func (s *Storage) GetCurrentContext() (*Context, error) {
	config, err := s.Load()
	if err != nil {
		return nil, err
	}

	if config.CurrentContext == "" {
		return nil, nil
	}

	return config.GetContext(config.CurrentContext), nil
}

// GetCurrentContextName returns the name of the currently selected context.
// Returns an empty string if no context is selected.
func (s *Storage) GetCurrentContextName() (string, error) {
	config, err := s.Load()
	if err != nil {
		return "", err
	}

	return config.CurrentContext, nil
}

// SetCurrentContext sets the current context to the given name.
// Returns an error if the context doesn't exist.
func (s *Storage) SetCurrentContext(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	config, err := s.loadLocked()
	if err != nil {
		return err
	}

	if !config.HasContext(name) {
		return &ContextNotFoundError{Name: name}
	}

	config.CurrentContext = name
	return s.saveLocked(config)
}

// AddContext adds a new context with the given name and endpoint.
// Returns an error if a context with the same name already exists.
func (s *Storage) AddContext(name, endpoint string, settings *ContextSettings) error {
	if err := ValidateContextName(name); err != nil {
		return err
	}

	if endpoint == "" {
		return fmt.Errorf("endpoint cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	config, err := s.loadLocked()
	if err != nil {
		return err
	}

	if config.HasContext(name) {
		return fmt.Errorf("context %q already exists", name)
	}

	config.AddOrUpdateContext(Context{
		Name:     name,
		Endpoint: endpoint,
		Settings: settings,
	})

	return s.saveLocked(config)
}

// UpdateContext updates an existing context.
// Returns an error if the context doesn't exist.
func (s *Storage) UpdateContext(name, endpoint string, settings *ContextSettings) error {
	if err := ValidateContextName(name); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	config, err := s.loadLocked()
	if err != nil {
		return err
	}

	if !config.HasContext(name) {
		return &ContextNotFoundError{Name: name}
	}

	config.AddOrUpdateContext(Context{
		Name:     name,
		Endpoint: endpoint,
		Settings: settings,
	})

	return s.saveLocked(config)
}

// DeleteContext removes a context by name.
// Returns an error if the context doesn't exist.
func (s *Storage) DeleteContext(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	config, err := s.loadLocked()
	if err != nil {
		return err
	}

	if !config.RemoveContext(name) {
		return &ContextNotFoundError{Name: name}
	}

	return s.saveLocked(config)
}

// RenameContext renames a context from oldName to newName.
// Returns an error if the old context doesn't exist or the new name already exists.
func (s *Storage) RenameContext(oldName, newName string) error {
	if err := ValidateContextName(newName); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	config, err := s.loadLocked()
	if err != nil {
		return err
	}

	oldCtx := config.GetContext(oldName)
	if oldCtx == nil {
		return &ContextNotFoundError{Name: oldName}
	}

	if oldName != newName && config.HasContext(newName) {
		return fmt.Errorf("context %q already exists", newName)
	}

	// Check if we need to update current context before removing
	wasCurrentContext := config.CurrentContext == oldName

	// Create new context with the new name
	newCtx := Context{
		Name:     newName,
		Endpoint: oldCtx.Endpoint,
		Settings: oldCtx.Settings,
	}

	// Remove old and add new
	config.RemoveContext(oldName)
	config.AddOrUpdateContext(newCtx)

	// Update current context if it was the renamed one
	if wasCurrentContext {
		config.CurrentContext = newName
	}

	return s.saveLocked(config)
}

// ListContexts returns all defined contexts.
func (s *Storage) ListContexts() ([]Context, error) {
	config, err := s.Load()
	if err != nil {
		return nil, err
	}

	return config.Contexts, nil
}

// GetContext returns the context with the given name.
// Returns nil if the context doesn't exist.
func (s *Storage) GetContext(name string) (*Context, error) {
	config, err := s.Load()
	if err != nil {
		return nil, err
	}

	return config.GetContext(name), nil
}

// GetContextNames returns a list of all context names for shell completion.
func (s *Storage) GetContextNames() ([]string, error) {
	config, err := s.Load()
	if err != nil {
		return nil, err
	}

	names := make([]string, len(config.Contexts))
	for i, ctx := range config.Contexts {
		names[i] = ctx.Name
	}
	return names, nil
}
