package testing

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
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

	scenario.InstanceLogs = &InstanceLogExpectation{Occurrences: map[string]int{"Suspending MCPServer service x": 1}}
	if err := loader.validateScenario(scenario, "logs-are-clean.yaml"); err != nil {
		t.Errorf("a scenario with an occurrences assertion should load, got: %v", err)
	}

	scenario.InstanceLogs.Occurrences["Suspending MCPServer service x"] = -1
	err = loader.validateScenario(scenario, "logs-are-clean.yaml")
	if err == nil || !strings.Contains(err.Error(), "must not be negative") {
		t.Errorf("a negative occurrence count should be rejected at load time, got: %v", err)
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

	t.Run("occurrences counts lines across stdout and stderr", func(t *testing.T) {
		exp := &InstanceLogExpectation{Occurrences: map[string]int{
			"SSO: ":               1, // both stdout lines carry it: a miss
			"onAuthenticated":     1,
			"something on stderr": 1,
			"never logged":        0,
			"initSSOForSession":   2, // logged once: a miss
		}}
		err := validateInstanceLogs(exp, logs)
		if err == nil {
			t.Fatal("wrong occurrence counts must fail the scenario")
		}
		msg := err.Error()
		if !strings.Contains(msg, `contain "SSO: " on 2 line(s), want exactly 1`) {
			t.Errorf("a substring found too often should be reported with both counts, got: %v", msg)
		}
		if !strings.Contains(msg, `contain "initSSOForSession" on 1 line(s), want exactly 2`) {
			t.Errorf("a substring found too rarely should be reported with both counts, got: %v", msg)
		}
		for _, satisfied := range []string{`"onAuthenticated"`, `"something on stderr"`, `"never logged"`} {
			if strings.Contains(msg, satisfied) {
				t.Errorf("a satisfied count must not be reported, got: %v", msg)
			}
		}
		if strings.Index(msg, `"SSO: "`) > strings.Index(msg, `"initSSOForSession"`) {
			t.Errorf("misses should be reported in sorted order, got: %v", msg)
		}

		delete(exp.Occurrences, "SSO: ")
		exp.Occurrences["initSSOForSession"] = 1
		if err := validateInstanceLogs(exp, logs); err != nil {
			t.Fatalf("exact counts should pass, got: %v", err)
		}
	})

	t.Run("clean logs pass", func(t *testing.T) {
		clean := &InstanceLogs{Stdout: "level=INFO msg=ready\n"}
		if err := validateInstanceLogs(&InstanceLogExpectation{NotContains: []string{"eyJ"}}, clean); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestInstanceLogsPending(t *testing.T) {
	logs := &InstanceLogs{
		Stdout: "msg=\"Core catalogue rebuilt\" trigger=initial\n",
		Stderr: "msg=\"Core catalogue rebuilt\" trigger=age\nmsg=\"Core catalogue rebuilt\" trigger=age\n",
	}
	for name, tc := range map[string]struct {
		expected *InstanceLogExpectation
		logs     *InstanceLogs
		pending  bool
	}{
		"no captured logs":               {&InstanceLogExpectation{Contains: []string{"x"}}, nil, false},
		"contains not logged":            {&InstanceLogExpectation{Contains: []string{"trigger=invalidated"}}, logs, true},
		"contains logged":                {&InstanceLogExpectation{Contains: []string{"trigger=initial", "trigger=age"}}, logs, false},
		"count not reached":              {&InstanceLogExpectation{Occurrences: map[string]int{"trigger=age": 3}}, logs, true},
		"count reached":                  {&InstanceLogExpectation{Occurrences: map[string]int{"trigger=age": 2}}, logs, false},
		"count exceeded is final":        {&InstanceLogExpectation{Occurrences: map[string]int{"trigger=age": 1}}, logs, false},
		"not_contains hit is final":      {&InstanceLogExpectation{NotContains: []string{"trigger=age"}}, logs, false},
		"a zero count waits for nothing": {&InstanceLogExpectation{Occurrences: map[string]int{"trigger=invalidated": 0}}, logs, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := instanceLogsPending(tc.expected, tc.logs); got != tc.pending {
				t.Errorf("instanceLogsPending = %t, want %t", got, tc.pending)
			}
		})
	}
}

// TestAwaitInstanceLogs covers a line the instance logs after the last step
// answered -- the core catalogue's background rebuild -- which a single read
// right after the step misses.
func TestAwaitInstanceLogs(t *testing.T) {
	lc := newLogCapture()
	t.Cleanup(lc.close)
	instance := &MusterInstance{ID: "instance"}
	r := &testRunner{instanceManager: &musterInstanceManager{
		processes: map[string]*managedProcess{instance.ID: {logCapture: lc}},
	}}
	expected := &InstanceLogExpectation{Occurrences: map[string]int{"trigger=age": 1}}

	result := &TestScenarioResult{}
	r.collectInstanceLogs(instance, result)
	if err := validateInstanceLogs(expected, result.InstanceLogs); err == nil {
		t.Fatal("the read before the rebuild logged should miss it")
	}

	if _, err := io.WriteString(lc.stderrWriter, "msg=\"Core catalogue rebuilt\" trigger=age\n"); err != nil {
		t.Fatal(err)
	}
	r.awaitInstanceLogs(context.Background(), expected, instance, result)
	if err := validateInstanceLogs(expected, result.InstanceLogs); err != nil {
		t.Fatalf("awaitInstanceLogs should have read the line logged after the step: %v", err)
	}

	// An expectation that failed for good does not wait out the settle time.
	final := &InstanceLogExpectation{NotContains: []string{"trigger=age"}}
	start := time.Now()
	r.awaitInstanceLogs(context.Background(), final, instance, result)
	if waited := time.Since(start); waited >= instanceLogsSettle {
		t.Errorf("a not_contains hit waited %s", waited)
	}
}
