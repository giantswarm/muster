package capability

import (
	"fmt"
	"sync"

	"muster/internal/api"
	"muster/internal/config"
	"muster/pkg/logging"

	"gopkg.in/yaml.v3"
)

// CapabilityManager manages capability definitions and their availability
type CapabilityManager struct {
	mu           sync.RWMutex
	loader       *config.ConfigurationLoader
	definitions  map[string]*api.Capability // capability name -> definition
	toolChecker  *api.ToolChecker
	exposedTools map[string]bool // Track which capability tools we've exposed
	storage      *config.Storage
	configPath   string // Optional custom config path
}

// NewCapabilityManager creates a new capability manager
func NewCapabilityManager(storage *config.Storage, toolChecker *api.ToolChecker) (*CapabilityManager, error) {
	loader, err := config.NewConfigurationLoader()
	if err != nil {
		return nil, fmt.Errorf("failed to create configuration loader: %w", err)
	}

	// Extract config path from storage if it has one
	var configPath string
	if storage != nil {
		// We can't directly access the configPath from storage, so we'll pass it via arg later
		// For now, leave it empty
	}

	manager := &CapabilityManager{
		loader:       loader,
		definitions:  make(map[string]*api.Capability),
		toolChecker:  toolChecker,
		exposedTools: make(map[string]bool),
		storage:      storage,
		configPath:   configPath,
	}

	// Subscribe to tool update events for auto-refresh
	api.SubscribeToToolUpdates(manager)
	logging.Debug("CapabilityManager", "Subscribed to tool update events")

	return manager, nil
}

// SetConfigPath sets the custom configuration path
func (cm *CapabilityManager) SetConfigPath(configPath string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.configPath = configPath
}

// LoadDefinitions loads all capability definitions using the unified configuration loading.
// All capabilities are just YAML files, regardless of how they were created.
func (cm *CapabilityManager) LoadDefinitions() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Clear existing definitions
	cm.definitions = make(map[string]*api.Capability)
	cm.exposedTools = make(map[string]bool)

	// Load all capability YAML files using the config path-aware helper
	definitions, errorCollection, err := config.LoadAndParseYAMLWithConfig(cm.configPath, "capabilities", func(def api.Capability) error {
		return cm.validateDefinition(&def)
	})
	if err != nil {
		return fmt.Errorf("failed to load capability definitions: %w", err)
	}

	// Log any validation errors but continue with valid definitions
	if errorCollection.HasErrors() {
		logging.Warn("CapabilityManager", "Some capability files had errors:\n%s", errorCollection.GetSummary())
	}

	// Add all valid definitions to in-memory store
	for i := range definitions {
		def := definitions[i] // Important: take a copy
		cm.definitions[def.Name] = &def
		logging.Info("CapabilityManager", "Loaded capability definition: %s (type: %s)", def.Name, def.Type)
	}

	// Check which capabilities can be exposed
	cm.updateAvailableCapabilities()

	logging.Info("CapabilityManager", "Loaded %d capability definitions from YAML files", len(definitions))
	return nil
}

// validateDefinition validates a capability definition with comprehensive checks
func (cm *CapabilityManager) validateDefinition(def *api.Capability) error {
	var errors config.ValidationErrors

	// Validate entity name using common helper
	if err := config.ValidateEntityName(def.Name, "capability"); err != nil {
		errors = append(errors, err.(config.ValidationError))
	}

	// Validate type
	if err := config.ValidateRequired("type", def.Type, "capability"); err != nil {
		errors = append(errors, err.(config.ValidationError))
	}

	// Validate description (optional but recommended)
	if def.Description != "" {
		if err := config.ValidateMaxLength("description", def.Description, 500); err != nil {
			errors = append(errors, err.(config.ValidationError))
		}
	}

	// Validate operations
	if len(def.Operations) == 0 {
		errors.Add("operations", "must have at least one operation for capability")
	} else {
		// Validate each operation
		for opName, op := range def.Operations {
			if opName == "" {
				errors.Add("operations", "operation name cannot be empty")
				continue
			}

			// Validate operation description
			if op.Description == "" {
				errors.Add(fmt.Sprintf("operations.%s.description", opName), "is required for capability operation")
			} else if err := config.ValidateMaxLength(fmt.Sprintf("operations.%s.description", opName), op.Description, 300); err != nil {
				errors = append(errors, err.(config.ValidationError))
			}

			// Validate required tools
			for i, tool := range op.Requires {
				if tool == "" {
					errors.Add(fmt.Sprintf("operations.%s.requires[%d]", opName, i), "tool name cannot be empty")
				}
			}
		}
	}

	// Validate the capability type using existing logic
	if def.Type != "" && !api.IsValidCapabilityType(def.Type) {
		errors.Add("type", "is not a valid capability type")
	}

	if errors.HasErrors() {
		return config.FormatValidationError("capability", def.Name, errors)
	}

	return nil
}

// ValidateDefinition validates a capability definition without persisting it
func (cm *CapabilityManager) ValidateDefinition(def *api.Capability) error {
	return cm.validateDefinition(def)
}

// GetDefinition returns a capability definition by name
func (cm *CapabilityManager) GetDefinition(name string) (api.Capability, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	def, exists := cm.definitions[name]
	if !exists {
		return api.Capability{}, false
	}
	return *def, true
}

// ListDefinitions returns all capability definitions
func (cm *CapabilityManager) ListDefinitions() []api.Capability {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	result := make([]api.Capability, 0, len(cm.definitions))
	for _, def := range cm.definitions {
		result = append(result, *def)
	}
	return result
}

// ListAvailableDefinitions returns only capability definitions that have all required tools available
func (cm *CapabilityManager) ListAvailableDefinitions() []api.Capability {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	result := make([]api.Capability, 0)
	for _, def := range cm.definitions {
		if cm.isDefinitionAvailable(def) {
			result = append(result, *def)
		}
	}
	return result
}

// IsAvailable checks if a capability is available (has all required tools)
func (cm *CapabilityManager) IsAvailable(name string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	def, exists := cm.definitions[name]
	if !exists {
		return false
	}
	return cm.isDefinitionAvailable(def)
}

// isDefinitionAvailable checks if a capability definition has all required tools available
func (cm *CapabilityManager) isDefinitionAvailable(def *api.Capability) bool {
	for _, op := range def.Operations {
		if !cm.areRequiredToolsAvailable(op.Requires) {
			return false
		}
	}
	return true
}

// RefreshAvailability refreshes the availability status of all capabilities
func (cm *CapabilityManager) RefreshAvailability() {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.updateAvailableCapabilities()
}

// GetDefinitionsPath returns the path where capability definitions are loaded from
func (cm *CapabilityManager) GetDefinitionsPath() string {
	configDir, err := config.GetConfigurationPaths()
	if err != nil {
		logging.Error("CapabilityManager", err, "Failed to get configuration path")
		return "error determining path"
	}

	configPath := fmt.Sprintf("%s/capabilities", configDir)
	return fmt.Sprintf("Configuration: %s", configPath)
}

// updateAvailableCapabilities checks tool availability and updates exposed capabilities
func (cm *CapabilityManager) updateAvailableCapabilities() {
	for _, def := range cm.definitions {
		// Check each operation
		for opName, op := range def.Operations {
			if cm.areRequiredToolsAvailable(op.Requires) {
				// Mark this capability operation as available with api_ format
				toolName := fmt.Sprintf("api_%s_%s", def.Type, opName)
				if !cm.exposedTools[toolName] {
					cm.exposedTools[toolName] = true
					logging.Info("CapabilityManager", "Capability operation available: %s", toolName)
				}
			} else {
				// Mark as unavailable
				toolName := fmt.Sprintf("api_%s_%s", def.Type, opName)
				if cm.exposedTools[toolName] {
					delete(cm.exposedTools, toolName)
					logging.Info("CapabilityManager", "Capability operation unavailable: %s (missing tools)", toolName)
				}
			}
		}
	}
}

// areRequiredToolsAvailable checks if all required tools are available
func (cm *CapabilityManager) areRequiredToolsAvailable(requiredTools []string) bool {
	for _, tool := range requiredTools {
		if !cm.toolChecker.IsToolAvailable(tool) {
			return false
		}
	}
	return true
}

// GetAvailableCapabilityTools returns all capability tools that can be exposed
func (cm *CapabilityManager) GetAvailableCapabilityTools() []string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	tools := make([]string, 0, len(cm.exposedTools))
	for tool := range cm.exposedTools {
		tools = append(tools, tool)
	}
	return tools
}

// GetOperationForTool returns the operation definition for a capability tool
func (cm *CapabilityManager) GetOperationForTool(toolName string) (*api.OperationDefinition, *api.Capability, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// Tool names are in format api_<type>_<operation>
	// e.g., api_auth_login -> auth type, login operation

	for _, def := range cm.definitions {
		for opName, op := range def.Operations {
			// Check api_ format: api_<type>_<operation>
			expectedTool := fmt.Sprintf("api_%s_%s", def.Type, opName)
			if expectedTool == toolName {
				return &op, def, nil
			}
		}
	}

	return nil, nil, fmt.Errorf("no operation found for tool %s", toolName)
}

// CreateCapability creates a new capability definition
func (cm *CapabilityManager) CreateCapability(def *api.Capability) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if _, exists := cm.definitions[def.Name]; exists {
		return fmt.Errorf("capability '%s' already exists", def.Name)
	}

	// Validate the definition
	if err := cm.validateDefinition(def); err != nil {
		return fmt.Errorf("invalid capability definition: %w", err)
	}

	// Serialize to YAML
	data, err := yaml.Marshal(def)
	if err != nil {
		return fmt.Errorf("failed to marshal capability definition %s: %w", def.Name, err)
	}

	// Save to storage
	if err := cm.storage.Save("capabilities", def.Name, data); err != nil {
		return fmt.Errorf("failed to save capability definition %s: %w", def.Name, err)
	}

	// Add to in-memory store after successful save
	cm.definitions[def.Name] = def
	cm.updateAvailableCapabilities()

	logging.Info("CapabilityManager", "Created capability definition: %s (type: %s)", def.Name, def.Type)
	return nil
}

// UpdateCapability updates an existing capability definition
func (cm *CapabilityManager) UpdateCapability(def *api.Capability) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if _, exists := cm.definitions[def.Name]; !exists {
		return api.NewCapabilityNotFoundError(def.Name)
	}

	// Validate the definition
	if err := cm.validateDefinition(def); err != nil {
		return fmt.Errorf("invalid capability definition: %w", err)
	}

	// Serialize to YAML
	data, err := yaml.Marshal(def)
	if err != nil {
		return fmt.Errorf("failed to marshal capability definition %s: %w", def.Name, err)
	}

	// Save to storage
	if err := cm.storage.Save("capabilities", def.Name, data); err != nil {
		return fmt.Errorf("failed to save capability definition %s: %w", def.Name, err)
	}

	// Update in-memory store after successful save
	cm.definitions[def.Name] = def
	cm.updateAvailableCapabilities()

	logging.Info("CapabilityManager", "Updated capability definition: %s (type: %s)", def.Name, def.Type)
	return nil
}

// DeleteCapability deletes a capability definition
func (cm *CapabilityManager) DeleteCapability(name string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if _, exists := cm.definitions[name]; !exists {
		return api.NewCapabilityNotFoundError(name)
	}

	// Delete from YAML files
	if err := cm.storage.Delete("capabilities", name); err != nil {
		return fmt.Errorf("failed to delete capability definition %s: %w", name, err)
	}

	// Remove from in-memory store after successful deletion
	delete(cm.definitions, name)
	cm.updateAvailableCapabilities()

	logging.Info("CapabilityManager", "Deleted capability definition: %s", name)
	return nil
}

// UpdateDefinition updates a capability definition
func (cm *CapabilityManager) UpdateDefinition(def *api.Capability) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.definitions[def.Name] = def
	cm.updateAvailableCapabilities()
}

// OnToolsUpdated implements ToolUpdateSubscriber interface
func (cm *CapabilityManager) OnToolsUpdated(event api.ToolUpdateEvent) {
	logging.Debug("CapabilityManager", "Received tool update event: type=%s, server=%s, tools=%d",
		event.Type, event.ServerName, len(event.Tools))

	// Refresh Capability availability when tools are updated
	cm.RefreshAvailability()

	logging.Debug("CapabilityManager", "Refreshed Capability availability due to tool update")
}
