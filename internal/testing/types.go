package testing

import (
	"context"
	"os"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// TestCategory represents the category of tests to execute
type TestCategory string

const (
	// CategoryBehavioral represents BDD-style behavioral tests
	CategoryBehavioral TestCategory = "behavioral"
	// CategoryIntegration represents integration and end-to-end tests
	CategoryIntegration TestCategory = "integration"
)

// TestConcept represents the core muster concept being tested
type TestConcept string

const (
	// ConceptServiceClass represents ServiceClass management tests
	ConceptServiceClass TestConcept = "serviceclass"
	// ConceptWorkflow represents Workflow execution tests
	ConceptWorkflow TestConcept = "workflow"
	// ConceptMCPServer represents MCPServer management tests
	ConceptMCPServer TestConcept = "mcpserver"

	// ConceptService represents Service lifecycle tests
	ConceptService TestConcept = "service"
)

// TestResult represents the result of test execution
type TestResult string

const (
	// ResultPassed indicates the test passed successfully
	ResultPassed TestResult = "PASSED"
	// ResultFailed indicates the test failed
	ResultFailed TestResult = "FAILED"
	// ResultSkipped indicates the test was skipped
	ResultSkipped TestResult = "SKIPPED"
	// ResultError indicates an error occurred during test execution
	ResultError TestResult = "ERROR"
)

// ExecutionMode represents the mode of test execution
type ExecutionMode string

const (
	// ExecutionModeCLI represents command line interface execution
	ExecutionModeCLI ExecutionMode = "cli"
	// ExecutionModeMCPServer represents MCP server execution via stdio
	ExecutionModeMCPServer ExecutionMode = "mcp-server"
)

// TestLogger provides centralized logging for test execution
type TestLogger interface {
	// Debug logs debug-level messages (only shown when debug=true)
	Debug(format string, args ...interface{})
	// Info logs info-level messages (shown when verbose=true or debug=true)
	Info(format string, args ...interface{})
	// Error logs error-level messages (always shown)
	Error(format string, args ...interface{})
	// IsDebugEnabled returns whether debug logging is enabled
	IsDebugEnabled() bool
	// IsVerboseEnabled returns whether verbose logging is enabled
	IsVerboseEnabled() bool
}

// TestConfiguration defines the overall test execution configuration
type TestConfiguration struct {
	// Timeout is the overall test execution timeout
	Timeout time.Duration `yaml:"timeout"`
	// Category filter for test execution
	Category TestCategory `yaml:"category,omitempty"`
	// Concept filter for test execution
	Concept TestConcept `yaml:"concept,omitempty"`
	// Scenario filter for specific scenario execution
	Scenario string `yaml:"scenario,omitempty"`
	// Parallel is the number of parallel test workers
	Parallel int `yaml:"parallel"`
	// FailFast stops execution on first failure
	FailFast bool `yaml:"fail_fast"`
	// Verbose enables detailed output
	Verbose bool `yaml:"verbose"`
	// Debug enables debug logging and MCP tracing
	Debug bool `yaml:"debug"`
	// ConfigPath is the path to test scenario definitions
	ConfigPath string `yaml:"config_path,omitempty"`
	// ReportPath is the path to save detailed test reports
	ReportPath string `yaml:"report_path,omitempty"`
	// BasePort is the starting port number for muster instances
	BasePort int `yaml:"base_port,omitempty"`
	// KeepTempConfig keeps temporary config directory after test execution
	KeepTempConfig bool `yaml:"keep_temp_config,omitempty"`
}

// TestScenario defines a single test scenario
type TestScenario struct {
	// Name is the unique identifier for the scenario
	Name string `yaml:"name"`
	// Category is the test category (behavioral, integration)
	Category TestCategory `yaml:"category"`
	// Concept is the core concept being tested
	Concept TestConcept `yaml:"concept"`
	// Description provides human-readable scenario description
	Description string `yaml:"description"`
	// Prerequisites define setup requirements
	Prerequisites []string `yaml:"prerequisites,omitempty"`
	// Steps define the test execution steps
	Steps []TestStep `yaml:"steps"`
	// Cleanup defines teardown steps
	Cleanup []TestStep `yaml:"cleanup,omitempty"`
	// Timeout for this specific scenario
	Timeout time.Duration `yaml:"timeout,omitempty"`
	// Tags for additional categorization
	Tags []string `yaml:"tags,omitempty"`
	// Skip indicates whether this scenario should be skipped
	Skip bool `yaml:"skip,omitempty"`
	// PreConfiguration defines muster instance setup
	PreConfiguration *MusterPreConfiguration `yaml:"pre_configuration,omitempty"`
}

// MusterPreConfiguration defines how to pre-configure an muster serve instance
type MusterPreConfiguration struct {
	// MCPServers defines MCP server configurations to load
	MCPServers []MCPServerConfig `yaml:"mcp_servers,omitempty"`
	// Workflows defines workflow definitions to load
	Workflows []WorkflowConfig `yaml:"workflows,omitempty"`

	// ServiceClasses defines service class definitions to load
	ServiceClasses []ServiceClassConfig `yaml:"service_classes,omitempty"`
	// Services defines service instance definitions to load
	Services []ServiceConfig `yaml:"services,omitempty"`
	// MainConfig defines the main muster configuration
	MainConfig *MainConfig `yaml:"main_config,omitempty"`

	// MockOAuthServers defines mock OAuth servers to start for testing
	MockOAuthServers []MockOAuthServerConfig `yaml:"mock_oauth_servers,omitempty"`
}

// MCPServerConfig represents an MCP server configuration
type MCPServerConfig struct {
	// Name is the unique identifier for the MCP server
	Name string `yaml:"name"`
	// Config contains the server-specific configuration (can include tools for mock servers)
	Config map[string]interface{} `yaml:"config"`
}

// WorkflowConfig represents a workflow configuration
type WorkflowConfig struct {
	// Name is the unique identifier for the workflow
	Name string `yaml:"name"`
	// Config contains the workflow definition
	Config map[string]interface{} `yaml:"config"`
}

// ServiceClassConfig represents a service class configuration
type ServiceClassConfig map[string]interface{}

// ServiceConfig represents a service instance configuration
type ServiceConfig struct {
	// Name is the unique identifier for the service instance
	Name string `yaml:"name"`
	// Config contains the service instance definition
	Config map[string]interface{} `yaml:"config"`
}

// MainConfig represents the main muster configuration
type MainConfig struct {
	// Config contains the main configuration
	Config map[string]interface{} `yaml:"config"`
}

// MusterInstance represents a managed muster serve instance
type MusterInstance struct {
	// ID is the unique identifier for this instance
	ID string
	// ConfigPath is the path to the temporary configuration directory
	ConfigPath string
	// Port is the port the instance is listening on
	Port int
	// Endpoint is the full MCP endpoint URL
	Endpoint string
	// Process is the running muster serve process
	Process *os.Process
	// StartTime when the instance was started
	StartTime time.Time
	// Logs contains the collected stdout and stderr from the instance
	Logs *InstanceLogs
	// ExpectedTools contains the list of tools expected to be available from MCP servers
	ExpectedTools []string
	// ExpectedServiceClasses contains the list of ServiceClasses expected to be available
	ExpectedServiceClasses []string
	// ExpectedMCPServers contains the list of MCP server names expected to be registered.
	// This includes OAuth-protected servers which may be in "auth_required" state.
	// Used by WaitForReady to ensure servers are registered before tests run.
	ExpectedMCPServers []string
	// MockHTTPServers contains references to mock HTTP servers started for this instance
	MockHTTPServers map[string]*MockHTTPServerInfo
	// MockOAuthServers contains references to mock OAuth servers started for this instance
	MockOAuthServers map[string]*MockOAuthServerInfo
	// MusterOAuthAccessToken is the access token for authenticating with muster's OAuth server.
	// This is set when a mock OAuth server is configured with UseAsMusterOAuthServer=true.
	// The test framework uses this token to authenticate with muster without a browser flow.
	MusterOAuthAccessToken string
}

// MockHTTPServerInfo contains information about a running mock HTTP server
type MockHTTPServerInfo struct {
	// Name is the name of the MCP server
	Name string
	// Port is the port the mock HTTP server is listening on
	Port int
	// Transport is the transport type (sse or streamable-http)
	Transport string
	// Endpoint is the full URL endpoint for the server
	Endpoint string
}

// MockOAuthServerConfig defines a mock OAuth server for testing
type MockOAuthServerConfig struct {
	// Name is the unique identifier for this OAuth server
	Name string `yaml:"name"`

	// Issuer is the OAuth issuer URL (auto-generated if not specified)
	Issuer string `yaml:"issuer,omitempty"`

	// Scopes are the scopes this OAuth server accepts
	Scopes []string `yaml:"scopes,omitempty"`

	// AutoApprove automatically approves authentication requests
	AutoApprove bool `yaml:"auto_approve,omitempty"`

	// PKCERequired enforces PKCE flow
	PKCERequired bool `yaml:"pkce_required,omitempty"`

	// TokenLifetime is how long tokens are valid (e.g., "1h", "30m")
	TokenLifetime string `yaml:"token_lifetime,omitempty"`

	// ClientID is the expected OAuth client ID
	ClientID string `yaml:"client_id,omitempty"`

	// ClientSecret is the expected OAuth client secret
	ClientSecret string `yaml:"client_secret,omitempty"`

	// SimulateError can simulate error conditions
	SimulateError string `yaml:"simulate_error,omitempty"`

	// UseMockClock enables a mock clock for testing token expiry
	// When true, the test_advance_oauth_clock tool can be used to
	// advance time without waiting
	UseMockClock bool `yaml:"use_mock_clock,omitempty"`

	// UseAsMusterOAuthServer configures this mock OAuth server as muster's
	// OAuth server (aggregator.oauthServer), enabling SSO token forwarding
	// tests where muster itself requires OAuth authentication.
	// When true, the aggregator will be configured with oauthServer.enabled=true
	// and the issuer will be set to this mock server's URL.
	UseAsMusterOAuthServer bool `yaml:"use_as_muster_oauth_server,omitempty"`

	// TrustedIssuers configures RFC 8693 token exchange support.
	// When configured, this OAuth server can exchange tokens from the listed
	// issuers for tokens valid on this server. This enables cross-cluster SSO testing.
	// Each entry maps a connector_id to a trusted OAuth server name.
	TrustedIssuers []TrustedIssuerConfig `yaml:"trusted_issuers,omitempty"`

	// UseTLS enables HTTPS mode with a self-signed certificate.
	// This is required for OAuth servers used as token exchange targets,
	// since RFC 8693 token exchange endpoints must use HTTPS for security.
	// Note: This is automatically set to true when UseAsMusterOAuthServer is true.
	UseTLS bool `yaml:"use_tls,omitempty"`
}

// TrustedIssuerConfig defines a trusted issuer for RFC 8693 token exchange
type TrustedIssuerConfig struct {
	// ConnectorID is the identifier used in the token exchange request
	// to specify which connector to use (maps to audience in RFC 8693)
	ConnectorID string `yaml:"connector_id"`

	// OAuthServerRef references the mock OAuth server by name whose tokens
	// should be accepted for exchange
	OAuthServerRef string `yaml:"oauth_server_ref"`
}

// MCPServerOAuthConfig defines OAuth protection for an MCP server in tests
type MCPServerOAuthConfig struct {
	// Required indicates this server requires OAuth authentication
	Required bool `yaml:"required"`

	// MockOAuthServerRef references a mock OAuth server by name
	MockOAuthServerRef string `yaml:"mock_oauth_server_ref"`

	// Scope is the required OAuth scope
	Scope string `yaml:"scope,omitempty"`
}

// MockOAuthServerInfo contains info about a running mock OAuth server
type MockOAuthServerInfo struct {
	// Name is the unique identifier for this OAuth server
	Name string
	// Port is the port the OAuth server is listening on
	Port int
	// IssuerURL is the OAuth issuer URL
	IssuerURL string
	// AccessToken is a pre-generated access token for test framework authentication.
	// This is only set when the OAuth server is used as muster's OAuth server
	// (UseAsMusterOAuthServer=true), allowing the test framework to authenticate
	// with muster without implementing the full OAuth browser flow.
	AccessToken string
	// IDToken is the ID token from the OAuth response.
	// This is used for SSO token forwarding tests.
	IDToken string
	// UseAsMusterOAuthServer indicates this OAuth server is used as muster's OAuth server.
	UseAsMusterOAuthServer bool
}

// InstanceLogs contains the captured logs from an muster instance
type InstanceLogs struct {
	// Stdout contains the standard output
	Stdout string
	// Stderr contains the standard error output
	Stderr string
	// Combined contains both stdout and stderr in chronological order
	Combined string
}

// MusterInstanceManager manages muster serve instances for testing
type MusterInstanceManager interface {
	// CreateInstance creates a new muster serve instance with the given configuration.
	// The logger parameter allows scenario-specific logging with prefixes for parallel execution.
	CreateInstance(ctx context.Context, scenarioName string, config *MusterPreConfiguration, logger TestLogger) (*MusterInstance, error)
	// DestroyInstance stops and cleans up an muster serve instance.
	// The logger parameter allows scenario-specific logging with prefixes for parallel execution.
	DestroyInstance(ctx context.Context, instance *MusterInstance, logger TestLogger) error
	// WaitForReady waits for an instance to be ready to accept connections.
	// The logger parameter allows scenario-specific logging with prefixes for parallel execution.
	WaitForReady(ctx context.Context, instance *MusterInstance, logger TestLogger) error
}

// TestStep defines a single step within a test scenario
type TestStep struct {
	// ID is the step identifier
	ID string `yaml:"id"`
	// Description explains what the step does
	Description string `yaml:"description,omitempty"`
	// Tool is the MCP tool to invoke
	Tool string `yaml:"tool"`
	// Args are the tool arguments as a map
	Args map[string]interface{} `yaml:"args"`
	// Expected defines the expected outcome
	Expected TestExpectation `yaml:"expected"`
	// Retry configuration for this step
	Retry *RetryConfig `yaml:"retry,omitempty"`
	// Timeout for this specific step
	Timeout time.Duration `yaml:"timeout,omitempty"`
	// AsUser specifies which user session to execute this step as.
	// For multi-user testing scenarios. If not set, uses the current user.
	AsUser string `yaml:"as_user,omitempty"`
}

// TestExpectation defines what result is expected from a test step
type TestExpectation struct {
	// Success indicates whether the tool call should succeed
	Success bool `yaml:"success"`
	// ErrorContains checks if error message contains specific text
	ErrorContains []string `yaml:"error_contains,omitempty"`
	// Contains checks if response contains specific text
	Contains []string `yaml:"contains,omitempty"`
	// NotContains checks if response does not contain specific text
	NotContains []string `yaml:"not_contains,omitempty"`
	// JSONPath allows checking specific JSON response fields
	JSONPath map[string]interface{} `yaml:"json_path,omitempty"`
	// StatusCode for HTTP-based expectations
	StatusCode int `yaml:"status_code,omitempty"`
	// WaitForState enables polling for state changes with timeout
	WaitForState time.Duration `yaml:"wait_for_state,omitempty"`
}

// RetryConfig defines retry behavior for test steps
type RetryConfig struct {
	// Count is the number of retry attempts
	Count int `yaml:"count"`
	// Delay between retry attempts
	Delay time.Duration `yaml:"delay"`
	// BackoffMultiplier for exponential backoff
	BackoffMultiplier float64 `yaml:"backoff_multiplier,omitempty"`
}

// TestSuiteResult represents the overall result of test suite execution
type TestSuiteResult struct {
	// StartTime when test execution began
	StartTime time.Time `json:"start_time"`
	// EndTime when test execution completed
	EndTime time.Time `json:"end_time"`
	// Duration of test execution
	Duration time.Duration `json:"duration"`
	// TotalScenarios is the total number of scenarios executed
	TotalScenarios int `json:"total_scenarios"`
	// PassedScenarios is the number of scenarios that passed
	PassedScenarios int `json:"passed_scenarios"`
	// FailedScenarios is the number of scenarios that failed
	FailedScenarios int `json:"failed_scenarios"`
	// SkippedScenarios is the number of scenarios that were skipped
	SkippedScenarios int `json:"skipped_scenarios"`
	// ErrorScenarios is the number of scenarios that had errors
	ErrorScenarios int `json:"error_scenarios"`
	// ScenarioResults contains individual scenario results
	ScenarioResults []TestScenarioResult `json:"scenario_results"`
	// Configuration used for this test run
	Configuration TestConfiguration `json:"configuration"`
}

// TestScenarioResult represents the result of a single test scenario
type TestScenarioResult struct {
	// Scenario is the scenario that was executed
	Scenario TestScenario `json:"scenario"`
	// Result is the overall result of the scenario
	Result TestResult `json:"result"`
	// StartTime when scenario execution began
	StartTime time.Time `json:"start_time"`
	// EndTime when scenario execution completed
	EndTime time.Time `json:"end_time"`
	// Duration of scenario execution
	Duration time.Duration `json:"duration"`
	// StepResults contains individual step results
	StepResults []TestStepResult `json:"step_results"`
	// Error message if the scenario failed or had an error
	Error string `json:"error,omitempty"`
	// Output from scenario execution
	Output string `json:"output,omitempty"`
	// InstanceLogs contains logs from the muster serve instance
	InstanceLogs *InstanceLogs `json:"instance_logs,omitempty"`
}

// TestStepResult represents the result of a single test step
type TestStepResult struct {
	// Step is the step that was executed
	Step TestStep `json:"step"`
	// Result is the result of the step
	Result TestResult `json:"result"`
	// StartTime when step execution began
	StartTime time.Time `json:"start_time"`
	// EndTime when step execution completed
	EndTime time.Time `json:"end_time"`
	// Duration of step execution
	Duration time.Duration `json:"duration"`
	// Response from the MCP tool call
	Response interface{} `json:"response,omitempty"`
	// Error message if the step failed
	Error string `json:"error,omitempty"`
	// RetryCount is the number of retries attempted
	RetryCount int `json:"retry_count"`
}

// TestRunner interface defines the test execution engine
type TestRunner interface {
	// Run executes test scenarios according to the configuration
	Run(ctx context.Context, config TestConfiguration, scenarios []TestScenario) (*TestSuiteResult, error)
}

// MCPTestClient interface defines the MCP client for testing
type MCPTestClient interface {
	// Connect establishes connection to the MCP aggregator
	Connect(ctx context.Context, endpoint string) error
	// ConnectWithAuth establishes connection to the MCP aggregator with an access token.
	// This is used when muster's OAuth server is enabled and requires authentication.
	ConnectWithAuth(ctx context.Context, endpoint, accessToken string) error
	// CallTool invokes an MCP tool with the given args
	CallTool(ctx context.Context, toolName string, args map[string]interface{}) (interface{}, error)
	// ListTools returns available MCP tools
	ListTools(ctx context.Context) ([]string, error)
	// ListToolsWithSchemas returns available MCP tools with their full schemas
	ListToolsWithSchemas(ctx context.Context) ([]mcp.Tool, error)
	// ReadResource reads an MCP resource by URI
	ReadResource(ctx context.Context, uri string) (*mcp.ReadResourceResult, error)
	// Close closes the MCP connection
	Close() error
	// GetSessionID returns the client's session ID (may be empty if not set)
	GetSessionID() string
	// ReconnectWithSession reconnects using a specific session ID and new access token.
	// This is used to test proactive SSO re-triggering when a token changes for the same session.
	ReconnectWithSession(ctx context.Context, endpoint, accessToken, sessionID string) error
}

// TestScenarioLoader interface defines how test scenarios are loaded
type TestScenarioLoader interface {
	// LoadScenarios loads test scenarios from the given path
	LoadScenarios(configPath string) ([]TestScenario, error)
	// FilterScenarios filters scenarios based on the configuration
	FilterScenarios(scenarios []TestScenario, config TestConfiguration) []TestScenario
}

// TestReporter interface defines how test results are reported
type TestReporter interface {
	// ReportStart is called when test execution begins
	ReportStart(config TestConfiguration)
	// ReportScenarioStart is called when a scenario begins
	ReportScenarioStart(scenario TestScenario)
	// ReportStepResult is called when a step completes
	ReportStepResult(stepResult TestStepResult)
	// ReportScenarioResult is called when a scenario completes
	ReportScenarioResult(scenarioResult TestScenarioResult)
	// ReportSuiteResult is called when all tests complete
	ReportSuiteResult(suiteResult TestSuiteResult)
	// SetParallelMode enables or disables parallel output buffering
	SetParallelMode(parallel bool)
}

// StructuredTestReporter extends TestReporter with methods for structured data access
// This is typically used in MCP server mode where results need to be queried programmatically
type StructuredTestReporter interface {
	TestReporter
	// GetCurrentSuiteResult returns the current test suite result
	GetCurrentSuiteResult() *TestSuiteResult
	// GetScenarioStates returns the current state of all scenarios
	GetScenarioStates() map[string]*ScenarioState
	// GetCurrentResults returns the current scenario results
	GetCurrentResults() []TestScenarioResult
	// GetResultsAsJSON returns the current results as JSON
	GetResultsAsJSON() (string, error)
	// IsVerbose returns whether verbose reporting is enabled
	IsVerbose() bool
	// IsDebug returns whether debug reporting is enabled
	IsDebug() bool
}
