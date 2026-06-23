package api

import (
	"strings"
	"testing"
)

func apiBoolPtr(b bool) *bool { return &b }

func TestAuthoringWarnings_DeprecatedStore(t *testing.T) {
	wf := &Workflow{
		Name: "demo",
		Steps: []WorkflowStep{
			{ID: "uses-store", Store: true},                            // deprecated
			{ID: "uses-output", Output: apiBoolPtr(true)},              // not deprecated
			{ID: "output-wins", Store: true, Output: apiBoolPtr(true)}, // output set => store ignored
			{ID: "neither"}, // not deprecated
			{ID: "loop", ForEach: &WorkflowForEach{Steps: []WorkflowSubStep{
				{ID: "sub-store", Store: true},
				{ID: "sub-output", Output: apiBoolPtr(false)},
			}}},
			{ID: "group", Parallel: []WorkflowSubStep{{ID: "par-store", Store: true}}},
		},
		OnFailure: []WorkflowSubStep{{ID: "cleanup", Store: true}},
	}

	warnings := AuthoringWarnings(wf)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly one warning, got %d: %v", len(warnings), warnings)
	}
	for _, id := range []string{"uses-store", "loop.forEach.sub-store", "group.parallel.par-store", "onFailure.cleanup"} {
		if !strings.Contains(warnings[0], id) {
			t.Errorf("expected deprecation warning to name %q, got: %s", id, warnings[0])
		}
	}
	// "uses-output" and "output-wins" both contain the substring "output"; assert
	// on word boundaries to confirm the bare IDs are not listed.
	for _, id := range []string{"uses-output,", "output-wins,", "neither,", "sub-output,"} {
		if strings.Contains(warnings[0]+",", id) {
			t.Errorf("did not expect deprecation warning to name %q, got: %s", id, warnings[0])
		}
	}
}

func TestAuthoringWarnings_OutputTemplateShadowsStepFlags(t *testing.T) {
	wf := &Workflow{
		Name: "demo",
		Steps: []WorkflowStep{
			{ID: "a", Output: apiBoolPtr(true)},
			{ID: "b", Store: true},
			{ID: "c"}, // no flag, not inert
		},
		Output: map[string]interface{}{"x": "{{ .results.a }}"},
	}

	warnings := AuthoringWarnings(wf)
	// Expect both the deprecated-store warning (for b) and the inert-flag warning.
	if len(warnings) != 2 {
		t.Fatalf("expected two warnings, got %d: %v", len(warnings), warnings)
	}

	var inert string
	for _, w := range warnings {
		if strings.Contains(w, "output' template") {
			inert = w
		}
	}
	if inert == "" {
		t.Fatalf("expected an inert-flag warning, got: %v", warnings)
	}
	for _, id := range []string{"a", "b"} {
		if !strings.Contains(inert, id) {
			t.Errorf("expected inert-flag warning to name %q, got: %s", id, inert)
		}
	}
}

func TestAuthoringWarnings_Clean(t *testing.T) {
	wf := &Workflow{Name: "demo", Steps: []WorkflowStep{{ID: "x", Output: apiBoolPtr(true)}}}
	if w := AuthoringWarnings(wf); len(w) != 0 {
		t.Errorf("expected no warnings, got: %v", w)
	}
	if w := AuthoringWarnings(nil); len(w) != 0 {
		t.Errorf("expected no warnings for nil workflow, got: %v", w)
	}
}
