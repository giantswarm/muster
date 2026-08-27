package testing

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestRetryIsRejectedAtLoadTime covers the one step field the YAML schema
// accepted but the runner never executed.
//
// retry (count, delay, backoff_multiplier) was declared on TestStep and range
// checked by validateStep, and nothing else ever read it. A step declaring it
// got exactly one attempt while reading as if it polled, so scenarios that used
// it to wait for eventually-consistent state were flake-prone in precisely the
// place their authors thought they had covered.
//
// The YAML decode is not strict, so simply deleting the field would restore the
// silent pass: retry would be dropped during unmarshalling and the step would
// still get its single attempt, with nothing to tell the author. Keeping the
// field and rejecting it makes such a scenario fail with an explanation
// instead -- the same treatment status_code gets, for the same reason.
func TestRetryIsRejectedAtLoadTime(t *testing.T) {
	loader := &scenarioLoader{}

	step := TestStep{
		ID:       "polls-for-eventual-state",
		Tool:     "core_mcpserver_list",
		Expected: TestExpectation{Success: true},
		Retry:    map[string]interface{}{"count": 5, "delay": "1s"},
	}

	err := loader.validateStep(step, 0)
	if err == nil {
		t.Fatal("validateStep accepted retry; a scenario declaring it would get one attempt " +
			"while reading as if it polled")
	}
	if !containsText(err.Error(), "wait_for_state") {
		t.Errorf("the rejection should point at the supported alternative, got: %v", err)
	}

	step.Retry = nil
	if err := loader.validateStep(step, 0); err != nil {
		t.Errorf("a step without retry should load, got: %v", err)
	}
}

// TestEveryStepFieldIsActedOn is the structural regression guard for the class of
// bug retry was.
//
// types.go declaring a field and scenario_loader.go validating it prove only
// that a scenario may write the key -- not that anything happens when it does.
// retry had both and no more, which is exactly why it looked implemented from a
// scenario and from the loader tests while doing nothing.
//
// So every TestStep field must name a file that ACTS on it, and those two files
// do not count. The named file is then checked for a reference, so the
// accounting cannot go stale: deleting the code that honours a field fails here
// rather than silently weakening every scenario that sets it.
func TestEveryStepFieldIsActedOn(t *testing.T) {
	// actedOnIn maps a TestStep field to the file that does something with it,
	// beyond declaring and validating it.
	actedOnIn := map[string]string{
		"ID":          "test_runner.go", // keys the stored result for templating
		"Description": "test_reporter.go",
		"Tool":        "test_runner.go",
		"Args":        "test_runner.go",
		"Expected":    "test_runner.go",
		"Timeout":     "test_runner.go",
		"AsUser":      "test_runner.go",
	}

	// rejectedAtLoadTime holds the fields that are deliberately inert: accepted
	// by the decode only so that declaring them fails loudly. Each names the
	// test proving the rejection, so "inert on purpose" cannot quietly decay
	// into "ignored".
	rejectedAtLoadTime := map[string]string{
		"Retry": "see TestRetryIsRejectedAtLoadTime",
	}

	stepType := reflect.TypeOf(TestStep{})
	for i := 0; i < stepType.NumField(); i++ {
		field := stepType.Field(i).Name

		if reason, ok := rejectedAtLoadTime[field]; ok {
			t.Logf("TestStep.%s: rejected at load time; %s", field, reason)
			continue
		}

		file, ok := actedOnIn[field]
		if !ok {
			t.Errorf("TestStep.%s has no file recorded as acting on it.\n"+
				"Declaring a field in types.go and range checking it in scenario_loader.go does "+
				"not make it do anything -- that is all retry ever had, and a step declaring it "+
				"got exactly one attempt. Add the field here naming the file that honours it, or "+
				"reject it at load time and record it in rejectedAtLoadTime.", field)
			continue
		}

		source, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("TestStep.%s names %s, which cannot be read: %v", field, file, err)
			continue
		}
		if !strings.Contains(string(source), "step."+field) &&
			!strings.Contains(string(source), "Step."+field) {
			t.Errorf("TestStep.%s is recorded as acted on in %s, but that file never references "+
				"it. Either the code that honoured the field was removed -- in which case every "+
				"scenario setting it now passes vacuously -- or this accounting is stale.",
				field, file)
		}
	}
}
