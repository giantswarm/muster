package template

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"text/template/parse"

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
// against the context map. It delegates to ResolvePath, treating the context map
// itself as the navigation root.
func (e *Engine) resolvePath(path string, context map[string]interface{}) (interface{}, error) {
	return e.ResolvePath(context, path)
}

// ResolvePath navigates a dotted path with optional array indexing against an
// arbitrary root value and returns the typed value found there. It is the single
// path navigator shared by template variable substitution, workflow output
// projections, and condition jsonPath expectations.
//
// Supported syntax:
//   - object navigation: "data.field.subfield"
//   - array indexing: "items[0]", "data.items[2].name"
//   - chained indices: "matrix[0][1]"
//
// The root may be the template context map (first segment is then a top-level
// variable name) or any nested value (e.g. a tool result object).
func (e *Engine) ResolvePath(root interface{}, path string) (interface{}, error) {
	current := root
	for _, segment := range strings.Split(path, ".") {
		if segment == "" {
			continue
		}

		name, indices, err := parsePathSegment(segment, path)
		if err != nil {
			return nil, err
		}

		if name != "" {
			obj, ok := current.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("cannot access '%s' in path '%s': not an object", name, path)
			}
			value, exists := obj[name]
			if !exists {
				return nil, fmt.Errorf("path '%s' not found", path)
			}
			current = value
		}

		for _, idx := range indices {
			arr, ok := current.([]interface{})
			if !ok {
				return nil, fmt.Errorf("cannot index '%s' in path '%s': not an array", segment, path)
			}
			if idx < 0 || idx >= len(arr) {
				return nil, fmt.Errorf("index %d out of range in path '%s'", idx, path)
			}
			current = arr[idx]
		}
	}

	return current, nil
}

// parsePathSegment splits a single path segment into its property name and any
// trailing array indices, e.g. "items[0][1]" -> ("items", [0, 1]).
func parsePathSegment(segment, path string) (string, []int, error) {
	bracket := strings.IndexByte(segment, '[')
	if bracket < 0 {
		return segment, nil, nil
	}

	name := segment[:bracket]
	rest := segment[bracket:]
	var indices []int
	for len(rest) > 0 {
		if rest[0] != '[' {
			return "", nil, fmt.Errorf("invalid index syntax in path '%s'", path)
		}
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			return "", nil, fmt.Errorf("unterminated index in path '%s'", path)
		}
		idx, err := strconv.Atoi(rest[1:end])
		if err != nil {
			return "", nil, fmt.Errorf("invalid array index %q in path '%s'", rest[1:end], path)
		}
		indices = append(indices, idx)
		rest = rest[end+1:]
	}

	return name, indices, nil
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

// RenderGoTemplateTyped renders a template while preserving the actual Go type
// of its result, used where structured JSON output matters (workflow output
// projections and jsonPath expectations).
//
// When the template body is a single output action it is evaluated for its
// typed value rather than its rendered text: "{{ len .x }}" yields the number
// 3, "{{ eq .a .b }}" a bool, "{{ .v }}" the value's own type, and
// "{{ printf \"%02d\" .n }}" the string "08". Templates that mix literal text
// with actions, or contain several actions, render to a string because their
// concatenated form is inherently textual.
//
// Crucially this never inspects the rendered text to guess a type, so a
// numeric-looking string such as a version "1.20" or a zero-padded "08" is
// preserved exactly instead of being silently coerced to a number. That guess
// was the source of lossy numeric coercion in earlier versions.
func (e *Engine) RenderGoTemplateTyped(templateStr string, context map[string]interface{}) (interface{}, error) {
	probe, err := template.New("template").Funcs(sprig.TxtFuncMap()).Option("missingkey=error").Parse(templateStr)
	if err != nil {
		return nil, fmt.Errorf("invalid template: %w", err)
	}

	if pipe := singleActionPipe(probe); pipe != nil {
		return renderCapturedPipe(pipe.String(), context)
	}

	var buf bytes.Buffer
	if err := probe.Execute(&buf, context); err != nil {
		return nil, fmt.Errorf("template execution failed: %w", err)
	}
	return buf.String(), nil
}

// singleActionPipe returns the pipeline of a template whose body is exactly one
// output action — no surrounding text, no additional nodes and no variable
// declaration ("{{ $x := ... }}") — or nil for anything else. Only such a
// pipeline can be meaningfully evaluated for a single typed value.
func singleActionPipe(t *template.Template) *parse.PipeNode {
	if t.Tree == nil || t.Root == nil || len(t.Root.Nodes) != 1 {
		return nil
	}
	action, ok := t.Root.Nodes[0].(*parse.ActionNode)
	if !ok || action.Pipe == nil || len(action.Pipe.Decl) > 0 {
		return nil
	}
	return action.Pipe
}

// renderCapturedPipe evaluates a single pipeline and returns the typed value it
// produced, by piping it into a capture function whose argument keeps its
// concrete Go type. The rendered text itself is discarded.
func renderCapturedPipe(pipeText string, context map[string]interface{}) (interface{}, error) {
	var captured interface{}
	funcs := sprig.TxtFuncMap()
	funcs["__capture"] = func(v interface{}) string {
		captured = v
		return ""
	}
	tmpl, err := template.New("template").Funcs(funcs).Option("missingkey=error").Parse("{{ " + pipeText + " | __capture }}")
	if err != nil {
		return nil, fmt.Errorf("invalid template: %w", err)
	}
	if err := tmpl.Execute(io.Discard, context); err != nil {
		return nil, fmt.Errorf("template execution failed: %w", err)
	}
	return captured, nil
}
