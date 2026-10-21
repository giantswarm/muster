package workflow

import (
	"fmt"

	"github.com/giantswarm/muster/v5/internal/api"
	musterv1alpha1 "github.com/giantswarm/muster/v5/pkg/apis/muster/v1alpha1"
)

// The CRD conversion and the advertised input schema of a nested forEach, a
// paginate block and a jq program.

// convertNestedSteps converts the CRD body of a nested forEach to internal
// format.
func (a *Adapter) convertNestedSteps(crdSteps []musterv1alpha1.WorkflowNestedStep) []api.WorkflowSubStep {
	subSteps := make([]api.WorkflowSubStep, 0, len(crdSteps))
	for _, crdStep := range crdSteps {
		sub := api.WorkflowSubStep{
			ID:           crdStep.ID,
			Tool:         crdStep.Tool,
			Args:         a.convertRawExtensionMap(crdStep.Args),
			Output:       crdStep.Output,
			Store:        crdStep.Store,
			AllowFailure: crdStep.AllowFailure,
			Description:  crdStep.Description,
			Paginate:     convertPaginateFromCRD(crdStep.Paginate),
			JQ:           crdStep.JQ,
		}
		if crdStep.Condition != nil {
			sub.Condition = a.convertWorkflowCondition(crdStep.Condition)
		}
		subSteps = append(subSteps, sub)
	}
	return subSteps
}

// convertNestedStepsToCRD converts the body of a nested forEach to CRD
// format. The CRD holds tool calls only there; a deeper forEach has been
// rejected by the validation before a definition is written.
func (a *Adapter) convertNestedStepsToCRD(subSteps []api.WorkflowSubStep) []musterv1alpha1.WorkflowNestedStep {
	crdSteps := make([]musterv1alpha1.WorkflowNestedStep, 0, len(subSteps))
	for _, sub := range subSteps {
		crdStep := musterv1alpha1.WorkflowNestedStep{
			ID:           sub.ID,
			Tool:         sub.Tool,
			Args:         a.convertToRawExtensionMap(sub.Args),
			Output:       sub.Output,
			Store:        sub.Store,
			AllowFailure: sub.AllowFailure,
			Description:  sub.Description,
			Paginate:     convertPaginateToCRD(sub.Paginate),
			JQ:           sub.JQ,
		}
		if sub.Condition != nil {
			crdStep.Condition = a.convertWorkflowConditionToCRD(sub.Condition)
		}
		crdSteps = append(crdSteps, crdStep)
	}
	return crdSteps
}

// convertPaginateFromCRD converts a CRD paginate block to internal format.
func convertPaginateFromCRD(p *musterv1alpha1.WorkflowPaginate) *api.WorkflowPaginate {
	if p == nil {
		return nil
	}
	return &api.WorkflowPaginate{ItemsField: p.ItemsField, TokenField: p.TokenField, TokenArg: p.TokenArg, MaxPages: p.MaxPages}
}

// convertPaginateToCRD converts an internal paginate block to CRD format.
func convertPaginateToCRD(p *api.WorkflowPaginate) *musterv1alpha1.WorkflowPaginate {
	if p == nil {
		return nil
	}
	return &musterv1alpha1.WorkflowPaginate{ItemsField: p.ItemsField, TokenField: p.TokenField, TokenArg: p.TokenArg, MaxPages: p.MaxPages}
}

// getWorkflowForEachSchema returns the schema of a forEach; bodyNests says
// whether its body may hold a nested forEach.
func getWorkflowForEachSchema(description string, bodyNests bool) map[string]interface{} {
	return map[string]interface{}{
		api.SchemaKeyType:                 string(api.ArgTypeObject),
		api.SchemaKeyDescription:          description,
		api.SchemaKeyAdditionalProperties: false,
		api.SchemaKeyProperties: map[string]interface{}{
			"items": map[string]interface{}{
				api.SchemaKeyType:        string(api.ArgTypeString),
				api.SchemaKeyDescription: "Template expression resolving to a list, e.g. \"{{ .input.clusters }}\" or an earlier step's \"{{ .results.clusters.items }}\"",
			},
			"as": map[string]interface{}{
				api.SchemaKeyType:        string(api.ArgTypeString),
				api.SchemaKeyDescription: "Loop variable name exposed as {{ .vars.<as> }} and $vars.<as> in jq (default \"item\"); the index is {{ .vars.<as>_index }}",
			},
			"parallelism": map[string]interface{}{
				api.SchemaKeyType:        string(api.ArgTypeInteger),
				api.SchemaKeyDescription: fmt.Sprintf("How many iterations may run at once, 1 to %d (default: one after another). Results and records come out in item order either way; a nested loop's calls in flight are at most outer times inner parallelism.", api.MaxForEachParallelism),
				"minimum":                1,
				"maximum":                api.MaxForEachParallelism,
			},
			"steps": map[string]interface{}{
				api.SchemaKeyType:        string(api.ArgTypeArray),
				api.SchemaKeyDescription: "Body executed for each item. After the loop, the forEach step's id holds one record per iteration: {index, item, status, results: {<sub-step id>: result}, error (for a failed iteration)}.",
				api.SchemaKeyItems:       getWorkflowSubStepSchema(bodyNests),
				"minItems":               1,
			},
		},
		api.SchemaKeyRequired: []string{"items", "steps"},
	}
}

// getWorkflowPaginateSchema returns the schema of a tool step's paginate block.
func getWorkflowPaginateSchema() map[string]interface{} {
	return map[string]interface{}{
		api.SchemaKeyType:                 string(api.ArgTypeObject),
		api.SchemaKeyDescription:          "Follow the tool's next-page token and store every page's items as one list (tool steps only). The stored result is the last page with that list holding every page's items, plus pagination.pages and pagination.truncated; a truncated result keeps the token to resume from.",
		api.SchemaKeyAdditionalProperties: false,
		api.SchemaKeyProperties: map[string]interface{}{
			"itemsField": map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString), api.SchemaKeyDescription: "Where a page's list is, a dotted path (default \"items\")"},
			"tokenField": map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString), api.SchemaKeyDescription: "Where a page's next-page token is, a dotted path (default \"continue\")"},
			"tokenArg":   map[string]interface{}{api.SchemaKeyType: string(api.ArgTypeString), api.SchemaKeyDescription: "The tool argument the token is passed in (default \"continue\")"},
			"maxPages": map[string]interface{}{
				api.SchemaKeyType:        string(api.ArgTypeInteger),
				api.SchemaKeyDescription: fmt.Sprintf("Most pages fetched, 1 to %d (default %d)", api.MaxPaginateMaxPages, api.DefaultPaginateMaxPages),
				"minimum":                1,
				"maximum":                api.MaxPaginateMaxPages,
			},
		},
	}
}

// getWorkflowJQSchema returns the schema of a step's jq program.
func getWorkflowJQSchema() map[string]interface{} {
	return map[string]interface{}{
		api.SchemaKeyType:        string(api.ArgTypeString),
		api.SchemaKeyDescription: "A jq program that reshapes the step's result before it is stored -- the tool's result, or a forEach step's per-iteration records -- so later steps and the output template see only what it yields, e.g. '[.items[] | {name: .metadata.name}]'. $input is the workflow input, $vars the loop variables. It must yield one value: wrap a stream in [ ].",
	}
}
