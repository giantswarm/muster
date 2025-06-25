package testing

import (
	"encoding/json"
	"sync"
	"time"
)

// structuredReporter implements TestReporter for MCP server mode
// It captures all reporting data without writing to stdio
type structuredReporter struct {
	mu             sync.RWMutex
	verbose        bool
	debug          bool
	reportPath     string
	config         TestConfiguration
	scenarioStates map[string]*ScenarioState
	suiteResult    *TestSuiteResult
	currentResults []TestScenarioResult
}

// ScenarioState tracks the state of a running scenario
type ScenarioState struct {
	Scenario    TestScenario     `json:"scenario"`
	StartTime   time.Time        `json:"start_time"`
	StepResults []TestStepResult `json:"step_results"`
	Status      string           `json:"status"` // "running", "completed", "failed"
}

// NewStructuredReporter creates a reporter that captures structured data without stdio output
func NewStructuredReporter(verbose, debug bool, reportPath string) TestReporter {
	return &structuredReporter{
		verbose:        verbose,
		debug:          debug,
		reportPath:     reportPath,
		scenarioStates: make(map[string]*ScenarioState),
		currentResults: make([]TestScenarioResult, 0),
	}
}

// ReportStart is called when test execution begins
func (r *structuredReporter) ReportStart(config TestConfiguration) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.config = config
	r.suiteResult = &TestSuiteResult{
		StartTime:       time.Now(),
		ScenarioResults: make([]TestScenarioResult, 0),
		Configuration:   config,
	}
}

// ReportScenarioStart is called when a scenario begins
func (r *structuredReporter) ReportScenarioStart(scenario TestScenario) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.scenarioStates[scenario.Name] = &ScenarioState{
		Scenario:    scenario,
		StartTime:   time.Now(),
		StepResults: make([]TestStepResult, 0),
		Status:      "running",
	}
}

// ReportStepResult is called when a step completes
func (r *structuredReporter) ReportStepResult(stepResult TestStepResult) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Find the scenario this step belongs to and add the result
	for _, state := range r.scenarioStates {
		// Check if this step belongs to this scenario
		for _, step := range state.Scenario.Steps {
			if step.ID == stepResult.Step.ID {
				state.StepResults = append(state.StepResults, stepResult)
				return
			}
		}
		// Check cleanup steps too
		for _, step := range state.Scenario.Cleanup {
			if step.ID == stepResult.Step.ID {
				state.StepResults = append(state.StepResults, stepResult)
				return
			}
		}
	}
}

// ReportScenarioResult is called when a scenario completes
func (r *structuredReporter) ReportScenarioResult(scenarioResult TestScenarioResult) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Update the scenario state
	if state, exists := r.scenarioStates[scenarioResult.Scenario.Name]; exists {
		if scenarioResult.Result == ResultPassed {
			state.Status = "completed"
		} else {
			state.Status = "failed"
		}
	}

	// Add to current results
	r.currentResults = append(r.currentResults, scenarioResult)

	// Update suite result if it exists
	if r.suiteResult != nil {
		r.suiteResult.ScenarioResults = append(r.suiteResult.ScenarioResults, scenarioResult)
		r.updateSuiteCounters(scenarioResult)
	}
}

// ReportSuiteResult is called when all tests complete
func (r *structuredReporter) ReportSuiteResult(suiteResult TestSuiteResult) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.suiteResult = &suiteResult

	// Mark all scenarios as completed
	for _, state := range r.scenarioStates {
		if state.Status == "running" {
			state.Status = "completed"
		}
	}
}

// GetCurrentSuiteResult returns the current test suite result
func (r *structuredReporter) GetCurrentSuiteResult() *TestSuiteResult {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.suiteResult == nil {
		return nil
	}

	// Return a copy to avoid race conditions
	result := *r.suiteResult
	result.ScenarioResults = make([]TestScenarioResult, len(r.suiteResult.ScenarioResults))
	copy(result.ScenarioResults, r.suiteResult.ScenarioResults)

	return &result
}

// GetScenarioStates returns the current state of all scenarios
func (r *structuredReporter) GetScenarioStates() map[string]*ScenarioState {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Return a copy to avoid race conditions
	states := make(map[string]*ScenarioState)
	for name, state := range r.scenarioStates {
		stateCopy := *state
		stateCopy.StepResults = make([]TestStepResult, len(state.StepResults))
		copy(stateCopy.StepResults, state.StepResults)
		states[name] = &stateCopy
	}

	return states
}

// GetCurrentResults returns the current scenario results
func (r *structuredReporter) GetCurrentResults() []TestScenarioResult {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Return a copy to avoid race conditions
	results := make([]TestScenarioResult, len(r.currentResults))
	copy(results, r.currentResults)
	return results
}

// GetResultsAsJSON returns the current results as JSON
func (r *structuredReporter) GetResultsAsJSON() (string, error) {
	result := r.GetCurrentSuiteResult()
	if result == nil {
		return `{"status": "no_results", "message": "No test results available"}`, nil
	}

	jsonData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}

	return string(jsonData), nil
}

// IsVerbose returns whether verbose reporting is enabled
func (r *structuredReporter) IsVerbose() bool {
	return r.verbose
}

// IsDebug returns whether debug reporting is enabled
func (r *structuredReporter) IsDebug() bool {
	return r.debug
}

// SetParallelMode enables or disables parallel output buffering
func (r *structuredReporter) SetParallelMode(parallel bool) {
	// Structured reporter doesn't need special parallel handling - it captures all data for programmatic access
}

// updateSuiteCounters updates the suite-level counters based on scenario result
func (r *structuredReporter) updateSuiteCounters(scenarioResult TestScenarioResult) {
	switch scenarioResult.Result {
	case ResultPassed:
		r.suiteResult.PassedScenarios++
	case ResultFailed:
		r.suiteResult.FailedScenarios++
	case ResultSkipped:
		r.suiteResult.SkippedScenarios++
	case ResultError:
		r.suiteResult.ErrorScenarios++
	}

	r.suiteResult.TotalScenarios = len(r.suiteResult.ScenarioResults)
}
