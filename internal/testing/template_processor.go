package testing

import (
	"fmt"
	"strings"
	"sync"

	"github.com/giantswarm/muster/internal/template"
	"github.com/giantswarm/muster/pkg/logging"
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

	// Use selective template resolution - only resolve variables that exist in the scenario context
	// This prevents errors when workflows contain templates like {{ .input.error_message }}
	// that should be preserved for execution-time resolution
	resolved, err := tp.resolveSafeTemplates(args, templateContext)
	if err != nil {
		return nil, err
	}

	// The result should be a map for arguments
	resolvedMap, ok := resolved.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("template resolution returned unexpected type: %T", resolved)
	}

	logging.Debug("TestFramework", "Template resolution completed. Original: %v, Resolved: %v", args, resolvedMap)
	return resolvedMap, nil
}

// resolveSafeTemplates recursively processes arguments and only resolves templates
// for variables that exist in the scenario context, leaving others untouched
func (tp *TemplateProcessor) resolveSafeTemplates(input interface{}, context map[string]interface{}) (interface{}, error) {
	switch v := input.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{})
		for key, value := range v {
			resolved, err := tp.resolveSafeTemplates(value, context)
			if err != nil {
				return nil, err
			}
			result[key] = resolved
		}
		return result, nil

	case []interface{}:
		result := make([]interface{}, len(v))
		for i, item := range v {
			resolved, err := tp.resolveSafeTemplates(item, context)
			if err != nil {
				return nil, err
			}
			result[i] = resolved
		}
		return result, nil

	case string:
		// Only resolve templates if the referenced variable exists in context
		return tp.resolveSafeString(v, context), nil

	default:
		// For other types (numbers, booleans, etc.), return as-is
		return v, nil
	}
}

// resolveSafeString resolves template variables in a string only if they exist in the context
func (tp *TemplateProcessor) resolveSafeString(input string, context map[string]interface{}) string {
	// Check if this string contains template syntax
	if !strings.Contains(input, "{{") || !strings.Contains(input, "}}") {
		return input
	}

	// Extract variable names from the template
	variables := tp.extractVariableNames(input)

	// Check if all variables exist in the context
	for _, variable := range variables {
		if _, exists := context[variable]; !exists {
			// If any variable doesn't exist, return the string unchanged
			logging.Debug("TestFramework", "Template variable '%s' not found in context, leaving template unchanged: %s", variable, input)
			return input
		}
	}

	// All variables exist, safe to resolve
	resolved, err := tp.engine.Replace(input, context)
	if err != nil {
		// If resolution fails, return original string (safer than failing)
		logging.Debug("TestFramework", "Template resolution failed for '%s', keeping original: %v", input, err)
		return input
	}

	if resolvedStr, ok := resolved.(string); ok {
		return resolvedStr
	}

	// If resolution didn't return a string, convert to string
	return fmt.Sprintf("%v", resolved)
}

// extractVariableNames extracts variable names from template strings like {{ variable.path }}
func (tp *TemplateProcessor) extractVariableNames(s string) []string {
	var variables []string

	// Simple extraction: find all {{ ... }} and get the root variable name
	for {
		start := strings.Index(s, "{{")
		if start == -1 {
			break
		}

		end := strings.Index(s[start:], "}}")
		if end == -1 {
			break
		}

		// Extract the variable part
		variable := strings.TrimSpace(s[start+2 : start+end])

		// Remove leading dots and spaces
		variable = strings.TrimPrefix(variable, ".")
		variable = strings.TrimSpace(variable)

		// Get the root variable name (before any dots)
		if dotIndex := strings.Index(variable, "."); dotIndex >= 0 {
			variable = variable[:dotIndex]
		}

		// Add to list if not already present and not empty
		if variable != "" {
			found := false
			for _, existing := range variables {
				if existing == variable {
					found = true
					break
				}
			}
			if !found {
				variables = append(variables, variable)
			}
		}

		// Move past this template
		s = s[start+end+2:]
	}

	return variables
}
