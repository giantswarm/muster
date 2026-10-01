package workflow

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/giantswarm/muster/v5/internal/api"
)

// stepFieldArgs is a workflow definition that uses every multi-word step,
// sub-step and condition field, spelled with the given names. camelCase is the
// CRD spelling the docs teach; snake_case is the deprecated alias.
func stepFieldArgs(allowFailure, fromStep, expectNot, jsonPath string) map[string]interface{} {
	return map[string]interface{}{
		"name": "field-names",
		"steps": []interface{}{
			map[string]interface{}{"id": "probe", "tool": "core_service_list", allowFailure: true},
			map[string]interface{}{
				"id":   "fallback",
				"tool": "core_service_list",
				"condition": map[string]interface{}{
					fromStep: "probe",
					expectNot: map[string]interface{}{
						jsonPath: map[string]interface{}{"status": "ok"},
					},
				},
			},
			map[string]interface{}{
				"id": "group",
				"parallel": []interface{}{
					map[string]interface{}{"id": "a", "tool": "core_service_list", allowFailure: true},
				},
			},
		},
		"onFailure": []interface{}{
			map[string]interface{}{"id": "cleanup", "tool": "core_service_list", allowFailure: true},
		},
	}
}

// TestStepFieldNames_AcceptedByValidateAndCreate covers the spelling a
// definition copied from a Workflow manifest uses: workflow_validate and
// workflow_create accept the CRD's camelCase field names, and still accept the
// deprecated snake_case aliases.
func TestStepFieldNames_AcceptedByValidateAndCreate(t *testing.T) {
	spellings := map[string]map[string]interface{}{
		"camelCase":  stepFieldArgs("allowFailure", "fromStep", "expectNot", "jsonPath"),
		"snake_case": stepFieldArgs("allow_failure", "from_step", "expect_not", "json_path"),
	}

	for label, args := range spellings {
		for _, tool := range []string{"workflow_validate", "workflow_create"} {
			t.Run(label+"/"+tool, func(t *testing.T) {
				sa := &stubMusterClient{}
				adapter := NewAdapterWithClient(sa, "test-ns", nil, nil, "")
				t.Cleanup(adapter.stopGC)

				result, err := adapter.ExecuteTool(context.Background(), tool, args)
				if err != nil {
					t.Fatalf("%s failed: %v", tool, err)
				}
				if result.IsError {
					t.Fatalf("%s rejected the definition: %s", tool, resultText(t, result))
				}
			})
		}
	}
}

// TestStepFieldNames_DecodedAsSet checks the strict request decoder carries the
// flag through under either spelling, and that a typo is still rejected.
func TestStepFieldNames_DecodedAsSet(t *testing.T) {
	for _, key := range []string{"allowFailure", "allow_failure"} {
		var req api.WorkflowValidateRequest
		args := map[string]interface{}{
			"name":  "w",
			"steps": []interface{}{map[string]interface{}{"id": "s", "tool": "t", key: true}},
		}
		if err := api.ParseRequest(args, &req); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if !req.Steps[0].AllowFailure {
			t.Errorf("%s: AllowFailure not set", key)
		}
	}

	var req api.WorkflowValidateRequest
	typo := map[string]interface{}{
		"name":  "w",
		"steps": []interface{}{map[string]interface{}{"id": "s", "tool": "t", "allowFail": true}},
	}
	if err := api.ParseRequest(typo, &req); err == nil || !strings.Contains(err.Error(), "allowFail") {
		t.Errorf("a misspelt step field must be rejected by name, got %v", err)
	}
}

// TestStepSchema_MatchesDecoder pins the input schema workflow_create and
// workflow_validate advertise to the field names their request decoder
// accepts: every advertised property must be a JSON field of the Go type it
// decodes into.
func TestStepSchema_MatchesDecoder(t *testing.T) {
	steps := getWorkflowStepsSchema()[api.SchemaKeyItems].(map[string]interface{})
	stepProps := properties(t, steps)
	condition := stepProps["condition"].(map[string]interface{})
	conditionProps := properties(t, condition)
	forEach := stepProps["forEach"].(map[string]interface{})
	subStep := getWorkflowSubStepSchema()

	cases := map[string]struct {
		schema map[string]interface{}
		goType interface{}
	}{
		"step":      {steps, api.WorkflowStep{}},
		"sub-step":  {subStep, api.WorkflowSubStep{}},
		"onFailure": {getWorkflowOnFailureSchema()[api.SchemaKeyItems].(map[string]interface{}), api.WorkflowSubStep{}},
		"forEach":   {forEach, api.WorkflowForEach{}},
		"condition": {condition, api.WorkflowCondition{}},
		"expect":    {conditionProps["expect"].(map[string]interface{}), api.WorkflowConditionExpectation{}},
	}

	for label, c := range cases {
		t.Run(label, func(t *testing.T) {
			fields := jsonFieldNames(reflect.TypeOf(c.goType))
			var missing []string
			for name := range properties(t, c.schema) {
				if !fields[name] {
					missing = append(missing, name)
				}
			}
			sort.Strings(missing)
			if len(missing) > 0 {
				t.Errorf("schema advertises %v, which %T does not decode", missing, c.goType)
			}
		})
	}
}

func properties(t *testing.T, schema map[string]interface{}) map[string]interface{} {
	t.Helper()
	props, ok := schema[api.SchemaKeyProperties].(map[string]interface{})
	if !ok {
		t.Fatalf("schema has no properties: %v", schema)
	}
	return props
}

func jsonFieldNames(typ reflect.Type) map[string]bool {
	names := make(map[string]bool, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			names[name] = true
		}
	}
	return names
}
