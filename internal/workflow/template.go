package workflow

import (
	"fmt"
	"strings"

	"github.com/giantswarm/muster/pkg/logging"
)

// executionContext holds the state during workflow execution.
type executionContext struct {
	input        map[string]interface{} // Original input arguments
	variables    map[string]interface{} // User-defined variables
	results      map[string]interface{} // Results from previous steps
	templateVars []string               // Track template variables used
	stepMetadata []stepMetadata         // Track step metadata
}

// resolveArguments resolves template variables in step arguments.
func (we *WorkflowExecutor) resolveArguments(args map[string]interface{}, ctx *executionContext) (map[string]interface{}, error) {
	resolved := make(map[string]interface{})

	for key, value := range args {
		resolvedValue, err := we.resolveValue(value, ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to render arguments for argument '%s': %w", key, err)
		}
		resolved[key] = resolvedValue
	}

	return resolved, nil
}

// resolveValue recursively resolves template variables in a value.
// Maps and slices are traversed; other primitive types are returned as-is.
func (we *WorkflowExecutor) resolveValue(value interface{}, ctx *executionContext) (interface{}, error) {
	switch v := value.(type) {
	case string:
		if strings.Contains(v, "{{") && strings.Contains(v, "}}") {
			return we.resolveTemplate(v, ctx)
		}
		return v, nil

	case map[string]interface{}:
		resolved := make(map[string]interface{})
		for k, val := range v {
			resolvedVal, err := we.resolveValue(val, ctx)
			if err != nil {
				return nil, err
			}
			resolved[k] = resolvedVal
		}
		return resolved, nil

	case []interface{}:
		resolved := make([]interface{}, len(v))
		for i, val := range v {
			resolvedVal, err := we.resolveValue(val, ctx)
			if err != nil {
				return nil, err
			}
			resolved[i] = resolvedVal
		}
		return resolved, nil

	default:
		return value, nil
	}
}

// resolveTemplate resolves a template string. Simple variable accesses
// (`{{ .input.foo }}`, `{{ .results.bar }}`, `{{ .vars.baz }}`) preserve the
// original Go type by reading the value directly from the context map; full
// templates fall through to the text-template engine which always returns a
// string.
func (we *WorkflowExecutor) resolveTemplate(templateStr string, ctx *executionContext) (interface{}, error) {
	logging.Debug("WorkflowExecutor", "Resolving template: %s", templateStr)
	logging.Debug("WorkflowExecutor", "Original results: %v", ctx.results)

	// Track which input variables the template references — surfaced later
	// in step metadata for debugging.
	if strings.Contains(templateStr, ".input.") {
		words := strings.Fields(templateStr)
		for _, word := range words {
			if strings.Contains(word, ".input.") {
				if start := strings.Index(word, ".input."); start != -1 {
					remaining := word[start+1:]
					if end := strings.IndexAny(remaining, " }"); end != -1 {
						varName := remaining[:end]
						if varName != "" && !contains(ctx.templateVars, varName) {
							ctx.templateVars = append(ctx.templateVars, varName)
						}
					} else {
						varName := strings.TrimSuffix(remaining, "}}")
						varName = strings.TrimSuffix(varName, "}")
						if varName != "" && !contains(ctx.templateVars, varName) {
							ctx.templateVars = append(ctx.templateVars, varName)
						}
					}
				}
			}
		}
	}

	templateCtx := map[string]interface{}{
		"input":   ctx.input,
		"vars":    ctx.variables,
		"results": ctx.results,
		"context": ctx.results, // Alias for results to support .context.variable syntax
	}

	logging.Debug("WorkflowExecutor", "Template context results (raw): %v", templateCtx["results"])

	if we.isSimpleVariableAccess(templateStr) {
		if originalValue := we.getOriginalValue(templateStr, ctx); originalValue != nil {
			return originalValue, nil
		}
	}

	result, err := we.template.RenderGoTemplate(templateStr, templateCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to render arguments: %w", err)
	}

	logging.Debug("WorkflowExecutor", "Template result: %v", result)
	return result, nil
}

// isSimpleVariableAccess reports whether the template is a single-variable
// access pattern like `{{ .input.foo }}` — used to short-circuit through
// getOriginalValue and preserve the underlying Go type.
func (we *WorkflowExecutor) isSimpleVariableAccess(templateStr string) bool {
	trimmed := strings.TrimSpace(templateStr)
	if !strings.HasPrefix(trimmed, "{{") || !strings.HasSuffix(trimmed, "}}") {
		return false
	}

	inner := strings.TrimSpace(trimmed[2 : len(trimmed)-2])
	return strings.HasPrefix(inner, ".input.") || strings.HasPrefix(inner, ".results.") || strings.HasPrefix(inner, ".vars.")
}

// getOriginalValue extracts the raw context value referenced by a simple
// variable-access template. Returns nil if the template isn't a simple
// access or the key doesn't exist.
func (we *WorkflowExecutor) getOriginalValue(templateStr string, ctx *executionContext) interface{} {
	trimmed := strings.TrimSpace(templateStr)
	if !strings.HasPrefix(trimmed, "{{") || !strings.HasSuffix(trimmed, "}}") {
		return nil
	}

	inner := strings.TrimSpace(trimmed[2 : len(trimmed)-2])

	switch {
	case strings.HasPrefix(inner, ".input."):
		return ctx.input[inner[7:]]
	case strings.HasPrefix(inner, ".results."):
		return ctx.results[inner[9:]]
	case strings.HasPrefix(inner, ".vars."):
		return ctx.variables[inner[6:]]
	}

	return nil
}

// contains is a small slice-string helper used by template-variable tracking.
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
