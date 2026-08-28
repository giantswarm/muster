package testing

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv" // Added for strconv.Atoi
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// testRunner implements the TestRunner interface
// defaultStartupParallel bounds how many scenarios may be in their
// instance-startup phase (CreateInstance + WaitForReady) at the same time.
// Starting all --parallel instances at t=0 is the measured mechanism behind
// the intermittent CI readiness timeouts of giantswarm/muster#1101: a herd of
// 50 cold-starting Go processes is fine on >=4 dedicated cores (suite in ~4s)
// but produces 15s+ readiness stragglers and scenario-timeout kills at the
// 1-2 effective cores a contended CI container actually delivers. Eight is
// measured to hold 10/10 clean at 2 effective cores (half a CI large's
// nominal 4 vCPUs) where sixteen still failed 1 run in 8; the cost is ~4s of
// startup serialization on an unconstrained 24-core box (pass a negative
// --startup-parallel to disable). A fixed constant is deliberate: containers
// on shares-based CI report the HOST's core count, so any NumCPU-derived
// bound would silently disable itself exactly where it matters. Steady-state
// scenario parallelism is unaffected.
const defaultStartupParallel = 8

type testRunner struct {
	client          MCPTestClient
	loader          TestScenarioLoader
	reporter        TestReporter
	instanceManager MusterInstanceManager
	debug           bool
	logger          TestLogger

	// startupSem, when non-nil, is the counting semaphore implementing the
	// startup bound above. Sized once per Run from the configuration.
	startupSem chan struct{}
}

// NewTestRunnerWithLogger creates a new test runner with custom logger
func NewTestRunnerWithLogger(client MCPTestClient, loader TestScenarioLoader, reporter TestReporter, instanceManager MusterInstanceManager, debug bool, logger TestLogger) TestRunner {
	return &testRunner{
		client:          client,
		loader:          loader,
		reporter:        reporter,
		instanceManager: instanceManager,
		debug:           debug,
		logger:          logger,
	}
}

// Run executes test scenarios according to the configuration
func (r *testRunner) Run(ctx context.Context, config TestConfiguration, scenarios []TestScenario) (*TestSuiteResult, error) {
	// Create the test suite result
	result := &TestSuiteResult{
		StartTime:       time.Now(),
		TotalScenarios:  len(scenarios),
		ScenarioResults: make([]TestScenarioResult, 0, len(scenarios)),
		Configuration:   config,
	}

	// Report test start
	r.reporter.ReportStart(config)

	// Filter scenarios based on configuration
	filteredScenarios := r.loader.FilterScenarios(scenarios, config)
	result.TotalScenarios = len(filteredScenarios)

	// Check if a specific scenario was requested but not found
	if len(filteredScenarios) == 0 && config.Scenario != "" {
		// Get available scenario names for helpful error message
		var availableScenarios []string
		for _, scenario := range scenarios {
			availableScenarios = append(availableScenarios, scenario.Name)
		}

		// Check if the user accidentally added .yaml extension
		scenarioWithoutExt := strings.TrimSuffix(config.Scenario, ".yaml")
		scenarioWithoutExt = strings.TrimSuffix(scenarioWithoutExt, ".yml")

		// Check if the scenario exists without the extension
		var suggestions []string
		for _, name := range availableScenarios {
			if name == scenarioWithoutExt {
				suggestions = append(suggestions, fmt.Sprintf("Did you mean '%s' instead of '%s'?", name, config.Scenario))
			}
		}

		// If no direct match, look for similar names
		if len(suggestions) == 0 {
			for _, name := range availableScenarios {
				if strings.Contains(name, scenarioWithoutExt) || strings.Contains(scenarioWithoutExt, name) {
					suggestions = append(suggestions, name)
				}
			}
		}

		errorMsg := fmt.Sprintf("scenario '%s' not found", config.Scenario)
		if len(suggestions) > 0 {
			if len(suggestions) == 1 && strings.HasPrefix(suggestions[0], "Did you mean") {
				errorMsg += fmt.Sprintf("\n%s", suggestions[0])
			} else {
				errorMsg += "\n\nSimilar scenarios found:\n"
				for _, suggestion := range suggestions {
					errorMsg += fmt.Sprintf("  • %s\n", suggestion)
				}
			}
		} else {
			errorMsg += "\n\nAvailable scenarios:\n"
			for _, name := range availableScenarios {
				errorMsg += fmt.Sprintf("  • %s\n", name)
			}
		}

		return result, fmt.Errorf("%s", errorMsg)
	}

	if len(filteredScenarios) == 0 {
		r.reporter.ReportSuiteResult(*result)
		return result, nil
	}

	// Bound concurrent instance startups (see defaultStartupParallel). Zero
	// means "use the default"; a negative value disables the bound.
	startupParallel := config.StartupParallel
	if startupParallel == 0 {
		startupParallel = defaultStartupParallel
	}
	r.startupSem = nil
	if startupParallel > 0 && config.Parallel > startupParallel {
		r.startupSem = make(chan struct{}, startupParallel)
	}

	// Execute scenarios based on parallel configuration
	// Each scenario now manages its own muster instance
	if config.Parallel <= 1 {
		// Sequential execution
		r.reporter.SetParallelMode(false)
		for _, scenario := range filteredScenarios {
			scenarioResult := r.runScenario(ctx, scenario, config)
			result.ScenarioResults = append(result.ScenarioResults, scenarioResult)

			// Update counters
			r.updateCounters(result, scenarioResult)

			// Report individual scenario result
			r.reporter.ReportScenarioResult(scenarioResult)

			// Check fail-fast
			if config.FailFast && scenarioResult.Result == ResultFailed {
				break
			}
		}
	} else {
		// Parallel execution
		r.reporter.SetParallelMode(true)
		results := r.runScenariosParallel(ctx, filteredScenarios, config, result)
		result.ScenarioResults = results
	}

	// Finalize result
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(result.StartTime)

	// Report final suite result
	r.reporter.ReportSuiteResult(*result)

	return result, nil
}

// runScenariosParallel executes scenarios in parallel with a worker pool
// Each scenario gets its own muster instance
func (r *testRunner) runScenariosParallel(ctx context.Context, scenarios []TestScenario, config TestConfiguration, suiteResult *TestSuiteResult) []TestScenarioResult {
	// Create channels
	scenarioChan := make(chan TestScenario, len(scenarios))
	resultChan := make(chan TestScenarioResult, len(scenarios))

	// Send scenarios to channel
	for _, scenario := range scenarios {
		scenarioChan <- scenario
	}
	close(scenarioChan)

	// Create worker pool
	var wg sync.WaitGroup
	numWorkers := config.Parallel
	if numWorkers > len(scenarios) {
		numWorkers = len(scenarios)
	}

	// Start workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for scenario := range scenarioChan {
				if r.debug {
					r.logger.Debug("🔄 Worker %d executing scenario: %s\n", workerID, scenario.Name)
				}

				// Each worker runs scenario with its own muster instance
				scenarioResult := r.runScenario(ctx, scenario, config)
				resultChan <- scenarioResult
			}
		}(i)
	}

	// Wait for all workers to complete
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results and handle fail-fast in main thread
	var results []TestScenarioResult
	expectedResults := len(scenarios)

	for result := range resultChan {
		results = append(results, result)

		// 🚀 REAL-TIME: Report result immediately as it comes in
		r.updateCounters(suiteResult, result)
		r.reporter.ReportScenarioResult(result)

		// Handle fail-fast by breaking out of collection loop
		// This allows workers to finish naturally without deadlocking
		if config.FailFast && result.Result == ResultFailed {
			if r.debug {
				r.logger.Debug("🛑 Fail-fast triggered by scenario: %s\n", result.Scenario.Name)
			}
			break
		}
	}

	// If we broke early due to fail-fast, continue collecting remaining results
	// but don't process them (just let workers finish cleanly)
	if len(results) < expectedResults {
		for result := range resultChan {
			// Collect remaining results but don't report them in fail-fast mode
			results = append(results, result)
			if r.debug {
				r.logger.Debug("📋 Collected remaining result: %s (not reported due to fail-fast)\n", result.Scenario.Name)
			}
		}
	}

	return results
}

// collectInstanceLogs collects logs from an muster instance and stores them in the result
func (r *testRunner) collectInstanceLogs(instance *MusterInstance, result *TestScenarioResult) {
	if instance == nil {
		return
	}

	// Get the managed process to collect logs
	if manager, ok := r.instanceManager.(*musterInstanceManager); ok {
		manager.mu.RLock()
		if managedProc, exists := manager.processes[instance.ID]; exists && managedProc != nil && managedProc.logCapture != nil {
			// Get logs without closing the capture yet (defer will handle that)
			instance.Logs = managedProc.logCapture.getLogs()
			result.InstanceLogs = instance.Logs
			if r.debug {
				r.logger.Debug("📋 Collected instance logs for result: stdout=%d chars, stderr=%d chars\n",
					len(instance.Logs.Stdout), len(instance.Logs.Stderr))
			}
		}
		manager.mu.RUnlock()
	}
}

// runScenario executes a single test scenario with template variable support
func (r *testRunner) runScenario(ctx context.Context, scenario TestScenario, config TestConfiguration) TestScenarioResult {
	result := TestScenarioResult{
		Scenario:    scenario,
		StartTime:   time.Now(),
		StepResults: make([]TestStepResult, 0, len(scenario.Steps)),
		Result:      ResultPassed,
	}

	// Report scenario start
	r.reporter.ReportScenarioStart(scenario)

	// Create a prefixed logger for this scenario when running in parallel with verbose/debug
	// This helps distinguish which log messages belong to which scenario
	logger := r.logger
	if config.Parallel > 1 && (r.debug || logger.IsVerboseEnabled()) {
		prefix := GenerateScenarioPrefix(scenario.Name)
		logger = NewPrefixedLogger(r.logger, prefix)
	}

	// Create scenario context for template variable support
	scenarioContext := NewScenarioContext()

	// Take a startup slot BEFORE the scenario timeout starts ticking: a
	// scenario queued behind other startups must not burn its step budget
	// waiting, or slow startups get killed mid-boot by their own timeout.
	releaseStartup := func() {}
	if r.startupSem != nil {
		select {
		case r.startupSem <- struct{}{}:
			var once sync.Once
			releaseStartup = func() { once.Do(func() { <-r.startupSem }) }
		case <-ctx.Done():
			result.Result = ResultError
			result.Error = "test run canceled while waiting for an instance startup slot"
			result.EndTime = time.Now()
			result.Duration = result.EndTime.Sub(result.StartTime)
			return result
		}
	}
	defer releaseStartup()

	// Apply scenario timeout if specified
	scenarioCtx := ctx
	if scenario.Timeout > 0 {
		var cancel context.CancelFunc
		scenarioCtx, cancel = context.WithTimeout(ctx, scenario.Timeout)
		defer cancel()
	}

	// Create and start muster instance for this scenario
	var instance *MusterInstance
	var err error

	if r.debug {
		logger.Debug("🏗️  Creating muster instance for scenario: %s\n", scenario.Name)
	}

	instance, err = r.instanceManager.CreateInstance(scenarioCtx, scenario.Name, scenario.PreConfiguration, logger)
	if err != nil {
		result.Result = ResultError
		result.Error = fmt.Sprintf("failed to create muster instance: %v", err)
		result.EndTime = time.Now()
		result.Duration = result.EndTime.Sub(result.StartTime)

		r.collectInstanceLogs(instance, &result)

		return result
	}

	if r.debug {
		logger.Debug("✅ Created muster instance %s (port: %d)\n", instance.ID, instance.Port)
	}

	// Ensure cleanup of instance
	defer func() {
		if instance != nil {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if err := r.instanceManager.DestroyInstance(cleanupCtx, instance, logger); err != nil {
				if r.debug {
					logger.Debug("⚠️  Failed to destroy muster instance %s: %v\n", instance.ID, err)
				}
			} else {
				// Final log storage - may have been updated during destruction
				if instance.Logs != nil && result.InstanceLogs == nil {
					result.InstanceLogs = instance.Logs
				}
				if r.debug {
					logger.Debug("✅ Cleanup complete for muster instance %s\n", instance.ID)
				}
			}
		}
	}()

	// Wait for instance to be ready
	if err := r.instanceManager.WaitForReady(scenarioCtx, instance, logger); err != nil {
		result.Result = ResultError
		result.Error = fmt.Sprintf("muster instance not ready: %v", err)
		result.EndTime = time.Now()
		result.Duration = result.EndTime.Sub(result.StartTime)

		r.collectInstanceLogs(instance, &result)

		return result
	}

	// Startup is done — hand the slot to the next queued scenario. Step
	// execution runs at full --parallel width.
	releaseStartup()

	// Create isolated MCP client for this scenario
	// This ensures each parallel scenario has its own client and context
	scenarioClient := NewMCPTestClientWithLogger(r.debug, logger)

	// Connect the isolated MCP client to this specific instance
	// Use authenticated connection if muster's OAuth server is enabled
	var connectErr error
	if instance.MusterOAuthAccessToken != "" {
		if r.debug {
			logger.Debug("🔐 Connecting with OAuth token (muster OAuth server enabled)\n")
		}
		connectErr = scenarioClient.ConnectWithAuth(scenarioCtx, instance.Endpoint, instance.MusterOAuthAccessToken)
	} else {
		connectErr = scenarioClient.Connect(scenarioCtx, instance.Endpoint)
	}
	if connectErr != nil {
		result.Result = ResultError
		result.Error = fmt.Sprintf("failed to connect to muster instance: %v", connectErr)
		result.EndTime = time.Now()
		result.Duration = result.EndTime.Sub(result.StartTime)

		r.collectInstanceLogs(instance, &result)

		return result
	}

	// Ensure isolated MCP client is closed properly
	defer func() {
		if r.debug {
			logger.Debug("🔌 Closing isolated MCP client connection to %s\n", instance.Endpoint)
		}

		// Close with timeout to avoid hanging
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()

		done := make(chan struct{})
		go func() {
			_ = scenarioClient.Close()
			close(done)
		}()

		// Waiting on done is the actual synchronization: scenarioClient.Close()
		// closes the connection synchronously, so once done is signaled the
		// teardown is complete. No blind delay is needed afterwards.
		select {
		case <-done:
			if r.debug {
				logger.Debug("✅ Isolated MCP client closed successfully\n")
			}
		case <-closeCtx.Done():
			if r.debug {
				logger.Debug("⏰ Isolated MCP client close timeout - connection may have been reset\n")
			}
		}
	}()

	if r.debug {
		logger.Debug("✅ Connected isolated MCP client to muster instance %s at %s\n", instance.ID, instance.Endpoint)
	}

	// Create test tools handler for this scenario
	testToolsHandler := NewTestToolsHandler(r.instanceManager, instance, r.debug, logger)

	// Pass the MCP client to the test tools handler so it can call authenticate tools
	testToolsHandler.SetMCPClient(scenarioClient)

	// Ensure cleanup of additional user clients created during multi-user testing
	defer func() {
		if testToolsHandler != nil {
			testToolsHandler.CloseAllUserClients()
		}
	}()

	// Execute steps using the isolated client
	for _, step := range scenario.Steps {
		stepResult := r.runStepWithTestTools(scenarioCtx, step, config, scenarioClient, scenarioContext, testToolsHandler, logger)
		result.StepResults = append(result.StepResults, stepResult)

		// Report step result
		r.reporter.ReportStepResult(stepResult)

		// Check if step failed
		if stepResult.Result == ResultFailed || stepResult.Result == ResultError {
			result.Result = stepResult.Result
			result.Error = stepResult.Error
			break
		}
	}

	// Execute cleanup steps regardless of main scenario outcome using the isolated client
	if len(scenario.Cleanup) > 0 {
		for _, cleanupStep := range scenario.Cleanup {
			stepResult := r.runStepWithTestTools(scenarioCtx, cleanupStep, config, scenarioClient, scenarioContext, testToolsHandler, logger)
			result.StepResults = append(result.StepResults, stepResult)
			r.reporter.ReportStepResult(stepResult)

			// Cleanup step failures should also fail the scenario
			if stepResult.Result == ResultFailed || stepResult.Result == ResultError {
				// Only update if the scenario hasn't already failed
				if result.Result == ResultPassed {
					result.Result = stepResult.Result
					result.Error = stepResult.Error
				}
			}
		}
	}

	// Finalize result - collect instance logs before ending
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(result.StartTime)

	// A failing step against a dead instance is a different bug class than a
	// failing step against a live one. Surface an unexpected instance death
	// prominently so CI failures are diagnosable from the summary line alone.
	if result.Result != ResultPassed {
		if exited, waitErr := r.instanceManager.InstanceExitStatus(instance); exited {
			death := fmt.Sprintf("muster instance process died mid-scenario (wait result: %v)", waitErr)
			if result.Error != "" {
				result.Error = death + "; " + result.Error
			} else {
				result.Error = death
			}
		}
	}

	// Collect instance logs by triggering the destroy process early
	// The defer cleanup will handle the actual cleanup, but we need logs now
	r.collectInstanceLogs(instance, &result)

	return result
}

// runStep executes a single test step using the specified MCP client with template variable support
func (r *testRunner) runStep(ctx context.Context, step TestStep, config TestConfiguration, client MCPTestClient, scenarioContext *ScenarioContext, logger TestLogger) TestStepResult {
	result := TestStepResult{
		Step:      step,
		StartTime: time.Now(),
		Result:    ResultPassed,
	}

	// Apply step timeout if specified
	stepCtx := ctx
	if step.Timeout > 0 {
		var cancel context.CancelFunc
		stepCtx, cancel = context.WithTimeout(ctx, step.Timeout)
		defer cancel()
	}

	// Resolve template variables in step arguments if scenario context is available
	resolvedArgs := step.Args
	if scenarioContext != nil {
		processor := NewTemplateProcessor(scenarioContext)

		var err error
		resolvedArgs, err = processor.ResolveArgs(step.Args)
		if err != nil {
			result.Result = ResultError
			result.Error = fmt.Sprintf("template resolution failed: %v", err)
			result.EndTime = time.Now()
			result.Duration = result.EndTime.Sub(result.StartTime)
			return result
		}

		if r.debug {
			logger.Debug("🔧 Step %s: Template resolution completed\n", step.ID)
		}
	}

	// Execute the tool call with resolved arguments
	response, err := client.CallTool(stepCtx, step.Tool, resolvedArgs)

	// Store response (even if there's an error)
	result.Response = response
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(result.StartTime)

	// Validate expectations (always check, even with errors - they might be expected)
	if !r.validateExpectationsWithClient(stepCtx, step.Expected, response, err, client, step.Tool, resolvedArgs, logger) {
		if err != nil {
			result.Result = ResultError
			result.Error = fmt.Sprintf("tool call failed: %v", err)
		} else {
			result.Result = ResultFailed
			result.Error = "step expectations not met"
		}
		return result
	}

	// Success - expectations met, even if there was an error
	result.Result = ResultPassed

	// Store result in scenario context automatically for template variables
	if scenarioContext != nil {
		storableResult := r.extractStorableResult(response, logger)
		scenarioContext.StoreResult(step.ID, storableResult)
		if r.debug {
			logger.Debug("🔗 Stored result from step %s for template variables: %v\n", step.ID, storableResult)
		}
	}

	return result
}

// runStepWithTestTools executes a single test step, handling test tools specially.
// Test tools (prefixed with "test_") are handled directly by the test runner
// instead of being sent to the muster MCP server.
//
// For multi-user scenarios:
// - If step.AsUser is set, temporarily switch to that user before executing
// - Test tools that manage users (create, switch) update the handler's current user
// - Regular tool calls use the current user's MCP client
func (r *testRunner) runStepWithTestTools(ctx context.Context, step TestStep, config TestConfiguration, client MCPTestClient, scenarioContext *ScenarioContext, testToolsHandler *TestToolsHandler, logger TestLogger) TestStepResult {
	// Handle as_user field - switch to specified user before executing
	if step.AsUser != "" && testToolsHandler != nil {
		previousUser := testToolsHandler.GetCurrentUserName()
		if step.AsUser != previousUser {
			// Check if user exists using encapsulated HasUser method
			if testToolsHandler.HasUser(step.AsUser) {
				testToolsHandler.SwitchToUser(step.AsUser)
				if r.debug {
					logger.Debug("👤 Step '%s': temporarily switching to user '%s' (was '%s')\n",
						step.ID, step.AsUser, previousUser)
				}
				// Use the new user's client for this step
				client = testToolsHandler.GetCurrentClient()
			} else {
				// User doesn't exist - fail the step
				return TestStepResult{
					Step:      step,
					StartTime: time.Now(),
					EndTime:   time.Now(),
					Result:    ResultError,
					Error:     fmt.Sprintf("as_user '%s' not found; use test_create_user first", step.AsUser),
				}
			}
		}
	}

	// Check if this is a test tool that should be handled locally
	if IsTestTool(step.Tool) {
		return r.runTestToolStep(ctx, step, config, scenarioContext, testToolsHandler, logger)
	}

	// For regular tools, use the current user's client from the handler
	if testToolsHandler != nil {
		client = testToolsHandler.GetCurrentClient()
	}

	// Delegate to the standard runStep for regular tools
	return r.runStep(ctx, step, config, client, scenarioContext, logger)
}

// runTestToolStep executes a test helper tool locally in the test runner.
func (r *testRunner) runTestToolStep(ctx context.Context, step TestStep, config TestConfiguration, scenarioContext *ScenarioContext, testToolsHandler *TestToolsHandler, logger TestLogger) TestStepResult {
	result := TestStepResult{
		Step:      step,
		StartTime: time.Now(),
		Result:    ResultPassed,
	}

	if r.debug {
		logger.Debug("🧪 Executing test tool: %s\n", step.Tool)
	}

	// Apply step timeout if specified
	stepCtx := ctx
	if step.Timeout > 0 {
		var cancel context.CancelFunc
		stepCtx, cancel = context.WithTimeout(ctx, step.Timeout)
		defer cancel()
	}

	// Resolve template variables in step arguments if scenario context is available
	resolvedArgs := step.Args
	if scenarioContext != nil {
		processor := NewTemplateProcessor(scenarioContext)

		var err error
		resolvedArgs, err = processor.ResolveArgs(step.Args)
		if err != nil {
			result.Result = ResultError
			result.Error = fmt.Sprintf("template resolution failed: %v", err)
			result.EndTime = time.Now()
			result.Duration = result.EndTime.Sub(result.StartTime)
			return result
		}
	}

	// Execute the test tool, re-invoking it until expectations hold when the
	// step declares wait_for_state. Test tools observe eventually-consistent
	// state just like the regular tools do -- an exported metric reflects a
	// reconcile that has already happened, but only from the next collection.
	response, err := r.callTestToolWithWait(stepCtx, testToolsHandler, step, resolvedArgs, logger)

	// Wrap the response in MCP-compatible format
	wrappedResult := WrapTestToolResult(response, err)
	result.Response = wrappedResult
	result.EndTime = time.Now()
	result.Duration = result.EndTime.Sub(result.StartTime)

	// Validate expectations
	if !r.validateTestToolExpectations(step.Expected, response, err, logger) {
		if err != nil {
			result.Result = ResultError
			result.Error = fmt.Sprintf("test tool failed: %v", err)
		} else {
			result.Result = ResultFailed
			result.Error = "test tool expectations not met"
		}
		return result
	}

	// Success
	result.Result = ResultPassed

	// Store result in scenario context for template variables
	if scenarioContext != nil {
		scenarioContext.StoreResult(step.ID, response)
		if r.debug {
			logger.Debug("🔗 Stored test tool result from step %s\n", step.ID)
		}
	}

	return result
}

// testToolInvoker is the part of TestToolsHandler that executing a test_* step
// depends on. Narrowing the dependency keeps the wait_for_state polling loop
// testable without standing up a full handler and its instance fixtures.
type testToolInvoker interface {
	HandleTestTool(ctx context.Context, toolName string, args map[string]interface{}) (interface{}, error)
}

// testToolPollInterval is how often a wait_for_state test-tool step re-invokes
// its tool. Matches the 1s interval the regular tool path polls at.
const testToolPollInterval = 1 * time.Second

// callTestToolWithWait invokes a test tool once, or repeatedly until its
// expectations hold when the step sets wait_for_state.
//
// Mirrors validateExpectationsWithClient's polling for regular tool steps. The
// last response is returned either way, so a timeout is reported by the normal
// validation path with the actual response attached rather than as a bare
// timeout.
func (r *testRunner) callTestToolWithWait(ctx context.Context, handler testToolInvoker, step TestStep, args map[string]interface{}, logger TestLogger) (interface{}, error) {
	response, err := handler.HandleTestTool(ctx, step.Tool, args)
	if step.Expected.WaitForState <= 0 {
		return response, err
	}
	if err == nil && r.validateTestToolExpectations(step.Expected, response, nil, logger) {
		return response, nil
	}

	waitCtx, cancel := context.WithTimeout(ctx, step.Expected.WaitForState)
	defer cancel()

	ticker := time.NewTicker(testToolPollInterval)
	defer ticker.Stop()

	if r.debug {
		logger.Debug("🔄 Polling test tool %s for up to %v\n", step.Tool, step.Expected.WaitForState)
	}

	for {
		select {
		case <-waitCtx.Done():
			if r.debug {
				logger.Debug("⏰ Test tool %s did not meet expectations within %v\n", step.Tool, step.Expected.WaitForState)
			}
			return response, err
		case <-ticker.C:
			response, err = handler.HandleTestTool(waitCtx, step.Tool, args)
			if err == nil && r.validateTestToolExpectations(step.Expected, response, nil, logger) {
				return response, nil
			}
		}
	}
}

// validateTestToolExpectations checks a test_* step response against its
// expectations, by adapting the test-tool response shape and delegating to the
// shared checkExpectations implementation.
func (r *testRunner) validateTestToolExpectations(expected TestExpectation, response interface{}, err error, logger TestLogger) bool {
	return r.checkExpectations(expected, r.viewOfTestToolResponse(response, err), logger)
}

// validateExpectationsWithClient checks if the step response meets the expected criteria with state waiting support
func (r *testRunner) validateExpectationsWithClient(ctx context.Context, expected TestExpectation, response interface{}, err error, client MCPTestClient, stepTool string, stepArgs map[string]interface{}, logger TestLogger) bool {
	// Handle state waiting if configured
	if expected.WaitForState > 0 {
		if r.debug {
			logger.Debug("⏳ State waiting configured - polling for expected state\n")
		}

		// The caller has already invoked the tool once. Judge that response
		// before polling, so a step whose first call already satisfies its
		// expectations passes on it without a second invocation. Otherwise
		// wait_for_state discards a passing result and re-runs the tool: that
		// costs a poll interval on every such step, and for a tool whose
		// invocation IS the assertion under test it can turn a step that
		// succeeded into a failure, reported against the first response so the
		// output shows a passing payload on a failed step.
		//
		// callTestToolWithWait already does this for test_* steps. The two
		// paths have to agree about what wait_for_state means (#1038).
		if err == nil && r.validateExpectations(expected, response, nil, logger) {
			if r.debug {
				logger.Debug("✅ Expectations already met on the first call - not polling\n")
			}
			return true
		}

		// Use the configured timeout
		timeout := expected.WaitForState
		pollInterval := 1 * time.Second // Default poll interval

		// Start polling with timeout
		waitCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		pollTicker := time.NewTicker(pollInterval)
		defer pollTicker.Stop()

		if r.debug {
			logger.Debug("🔄 Starting state polling: tool=%s, timeout=%v, interval=%v\n", stepTool, timeout, pollInterval)
		}

		// Poll for expected state
		for {
			select {
			case <-waitCtx.Done():
				if r.debug {
					logger.Debug("⏰ State waiting timeout reached\n")
				}
				return false // Timeout reached without achieving expected state

			case <-pollTicker.C:
				// Make status call using the polling tool and args
				response, err := client.CallTool(waitCtx, stepTool, stepArgs)

				if r.debug {
					logger.Debug("📊 Status poll result: error=%v\n", err)
				}

				// Check if the status call succeeded and meets JSON path expectations
				if err == nil {
					if r.validateExpectations(expected, response, nil, logger) {
						if r.debug {
							logger.Debug("✅ Expected state achieved!\n")
						}
						return true
					} else {
						if r.debug {
							logger.Debug("🔄 State not yet achieved, continuing to poll...\n")
						}
					}
				}
			}
		}
	}

	// Continue with normal validation using original response and error
	return r.validateExpectations(expected, response, err, logger)
}

// validateExpectations checks an MCP tool step response against its
// expectations, by adapting the MCP response shape and delegating to the
// shared checkExpectations implementation.
func (r *testRunner) validateExpectations(expected TestExpectation, response interface{}, err error, logger TestLogger) bool {
	return r.checkExpectations(expected, r.viewOfMCPResponse(response, err, logger), logger)
}

// containsText checks if text contains the expected substring (case-insensitive)
func containsText(text, expected string) bool {
	// Simple case-insensitive contains check
	// In production, this could be more sophisticated
	return len(text) >= len(expected) &&
		containsSubstring(text, expected)
}

// containsSubstring performs case-insensitive substring search
func containsSubstring(text, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(text) < len(substr) {
		return false
	}

	// Convert to lowercase for case-insensitive comparison
	textLower := toLower(text)
	substrLower := toLower(substr)

	for i := 0; i <= len(textLower)-len(substrLower); i++ {
		if textLower[i:i+len(substrLower)] == substrLower {
			return true
		}
	}
	return false
}

// toLower converts string to lowercase
func toLower(s string) string {
	result := make([]byte, len(s))
	for i, b := range []byte(s) {
		if b >= 'A' && b <= 'Z' {
			result[i] = b + 32
		} else {
			result[i] = b
		}
	}
	return string(result)
}

// extractJSONFromMCPResponse attempts to extract JSON from MCP CallToolResult
func (r *testRunner) extractJSONFromMCPResponse(response interface{}, logger TestLogger) map[string]interface{} {
	// Handle MCP CallToolResult structure properly
	if mcpResult, ok := response.(*mcp.CallToolResult); ok {
		// Extract text content from MCP result
		for _, content := range mcpResult.Content {
			if textContent, ok := mcp.AsTextContent(content); ok {
				// Try to parse the text content as JSON
				var result map[string]interface{}
				if err := json.Unmarshal([]byte(textContent.Text), &result); err == nil {
					if r.debug {
						logger.Debug("🔍 Successfully extracted JSON from MCP response: %+v\n", result)
					}
					return result
				} else {
					if r.debug {
						logger.Debug("🔍 Failed to parse MCP text content as JSON: %v\n", err)
						logger.Debug("🔍 Text content was: %s\n", textContent.Text)
					}
				}
			}
		}
	}

	// Try to handle other response types
	if respMap, ok := response.(map[string]interface{}); ok {
		return respMap
	}

	if r.debug {
		logger.Debug("🔍 Could not extract JSON from response type %T\n", response)
	}
	return nil
}

// updateCounters updates the result counters based on a scenario result
func (r *testRunner) updateCounters(suiteResult *TestSuiteResult, scenarioResult TestScenarioResult) {
	switch scenarioResult.Result {
	case ResultPassed:
		suiteResult.PassedScenarios++
	case ResultFailed:
		suiteResult.FailedScenarios++
	case ResultSkipped:
		suiteResult.SkippedScenarios++
	case ResultError:
		suiteResult.ErrorScenarios++
	}
}

// extractStorableResult extracts a storable result from a response
func (r *testRunner) extractStorableResult(response interface{}, logger TestLogger) interface{} {
	// Handle different response types
	switch resp := response.(type) {
	case *mcp.CallToolResult:
		// Extract text content from MCP result
		var textParts []string
		for _, content := range resp.Content {
			if textContent, ok := mcp.AsTextContent(content); ok {
				textParts = append(textParts, textContent.Text)
			}
		}

		if len(textParts) == 0 {
			return response // Return original if no text content
		}

		// Join all text parts
		combinedText := strings.Join(textParts, " ")

		// Try to parse as JSON first
		var jsonResult interface{}
		if err := json.Unmarshal([]byte(combinedText), &jsonResult); err == nil {
			// Successfully parsed as JSON, return the structured data
			if r.debug {
				logger.Debug("🔍 Extracted JSON result for template variables: %v\n", jsonResult)
			}
			return jsonResult
		}

		// If not JSON, return as string
		if r.debug {
			logger.Debug("🔍 Extracted text result for template variables: %s\n", combinedText)
		}
		return combinedText
	default:
		// For other response types, return as-is
		return response
	}
}

// resolveJSONPath resolves a JSON path in a map, supporting dot notation and array indexing
func (r *testRunner) resolveJSONPath(obj map[string]interface{}, path string) (interface{}, bool) {
	// Handle direct key access first (no dots)
	if !strings.Contains(path, ".") {
		if val, exists := obj[path]; exists {
			return val, true
		}
		return nil, false
	}

	// Split the path into segments
	segments := strings.Split(path, ".")
	current := interface{}(obj)

	// Traverse the data based on the segments
	for i, segment := range segments {
		switch currentData := current.(type) {
		case map[string]interface{}:
			if val, exists := currentData[segment]; exists {
				// If this is the last segment, return the value
				if i == len(segments)-1 {
					return val, true
				}
				// Continue traversing
				current = val
			} else {
				return nil, false
			}
		case []interface{}:
			// Handle array indexing - segment should be a numeric index
			if index, err := strconv.Atoi(segment); err == nil {
				if index >= 0 && index < len(currentData) {
					// If this is the last segment, return the array element
					if i == len(segments)-1 {
						return currentData[index], true
					}
					// Continue traversing
					current = currentData[index]
				} else {
					// Index out of bounds
					return nil, false
				}
			} else {
				// Not a valid array index
				return nil, false
			}
		default:
			// Value exists but is not a map or array, and we have more segments to traverse
			return nil, false
		}
	}

	// This should not be reached, but return the current object if all segments were traversed
	return current, true
}

// compareValuesEnhanced compares two values for equality with partial matching support
func (r *testRunner) compareValuesEnhanced(actual, expected interface{}) bool {
	// Handle nil cases first
	if actual == nil || expected == nil {
		return actual == expected
	}

	// Handle slice/array comparisons to prevent panic
	actualVal := reflect.ValueOf(actual)
	expectedVal := reflect.ValueOf(expected)

	// Check if both are slices or arrays
	if actualVal.Kind() == reflect.Slice || actualVal.Kind() == reflect.Array {
		if expectedVal.Kind() == reflect.Slice || expectedVal.Kind() == reflect.Array {
			// Compare lengths first
			if actualVal.Len() != expectedVal.Len() {
				return false
			}

			// Compare each element
			for i := 0; i < actualVal.Len(); i++ {
				actualItem := actualVal.Index(i).Interface()
				expectedItem := expectedVal.Index(i).Interface()
				if !r.compareValuesEnhanced(actualItem, expectedItem) {
					return false
				}
			}
			return true
		}
		// One is slice/array, other is not - not equal
		return false
	}

	// Handle map comparisons with partial matching support
	if actualVal.Kind() == reflect.Map && expectedVal.Kind() == reflect.Map {
		expectedKeys := expectedVal.MapKeys()

		// For partial matching, we only check that all expected keys exist and match
		// We don't require that the actual map has the same number of keys
		// This allows the actual map to have additional fields not specified in expected

		// Check each expected key-value pair
		for _, key := range expectedKeys {
			actualValue := actualVal.MapIndex(key)
			expectedValue := expectedVal.MapIndex(key)

			if !actualValue.IsValid() {
				return false // Key doesn't exist in actual
			}

			if !r.compareValuesEnhanced(actualValue.Interface(), expectedValue.Interface()) {
				return false
			}
		}
		return true
	}

	// Direct equality check for comparable types (but not slices/arrays)
	if actualVal.Type().Comparable() && expectedVal.Type().Comparable() {
		if actual == expected {
			return true
		}
	}

	// Handle boolean comparisons
	if expectedBool, ok := expected.(bool); ok {
		if actualBool, ok := actual.(bool); ok {
			return actualBool == expectedBool
		}
		// Convert string to bool if needed
		if actualStr, ok := actual.(string); ok {
			if actualStr == "true" {
				return expectedBool
			}
			if actualStr == "false" {
				return !expectedBool
			}
		}
	}

	// Handle string comparisons
	if expectedStr, ok := expected.(string); ok {
		if actualStr, ok := actual.(string); ok {
			return actualStr == expectedStr
		}
		// Convert other types to string for comparison
		actualStr := fmt.Sprintf("%v", actual)
		return actualStr == expectedStr
	}

	// Handle numeric comparisons (int, float64, etc.)
	if expectedFloat, ok := expected.(float64); ok {
		if actualFloat, ok := actual.(float64); ok {
			return actualFloat == expectedFloat
		}
		if actualInt, ok := actual.(int); ok {
			return float64(actualInt) == expectedFloat
		}
	}

	if expectedInt, ok := expected.(int); ok {
		if actualInt, ok := actual.(int); ok {
			return actualInt == expectedInt
		}
		if actualFloat, ok := actual.(float64); ok {
			return actualFloat == float64(expectedInt)
		}
	}

	// For other types, convert both to strings and compare
	actualStr := fmt.Sprintf("%v", actual)
	expectedStr := fmt.Sprintf("%v", expected)
	return actualStr == expectedStr
}
