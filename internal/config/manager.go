package config

import (
	"fmt"
	"path/filepath"
	"sync"
)

// DefinitionManager defines the common interface for all definition managers (ServiceClass, Capability, Workflow)
// This ensures NO DIFFERENCE in management patterns across packages
type DefinitionManager[T any] interface {
	// Loading and initialization
	LoadDefinitions() error

	// Definition retrieval
	GetDefinition(name string) (T, bool)
	ListDefinitions() []T
	ListAvailableDefinitions() []T

	// Availability management
	IsAvailable(name string) bool
	RefreshAvailability()

	// Information retrieval
	GetDefinitionsPath() string
}

// ToolAvailabilityChecker is the common interface for checking tool availability
// All managers that deal with tools should use this interface
type ToolAvailabilityChecker interface {
	IsToolAvailable(toolName string) bool
	GetAvailableTools() []string
}

// ManagerConfig provides common configuration for all managers
type ManagerConfig struct {
	ToolChecker ToolAvailabilityChecker
	ConfigDir   string // For legacy support and agent workflows
}

// CommonManager provides shared functionality for all definition managers
type CommonManager[T any] struct {
	mu             sync.RWMutex
	loader         *ConfigurationLoader
	definitions    map[string]*T
	availableItems map[string]bool
	config         ManagerConfig
	subDirectory   string // The subdirectory name (e.g., "serviceclasses", "capabilities", "workflows")
}

// NewCommonManager creates a new common manager base
func NewCommonManager[T any](subDirectory string, config ManagerConfig) (*CommonManager[T], error) {
	loader, err := NewConfigurationLoader()
	if err != nil {
		return nil, fmt.Errorf("failed to create configuration loader: %w", err)
	}

	return &CommonManager[T]{
		loader:         loader,
		definitions:    make(map[string]*T),
		availableItems: make(map[string]bool),
		config:         config,
		subDirectory:   subDirectory,
	}, nil
}

// GetDefinitionsPath returns the paths where definitions are loaded from
func (cm *CommonManager[T]) GetDefinitionsPath() string {
	userDir, projectDir, err := GetConfigurationPaths()
	if err != nil {
		return "error determining paths"
	}

	userPath := filepath.Join(userDir, cm.subDirectory)
	projectPath := filepath.Join(projectDir, cm.subDirectory)

	return fmt.Sprintf("User: %s, Project: %s", userPath, projectPath)
}

// GetDefinition returns a definition by name
func (cm *CommonManager[T]) GetDefinition(name string) (T, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	var zero T
	def, exists := cm.definitions[name]
	if !exists {
		return zero, false
	}
	return *def, true
}

// IsAvailable checks if an item is available
func (cm *CommonManager[T]) IsAvailable(name string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	return cm.availableItems[name]
}
