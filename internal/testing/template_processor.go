package testing

import (
	"fmt"
	"sync"

	"muster/internal/template"
	"muster/pkg/logging"
)

// ScenarioContext holds the execution context for a test scenario
// including stored results from previous steps for template variable resolution
type ScenarioContext struct {
	storedResults map[string]interface{} // Store step results by variable name
	mu            sync.RWMutex           // Thread-safe access for parallel execution
}

// NewScenarioContext creates a new scenario execution context
func NewScenarioContext() *ScenarioContext {
	return &ScenarioContext{
		storedResults: make(map[string]interface{}),
	}
}

// StoreResult stores a step result under the given variable name
func (sc *ScenarioContext) StoreResult(name string, result interface{}) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.storedResults[name] = result
	logging.Debug("TestFramework", "Stored result for variable '%s': %v", name, result)
}

// GetStoredResult retrieves a stored result by variable name
func (sc *ScenarioContext) GetStoredResult(name string) (interface{}, bool) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	result, exists := sc.storedResults[name]
	return result, exists
}

// GetAllStoredResults returns a copy of all stored results for debugging
func (sc *ScenarioContext) GetAllStoredResults() map[string]interface{} {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	// Return a copy to avoid concurrent access issues
	copy := make(map[string]interface{})
	for k, v := range sc.storedResults {
		copy[k] = v
	}
	return copy
}

// TemplateProcessor handles template variable resolution in test step arguments
// using the existing enhanced template engine
type TemplateProcessor struct {
	context *ScenarioContext
	engine  *template.Engine
}

// NewTemplateProcessor creates a new template processor with the given scenario context
func NewTemplateProcessor(context *ScenarioContext) *TemplateProcessor {
	return &TemplateProcessor{
		context: context,
		engine:  template.New(),
	}
}

// ResolveArgs processes a map of arguments and resolves any template variables
func (tp *TemplateProcessor) ResolveArgs(args map[string]interface{}) (map[string]interface{}, error) {
	if args == nil {
		return nil, nil
	}

	// Get current stored results as context for template resolution
	templateContext := tp.context.GetAllStoredResults()

	// Use the enhanced template engine to resolve variables
	resolved, err := tp.engine.Replace(args, templateContext)
	if err != nil {
		return nil, err
	}

	// The engine returns interface{}, but we know it should be a map for arguments
	resolvedMap, ok := resolved.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("template resolution returned unexpected type: %T", resolved)
	}

	logging.Debug("TestFramework", "Template resolution completed. Original: %v, Resolved: %v", args, resolvedMap)
	return resolvedMap, nil
}
