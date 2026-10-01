package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestWorkflowStepUnmarshal_AliasAndCanonicalBothSet(t *testing.T) {
	var step WorkflowStep
	err := json.Unmarshal([]byte(`{"id":"s","tool":"t","allowFailure":true,"allow_failure":false}`), &step)
	if err == nil || !strings.Contains(err.Error(), "both set") {
		t.Fatalf("expected a both-set error, got %v", err)
	}
}

func TestWorkflowConditionUnmarshal_Aliases(t *testing.T) {
	var cond WorkflowCondition
	data := `{"from_step":"probe","expect_not":{"json_path":{"status":"ok"}}}`
	if err := json.Unmarshal([]byte(data), &cond); err != nil {
		t.Fatal(err)
	}
	if cond.FromStep != "probe" || cond.ExpectNot.JsonPath["status"] != "ok" {
		t.Errorf("aliases not decoded: %+v", cond)
	}
}

func TestDeprecatedFieldAliases(t *testing.T) {
	args := map[string]interface{}{
		"steps": []interface{}{
			map[string]interface{}{
				"id":            "probe",
				"allow_failure": true,
				// Tool arguments are the tool's own and never flagged.
				"args": map[string]interface{}{"allow_failure": true},
			},
			map[string]interface{}{
				"id": "fallback",
				"condition": map[string]interface{}{
					"from_step":  "probe",
					"expect_not": map[string]interface{}{"json_path": map[string]interface{}{}},
				},
			},
			map[string]interface{}{
				"id":       "group",
				"parallel": []interface{}{map[string]interface{}{"id": "a", "allow_failure": true}},
			},
			map[string]interface{}{"id": "clean", "allowFailure": true},
		},
		"onFailure": []interface{}{map[string]interface{}{"id": "undo", "allow_failure": true}},
	}

	want := []string{
		"onFailure undo: 'allow_failure' (use 'allowFailure')",
		"steps fallback condition.expect_not: 'json_path' (use 'jsonPath')",
		"steps fallback condition: 'expect_not' (use 'expectNot')",
		"steps fallback condition: 'from_step' (use 'fromStep')",
		"steps group parallel a: 'allow_failure' (use 'allowFailure')",
		"steps probe: 'allow_failure' (use 'allowFailure')",
	}
	if got := DeprecatedFieldAliases(args); !reflect.DeepEqual(got, want) {
		t.Errorf("DeprecatedFieldAliases() =\n%q\nwant\n%q", got, want)
	}
}
