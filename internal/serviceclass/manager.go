package serviceclass

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"muster/internal/api"
	"muster/internal/config"
	"muster/pkg/logging"

	"gopkg.in/yaml.v3"
)

// ServiceClassManager manages service class definitions and their availability
type ServiceClassManager struct {
	mu              sync.RWMutex
	loader          *config.ConfigurationLoader
	definitions     map[string]*api.ServiceClass // service class name -> definition
	toolChecker     config.ToolAvailabilityChecker
	exposedServices map[string]bool // Track which service classes are available
	storage         *config.Storage
	configPath      string // Optional custom config path

	// Callbacks for lifecycle events
	onUpdate []func(def *api.ServiceClass)
}

// NewServiceClassManager creates a new service class manager
func NewServiceClassManager(toolChecker config.ToolAvailabilityChecker, storage *config.Storage) (*ServiceClassManager, error) {
	if toolChecker == nil {
		return nil, fmt.Errorf("tool checker is required")
	}
	if storage == nil {
		return nil, fmt.Errorf("storage is required")
	}

	loader, err := config.NewConfigurationLoader()
	if err != nil {
		return nil, fmt.Errorf("failed to create configuration loader: %w", err)
	}

	// Extract config path from storage if it has one
	var configPath string
	if storage != nil {
		// We can't directly access the configPath from storage, so we'll pass it via parameter later
		// For now, leave it empty
	}

	manager := &ServiceClassManager{
		loader:          loader,
		definitions:     make(map[string]*api.ServiceClass),
		toolChecker:     toolChecker,
		exposedServices: make(map[string]bool),
		storage:         storage,
		configPath:      configPath,
		onUpdate:        []func(def *api.ServiceClass){},
	}

	// Subscribe to tool update events for auto-refresh
	api.SubscribeToToolUpdates(manager)
	logging.Debug("ServiceClassManager", "Subscribed to tool update events")

	return manager, nil
}

// SetConfigPath sets the custom configuration path
func (m *ServiceClassManager) SetConfigPath(configPath string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.configPath = configPath
}

// LoadDefinitions loads all service class definitions from YAML files.
// All service classes are just YAML files, regardless of how they were created.
func (m *ServiceClassManager) LoadDefinitions() error {
	// Load all service class YAML files using the config path-aware helper
	validator := func(def api.ServiceClass) error {
		return m.validateServiceClassDefinition(&def)
	}

	definitions, errorCollection, err := config.LoadAndParseYAMLWithConfig(m.configPath, "serviceclasses", validator)
	if err != nil {
		logging.Warn("ServiceClassManager", "Error loading service classes: %v", err)
		return err
	}

	// Log any validation errors but continue with valid definitions
	if errorCollection.HasErrors() {
		logging.Warn("ServiceClassManager", "Some service class files had errors:\n%s", errorCollection.GetSummary())
	}

	// Acquire lock to update in-memory state
	m.mu.Lock()
	defer m.mu.Unlock()

	// Clear the old definitions
	m.definitions = make(map[string]*api.ServiceClass)

	// Add all valid definitions to in-memory store
	for i := range definitions {
		def := definitions[i] // Important: take a copy
		m.definitions[def.Name] = &def
	}

	// Update availability
	m.updateServiceAvailability()

	logging.Info("ServiceClassManager", "Loaded %d service classes from YAML files", len(definitions))
	return nil
}

// CreateServiceClass creates and persists a new service class
func (m *ServiceClassManager) CreateServiceClass(sc api.ServiceClass) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.definitions[sc.Name]; exists {
		return fmt.Errorf("service class '%s' already exists", sc.Name)
	}

	// Validate before saving
	if err := m.validateServiceClassDefinition(&sc); err != nil {
		return fmt.Errorf("service class validation failed: %w", err)
	}

	data, err := yaml.Marshal(sc)
	if err != nil {
		return fmt.Errorf("failed to marshal service class %s: %w", sc.Name, err)
	}

	if err := m.storage.Save("serviceclasses", sc.Name, data); err != nil {
		return fmt.Errorf("failed to save service class %s: %w", sc.Name, err)
	}

	// Add to in-memory store after successful save
	m.definitions[sc.Name] = &sc
	m.updateServiceAvailability()

	logging.Info("ServiceClassManager", "Created service class %s", sc.Name)
	return nil
}

// UpdateServiceClass updates and persists an existing service class
func (m *ServiceClassManager) UpdateServiceClass(name string, sc api.ServiceClass) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.definitions[name]; !exists {
		return api.NewServiceClassNotFoundError(name)
	}
	sc.Name = name

	// Validate before saving
	if err := m.validateServiceClassDefinition(&sc); err != nil {
		return fmt.Errorf("service class validation failed: %w", err)
	}

	data, err := yaml.Marshal(sc)
	if err != nil {
		return fmt.Errorf("failed to marshal service class %s: %w", name, err)
	}

	if err := m.storage.Save("serviceclasses", name, data); err != nil {
		return fmt.Errorf("failed to save service class %s: %w", name, err)
	}

	// Update in-memory store after successful save
	m.definitions[name] = &sc
	m.updateServiceAvailability()

	logging.Info("ServiceClassManager", "Updated service class %s", name)
	return nil
}

// DeleteServiceClass deletes a service class from YAML files and memory
func (m *ServiceClassManager) DeleteServiceClass(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.definitions[name]; !exists {
		return api.NewServiceClassNotFoundError(name)
	}

	if err := m.storage.Delete("serviceclasses", name); err != nil {
		// If it doesn't exist in storage, but exists in memory (from file), that's ok.
		// We just need to remove it from memory.
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to delete service class %s from YAML files: %w", name, err)
		}
	}

	// Remove from in-memory store after successful deletion
	delete(m.definitions, name)
	m.updateServiceAvailability()

	logging.Info("ServiceClassManager", "Deleted service class %s", name)
	return nil
}

// OnUpdate adds a callback for service class updates
func (scm *ServiceClassManager) OnUpdate(callback func(def *api.ServiceClass)) {
	scm.mu.Lock()
	defer scm.mu.Unlock()
	scm.onUpdate = append(scm.onUpdate, callback)
}

// GetDefinitionsPath returns the paths where service class definitions are loaded from
func (scm *ServiceClassManager) GetDefinitionsPath() string {
	userDir, projectDir, err := config.GetConfigurationPaths()
	if err != nil {
		logging.Error("ServiceClassManager", err, "Failed to get configuration paths")
		return "error determining paths"
	}

	userPath := filepath.Join(userDir, "serviceclasses")
	projectPath := filepath.Join(projectDir, "serviceclasses")

	return fmt.Sprintf("User: %s, Project: %s", userPath, projectPath)
}

// GetAllDefinitions returns all service class definitions (for internal use)
func (scm *ServiceClassManager) GetAllDefinitions() map[string]*api.ServiceClass {
	scm.mu.RLock()
	defer scm.mu.RUnlock()

	// Return a copy to prevent external modifications
	result := make(map[string]*api.ServiceClass)
	for name, def := range scm.definitions {
		result[name] = def
	}
	return result
}

// validateServiceClassDefinition performs comprehensive validation on a service class definition
func (scm *ServiceClassManager) validateServiceClassDefinition(def *api.ServiceClass) error {
	var errors config.ValidationErrors

	// Validate entity name using common helper
	if err := config.ValidateEntityName(def.Name, "service class"); err != nil {
		errors = append(errors, err.(config.ValidationError))
	}

	// Note: Type field removed in Phase 3 - no longer required

	// Validate version
	if err := config.ValidateRequired("version", def.Version, "service class"); err != nil {
		errors = append(errors, err.(config.ValidationError))
	}

	// Validate description (optional but recommended)
	if def.Description != "" {
		if err := config.ValidateMaxLength("description", def.Description, 500); err != nil {
			errors = append(errors, err.(config.ValidationError))
		}
	}

	// Validate service type (allow any non-empty string for custom service types)
	if def.ServiceConfig.ServiceType != "" {
		if err := config.ValidateMinLength("serviceConfig.serviceType", def.ServiceConfig.ServiceType, 1); err != nil {
			errors = append(errors, err.(config.ValidationError))
		}
	}

	// Validate lifecycle tools
	if def.ServiceConfig.LifecycleTools.Start.Tool == "" {
		errors.Add("serviceConfig.lifecycleTools.start.tool", "is required for service class")
	}
	if def.ServiceConfig.LifecycleTools.Stop.Tool == "" {
		errors.Add("serviceConfig.lifecycleTools.stop.tool", "is required for service class")
	}

	// Validate optional lifecycle tools if present
	if def.ServiceConfig.LifecycleTools.Restart != nil && def.ServiceConfig.LifecycleTools.Restart.Tool == "" {
		errors.Add("serviceConfig.lifecycleTools.restart.tool", "cannot be empty if restart is specified")
	}
	if def.ServiceConfig.LifecycleTools.HealthCheck != nil && def.ServiceConfig.LifecycleTools.HealthCheck.Tool == "" {
		errors.Add("serviceConfig.lifecycleTools.healthCheck.tool", "cannot be empty if healthCheck is specified")
	}
	if def.ServiceConfig.LifecycleTools.Status != nil && def.ServiceConfig.LifecycleTools.Status.Tool == "" {
		errors.Add("serviceConfig.lifecycleTools.status.tool", "cannot be empty if status is specified")
	}

	// Validate operations if present (for compatibility)
	for opName, op := range def.Operations {
		if opName == "" {
			errors.Add("operations", "operation name cannot be empty")
			continue
		}

		// Validate operation description
		if op.Description == "" {
			errors.Add(fmt.Sprintf("operations.%s.description", opName), "is required for service class operation")
		}

		// Validate required tools
		for i, tool := range op.Requires {
			if tool == "" {
				errors.Add(fmt.Sprintf("operations.%s.requires[%d]", opName, i), "tool name cannot be empty")
			}
		}
	}

	if errors.HasErrors() {
		return config.FormatValidationError("service class", def.Name, errors)
	}

	return nil
}

// ValidateDefinition validates a serviceclass definition without persisting it
func (scm *ServiceClassManager) ValidateDefinition(def *api.ServiceClass) error {
	return scm.validateServiceClassDefinition(def)
}

// updateServiceAvailability checks tool availability and updates service class availability
func (scm *ServiceClassManager) updateServiceAvailability() {
	for name, def := range scm.definitions {
		requiredTools := scm.getRequiredTools(def)
		available := scm.areAllToolsAvailable(requiredTools)

		oldAvailable := scm.exposedServices[name]
		scm.exposedServices[name] = available

		if available && !oldAvailable {
			logging.Info("ServiceClassManager", "Service class available: %s", name)
		} else if !available && oldAvailable {
			logging.Warn("ServiceClassManager", "Service class unavailable: %s (missing tools)", name)
		}
	}
}

// getRequiredTools extracts all required tools from a service class definition
func (scm *ServiceClassManager) getRequiredTools(def *api.ServiceClass) []string {
	tools := make(map[string]bool)

	// Add lifecycle tools
	if def.ServiceConfig.LifecycleTools.Start.Tool != "" {
		tools[def.ServiceConfig.LifecycleTools.Start.Tool] = true
	}
	if def.ServiceConfig.LifecycleTools.Stop.Tool != "" {
		tools[def.ServiceConfig.LifecycleTools.Stop.Tool] = true
	}
	if def.ServiceConfig.LifecycleTools.Restart != nil && def.ServiceConfig.LifecycleTools.Restart.Tool != "" {
		tools[def.ServiceConfig.LifecycleTools.Restart.Tool] = true
	}
	if def.ServiceConfig.LifecycleTools.HealthCheck != nil && def.ServiceConfig.LifecycleTools.HealthCheck.Tool != "" {
		tools[def.ServiceConfig.LifecycleTools.HealthCheck.Tool] = true
	}
	if def.ServiceConfig.LifecycleTools.Status != nil && def.ServiceConfig.LifecycleTools.Status.Tool != "" {
		tools[def.ServiceConfig.LifecycleTools.Status.Tool] = true
	}

	// Add tools from operations (existing capability system compatibility)
	for _, op := range def.Operations {
		for _, tool := range op.Requires {
			tools[tool] = true
		}
	}

	// Convert to slice
	result := make([]string, 0, len(tools))
	for tool := range tools {
		result = append(result, tool)
	}

	return result
}

// areAllToolsAvailable checks if all required tools are available
func (scm *ServiceClassManager) areAllToolsAvailable(requiredTools []string) bool {
	if scm.toolChecker == nil {
		return false
	}

	for _, tool := range requiredTools {
		if !scm.toolChecker.IsToolAvailable(tool) {
			return false
		}
	}
	return true
}

// GetServiceClassDefinition returns a service class definition by name
func (scm *ServiceClassManager) GetServiceClassDefinition(name string) (*api.ServiceClass, bool) {
	scm.mu.RLock()
	defer scm.mu.RUnlock()

	def, exists := scm.definitions[name]
	return def, exists
}

// ListServiceClasses returns information about all service classes
func (scm *ServiceClassManager) ListServiceClasses() []api.ServiceClass {
	scm.mu.RLock()
	defer scm.mu.RUnlock()

	result := make([]api.ServiceClass, 0, len(scm.definitions))

	for _, def := range scm.definitions {
		requiredTools := scm.getRequiredTools(def)
		missingTools := scm.getMissingTools(requiredTools)
		available := len(missingTools) == 0

		info := api.ServiceClass{
			Name:                     def.Name,
			Version:                  def.Version,
			Description:              def.Description,
			ServiceConfig:            def.ServiceConfig,
			Operations:               def.Operations,
			ServiceType:              def.ServiceConfig.ServiceType,
			Available:                available,
			CreateToolAvailable:      scm.toolChecker != nil && scm.toolChecker.IsToolAvailable(def.ServiceConfig.LifecycleTools.Start.Tool),
			DeleteToolAvailable:      scm.toolChecker != nil && scm.toolChecker.IsToolAvailable(def.ServiceConfig.LifecycleTools.Stop.Tool),
			HealthCheckToolAvailable: def.ServiceConfig.LifecycleTools.HealthCheck != nil && scm.toolChecker != nil && scm.toolChecker.IsToolAvailable(def.ServiceConfig.LifecycleTools.HealthCheck.Tool),
			StatusToolAvailable:      def.ServiceConfig.LifecycleTools.Status != nil && scm.toolChecker != nil && scm.toolChecker.IsToolAvailable(def.ServiceConfig.LifecycleTools.Status.Tool),
			RequiredTools:            requiredTools,
			MissingTools:             missingTools,
		}

		result = append(result, info)
	}

	return result
}

// ListAvailableServiceClasses returns only service classes that have all required tools available
func (scm *ServiceClassManager) ListAvailableServiceClasses() []api.ServiceClass {
	all := scm.ListServiceClasses()
	result := make([]api.ServiceClass, 0, len(all))

	for _, info := range all {
		if info.Available {
			result = append(result, info)
		}
	}

	return result
}

// getMissingTools returns tools that are required but not available
func (scm *ServiceClassManager) getMissingTools(requiredTools []string) []string {
	if scm.toolChecker == nil {
		return requiredTools // All tools are missing if no checker
	}

	var missing []string
	for _, tool := range requiredTools {
		if !scm.toolChecker.IsToolAvailable(tool) {
			missing = append(missing, tool)
		}
	}
	return missing
}

// IsServiceClassAvailable checks if a service class is available
func (scm *ServiceClassManager) IsServiceClassAvailable(name string) bool {
	scm.mu.RLock()
	defer scm.mu.RUnlock()

	return scm.exposedServices[name]
}

// RefreshAvailability refreshes the availability status of all service classes
func (scm *ServiceClassManager) RefreshAvailability() {
	scm.mu.Lock()
	defer scm.mu.Unlock()

	scm.updateServiceAvailability()
}

// OnToolsUpdated implements ToolUpdateSubscriber interface
func (scm *ServiceClassManager) OnToolsUpdated(event api.ToolUpdateEvent) {
	logging.Debug("ServiceClassManager", "Received tool update event: type=%s, server=%s, tools=%d",
		event.Type, event.ServerName, len(event.Tools))

	// Refresh ServiceClass availability when tools are updated
	scm.RefreshAvailability()

	logging.Debug("ServiceClassManager", "Refreshed ServiceClass availability due to tool update")
}
