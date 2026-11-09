package workflow

import (
	"maps"
	"regexp"

	"github.com/giantswarm/muster/v5/internal/api"
)

// inputReference finds the workflow inputs a templated step argument reads:
// {{.input.name}} and {{index .input "name"}}.
var inputReference = regexp.MustCompile(`\.` + api.FieldInput + `\.([A-Za-z_][A-Za-z0-9_]*)|index\s+\.` + api.FieldInput + `\s+"([^"]+)"`)

// recordedInput returns the workflow inputs an execution record keeps: an
// input passed into a sensitive argument of a step's tool
// (api.SensitiveToolArgs), such as core_mcpserver_create's
// clientCredentials, is replaced by api.RedactedValue. The workflow runs
// with the inputs as given; only the record is redacted.
func recordedInput(workflow *api.Workflow, args map[string]interface{}) map[string]interface{} {
	sensitive := map[string]struct{}{}
	visit := func(tool string, stepArgs map[string]interface{}) {
		for _, name := range api.SensitiveToolArgs(tool) {
			if value, ok := stepArgs[name]; ok {
				collectInputReferences(value, sensitive)
			}
		}
	}
	var visitSubs func(subs []api.WorkflowSubStep)
	visitSubs = func(subs []api.WorkflowSubStep) {
		for _, sub := range subs {
			visit(sub.Tool, sub.Args)
			if sub.ForEach != nil {
				visitSubs(sub.ForEach.Steps)
			}
		}
	}
	for _, step := range workflow.Steps {
		visit(step.Tool, step.Args)
		if step.ForEach != nil {
			visitSubs(step.ForEach.Steps)
		}
		visitSubs(step.Parallel)
	}
	visitSubs(workflow.OnFailure)

	if len(sensitive) == 0 {
		return args
	}
	recorded := maps.Clone(args)
	for name := range sensitive {
		if _, ok := recorded[name]; ok {
			recorded[name] = api.RedactedValue
		}
	}
	return recorded
}

// collectInputReferences adds the inputs every template string in value
// reads to into.
func collectInputReferences(value interface{}, into map[string]struct{}) {
	switch v := value.(type) {
	case string:
		for _, match := range inputReference.FindAllStringSubmatch(v, -1) {
			for _, name := range match[1:] {
				if name != "" {
					into[name] = struct{}{}
				}
			}
		}
	case map[string]interface{}:
		for _, item := range v {
			collectInputReferences(item, into)
		}
	case []interface{}:
		for _, item := range v {
			collectInputReferences(item, into)
		}
	}
}
