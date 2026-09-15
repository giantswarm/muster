# muster test

Execute comprehensive behavioral and integration tests for muster

## Synopsis

The test command executes comprehensive behavioral and integration tests
for muster by creating clean, isolated instances of muster serve for each test scenario.

This command validates all core muster concepts including:
- Workflow execution and arg resolution
- MCPServer registration and tool aggregation

- Service lifecycle management and dependencies

Test execution modes:
1. Full Test Suite (default): Runs all behavioral and integration tests
2. Category-based: Run specific test categories (--category)
3. Concept-based: Run tests for specific concepts (--concept)
4. Scenario-based: Run individual test scenarios (--scenario)
   Mode-based: Run the scenarios of one definition source (--mode filesystem|kubernetes);
   mode kubernetes needs the envtest binaries (KUBEBUILDER_ASSETS) and is skipped without them
5. MCP Server mode (--mcp-server): Runs an MCP server that exposes test functionality via stdio
6. Schema Generation (--generate-schema): Generate API schema from muster serve instance
7. Scenario Validation (--validate-scenarios): Validate test scenarios against API schema

Test Categories:
- behavioral: BDD-style scenarios validating expected behavior
- integration: Component interaction and end-to-end validation

Core Concepts:
- workflow: Workflow execution and arg templating
- mcpserver: MCP server registration and tool aggregation

- service: Service lifecycle and dependency management

Schema Generation and Validation:
The test command can generate JSON schemas from live muster serve instances and validate
existing test scenarios against these schemas. This ensures test scenarios stay in sync
with the actual API as it evolves.

Example usage:
  muster test                              # Run all tests
  muster test --category=behavioral        # Run behavioral tests only
  muster test --concept=workflow          # Run Workflow tests
  muster test --scenario=basic-create     # Run specific scenario
  muster test --mode=kubernetes           # Run the Kubernetes-mode (envtest) scenarios only
  muster test --verbose --debug           # Detailed output and debugging
  muster test --fail-fast                 # Stop on first failure
  muster test --parallel=50               # Run with 50 parallel workers
  muster test --base-port=19000           # Use port 19000+ for test instances
  muster test --readiness-timeout=60s     # Allow slow instance startup (e.g. CI)
  muster test --mcp-server                # Run as MCP server (stdio transport)
  muster test --generate-schema           # Generate API schema from muster serve
  muster test --validate-scenarios        # Validate scenarios against schema

Schema Generation Examples:
  muster test --generate-schema --verbose --schema-output=api-v2.json
  muster test --validate-scenarios --schema-input=api-v2.json --verbose

In MCP Server mode:
- The test command acts as an MCP server using stdio transport
- It exposes all test functionality as MCP tools
- It's designed for integration with AI assistants like Claude or Cursor
- Configure it in your AI assistant's MCP settings

The test framework uses YAML-based test scenario definitions and automatically
creates clean, isolated muster serve instances for each test scenario.
Each scenario can specify pre-configuration including mock MCP servers,
MCPServer definitions and workflows.

Test results are reported with structured output suitable for CI/CD integration.

```
muster test [flags]
```

## Options

```
      --base-port int                Starting port number for test muster instances (default 18000)
      --category string              Run tests for specific category (behavioral, integration)
      --concept string               Run tests for specific concept (workflow, mcpserver, service)
      --config string                Path to test configuration directory (default: internal test scenarios)
      --config-name string           Name of the mock MCP server configuration
      --config-path string           Configuration directory (default "~/.config/muster")
      --debug                        Enable debug logging and MCP protocol tracing
      --fail-fast                    Stop test execution on first failure
      --generate-schema              Generate API schema from muster serve instance
  -h, --help                         help for test
      --keep-temp-config             Keep temporary config directory after test execution for debugging
      --mcp-server                   Run as MCP server (stdio transport)
      --mock-config string           Path to mock MCP server configuration file
      --mock-mcp-server              Run as mock MCP server
      --mode string                  Run only the scenarios of one definition source (filesystem, kubernetes); default: both
      --parallel int                 Number of parallel test workers (1-20) (default 1)
      --readiness-timeout duration   Per-instance deadline for expected resources (tools, workflows, MCP servers) to become available after startup; raise on slow or contended machines such as CI (default 15s)
      --report string                Path to save detailed test report (default: stdout only)
      --scenario string              Run specific test scenario by name
      --schema-input string          Input schema file for validation (default "schema.json")
      --schema-output string         Output file for generated schema (default "schema.json")
      --startup-parallel int         Max scenarios in their instance-startup phase at once (0 = default of 8, negative = unlimited); bounds the t=0 cold-start herd on small CI machines without limiting steady-state parallelism
      --timeout duration             Overall test execution timeout (default 10m0s)
      --validate-scenarios           Validate test scenarios against API schema
      --verbose                      Enable verbose test output
```

## SEE ALSO

* [muster](README.md)	 - Aggregate MCP servers behind one authenticated endpoint
