package services

import (
	"fmt"
	"time"

	"muster/internal/config"
	"muster/pkg/logging"

	"gopkg.in/yaml.v3"
)

// ServiceInstanceDefinition represents a service instance configuration that can be persisted to YAML
// This is a minimal structure focused only on what needs to be persisted
type ServiceInstanceDefinition struct {
	// Instance identification
	Name string `yaml:"name" json:"name"` // Unique instance name

	// Service class reference
	ServiceClassName string `yaml:"serviceClassName" json:"serviceClassName"` // Which service class to instantiate
	ServiceClassType string `yaml:"serviceClassType" json:"serviceClassType"` // Type of service class (informational)

	// Creation args
	Args map[string]interface{} `yaml:"args,omitempty" json:"args,omitempty"` // Args used to create the instance

	// Optional metadata
	Description string `yaml:"description,omitempty" json:"description,omitempty"` // Human-readable description
	Version     string `yaml:"version,omitempty" json:"version,omitempty"`         // Version of this instance definition

	// Lifecycle management
	AutoStart bool `yaml:"autoStart,omitempty" json:"autoStart,omitempty"` // Whether to start this instance automatically on load
	Enabled   bool `yaml:"enabled" json:"enabled"`                         // Whether this instance definition is enabled

	// Creation timestamp
	CreatedAt time.Time `yaml:"createdAt" json:"createdAt"`
}

// ServiceInstancePersistence provides minimal YAML persistence for service instances
// It integrates with the existing layered configuration system and is used by the orchestrator
type ServiceInstancePersistence struct {
	storage *config.Storage
}

// NewServiceInstancePersistence creates a new persistence helper
func NewServiceInstancePersistence(storage *config.Storage) *ServiceInstancePersistence {
	return &ServiceInstancePersistence{
		storage: storage,
	}
}

// LoadPersistedDefinitions loads all persisted service instance definitions from YAML files
// Returns definitions that are enabled and ready for instantiation
func (sip *ServiceInstancePersistence) LoadPersistedDefinitions() ([]ServiceInstanceDefinition, error) {
	// Load all service instance YAML files from user and project directories
	validator := func(def ServiceInstanceDefinition) error {
		return sip.validateDefinition(&def)
	}

	definitions, errorCollection, err := config.LoadAndParseYAML[ServiceInstanceDefinition]("serviceinstances", validator)
	if err != nil {
		logging.Warn("ServiceInstancePersistence", "Error loading service instances: %v", err)
		return nil, err
	}

	// Log any validation errors but continue with valid definitions
	if errorCollection != nil && errorCollection.HasErrors() {
		logging.Warn("ServiceInstancePersistence", "Some service instance files had errors:\n%s", errorCollection.GetSummary())
	}

	logging.Info("ServiceInstancePersistence", "Loaded %d service instance definitions from YAML files", len(definitions))
	return definitions, nil
}

// SaveDefinition persists a service instance definition to YAML
func (sip *ServiceInstancePersistence) SaveDefinition(def ServiceInstanceDefinition) error {
	if err := sip.validateDefinition(&def); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	// Convert to YAML
	yamlData, err := yaml.Marshal(def)
	if err != nil {
		return fmt.Errorf("failed to marshal service instance to YAML: %w", err)
	}

	// Save to storage
	if err := sip.storage.Save("serviceinstances", def.Name, yamlData); err != nil {
		return fmt.Errorf("failed to persist service instance definition: %w", err)
	}

	logging.Info("ServiceInstancePersistence", "Saved service instance definition: %s", def.Name)
	return nil
}

// DeleteDefinition removes a persisted service instance definition
func (sip *ServiceInstancePersistence) DeleteDefinition(name string) error {
	if err := sip.storage.Delete("serviceinstances", name); err != nil {
		return fmt.Errorf("failed to delete service instance definition: %w", err)
	}

	logging.Info("ServiceInstancePersistence", "Deleted service instance definition: %s", name)
	return nil
}

// GetDefinition loads a specific persisted definition by name
func (sip *ServiceInstancePersistence) GetDefinition(name string) (*ServiceInstanceDefinition, error) {
	data, err := sip.storage.Load("serviceinstances", name)
	if err != nil {
		return nil, fmt.Errorf("failed to load service instance definition %s: %w", name, err)
	}

	var def ServiceInstanceDefinition
	if err := yaml.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("failed to parse service instance definition %s: %w", name, err)
	}

	if err := sip.validateDefinition(&def); err != nil {
		return nil, fmt.Errorf("loaded definition is invalid: %w", err)
	}

	return &def, nil
}

// ListDefinitionNames returns the names of all persisted service instance definitions
func (sip *ServiceInstancePersistence) ListDefinitionNames() ([]string, error) {
	return sip.storage.List("serviceinstances")
}

// validateDefinition performs validation on a service instance definition
func (sip *ServiceInstancePersistence) validateDefinition(def *ServiceInstanceDefinition) error {
	if def.Name == "" {
		return fmt.Errorf("service instance name cannot be empty")
	}

	if def.ServiceClassName == "" {
		return fmt.Errorf("serviceClassName is required for service instance %s", def.Name)
	}

	if def.Args == nil {
		def.Args = make(map[string]interface{})
	}

	if def.CreatedAt.IsZero() {
		def.CreatedAt = time.Now()
	}

	return nil
}

// CreateDefinitionFromInstance creates a ServiceInstanceDefinition from orchestrator data
// This is a helper function for the orchestrator to easily create persistent definitions
func CreateDefinitionFromInstance(name, serviceClassName, serviceClassType string, args map[string]interface{}, autoStart bool) ServiceInstanceDefinition {
	return ServiceInstanceDefinition{
		Name:             name,
		ServiceClassName: serviceClassName,
		ServiceClassType: serviceClassType,
		Args:             args,
		Enabled:          true,
		AutoStart:        autoStart,
		CreatedAt:        time.Now(),
	}
}
