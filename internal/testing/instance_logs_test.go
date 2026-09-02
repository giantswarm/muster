package testing

import (
	"strings"
	"testing"
)

// TestInstanceLogsRequiresAnAssertion covers the load-time rule for the
// scenario-level instance_logs block: declaring it with nothing to check would
// pass vacuously, the same failure mode status_code and retry had.
func TestInstanceLogsRequiresAnAssertion(t *testing.T) {
	loader := &scenarioLoader{}
	scenario := TestScenario{
		Name:     "logs-are-clean",
		Category: CategoryBehavioral,
		Concept:  ConceptMCPServer,
		Steps: []TestStep{{
			ID:       "list",
			Tool:     "core_mcpserver_list",
			Expected: TestExpectation{Success: true},
		}},
		InstanceLogs: &InstanceLogExpectation{},
	}

	err := loader.validateScenario(scenario, "logs-are-clean.yaml")
	if err == nil {
		t.Fatal("validateScenario accepted an empty instance_logs block")
	}
	if !strings.Contains(err.Error(), "instance_logs") {
		t.Errorf("the rejection should name the block, got: %v", err)
	}

	scenario.InstanceLogs.NotContains = []string{"eyJ"}
	if err := loader.validateScenario(scenario, "logs-are-clean.yaml"); err != nil {
		t.Errorf("a scenario with a not_contains assertion should load, got: %v", err)
	}
}

func TestValidateInstanceLogs(t *testing.T) {
	logs := &InstanceLogs{
		Stdout: "level=INFO msg=\"SSO: onAuthenticated callback\" session.userID=alice...\n" +
			"level=INFO msg=\"SSO: initSSOForSession called\" session=eyJhbGciOiJSUzI1NiJ9.secret-payload.sig\n",
		Stderr: "warning: something on stderr\n",
	}

	t.Run("nil expectation is a no-op", func(t *testing.T) {
		if err := validateInstanceLogs(nil, nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("no captured logs cannot satisfy an assertion", func(t *testing.T) {
		err := validateInstanceLogs(&InstanceLogExpectation{NotContains: []string{"eyJ"}}, nil)
		if err == nil {
			t.Fatal("an assertion against missing logs must not pass vacuously")
		}
	})

	t.Run("contains is checked across stdout and stderr", func(t *testing.T) {
		exp := &InstanceLogExpectation{Contains: []string{"onAuthenticated callback", "something on stderr"}}
		if err := validateInstanceLogs(exp, logs); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		exp.Contains = append(exp.Contains, "never logged")
		err := validateInstanceLogs(exp, logs)
		if err == nil || !strings.Contains(err.Error(), `do not contain "never logged"`) {
			t.Fatalf("missing substring should be reported, got: %v", err)
		}
	})

	t.Run("not_contains reports the line without echoing the match", func(t *testing.T) {
		err := validateInstanceLogs(&InstanceLogExpectation{NotContains: []string{"eyJ"}}, logs)
		if err == nil {
			t.Fatal("forbidden substring present but no error")
		}
		msg := err.Error()
		if !strings.Contains(msg, "on 1 line(s), first at line 2") {
			t.Errorf("the report should locate the match by line, got: %v", msg)
		}
		if !strings.Contains(msg, "initSSOForSession called") {
			t.Errorf("the report should quote the line up to the match, got: %v", msg)
		}
		if strings.Contains(msg, "secret-payload") {
			t.Errorf("the report must not echo what follows the match, got: %v", msg)
		}
	})

	t.Run("clean logs pass", func(t *testing.T) {
		clean := &InstanceLogs{Stdout: "level=INFO msg=ready\n"}
		if err := validateInstanceLogs(&InstanceLogExpectation{NotContains: []string{"eyJ"}}, clean); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
