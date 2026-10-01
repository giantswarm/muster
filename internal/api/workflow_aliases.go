package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// The workflow definition fields carry the Workflow CRD's camelCase names.
// Earlier releases of the structured create/validate API spelt the multi-word
// fields in snake_case; those spellings stay accepted as deprecated aliases,
// mapped here from alias to canonical name per object.
var (
	stepFieldAliases        = map[string]string{"allow_failure": "allowFailure"}
	conditionFieldAliases   = map[string]string{"from_step": "fromStep", "expect_not": "expectNot"}
	expectationFieldAliases = map[string]string{"json_path": "jsonPath"}
)

// UnmarshalJSON decodes a step strictly, accepting the deprecated aliases.
func (s *WorkflowStep) UnmarshalJSON(data []byte) error {
	type plain WorkflowStep
	return decodeWithAliases(data, (*plain)(s), stepFieldAliases)
}

// UnmarshalJSON decodes a sub-step strictly, accepting the deprecated aliases.
func (s *WorkflowSubStep) UnmarshalJSON(data []byte) error {
	type plain WorkflowSubStep
	return decodeWithAliases(data, (*plain)(s), stepFieldAliases)
}

// UnmarshalJSON decodes a condition strictly, accepting the deprecated aliases.
func (c *WorkflowCondition) UnmarshalJSON(data []byte) error {
	type plain WorkflowCondition
	return decodeWithAliases(data, (*plain)(c), conditionFieldAliases)
}

// UnmarshalJSON decodes an expectation strictly, accepting the deprecated aliases.
func (e *WorkflowConditionExpectation) UnmarshalJSON(data []byte) error {
	type plain WorkflowConditionExpectation
	return decodeWithAliases(data, (*plain)(e), expectationFieldAliases)
}

// decodeWithAliases renames the deprecated alias keys of a JSON object to
// their canonical names and decodes it into v, rejecting unknown fields. A
// custom UnmarshalJSON does not inherit the caller's DisallowUnknownFields, so
// the strictness is applied here: a misspelt field must fail by name rather
// than be dropped.
func decodeWithAliases(data []byte, v interface{}, aliases map[string]string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for alias, canonical := range aliases {
		value, ok := fields[alias]
		if !ok {
			continue
		}
		if _, both := fields[canonical]; both {
			return fmt.Errorf("%q and its deprecated alias %q are both set", canonical, alias)
		}
		fields[canonical] = value
		delete(fields, alias)
	}
	normalized, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(normalized))
	decoder.DisallowUnknownFields()
	return decoder.Decode(v)
}

// DeprecatedFieldAliases lists every deprecated snake_case field a structured
// workflow definition (the create/validate/update arguments) still uses, each
// qualified by its step and naming the replacement, so the caller can warn
// about them.
func DeprecatedFieldAliases(args map[string]interface{}) []string {
	var found []string
	note := func(where string, obj map[string]interface{}, aliases map[string]string) {
		for alias, canonical := range aliases {
			if _, ok := obj[alias]; ok {
				found = append(found, fmt.Sprintf("%s: '%s' (use '%s')", where, alias, canonical))
			}
		}
	}
	var visitStep func(where string, step map[string]interface{})
	visitStep = func(where string, step map[string]interface{}) {
		note(where, step, stepFieldAliases)
		if cond, ok := step["condition"].(map[string]interface{}); ok {
			note(where+" condition", cond, conditionFieldAliases)
			for _, key := range []string{"expect", "expectNot", "expect_not"} {
				if expect, ok := cond[key].(map[string]interface{}); ok {
					note(where+" condition."+key, expect, expectationFieldAliases)
				}
			}
		}
		visitList := func(label string, list interface{}) {
			items, _ := list.([]interface{})
			for _, item := range items {
				if sub, ok := item.(map[string]interface{}); ok {
					visitStep(fmt.Sprintf("%s %s %v", where, label, sub["id"]), sub)
				}
			}
		}
		if forEach, ok := step["forEach"].(map[string]interface{}); ok {
			visitList("forEach", forEach[FieldSteps])
		}
		visitList("parallel", step["parallel"])
	}
	for _, key := range []string{FieldSteps, "onFailure"} {
		items, _ := args[key].([]interface{})
		for _, item := range items {
			if step, ok := item.(map[string]interface{}); ok {
				visitStep(fmt.Sprintf("%s %v", key, step["id"]), step)
			}
		}
	}
	sort.Strings(found)
	return found
}
