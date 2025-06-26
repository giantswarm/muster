// Package testing provides the comprehensive test framework for muster.
//
// This package implements the test framework architecture for Task 13, providing
// behavioral and integration testing capabilities for all muster core concepts.
//
// # Architecture Overview
//
// The testing framework is built on a modular architecture that enables flexible,
// scalable, and maintainable testing of muster's core functionality. The system
// is designed around the following principles:
//
// - **Separation of Concerns**: Each component has a single, well-defined responsibility
// - **Interface-Driven Design**: All components interact through well-defined interfaces
// - **Configurable Execution**: Support for parallel execution, timeouts, and retry logic
// - **Comprehensive Reporting**: Structured output for both human and machine consumption
// - **CI/CD Integration**: First-class support for automated testing pipelines
//
// ```
//
//	                ┌─────────────────┐
//	                │  muster test    │ (CLI Command)
//	                │   (cmd/test.go) │
//	                └─────────┬───────┘
//	                          │
//	                ┌─────────▼───────┐
//	                │   TestRunner    │ (Core Engine)
//	                │   (runner.go)   │
//	                └─────────┬───────┘
//	                          │
//	          ┌───────────────┼───────────────┐
//	          │               │               │
//	┌─────────▼──┐   ┌──────▼──────┐   ┌─────▼─────┐
//	│MCPTestClient│   │ScenarioLoader│   │ Reporter  │
//	│ (client.go) │   │ (config.go)  │   │(reporter.go)│
//	└─────────────┘   └─────────────┘   └───────────┘
//
// ```
//
// # Core Components
//
// ## Test Runner Engine (TestRunner interface)
//
// The TestRunner manages the complete test execution lifecycle:
// - **Parallel Execution**: Configurable worker pool for concurrent test execution
// - **Lifecycle Management**: Setup, execution, cleanup, and teardown phases
// - **Fail-Fast Logic**: Configurable early termination on test failures
// - **Timeout Handling**: Per-step and per-scenario timeout enforcement
// - **Result Aggregation**: Collection and consolidation of test results
//
// Key methods:
//   - RunScenarios(ctx context.Context, scenarios []TestScenario) (*TestResults, error)
//   - SetParallelWorkers(count int) - Configure concurrent execution
//   - SetFailFast(enabled bool) - Control failure handling behavior
//
// ## MCP Test Client (MCPTestClient interface)
//
// The MCPTestClient provides MCP protocol communication capabilities:
// - **Protocol Communication**: Direct communication with muster aggregator
// - **Tool Invocation**: Execute MCP tools with parameter validation
// - **Response Handling**: Parse and validate tool responses
// - **Connection Management**: Automatic connection handling and recovery
// - **Debug Tracing**: Protocol-level debugging and logging capabilities
//
// Key methods:
//   - Connect(ctx context.Context, endpoint string) error
//   - CallTool(ctx context.Context, name string, params map[string]interface{}) (*ToolResponse, error)
//   - ListTools(ctx context.Context) ([]ToolInfo, error)
//   - Close() error
//
// ## Scenario Loader (TestScenarioLoader interface)
//
// The TestScenarioLoader handles test configuration and filtering:
// - **YAML Parsing**: Parse and validate YAML scenario definitions
// - **Schema Validation**: Ensure scenario files conform to expected structure
// - **Category Filtering**: Filter tests by category (behavioral, integration)
// - **Concept Filtering**: Filter tests by concept (serviceclass, workflow, etc.)
// - **Configuration Management**: Load and merge configuration from multiple sources
//
// Key methods:
//   - LoadScenarios(ctx context.Context, paths []string) ([]TestScenario, error)
//   - FilterByCategory(scenarios []TestScenario, category string) []TestScenario
//   - FilterByConcept(scenarios []TestScenario, concept string) []TestScenario
//   - ValidateScenario(scenario *TestScenario) error
//
// ## Test Reporter (TestReporter interface)
//
// The TestReporter handles result collection and output formatting:
// - **Structured Output**: JSON and text output formats for different consumers
// - **Progress Reporting**: Real-time progress updates during execution
// - **Result Aggregation**: Collect and summarize test results across scenarios
// - **CI/CD Integration**: Exit codes and output formats for automation
// - **Performance Metrics**: Execution timing and resource usage statistics
//
// Key methods:
//   - ReportProgress(ctx context.Context, progress *TestProgress) error
//   - ReportResults(ctx context.Context, results *TestResults) error
//   - SetOutputFormat(format OutputFormat) - Configure output format
//   - GetSummary() *TestSummary
//
// # Type System
//
// ## Core Types
//
// **TestScenario**: Complete test scenario definition
//   - Metadata: name, category, concept, description, tags
//   - Configuration: timeout, retry settings, parallel execution hints
//   - Steps: Sequential list of test steps with validation rules
//   - Cleanup: Teardown steps executed after scenario completion
//
// **TestStep**: Individual test operation within a scenario
//   - Tool invocation: MCP tool name and parameters
//   - Validation: Expected outcomes and assertion rules
//   - Error handling: Retry logic and failure recovery
//   - Timing: Step-specific timeout and delay configurations
//
// **TestResults**: Aggregated results from test execution
//   - Overall status: pass/fail/error counts and percentages
//   - Detailed results: Per-scenario and per-step outcomes
//   - Performance metrics: Execution timing and resource usage
//   - Error information: Detailed error messages and stack traces
//
// ## Configuration Types
//
// **TestConfig**: Global test framework configuration
//   - Execution settings: parallel workers, timeout defaults
//   - Connection settings: MCP endpoint, authentication
//   - Output settings: Format, verbosity, reporting options
//   - Category/concept filtering: Test selection criteria
//
// **ScenarioFilter**: Test selection and filtering criteria
//   - Category-based filtering (behavioral, integration)
//   - Concept-based filtering (serviceclass, workflow, mcpserver, capability, service)
//   - Tag-based filtering for fine-grained test selection
//   - Name pattern matching for specific scenario selection
//
// # Test Categories
//
// ## Behavioral Tests
// - **Purpose**: Validate business logic and user-facing functionality
// - **Scope**: Based on Task 12 behavioral specifications
// - **Structure**: BDD-style scenarios with clear Given/When/Then patterns
// - **Focus**: User workflows, API contracts, and expected behavior
//
// ## Integration Tests
// - **Purpose**: Validate component interactions and end-to-end functionality
// - **Scope**: Multi-component scenarios and external system integration
// - **Structure**: Complex scenarios with dependency chains
// - **Focus**: System integration, data flow, and error propagation
//
// # Core Concepts Coverage
//
// ## ServiceClass Testing
// - **Management Operations**: Create, update, delete, list ServiceClass definitions
// - **Dynamic Instantiation**: Validate ServiceClass-to-Service instantiation
// - **Parameter Validation**: Test parameter templating and validation logic
// - **Tool Integration**: Verify ServiceClass tool definitions and availability
//
// ## Workflow Testing
// - **Execution Logic**: Validate workflow step execution and flow control
// - **Parameter Templating**: Test parameter substitution and variable scoping
// - **Error Handling**: Validate error propagation and recovery mechanisms
// - **Integration**: Test workflow interaction with other core concepts
//
// ## MCPServer Testing
// - **Registration**: Test MCP server registration and discovery
// - **Tool Aggregation**: Validate tool consolidation and namespace management
// - **Connection Management**: Test connection lifecycle and error recovery
// - **Protocol Compliance**: Validate MCP protocol adherence
//
// ## Capability Testing
// - **API Abstraction**: Test capability definition and operation mapping
// - **Operation Validation**: Validate capability operation execution
// - **Integration**: Test capability interaction with underlying tools
// - **Error Handling**: Validate capability error responses and fallback logic
//
// ## Service Testing
// - **Lifecycle Management**: Test service creation, management, and deletion
// - **Dependency Management**: Validate service dependency resolution
// - **State Transitions**: Test service state changes and event handling
// - **Integration**: Test service interaction with other system components
//
// # Mock MCP Server Support
//
// The testing framework includes comprehensive mock MCP server functionality:
//
// - **Mock Tool Definitions**: Define tools with configurable responses
// - **Conditional Responses**: Different responses based on input parameters
// - **Template-Based Responses**: Dynamic response generation using Go templates
// - **Error Simulation**: Simulate various error conditions and failures
// - **Standalone Mode**: Run mock servers independently for external testing
//
// Mock servers can be embedded in test scenarios or run standalone:
//
//	# Embedded in test scenario
//	mcpServers:
//	  - name: "mock-kubernetes"
//	    type: "mock"
//	    tools:
//	      - name: "kubectl_get_pods"
//	        responses:
//	          - condition: {namespace: "default"}
//	            response: "pod1\npod2\npod3"
//
//	# Standalone mode
//	muster test --mock-mcp-server --mock-config=mock-config.yaml
//
// # Extension Points
//
// The testing framework is designed for extensibility through several mechanisms:
//
// ## Custom Test Steps
// - Implement custom step types by extending the TestStep interface
// - Register custom step handlers with the TestRunner
// - Support for domain-specific validation logic
//
// ## Custom Reporters
// - Implement the TestReporter interface for custom output formats
// - Support for custom metrics collection and analysis
// - Integration with external monitoring and alerting systems
//
// ## Custom Scenario Loaders
// - Implement the TestScenarioLoader interface for alternative configuration sources
// - Support for dynamic scenario generation
// - Integration with external test management systems
//
// # Thread Safety
//
// The testing framework is designed with thread safety as a core requirement:
//
// - **Concurrent Execution**: All components support concurrent access
// - **Immutable Data**: Test scenarios and configuration are immutable after loading
// - **Thread-Safe Reporting**: Reporter implementations handle concurrent access
// - **Resource Isolation**: Each test execution maintains isolated state
//
// **Safety Guarantees**:
// - TestRunner can safely execute multiple scenarios concurrently
// - MCPTestClient instances are thread-safe for concurrent tool calls
// - TestReporter can safely handle concurrent progress and result reporting
// - Shared resources (connections, files) are protected with appropriate synchronization
//
// # Performance Characteristics
//
// The framework is optimized for both development and CI/CD environments:
//
// ## Execution Performance
// - **Parallel Execution**: Configurable worker pools (1-10 workers recommended)
// - **Connection Pooling**: Reuse MCP connections across multiple test steps
// - **Lazy Loading**: Scenarios loaded only when needed
// - **Memory Efficiency**: Streaming result processing for large test suites
//
// ## Scaling Characteristics
// - **Linear Scaling**: Performance scales linearly with parallel worker count
// - **Memory Usage**: O(n) memory usage relative to active scenarios
// - **Network Efficiency**: Connection reuse and request batching
// - **Resource Cleanup**: Automatic cleanup prevents resource leaks
//
// **Performance Recommendations**:
// - Use 2-4 parallel workers for development testing
// - Use 4-8 parallel workers for CI/CD environments
// - Implement timeouts appropriate for your environment (30s-5m recommended)
// - Monitor memory usage with large test suites (>100 scenarios)
//
// # Usage Patterns
//
// ## Basic Testing
//
//	```bash
//	muster test                          # Run all tests
//	muster test --category=behavioral    # Category-specific
//	muster test --concept=serviceclass  # Concept-specific
//	muster test --scenario=basic-create # Scenario-specific
//	```
//
// ## Advanced Configuration
//
//	```bash
//	muster test --parallel=4 --timeout=10m --fail-fast
//	muster test --verbose --debug --output-format=json
//	```
//
// ## CI/CD Integration
//
//	```bash
//	muster test --category=integration --output-format=junit --report-file=results.xml
//	```
//
// ## Mock MCP Server Testing
//
//	```bash
//	# Run with mock servers defined in scenarios
//	muster test --scenario=test-with-mock
//
//	# Run standalone mock server
//	muster test --mock-mcp-server --mock-config=mock-tools.yaml
//	```
//
// # Dependencies
//
// The testing framework requires:
// - A running muster aggregator server (muster serve)
// - MCP protocol communication capabilities
// - Access to all core_* MCP tools exposed by the aggregator
// - Write access for temporary files and test artifacts
//
// # Error Handling
//
// The framework implements comprehensive error handling:
// - **Graceful Degradation**: Continue execution when possible
// - **Detailed Error Messages**: Context-rich error information
// - **Error Classification**: Distinguish between test failures and framework errors
// - **Recovery Mechanisms**: Automatic retry and fallback logic where appropriate
//
// # Integration with CI/CD
//
// The framework provides first-class CI/CD support:
// - **Structured Output**: JSON, JUnit XML, and TAP output formats
// - **Exit Codes**: Proper exit codes for automation (0=success, 1=failures, 2=errors)
// - **Report Files**: File-based output for artifact collection
// - **Environment Integration**: Support for CI environment variables and configuration
//
// For complete usage documentation, see docs/testing/README.md
// For scenario authoring guidance, see docs/testing/scenarios.md
// For comprehensive examples, see docs/testing/examples/
package testing
