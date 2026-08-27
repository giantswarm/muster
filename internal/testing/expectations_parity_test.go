package testing

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// mcpResultOf builds the MCP-shaped response for a payload, the way an MCP tool
// step receives it: the JSON payload inside a text content block.
func mcpResultOf(t *testing.T, payload map[string]interface{}, isError bool) *mcp.CallToolResult {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshalling payload: %v", err)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(string(encoded))},
		IsError: isError,
	}
}

// expectationKindCase proves that one expectation kind is actually enforced, on
// both response shapes.
type expectationKindCase struct {
	// field is the TestExpectation field name this case covers.
	field string
	// expected declares the expectation under test.
	expected TestExpectation
	// payload is a response payload that violates it.
	payload map[string]interface{}
	// isError is the tool-level error flag the violating response carries.
	isError bool
}

// TestEveryExpectationKindIsEnforcedOnBothStepKinds is the structural regression
// guard for #1038.
//
// The framework has two response shapes -- *mcp.CallToolResult for MCP tool
// steps and a plain map for test_* steps -- and used to carry one copy of the
// expectation checks per shape. The copies drifted, so json_path, not_contains
// and wait_for_state were each honoured on one path and silently ignored on the
// other. A scenario declaring one on the wrong kind of step then passed
// regardless of the response, which is how a real session-isolation leak sat
// undetected behind a green suite.
//
// Each case declares an expectation plus a response that violates it, and
// asserts that BOTH entry points reject it. A kind dropped on one path fails
// here instead of silently weakening every scenario that uses it.
//
// The completeness check at the end fails when a field is added to
// TestExpectation without being accounted for, so a new expectation kind cannot
// be merged unenforced.
func TestEveryExpectationKindIsEnforcedOnBothStepKinds(t *testing.T) {
	cases := []expectationKindCase{
		{
			field:    "Success",
			expected: TestExpectation{Success: true},
			payload:  map[string]interface{}{"message": "boom"},
			isError:  true,
		},
		{
			field:    "ErrorContains",
			expected: TestExpectation{Success: false, ErrorContains: []string{"quota exceeded"}},
			payload:  map[string]interface{}{"message": "permission denied"},
			isError:  true,
		},
		{
			field:    "Contains",
			expected: TestExpectation{Success: true, Contains: []string{"x_server-beta_beta_tool"}},
			payload:  map[string]interface{}{"success": true, "tools": []string{"x_server-alpha_alpha_tool"}},
		},
		{
			field:    "NotContains",
			expected: TestExpectation{Success: true, NotContains: []string{"x_server-alpha_alpha_tool"}},
			payload:  map[string]interface{}{"success": true, "tools": []string{"x_server-alpha_alpha_tool"}},
		},
		{
			field:    "JSONPath",
			expected: TestExpectation{Success: true, JSONPath: map[string]interface{}{"state": "Connected"}},
			payload:  map[string]interface{}{"success": true, "state": "Failed"},
		},
	}

	runner := &testRunner{logger: NewSilentLogger(false, false)}
	covered := map[string]bool{}

	for _, tc := range cases {
		covered[tc.field] = true

		t.Run(tc.field+"/mcp_step", func(t *testing.T) {
			result := mcpResultOf(t, tc.payload, tc.isError)
			if runner.validateExpectations(tc.expected, result, nil, runner.logger) {
				t.Errorf("%s is not enforced for MCP tool steps: a violating response passed", tc.field)
			}
		})

		t.Run(tc.field+"/test_tool_step", func(t *testing.T) {
			response := map[string]interface{}{}
			for k, v := range tc.payload {
				response[k] = v
			}
			if tc.isError {
				response["isError"] = true
			}
			if runner.validateTestToolExpectations(tc.expected, response, nil, runner.logger) {
				t.Errorf("%s is not enforced for test_* steps: a violating response passed", tc.field)
			}
		})
	}

	// Kinds that are deliberately not assertions evaluated by checkExpectations.
	// Each names the test proving it is enforced elsewhere, so "accounted for"
	// cannot quietly degrade into "ignored".
	enforcedElsewhere := map[string]string{
		"WaitForState": "retry policy applied by the polling wrappers; see TestWaitForStatePollsBothStepKinds",
		"StatusCode":   "rejected at load time; see TestStatusCodeIsRejectedAtLoadTime",
	}

	expectationType := reflect.TypeOf(TestExpectation{})
	for i := 0; i < expectationType.NumField(); i++ {
		field := expectationType.Field(i).Name
		if covered[field] {
			continue
		}
		if reason, ok := enforcedElsewhere[field]; ok {
			t.Logf("%s: %s", field, reason)
			continue
		}
		t.Errorf("TestExpectation.%s has no enforcement case.\n"+
			"Every expectation kind the framework accepts must be enforced, or a scenario "+
			"declaring it passes vacuously (#1038). Add a case to this test, or record it in "+
			"enforcedElsewhere naming the test that covers it.", field)
	}
}

// TestBothStepKindsAgreeOnTheSamePayload pins the parity itself: for one payload
// and one expectation, the two entry points must reach the same verdict.
//
// The per-kind test above catches a kind that is ignored outright. This catches
// the subtler drift -- the same assertion meaning different things depending on
// which kind of step a scenario author happened to write it on.
func TestBothStepKindsAgreeOnTheSamePayload(t *testing.T) {
	payload := map[string]interface{}{
		"success":    true,
		"tool_count": 2,
		"state":      "Connected",
		"tools":      []string{"core_auth_login", "x_server-alpha_alpha_tool"},
	}

	expectations := []struct {
		name     string
		expected TestExpectation
	}{
		{"contains present", TestExpectation{Success: true, Contains: []string{"core_auth_login"}}},
		{"contains absent", TestExpectation{Success: true, Contains: []string{"x_server-beta"}}},
		{"not_contains present", TestExpectation{Success: true, NotContains: []string{"x_server-alpha"}}},
		{"not_contains absent", TestExpectation{Success: true, NotContains: []string{"x_server-beta"}}},
		{"json_path match", TestExpectation{Success: true, JSONPath: map[string]interface{}{"state": "Connected"}}},
		{"json_path mismatch", TestExpectation{Success: true, JSONPath: map[string]interface{}{"state": "Failed"}}},
		{"json_path missing key", TestExpectation{Success: true, JSONPath: map[string]interface{}{"absent": "x"}}},
		{"combined", TestExpectation{
			Success:     true,
			Contains:    []string{"core_auth_login"},
			NotContains: []string{"x_server-beta"},
			JSONPath:    map[string]interface{}{"tool_count": 2},
		}},
	}

	runner := &testRunner{logger: NewSilentLogger(false, false)}

	for _, tc := range expectations {
		t.Run(tc.name, func(t *testing.T) {
			viaMCP := runner.validateExpectations(tc.expected, mcpResultOf(t, payload, false), nil, runner.logger)
			viaTestTool := runner.validateTestToolExpectations(tc.expected, payload, nil, runner.logger)

			if viaMCP != viaTestTool {
				t.Errorf("the two step kinds disagree on the same payload: MCP=%v, test_*=%v.\n"+
					"An expectation must mean the same thing whichever kind of step it is written on.",
					viaMCP, viaTestTool)
			}
		})
	}
}

// TestStatusCodeIsRejectedAtLoadTime covers the one expectation kind the YAML
// schema accepts but neither response shape can carry.
//
// The YAML decode is not strict, so simply deleting the field would restore the
// silent pass: status_code would be dropped during unmarshalling and the step
// would assert nothing. Keeping the field and rejecting it at load time makes
// such a scenario fail with an explanation instead.
func TestStatusCodeIsRejectedAtLoadTime(t *testing.T) {
	loader := &scenarioLoader{}

	step := TestStep{
		ID:       "expects-an-http-status",
		Tool:     "core_service_status",
		Expected: TestExpectation{Success: true, StatusCode: 200},
	}

	err := loader.validateStep(step, 0)
	if err == nil {
		t.Fatal("validateStep accepted status_code; a scenario declaring it would assert nothing")
	}
	if !containsText(err.Error(), "json_path") {
		t.Errorf("the rejection should point at the supported alternative, got: %v", err)
	}

	step.Expected.StatusCode = 0
	if err := loader.validateStep(step, 0); err != nil {
		t.Errorf("a step without status_code should load, got: %v", err)
	}
}

// TestWaitForStatePollsBothStepKinds covers wait_for_state, which is a retry
// policy rather than an assertion: it re-invokes the tool until the other
// expectations hold. It was implemented for MCP steps long before test_* steps,
// which is the other half of #1038.
//
// Both cases return a response that fails the expectation on the first call and
// satisfies it later, so a path that does not poll fails the test.
func TestWaitForStatePollsBothStepKinds(t *testing.T) {
	expectedEventually := TestExpectation{
		Success:      true,
		JSONPath:     map[string]interface{}{"state": "Connected"},
		WaitForState: 10 * time.Second,
	}

	// settlesOnCall reports Failed until the given call number, then Connected.
	settlesOnCall := func(calls *int, settleAt int) map[string]interface{} {
		*calls++
		state := "Failed"
		if *calls >= settleAt {
			state = "Connected"
		}
		return map[string]interface{}{"success": true, "state": state}
	}

	t.Run("mcp_step", func(t *testing.T) {
		runner := &testRunner{logger: NewSilentLogger(false, false)}
		calls := 0
		client := &pollingStubClient{onCall: func() (interface{}, error) {
			return mcpResultOf(t, settlesOnCall(&calls, 2), false), nil
		}}

		first, err := client.CallTool(context.Background(), "core_mcpserver_get", nil)
		if err != nil {
			t.Fatalf("priming call: %v", err)
		}

		ok := runner.validateExpectationsWithClient(
			context.Background(), expectedEventually, first, nil, client,
			"core_mcpserver_get", nil, runner.logger,
		)
		if !ok {
			t.Error("wait_for_state did not poll for MCP tool steps")
		}
		if calls < 2 {
			t.Errorf("expected the tool to be re-invoked, got %d call(s)", calls)
		}
	})

	t.Run("test_tool_step", func(t *testing.T) {
		runner := &testRunner{logger: NewSilentLogger(false, false)}
		calls := 0
		invoker := &pollingStubInvoker{onCall: func() (interface{}, error) {
			return settlesOnCall(&calls, 2), nil
		}}

		step := TestStep{ID: "waits", Tool: "test_scrape_metrics", Expected: expectedEventually}
		response, err := runner.callTestToolWithWait(context.Background(), invoker, step, nil, runner.logger)
		if err != nil {
			t.Fatalf("callTestToolWithWait: %v", err)
		}
		if !runner.validateTestToolExpectations(expectedEventually, response, nil, runner.logger) {
			t.Error("wait_for_state did not poll for test_* steps: the step was judged on " +
				"its first response only")
		}
		if calls < 2 {
			t.Errorf("expected the test tool to be re-invoked, got %d call(s)", calls)
		}
	})
}

// TestWaitForStateAcceptsAFirstResponseThatAlreadyPasses is the other half of
// wait_for_state parity: polling must not throw away a result that already
// satisfies the expectations.
//
// The MCP path used to ignore the response its caller had just obtained and
// judge the step only from the first tick onward. A step whose first call
// passed was re-invoked and judged on the second result instead -- a wasted
// poll interval on every such step, and an outright failure when re-invoking
// does not produce the same answer, reported against the first (passing)
// response so the output showed a passing payload on a failed step. The
// test_* path never had this bug, which is the asymmetry #1038 is about.
//
// Both cases return a passing first response and a failing one thereafter, so
// a path that polls regardless fails the test instead of passing by luck.
func TestWaitForStateAcceptsAFirstResponseThatAlreadyPasses(t *testing.T) {
	expected := TestExpectation{
		Success:      true,
		JSONPath:     map[string]interface{}{"state": "Connected"},
		WaitForState: 10 * time.Second,
	}
	passing := map[string]interface{}{"success": true, "state": "Connected"}
	failing := map[string]interface{}{"success": true, "state": "Failed"}

	// The MCP caller invokes the tool itself and passes the response in, so any
	// call the stub sees is a re-invocation.
	t.Run("mcp_step", func(t *testing.T) {
		runner := &testRunner{logger: NewSilentLogger(false, false)}
		reinvocations := 0
		client := &pollingStubClient{onCall: func() (interface{}, error) {
			reinvocations++
			return mcpResultOf(t, failing, false), nil
		}}

		ok := runner.validateExpectationsWithClient(
			context.Background(), expected, mcpResultOf(t, passing, false), nil, client,
			"core_mcpserver_get", nil, runner.logger,
		)
		if !ok {
			t.Error("wait_for_state rejected a first response that already met the expectations")
		}
		if reinvocations != 0 {
			t.Errorf("the tool was re-invoked %d time(s) after its first response already passed",
				reinvocations)
		}
	})

	// callTestToolWithWait makes the first call itself, so exactly one is right.
	t.Run("test_tool_step", func(t *testing.T) {
		runner := &testRunner{logger: NewSilentLogger(false, false)}
		calls := 0
		invoker := &pollingStubInvoker{onCall: func() (interface{}, error) {
			calls++
			if calls == 1 {
				return passing, nil
			}
			return failing, nil
		}}

		step := TestStep{ID: "already-connected", Tool: "test_scrape_metrics", Expected: expected}
		response, err := runner.callTestToolWithWait(context.Background(), invoker, step, nil, runner.logger)
		if err != nil {
			t.Fatalf("callTestToolWithWait: %v", err)
		}
		if !runner.validateTestToolExpectations(expected, response, nil, runner.logger) {
			t.Error("wait_for_state discarded a first response that already met the expectations")
		}
		if calls != 1 {
			t.Errorf("expected exactly one invocation, got %d", calls)
		}
	})
}

// pollingStubClient is an MCPTestClient that returns a scripted response per
// call, for exercising the MCP wait_for_state polling loop. Only CallTool is
// reached; the embedded interface satisfies the rest.
type pollingStubClient struct {
	MCPTestClient
	onCall func() (interface{}, error)
}

func (c *pollingStubClient) CallTool(_ context.Context, _ string, _ map[string]interface{}) (interface{}, error) {
	return c.onCall()
}

// pollingStubInvoker is a testToolInvoker that returns a scripted response per
// call, for exercising the test-tool wait_for_state polling loop.
type pollingStubInvoker struct {
	onCall func() (interface{}, error)
}

func (i *pollingStubInvoker) HandleTestTool(_ context.Context, _ string, _ map[string]interface{}) (interface{}, error) {
	return i.onCall()
}
