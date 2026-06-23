package workflow

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/giantswarm/muster/pkg/logging"
)

// purePathPattern matches a single template that is a bare reference path —
// dots, array indices and identifier characters only, no functions or spaces —
// such as "{{ .results.pods.items }}" or "{{ .result.items[0].name }}". Such
// references can be resolved to their typed value via the shared path navigator
// instead of being stringified by text/template.
var purePathPattern = regexp.MustCompile(`^\{\{\s*\.([A-Za-z0-9_][A-Za-z0-9_.\[\]-]*)\s*\}\}$`)

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
						if varName != "" && !slices.Contains(ctx.templateVars, varName) {
							ctx.templateVars = append(ctx.templateVars, varName)
						}
					} else {
						varName := strings.TrimSuffix(remaining, "}}")
						varName = strings.TrimSuffix(varName, "}")
						if varName != "" && !slices.Contains(ctx.templateVars, varName) {
							ctx.templateVars = append(ctx.templateVars, varName)
						}
					}
				}
			}
		}
	}

	templateCtx := we.templateContext(ctx)

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

// renderTypedTemplate renders a single template string while preserving JSON
// types. A pure reference path (e.g. "{{ .results.pods.items }}") is resolved
// through the shared path navigator so objects, arrays and numbers keep their
// type at any depth. Any other expression goes through the typed template
// engine, which evaluates a single action for its real Go value (e.g.
// "{{ len .results.events.items }}" -> 3, "{{ printf \"%02d\" .n }}" -> "08")
// so the result is structured rather than stringly-typed — without ever
// guessing a type from the rendered text, which previously caused lossy numeric
// coercion of strings like versions and zero-padded values.
func (we *WorkflowExecutor) renderTypedTemplate(templateStr string, tctx map[string]interface{}) (interface{}, error) {
	if m := purePathPattern.FindStringSubmatch(strings.TrimSpace(templateStr)); m != nil {
		if v, err := we.template.ResolvePath(tctx, m[1]); err == nil {
			return v, nil
		}
		// On a navigation error fall through to the template engine so that
		// missing-key handling and any sprig defaulting still apply.
	}

	return we.template.RenderGoTemplateTyped(templateStr, tctx)
}

// renderOutputTemplate renders a workflow-level output template into a
// structured map, recursively resolving every templated leaf while preserving
// JSON types. It is evaluated once after all steps complete and used as the
// returned document in place of the default response.
func (we *WorkflowExecutor) renderOutputTemplate(output map[string]interface{}, execCtx *executionContext) (map[string]interface{}, error) {
	tctx := we.templateContext(execCtx)
	rendered, err := we.renderOutputTemplateValue(output, tctx)
	if err != nil {
		return nil, err
	}
	shaped, ok := rendered.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("output template must render to an object")
	}
	return shaped, nil
}

// renderOutputTemplateValue recursively renders an output template value: maps and slices
// are traversed, strings are rendered as typed templates, and other primitives
// are returned unchanged.
func (we *WorkflowExecutor) renderOutputTemplateValue(value interface{}, tctx map[string]interface{}) (interface{}, error) {
	switch v := value.(type) {
	case string:
		if !strings.Contains(v, "{{") {
			return v, nil
		}
		return we.renderTypedTemplate(v, tctx)

	case map[string]interface{}:
		out := make(map[string]interface{}, len(v))
		for k, val := range v {
			rendered, err := we.renderOutputTemplateValue(val, tctx)
			if err != nil {
				return nil, fmt.Errorf("output.%s: %w", k, err)
			}
			out[k] = rendered
		}
		return out, nil

	case []interface{}:
		out := make([]interface{}, len(v))
		for i, val := range v {
			rendered, err := we.renderOutputTemplateValue(val, tctx)
			if err != nil {
				return nil, fmt.Errorf("output[%d]: %w", i, err)
			}
			out[i] = rendered
		}
		return out, nil

	default:
		return value, nil
	}
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
		return ctx.input[strings.TrimPrefix(inner, ".input.")]
	case strings.HasPrefix(inner, ".results."):
		return ctx.results[strings.TrimPrefix(inner, ".results.")]
	case strings.HasPrefix(inner, ".vars."):
		return ctx.variables[strings.TrimPrefix(inner, ".vars.")]
	}

	return nil
}
