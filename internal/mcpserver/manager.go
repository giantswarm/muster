package mcpserver

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

// MCPServerManager manages MCP server definitions
type MCPServerManager struct {
	mu          sync.RWMutex
	loader      *config.ConfigurationLoader
	definitions map[string]*api.MCPServer // server name -> definition
	storage     *config.Storage
	configPath  string // Optional custom config path
}

// NewMCPServerManager creates a new MCP server manager
func NewMCPServerManager(storage *config.Storage) (*MCPServerManager, error) {
	loader, err := config.NewConfigurationLoader()
	if err != nil {
		return nil, fmt.Errorf("failed to create configuration loader: %w", err)
	}

	if storage == nil {
		return nil, fmt.Errorf("storage is required")
	}

	// Extract config path from storage if it has one
	var configPath string
	if storage != nil {
		// We can't directly access the configPath from storage, so we'll pass it via parameter later
		// For now, leave it empty
	}

	return &MCPServerManager{
		loader:      loader,
		definitions: make(map[string]*api.MCPServer),
		storage:     storage,
		configPath:  configPath,
	}, nil
}

// SetConfigPath sets the custom configuration path
func (msm *MCPServerManager) SetConfigPath(configPath string) {
	msm.mu.Lock()
	defer msm.mu.Unlock()
	msm.configPath = configPath
}

// LoadDefinitions loads all MCP server definitions from YAML files.
// All MCP servers are just YAML files, regardless of how they were created.
func (msm *MCPServerManager) LoadDefinitions() error {
	// Load all MCP server YAML files using the config path-aware helper
	validator := func(def api.MCPServer) error {
		return msm.validateDefinition(&def)
	}

	definitions, errorCollection, err := config.LoadAndParseYAMLWithConfig(msm.configPath, "mcpservers", validator)
	if err != nil {
		logging.Warn("MCPServerManager", "Error loading MCP servers: %v", err)
		return err
	}

	// Log any validation errors but continue with valid definitions
	if errorCollection != nil && errorCollection.HasErrors() {
		logging.Warn("MCPServerManager", "Some MCP server files had errors:\n%s", errorCollection.GetSummary())
	}

	// Acquire lock to update in-memory state
	msm.mu.Lock()
	defer msm.mu.Unlock()

	// Clear the old definitions
	msm.definitions = make(map[string]*api.MCPServer)

	// Add all valid definitions to in-memory store
	for i := range definitions {
		def := definitions[i] // Important: take a copy
		msm.definitions[def.Name] = &def
	}

	logging.Info("MCPServerManager", "Loaded %d MCP servers from YAML files", len(definitions))
	return nil
}

// validateDefinition performs comprehensive validation on an MCP server definition
func (msm *MCPServerManager) validateDefinition(def *api.MCPServer) error {
	var errors config.ValidationErrors

	// Validate entity name using common helper
	if err := config.ValidateEntityName(def.Name, "mcpserver"); err != nil {
		errors = append(errors, err.(config.ValidationError))
	}

	// Validate type
	if err := config.ValidateRequired("type", string(def.Type), "mcpserver"); err != nil {
		errors = append(errors, err.(config.ValidationError))
	} else {
		// Validate specific type
		validTypes := []string{string(api.MCPServerTypeLocalCommand), string(api.MCPServerTypeContainer)}
		if err := config.ValidateOneOf("type", string(def.Type), validTypes); err != nil {
			errors = append(errors, err.(config.ValidationError))
		}
	}

	// Validate type-specific requirements
	switch def.Type {
	case api.MCPServerTypeLocalCommand:
		if len(def.Command) == 0 {
			errors.Add("command", "is required for localCommand type")
		}
		if def.Image != "" {
			errors.Add("image", "cannot be specified for localCommand type")
		}
	case api.MCPServerTypeContainer:
		if def.Image == "" {
			errors.Add("image", "is required for container type")
		}
		if len(def.Command) > 0 {
			errors.Add("command", "cannot be specified for container type (use entrypoint instead)")
		}
	}

	// Note: Category and Icon validation removed in Phase 3

	if errors.HasErrors() {
		return config.FormatValidationError("mcpserver", def.Name, errors)
	}

	return nil
}

// ValidateDefinition validates an MCP server definition without persisting it
func (msm *MCPServerManager) ValidateDefinition(def *api.MCPServer) error {
	return msm.validateDefinition(def)
}

// GetDefinition returns an MCP server definition by name
func (msm *MCPServerManager) GetDefinition(name string) (api.MCPServer, bool) {
	msm.mu.RLock()
	defer msm.mu.RUnlock()

	def, exists := msm.definitions[name]
	if !exists {
		return api.MCPServer{}, false
	}
	return *def, true
}

// ListDefinitions returns all MCP server definitions
func (msm *MCPServerManager) ListDefinitions() []api.MCPServer {
	msm.mu.RLock()
	defer msm.mu.RUnlock()

	result := make([]api.MCPServer, 0, len(msm.definitions))
	for _, def := range msm.definitions {
		result = append(result, *def)
	}
	return result
}

// ListAvailableDefinitions returns all MCP server definitions
func (msm *MCPServerManager) ListAvailableDefinitions() []api.MCPServer {
	// All definitions are available since we don't need availability checking anymore
	return msm.ListDefinitions()
}

// GetDefinitionsPath returns the paths where MCP server definitions are loaded from
func (msm *MCPServerManager) GetDefinitionsPath() string {
	userDir, projectDir, err := config.GetConfigurationPaths()
	if err != nil {
		logging.Error("MCPServerManager", err, "Failed to get configuration paths")
		return "error determining paths"
	}

	userPath := filepath.Join(userDir, "mcpservers")
	projectPath := filepath.Join(projectDir, "mcpservers")

	return fmt.Sprintf("User: %s, Project: %s", userPath, projectPath)
}

// GetAllDefinitions returns all MCP server definitions (for internal use)
func (msm *MCPServerManager) GetAllDefinitions() map[string]*api.MCPServer {
	msm.mu.RLock()
	defer msm.mu.RUnlock()

	// Return a copy to prevent external modifications
	result := make(map[string]*api.MCPServer)
	for name, def := range msm.definitions {
		result[name] = def
	}
	return result
}

// UpdateDefinition updates an existing MCP server definition
func (msm *MCPServerManager) UpdateDefinition(name string, def api.MCPServer) error {
	msm.mu.Lock()
	defer msm.mu.Unlock()

	if err := msm.validateDefinition(&def); err != nil {
		return fmt.Errorf("invalid MCP server definition: %w", err)
	}

	existingDef, exists := msm.definitions[name]
	if !exists {
		return fmt.Errorf("MCP server definition %s does not exist", name)
	}

	*existingDef = def
	return nil
}

// CreateMCPServer creates and persists a new MCP server
func (msm *MCPServerManager) CreateMCPServer(def api.MCPServer) error {
	msm.mu.Lock()
	defer msm.mu.Unlock()

	if _, exists := msm.definitions[def.Name]; exists {
		return fmt.Errorf("MCP server '%s' already exists", def.Name)
	}

	if err := msm.validateDefinition(&def); err != nil {
		return fmt.Errorf("invalid MCP server definition: %w", err)
	}

	data, err := yaml.Marshal(def)
	if err != nil {
		return fmt.Errorf("failed to marshal MCP server %s: %w", def.Name, err)
	}

	if err := msm.storage.Save("mcpservers", def.Name, data); err != nil {
		return fmt.Errorf("failed to save MCP server %s: %w", def.Name, err)
	}

	// Add to in-memory store after successful save
	msm.definitions[def.Name] = &def

	logging.Info("MCPServerManager", "Created MCP server %s (type: %s)", def.Name, def.Type)
	return nil
}

// UpdateMCPServer updates and persists an existing MCP server
func (msm *MCPServerManager) UpdateMCPServer(name string, def api.MCPServer) error {
	msm.mu.Lock()
	defer msm.mu.Unlock()

	if _, exists := msm.definitions[name]; !exists {
		return api.NewMCPServerNotFoundError(name)
	}
	def.Name = name

	if err := msm.validateDefinition(&def); err != nil {
		return fmt.Errorf("invalid MCP server definition: %w", err)
	}

	data, err := yaml.Marshal(def)
	if err != nil {
		return fmt.Errorf("failed to marshal MCP server %s: %w", name, err)
	}

	if err := msm.storage.Save("mcpservers", name, data); err != nil {
		return fmt.Errorf("failed to save MCP server %s: %w", name, err)
	}

	// Update in-memory store after successful save
	msm.definitions[name] = &def

	logging.Info("MCPServerManager", "Updated MCP server %s (type: %s)", name, def.Type)
	return nil
}

// DeleteMCPServer deletes an MCP server from YAML files and memory
func (msm *MCPServerManager) DeleteMCPServer(name string) error {
	msm.mu.Lock()
	defer msm.mu.Unlock()

	if _, exists := msm.definitions[name]; !exists {
		return api.NewMCPServerNotFoundError(name)
	}

	if err := msm.storage.Delete("mcpservers", name); err != nil {
		// If it doesn't exist in storage, but exists in memory (from file), that's ok.
		// We just need to remove it from memory.
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to delete MCP server %s from YAML files: %w", name, err)
		}
	}

	// Remove from in-memory store after successful deletion
	delete(msm.definitions, name)

	logging.Info("MCPServerManager", "Deleted MCP server %s", name)
	return nil
}
