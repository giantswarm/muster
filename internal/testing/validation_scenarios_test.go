package testing

import (
	"strings"
	"testing"
)

func TestValidateStepTestTools(t *testing.T) {
	if r := validateStep(TestStep{ID: "s", Tool: TestToolCreateUser}, nil, false, false); !r.Valid {
		t.Errorf("registered test tool rejected: %v", r.Errors)
	}
	if r := validateStep(TestStep{ID: "s", Tool: "test_not_registered"}, nil, false, false); r.Valid {
		t.Error("unregistered test_ tool accepted")
	}
}

// TestCommittedScenariosValidateAgainstSchema keeps the scenario suite and the
// committed schema.json in step: every core_* step of every scenario must name
// a tool the schema carries, with arguments it declares. It is what
// `muster test --validate-scenarios` checks, run by `make test`.
func TestCommittedScenariosValidateAgainstSchema(t *testing.T) {
	schema, err := LoadSchemaFromFile("../../schema.json")
	if err != nil {
		t.Fatalf("load schema: %v", err)
	}

	scenarios, err := NewTestScenarioLoader(false).LoadScenarios("scenarios")
	if err != nil {
		t.Fatalf("load scenarios: %v", err)
	}
	if len(scenarios) == 0 {
		t.Fatal("no scenarios loaded")
	}

	results := ValidateScenariosAgainstSchema(scenarios, schema, false, false)
	if results.TotalErrors == 0 {
		return
	}

	var details strings.Builder
	for _, sr := range results.ScenarioResults {
		for _, e := range sr.Errors {
			details.WriteString("\n  " + sr.ScenarioName + ": " + e.Type + ": " + e.Message)
			if details.Len() > 4000 {
				details.WriteString("\n  …")
				t.Fatalf("%d validation errors (regenerate with `muster test --generate-schema`):%s", results.TotalErrors, details.String())
			}
		}
	}
	t.Fatalf("%d validation errors (regenerate with `muster test --generate-schema`):%s", results.TotalErrors, details.String())
}
