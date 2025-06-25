package testing

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// scenarioLoader implements the TestScenarioLoader interface
type scenarioLoader struct {
	debug  bool
	logger TestLogger
}

// NewTestScenarioLoader creates a new test scenario loader
func NewTestScenarioLoader(debug bool) TestScenarioLoader {
	return &scenarioLoader{
		debug:  debug,
		logger: NewStdoutLogger(false, debug), // Default to stdout logger
	}
}

// NewTestScenarioLoaderWithLogger creates a new test scenario loader with custom logger
func NewTestScenarioLoaderWithLogger(debug bool, logger TestLogger) TestScenarioLoader {
	return &scenarioLoader{
		debug:  debug,
		logger: logger,
	}
}

// LoadScenarios loads test scenarios from the given path
func (l *scenarioLoader) LoadScenarios(configPath string) ([]TestScenario, error) {
	var scenarios []TestScenario

	if l.debug {
		l.logger.Debug("📁 Loading test scenarios from: %s\n", configPath)
	}

	// Check if path exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("scenario path does not exist: %s", configPath)
	}

	// Check if it's a file or directory
	info, err := os.Stat(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat scenario path: %w", err)
	}

	if info.IsDir() {
		// Load all YAML files from directory
		scenarios, err = l.loadScenariosFromDirectory(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load scenarios from directory: %w", err)
		}
	} else {
		// Load single file
		scenario, err := l.loadScenarioFromFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load scenario from file: %w", err)
		}
		scenarios = append(scenarios, scenario)
	}

	if l.debug {
		l.logger.Debug("📋 Loaded %d test scenarios\n", len(scenarios))
		for _, scenario := range scenarios {
			l.logger.Debug("  • %s (%s/%s) - %d steps\n",
				scenario.Name, scenario.Category, scenario.Concept, len(scenario.Steps))
		}
	}

	return scenarios, nil
}

// loadScenariosFromDirectory loads all YAML scenario files from a directory
func (l *scenarioLoader) loadScenariosFromDirectory(dirPath string) ([]TestScenario, error) {
	var scenarios []TestScenario

	err := filepath.WalkDir(dirPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if d.IsDir() {
			return nil
		}

		// Only process YAML files
		if !l.isYAMLFile(path) {
			return nil
		}

		if l.debug {
			l.logger.Debug("📄 Loading scenario file: %s\n", path)
		}

		scenario, err := l.loadScenarioFromFile(path)
		if err != nil {
			return fmt.Errorf("failed to load scenario from %s: %w", path, err)
		}

		scenarios = append(scenarios, scenario)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to walk directory %s: %w", dirPath, err)
	}

	return scenarios, nil
}

// loadScenarioFromFile loads a single scenario from a YAML file
func (l *scenarioLoader) loadScenarioFromFile(filePath string) (TestScenario, error) {
	var scenario TestScenario

	// Read file content
	content, err := os.ReadFile(filePath)
	if err != nil {
		return scenario, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	// Parse YAML
	if err := yaml.Unmarshal(content, &scenario); err != nil {
		return scenario, fmt.Errorf("failed to parse YAML in %s: %w", filePath, err)
	}

	// Validate required fields
	if err := l.validateScenario(scenario, filePath); err != nil {
		return scenario, fmt.Errorf("invalid scenario in %s: %w", filePath, err)
	}

	return scenario, nil
}

// validateScenario validates that a scenario has required fields
func (l *scenarioLoader) validateScenario(scenario TestScenario, filePath string) error {
	if scenario.Name == "" {
		return fmt.Errorf("scenario name is required")
	}

	if scenario.Category == "" {
		return fmt.Errorf("scenario category is required")
	}

	if scenario.Concept == "" {
		return fmt.Errorf("scenario concept is required")
	}

	if len(scenario.Steps) == 0 {
		return fmt.Errorf("scenario must have at least one step")
	}

	// Validate each step
	for i, step := range scenario.Steps {
		if err := l.validateStep(step, i); err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
	}

	// Validate cleanup steps if present
	for i, step := range scenario.Cleanup {
		if err := l.validateStep(step, i); err != nil {
			return fmt.Errorf("cleanup step %d: %w", i+1, err)
		}
	}

	return nil
}

// validateStep validates that a step has required fields
func (l *scenarioLoader) validateStep(step TestStep, index int) error {
	if step.ID == "" {
		return fmt.Errorf("step id is required")
	}

	if step.Tool == "" {
		return fmt.Errorf("step tool is required")
	}

	// Validate retry configuration if present
	if step.Retry != nil {
		if step.Retry.Count < 0 {
			return fmt.Errorf("retry count cannot be negative")
		}
		if step.Retry.Delay < 0 {
			return fmt.Errorf("retry delay cannot be negative")
		}
		if step.Retry.BackoffMultiplier < 0 {
			return fmt.Errorf("backoff multiplier cannot be negative")
		}
	}

	return nil
}

// FilterScenarios filters scenarios based on the configuration
func (l *scenarioLoader) FilterScenarios(scenarios []TestScenario, config TestConfiguration) []TestScenario {
	if l.debug {
		l.logger.Debug("🔍 Filtering scenarios based on configuration\n")
		l.logger.Debug("  • Category filter: %s\n", string(config.Category))
		l.logger.Debug("  • Concept filter: %s\n", string(config.Concept))
		l.logger.Debug("  • Scenario filter: %s\n", config.Scenario)
	}

	var filtered []TestScenario

	for _, scenario := range scenarios {
		// Apply category filter
		if config.Category != "" && scenario.Category != config.Category {
			continue
		}

		// Apply concept filter
		if config.Concept != "" && scenario.Concept != config.Concept {
			continue
		}

		// Apply scenario name filter
		if config.Scenario != "" && scenario.Name != config.Scenario {
			continue
		}

		filtered = append(filtered, scenario)
	}

	if l.debug {
		l.logger.Debug("📊 Filtered to %d scenarios:\n", len(filtered))
		for _, scenario := range filtered {
			l.logger.Debug("  • %s (%s/%s)\n", scenario.Name, scenario.Category, scenario.Concept)
		}
	}

	return filtered
}

// isYAMLFile checks if a file has a YAML extension
func (l *scenarioLoader) isYAMLFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

// GetAvailableCategories returns all unique categories from scenarios
func (l *scenarioLoader) GetAvailableCategories(scenarios []TestScenario) []TestCategory {
	categoryMap := make(map[TestCategory]bool)

	for _, scenario := range scenarios {
		categoryMap[scenario.Category] = true
	}

	var categories []TestCategory
	for category := range categoryMap {
		categories = append(categories, category)
	}

	return categories
}

// GetAvailableConcepts returns all unique concepts from scenarios
func (l *scenarioLoader) GetAvailableConcepts(scenarios []TestScenario) []TestConcept {
	conceptMap := make(map[TestConcept]bool)

	for _, scenario := range scenarios {
		conceptMap[scenario.Concept] = true
	}

	var concepts []TestConcept
	for concept := range conceptMap {
		concepts = append(concepts, concept)
	}

	return concepts
}

// GetScenarioNames returns all scenario names
func (l *scenarioLoader) GetScenarioNames(scenarios []TestScenario) []string {
	var names []string
	for _, scenario := range scenarios {
		names = append(names, scenario.Name)
	}
	return names
}

// GetScenariosByTag returns scenarios that have the specified tag
func (l *scenarioLoader) GetScenariosByTag(scenarios []TestScenario, tag string) []TestScenario {
	var filtered []TestScenario

	for _, scenario := range scenarios {
		for _, scenarioTag := range scenario.Tags {
			if scenarioTag == tag {
				filtered = append(filtered, scenario)
				break
			}
		}
	}

	return filtered
}

// GetDefaultScenarioPath returns the default path for test scenarios
func GetDefaultScenarioPath() string {
	return "internal/testing/scenarios"
}

// LoadAndFilterScenarios provides a unified way to load and filter scenarios
func LoadAndFilterScenarios(configPath string, config TestConfiguration, logger TestLogger) ([]TestScenario, error) {
	// Determine the actual path to use
	actualPath := GetScenarioPath(configPath)

	// Create loader with appropriate logger
	var loader TestScenarioLoader
	if logger != nil {
		loader = NewTestScenarioLoaderWithLogger(config.Debug, logger)
	} else {
		loader = NewTestScenarioLoader(config.Debug)
	}

	// Load scenarios
	scenarios, err := loader.LoadScenarios(actualPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load scenarios from %s: %w", actualPath, err)
	}

	// Filter scenarios based on configuration
	filteredScenarios := loader.FilterScenarios(scenarios, config)

	return filteredScenarios, nil
}

// GetScenarioPath determines the actual scenario path to use, handling empty/default cases
func GetScenarioPath(configPath string) string {
	if configPath == "" {
		return GetDefaultScenarioPath()
	}
	return configPath
}

// CreateScenarioLoaderForContext creates a scenario loader appropriate for the given context
func CreateScenarioLoaderForContext(debug bool, logger TestLogger) TestScenarioLoader {
	if logger != nil {
		return NewTestScenarioLoaderWithLogger(debug, logger)
	}
	return NewTestScenarioLoader(debug)
}

// LoadScenariosForCompletion provides a simple way to load scenarios for shell completion
// This uses minimal logging to avoid interfering with completion output
func LoadScenariosForCompletion(configPath string) ([]TestScenario, error) {
	actualPath := GetScenarioPath(configPath)

	// Use minimal logging for completion
	loader := NewTestScenarioLoader(false)
	scenarios, err := loader.LoadScenarios(actualPath)
	if err != nil {
		// Return empty slice instead of error for completion
		return []TestScenario{}, nil
	}

	return scenarios, nil
}
