# muster test

Execute comprehensive behavioral and integration tests for Muster.

## Synopsis

```
muster test [OPTIONS]
```

## Description

The `test` command executes comprehensive behavioral and integration tests for Muster by creating clean, isolated instances of `muster serve` for each test scenario. It validates all core Muster concepts including ServiceClass management, workflow execution, MCP server registration, and service lifecycle management.

## Test Categories

### Behavioral Tests
BDD-style scenarios validating expected user behavior:
- ServiceClass creation and management
- Service lifecycle operations
- Workflow execution patterns
- Error handling scenarios

### Integration Tests
Component interaction and end-to-end validation:
- MCP server integration
- Tool aggregation functionality
- API schema validation
- Cross-component communication

## Core Concepts Tested

| Concept | Description | Example Scenarios |
|---------|-------------|------------------|
| `serviceclass` | ServiceClass management and dynamic instantiation | Creation, validation, templating |
| `workflow` | Workflow execution and argument templating | Parameter passing, step execution |
| `mcpserver` | MCP server registration and tool aggregation | Server lifecycle, tool discovery |
| `service` | Service lifecycle and dependency management | Start/stop, health checks, dependencies |

## Options

### Test Execution
- `--scenario` (string): Run specific test scenario by name
- `--category` (string): Run specific test category (behavioral\|integration)
- `--concept` (string): Run tests for specific concept (serviceclass\|workflow\|mcpserver\|service)

### Execution Control
- `--parallel` (int): Number of parallel test workers
  - Default: `1`
  - Recommended: `4-8` for faster execution
- `--fail-fast`: Stop on first test failure
  - Default: `false`
- `--base-port` (int): Base port for test instances
  - Default: `18000`
  - Each test instance uses sequential ports

### Output and Debugging
- `--verbose`: Enable detailed test output
- `--debug`: Enable debug logging for test scenarios

### Configuration
- `--config-path` (string): Custom configuration directory for tests
  - Default: `~/.config/muster`

### Schema Operations
- `--generate-schema`: Generate API schema from muster serve instance
- `--schema-output` (string): Output file for generated schema
  - Default: `schema.json`
- `--validate-scenarios`: Validate test scenarios against API schema
- `--schema-input` (string): Input schema file for validation

### MCP Server Mode
- `--mcp-server`: Run test framework as MCP server (stdio transport)

## Examples

### Running All Tests
```bash
# Run complete test suite
muster test

# Run with parallel execution for speed
muster test --parallel 4

# Run with verbose output
muster test --verbose --debug
```

### Running Specific Tests
```bash
# Run specific test scenario
muster test --scenario serviceclass-crud

# Run all behavioral tests
muster test --category behavioral

# Run all ServiceClass-related tests
muster test --concept serviceclass

# Run workflow tests with debugging
muster test --concept workflow --debug --verbose
```

### Parallel Execution
```bash
# Run with maximum parallelism
muster test --parallel 8

# Run on custom port range
muster test --parallel 4 --base-port 20000

# Fast execution with fail-fast
muster test --parallel 6 --fail-fast
```

### Schema Generation and Validation
```bash
# Generate API schema
muster test --generate-schema --schema-output api-v2.json

# Validate scenarios against schema
muster test --validate-scenarios --schema-input api-v2.json

# Generate and validate in one run
muster test --generate-schema --validate-scenarios --verbose
```

## Test Scenarios

### ServiceClass Tests
```bash
# Basic CRUD operations
muster test --scenario serviceclass-crud

# Complex ServiceClass with dependencies
muster test --scenario serviceclass-with-mock

# Parameter validation and templating
muster test --scenario serviceclass-arg-validation

# Availability checking
muster test --scenario serviceclass-check-available
```

### Service Lifecycle Tests
```bash
# Complete service lifecycle
muster test --scenario service-lifecycle

# Service creation with parameters
muster test --scenario service-create-with-params

# Service persistence and state
muster test --scenario service-persistence

# Error handling scenarios
muster test --scenario service-error-handling
```

### Workflow Tests
```bash
# Basic workflow execution
muster test --scenario workflow-basic

# Advanced workflow with conditionals
muster test --scenario workflow-advanced

# Argument templating and resolution
muster test --scenario workflow-arg-templating

# Workflow execution tracking
muster test --scenario workflow-execution-lifecycle
```

### MCP Server Tests
```bash
# MCP server lifecycle management
muster test --scenario mcpserver-lifecycle

# Tool availability and aggregation
muster test --scenario mcpserver-tool-availability-lifecycle

# Server health and connectivity
muster test --scenario mcpserver-check-available
```

## Test Framework Features

### Isolated Test Environments
Each test scenario runs in a completely isolated environment:

```bash
# Each test gets its own:
# - Muster serve instance on unique port
# - Temporary configuration directory
# - Clean state with no interference
# - Independent MCP server processes
```

### Automatic Cleanup
Tests automatically clean up resources:

```bash
# After each test:
# - Muster serve process is terminated
# - Temporary directories are cleaned
# - Ports are freed for reuse
# - Test artifacts are collected
```

### Mock Integration
Tests can use mock MCP servers for consistent results:

```bash
# Mock scenarios provide:
# - Predictable tool responses
# - Controlled failure simulation
# - Isolated testing without external dependencies
# - Consistent cross-environment results
```

## Output Formats

### Default Output
```bash
muster test --scenario serviceclass-crud
# Running scenario: serviceclass-crud
# ✓ Setup test environment
# ✓ Start muster serve on port 19001
# ✓ Create ServiceClass 'test-service'
# ✓ Verify ServiceClass availability
# ✓ Create service instance
# ✓ Check service status
# ✓ Cleanup test environment
# 
# PASS: serviceclass-crud (2.34s)
```

### Verbose Output
```bash
muster test --scenario serviceclass-crud --verbose
# Running scenario: serviceclass-crud
# [19:00:01] Setting up test environment in /tmp/muster-test-abc123
# [19:00:01] Starting muster serve on port 19001
# [19:00:02] Waiting for server readiness...
# [19:00:02] Server ready, executing test steps
# [19:00:02] Step 1: Create ServiceClass 'test-service'
# [19:00:02] → muster create serviceclass test-service
# [19:00:02] ✓ ServiceClass created successfully
# [19:00:03] Step 2: Verify ServiceClass availability
# [19:00:03] → muster check serviceclass test-service
# [19:00:03] ✓ ServiceClass is available
# [19:00:03] Cleaning up test environment
# 
# PASS: serviceclass-crud (2.34s)
```

### Debug Output
```bash
muster test --scenario serviceclass-crud --debug
# Includes all verbose output plus:
# [DEBUG] Configuration loaded from /tmp/muster-test-abc123
# [DEBUG] Starting muster serve with args: [--config-path, /tmp/muster-test-abc123, --port, 19001]
# [DEBUG] Server PID: 12345
# [DEBUG] Executing command: muster create serviceclass test-service
# [DEBUG] Command output: {"name": "test-service", "status": "Created"}
# [DEBUG] Terminating server process 12345
```

## Test Results and Reporting

### Summary Report
```bash
muster test --parallel 4
# Test Summary:
# ================
# Total scenarios: 45
# Passed: 43
# Failed: 2
# Skipped: 0
# Duration: 45.67s
# 
# Failed scenarios:
# - service-complex-dependencies (dependency timeout)
# - workflow-external-tool (tool not available)
```

### Structured Reporting
Test results are automatically structured for CI/CD integration:

```bash
# Exit codes:
# 0 = All tests passed
# 1 = Some tests failed
# 2 = Test framework error
# 3 = Configuration error
```

## Performance and Optimization

### Parallel Execution
```bash
# Optimal parallel settings by system:
muster test --parallel 4   # Standard development machine
muster test --parallel 8   # High-performance CI/CD runner
muster test --parallel 2   # Resource-constrained environment
```

### Port Management
```bash
# Custom port range for conflict avoidance
muster test --parallel 4 --base-port 25000
# Uses ports: 25001, 25002, 25003, 25004
```

### Fast Development Iteration
```bash
# Quick feedback during development
muster test --scenario my-new-feature --fail-fast --debug

# Test specific component after changes
muster test --concept serviceclass --parallel 2
```

## CI/CD Integration

### GitHub Actions Example
```yaml
name: Muster Tests
on: [push, pull_request]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - name: Install Go
        uses: actions/setup-go@v3
        with:
          go-version: '1.21'
      - name: Build Muster
        run: go install
      - name: Run Tests
        run: muster test --parallel 4 --verbose
```

### GitLab CI Example
```yaml
test:
  stage: test
  script:
    - go install
    - muster test --parallel 8 --fail-fast
  artifacts:
    reports:
      junit: test-results.xml
```

## Schema Generation and Validation

### API Schema Generation
```bash
# Generate current API schema
muster test --generate-schema --verbose

# Output to specific file
muster test --generate-schema --schema-output current-api.json

# The generated schema includes:
# - All API endpoints and their specifications
# - Tool definitions and parameters
# - Request/response formats
# - Error codes and structures
```

### Scenario Validation
```bash
# Validate all scenarios against schema
muster test --validate-scenarios --schema-input api-schema.json

# This ensures:
# - Test scenarios match actual API
# - No drift between tests and implementation
# - Scenarios cover all API endpoints
# - Parameter formats are correct
```

## MCP Server Mode

The test framework can run as an MCP server for AI assistant integration:

```bash
# Run test framework as MCP server
muster test --mcp-server

# Exposed tools:
# - run_test_scenario: Execute specific test scenarios
# - list_test_scenarios: Get available test scenarios
# - generate_api_schema: Generate current API schema
# - validate_test_scenarios: Validate scenarios against schema
```

### AI Assistant Configuration
```json
{
  "mcpServers": {
    "muster-test": {
      "command": "muster",
      "args": ["test", "--mcp-server"],
      "env": {}
    }
  }
}
```

## Error Handling and Troubleshooting

### Common Issues

#### Port Conflicts
```bash
muster test --parallel 4
# Error: Port 19001 already in use

# Solution: Use different base port
muster test --parallel 4 --base-port 20000
```

#### Configuration Issues
```bash
muster test --scenario serviceclass-crud
# Error: Failed to load test configuration

# Solution: Check configuration path
muster test --scenario serviceclass-crud --config-path ~/.config/muster
```

#### Test Environment Setup
```bash
muster test --scenario my-test --debug
# [DEBUG] Failed to create test directory: permission denied

# Solution: Check permissions
chmod 755 /tmp
# Or use custom test directory with --config-path
```

### Test Debugging

#### Individual Scenario Debugging
```bash
# Run single scenario with full debugging
muster test --scenario problematic-test --debug --verbose

# Check logs in test directory
ls -la /tmp/muster-test-*/
cat /tmp/muster-test-*/server.log
```

#### Manual Test Reproduction
```bash
# Set up test environment manually
mkdir /tmp/manual-test
cd /tmp/manual-test

# Start muster serve manually
muster serve --config-path . --port 19999 --debug &

# Run test commands manually
muster create serviceclass test-service
muster check serviceclass test-service
```

## Related Commands

- **[serve](serve.md)** - Start server instances (used by tests)
- **[create](create.md)** - Create resources (tested by framework)
- **[list](list.md)** - List resources (used in test validation)
- **[agent](agent.md)** - Interactive debugging of test scenarios

## Advanced Usage

### Custom Test Development
```bash
# Create new test scenario file
# Location: internal/testing/scenarios/my-custom-test.yaml

# Run your custom test
muster test --scenario my-custom-test --debug

# Validate scenario format
muster test --validate-scenarios --schema-input current-schema.json
```

### Performance Benchmarking
```bash
# Benchmark test execution time
time muster test --parallel 8

# Compare different parallelism levels
for p in 1 2 4 8; do
  echo "Testing with $p parallel workers:"
  time muster test --parallel $p --quiet
done
```

### Integration with Development Workflow
```bash
#!/bin/bash
# Pre-commit hook script

echo "Running Muster tests before commit..."

# Run tests with fail-fast for quick feedback
if ! muster test --parallel 4 --fail-fast --quiet; then
  echo "Tests failed! Commit aborted."
  exit 1
fi

echo "All tests passed. Commit proceeding."
``` 