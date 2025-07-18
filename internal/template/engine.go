package template

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
)

// Engine handles arg templating for service operations
type Engine struct {
	// Pattern to match template variables like {{ variableName }} or {{ variable.property.subproperty }}
	templatePattern *regexp.Regexp
}

// New creates a new template engine
func New() *Engine {
	return &Engine{
		templatePattern: regexp.MustCompile(`\{\{\s*\.?([a-zA-Z_][a-zA-Z0-9_.-]*)\s*\}\}`),
	}
}

// Replace replaces all template variables in a value with actual values from the context
func (e *Engine) Replace(value interface{}, context map[string]interface{}) (interface{}, error) {
	switch v := value.(type) {
	case string:
		return e.replaceStringTemplates(v, context)
	case map[string]interface{}:
		return e.replaceMapTemplates(v, context)
	case []interface{}:
		return e.replaceSliceTemplates(v, context)
	default:
		// Non-templatable types are returned as-is
		return value, nil
	}
}

// replaceStringTemplates replaces template variables in a string
func (e *Engine) replaceStringTemplates(template string, context map[string]interface{}) (string, error) {
	// Find all template variables
	matches := e.templatePattern.FindAllStringSubmatch(template, -1)

	// Track missing variables
	var missingVars []string

	result := template
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}

		varPath := match[1]

		// Resolve the variable path (supports dot notation)
		replacement, err := e.resolvePath(varPath, context)
		if err != nil {
			missingVars = append(missingVars, varPath)
			continue
		}

		// Convert replacement to string
		var replacementStr string
		switch r := replacement.(type) {
		case string:
			replacementStr = r
		case int, int32, int64:
			replacementStr = fmt.Sprintf("%d", r)
		case float32, float64:
			replacementStr = fmt.Sprintf("%f", r)
		case bool:
			replacementStr = fmt.Sprintf("%t", r)
		default:
			replacementStr = fmt.Sprintf("%v", r)
		}

		// Replace all occurrences of this variable (with and without dot prefix)
		placeholder := fmt.Sprintf("{{ %s }}", varPath)
		result = strings.ReplaceAll(result, placeholder, replacementStr)

		placeholderWithDot := fmt.Sprintf("{{ .%s }}", varPath)
		result = strings.ReplaceAll(result, placeholderWithDot, replacementStr)

		// Also handle version without spaces
		placeholderNoSpace := fmt.Sprintf("{{%s}}", varPath)
		result = strings.ReplaceAll(result, placeholderNoSpace, replacementStr)

		placeholderNoSpaceWithDot := fmt.Sprintf("{{.%s}}", varPath)
		result = strings.ReplaceAll(result, placeholderNoSpaceWithDot, replacementStr)
	}

	if len(missingVars) > 0 {
		return "", fmt.Errorf("missing template variables: %s", strings.Join(missingVars, ", "))
	}

	return result, nil
}

// replaceMapTemplates recursively replaces templates in a map
func (e *Engine) replaceMapTemplates(m map[string]interface{}, context map[string]interface{}) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	for key, value := range m {
		replacedValue, err := e.Replace(value, context)
		if err != nil {
			return nil, fmt.Errorf("error in key '%s': %w", key, err)
		}
		result[key] = replacedValue
	}

	return result, nil
}

// replaceSliceTemplates recursively replaces templates in a slice
func (e *Engine) replaceSliceTemplates(s []interface{}, context map[string]interface{}) ([]interface{}, error) {
	result := make([]interface{}, len(s))

	for i, value := range s {
		replacedValue, err := e.Replace(value, context)
		if err != nil {
			return nil, fmt.Errorf("error at index %d: %w", i, err)
		}
		result[i] = replacedValue
	}

	return result, nil
}

// ExtractVariables extracts all template variable names from a value
func (e *Engine) ExtractVariables(value interface{}) []string {
	variables := make(map[string]bool)
	e.extractVariablesRecursive(value, variables)

	// Convert map to slice
	result := make([]string, 0, len(variables))
	for varName := range variables {
		result = append(result, varName)
	}

	return result
}

// extractVariablesRecursive recursively extracts variables from any value type
func (e *Engine) extractVariablesRecursive(value interface{}, variables map[string]bool) {
	switch v := value.(type) {
	case string:
		matches := e.templatePattern.FindAllStringSubmatch(v, -1)
		for _, match := range matches {
			if len(match) >= 2 {
				variables[match[1]] = true
			}
		}
	case map[string]interface{}:
		for _, val := range v {
			e.extractVariablesRecursive(val, variables)
		}
	case []interface{}:
		for _, val := range v {
			e.extractVariablesRecursive(val, variables)
		}
	}
}

// ValidateContext ensures all required variables are present in the context
func (e *Engine) ValidateContext(value interface{}, context map[string]interface{}) error {
	requiredVars := e.ExtractVariables(value)

	var missingVars []string
	for _, varName := range requiredVars {
		if _, exists := context[varName]; !exists {
			missingVars = append(missingVars, varName)
		}
	}

	if len(missingVars) > 0 {
		return fmt.Errorf("missing required variables: %s", strings.Join(missingVars, ", "))
	}

	return nil
}

// resolvePath resolves a dot-notation path like "variable_name.property.subproperty"
func (e *Engine) resolvePath(path string, context map[string]interface{}) (interface{}, error) {
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty template path")
	}

	// Get root variable from context
	rootName := parts[0]
	currentValue, exists := context[rootName]
	if !exists {
		return nil, fmt.Errorf("variable '%s' not found in context", rootName)
	}

	// Navigate nested properties
	for i, part := range parts[1:] {
		var err error
		currentValue, err = e.getProperty(currentValue, part)
		if err != nil {
			return nil, fmt.Errorf("failed to access property '%s' at position %d in path '%s': %w", part, i+1, path, err)
		}
	}

	return currentValue, nil
}

// getProperty extracts a property from an object
func (e *Engine) getProperty(obj interface{}, property string) (interface{}, error) {
	switch v := obj.(type) {
	case map[string]interface{}:
		if value, exists := v[property]; exists {
			return value, nil
		}
		return nil, fmt.Errorf("property '%s' not found in object", property)
	default:
		return nil, fmt.Errorf("cannot access property '%s' on non-object type %T", property, obj)
	}
}

// RenderGoTemplate renders a full Go template with Sprig template functions
// This is used for complex expressions like {{ eq .input.var "value" }}
func (e *Engine) RenderGoTemplate(templateStr string, context map[string]interface{}) (interface{}, error) {
	tmpl, err := template.New("template").Funcs(sprig.TxtFuncMap()).Option("missingkey=error").Parse(templateStr)
	if err != nil {
		return nil, fmt.Errorf("invalid template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, context); err != nil {
		return nil, fmt.Errorf("template execution failed: %w", err)
	}

	result := buf.String()

	// Try to parse as boolean first (common for eq/ne functions)
	if result == "true" {
		return true, nil
	}
	if result == "false" {
		return false, nil
	}

	// Return as string for other results
	return result, nil
}
