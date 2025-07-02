# muster Test Framework

## Recent Updates and Improvements

**🆕 Major Architecture Improvements**: The muster test framework has been significantly enhanced with the following key changes:

### Isolated Test Execution
- **Separate muster Instances**: Each test scenario now runs against its own dedicated muster serve instance
- **Port Isolation**: Instances are automatically assigned unique ports (starting from base port 18000)
- **Configuration Isolation**: Each scenario gets its own temporary configuration directory
- **Complete Cleanup**: Instances and configurations are automatically cleaned up after each test

### Updated Tool Naming Conventions
- **Workflows**: Now use `workflow_<workflow-name>` prefix (old `action_<workflow-name>` is deprecated)
- **Mock Tools**: Use `x_<mockserver-name>_<tool-name>` pattern for mock MCP server tools
- **Core Tools**: Continue to use direct names like `core_serviceclass_create`

### Essential Mock Integration
- **Tests Core Functionality**: Mock MCP servers are essential for testing muster's core MCP server management and tool aggregation capabilities
- **Enables Concept Testing**: Other muster concepts (workflows, serviceclasses, capabilities, services) depend on MCP server tools being available
- **Automatic Configuration**: Mock MCP server config files and server definitions are generated from scenario definitions
- **Full Integration Testing**: Mock servers run as separate processes managed by muster serve, so the test framework can test the complete mcpserver management workflow
- **Tool Aggregation Testing**: Validates that mock tools are properly exposed through muster's aggregated MCP interface

### Dual Execution Modes
- **CLI Mode**: Traditional command-line execution with `muster test`
- **MCP Server Mode**: New `muster test --mcp-server` for IDE integration and AI-powered testing

### API Schema Generation and Validation
- **Schema Generation**: Generate JSON schemas from live muster serve instances (`--generate-schema`)
- **Scenario Validation**: Validate test scenarios against API schemas (`--validate-scenarios`) 
- **Unified Validation**: Both CLI and MCP server provide identical validation functionality
- **Tool Prefix Validation**: Smart validation rules for `core_*`, `x_*`, `workflow_*`, and `api_*` tools
- **CI/CD Integration**: Automated schema validation to catch API compatibility issues

### Improved Debugging
- **Instance Log Capture**: All stdout/stderr from test instances is captured and available
- **Enhanced Error Reporting**: Detailed error information with context from instance logs
- **Debug Mode**: Comprehensive debugging output with `--debug` flag

## Overview

The muster test framework provides a comprehensive testing solution for validating all muster functionality through automated test scenarios. As an muster developer, you can use this framework to:

- **Test Core MCP Integration**: Validate muster's ability to manage MCP servers and aggregate their tools
- **Test Concept Dependencies**: Verify that workflows, serviceclasses, capabilities, and services work correctly with MCP server tools  
- **Catch Regressions**: Automatically detect when changes break existing functionality  
- **Validate New Features**: Ensure new implementations work correctly across different scenarios
- **Debug Issues**: Systematically reproduce and diagnose problems with comprehensive logging
- **Ensure Quality**: Maintain high confidence in muster reliability and correctness

The framework executes test scenarios written in YAML that define step-by-step operations and expected outcomes, creating **isolated muster instances** for each test scenario to ensure complete test isolation and reliable results. Mock MCP servers are used to test muster's core mcpserver management capabilities and enable testing of concepts that depend on external tool availability.

## Test Execution Architecture

### Separate muster serve Instances

**🏗️ Each test scenario runs against its own dedicated muster serve instance!**

The framework automatically creates and manages isolated muster instances for optimal test reliability:

- **Individual Configuration**: Each scenario gets its own temporary configuration directory with generated config files
- **Separate Ports**: Each instance binds to a unique port to avoid conflicts (starting from base port 18000)
- **Mock Integration**: Mock MCP servers test muster's core mcpserver management and enable testing of concepts that depend on MCP server tools
- **Complete Isolation**: Scenarios cannot interfere with each other's resources or state
- **Automatic Cleanup**: Instances, configurations, and temporary files are cleaned up after each test
- **Log Capture**: All stdout/stderr from each instance is captured and available for debugging

### Configuration Generation

For each test scenario, the framework generates the necessary files before starting muster serve to test the complete muster ecosystem:

1. **Mock MCP Server Configs**: Individual configuration files for each mock MCP server defined in the scenario
2. **MCP Server Definitions**: MCP Server definition files that tell muster serve how to manage the mock MCP servers
3. **muster Configuration**: Main muster config file that references defines the aggregated MCP server port
4. **ServiceClass Definitions**: Generated from the scenario's `pre_configuration.service_classes`
5. **Workflow Definitions**: Generated from the scenario's `pre_configuration.workflows`

**Why Mock Servers Are Essential**: Since workflows, serviceclasses, capabilities, and services all depend on MCP server tools being available through muster's aggregated MCP interface, mock mcpservers are required to test these concepts properly. Without them, you can only test the core CRUD operations but not the actual functionality that depends on external tools.

### Mock MCP Server Tool Naming

**Important Naming Convention**: Mock MCP server tools follow a specific naming pattern:

- **In Mock Config**: Tools are defined with simple names like `"mock-tool1"`
- **In Test Scenarios**: Tools must be referenced as `"x_<mockserver>_<tool-name>"`

**Example**:
```yaml
# In pre_configuration.mcp_servers:
- name: "kubernetes-mock"
  config:
    tools:
      - name: "get_pods"        # ← Defined as simple name
        # ... tool definition

# In test steps:
steps:
  - id: "test-k8s-tool"
    tool: "x_kubernetes-mock_get_pods"   # ← Referenced with x_ prefix
    args:
      namespace: "default"
```

### Workflow Naming

**Updated Naming Convention**: Workflows are now exposed with `workflow_` prefix:

- **Current**: `workflow_<workflow-name>` ✅
- **Old/Deprecated**: `action_<workflow-name>` ❌ (no longer works)

**Example**:
```yaml
# Workflow definition in pre_configuration:
workflows:
  - name: "deploy-app"
    # ... workflow definition

# Usage in test steps:
steps:
  - id: "run-deployment"
    tool: "workflow_deploy-app"   # ← Use workflow_ prefix
    args:
      app_name: "test-app"
```

## Quick Start

### 1. No Setup Required! 

**🎉 The test framework now manages muster instances automatically!**

Unlike previous versions, you **do not need** to start an external muster aggregator. The framework creates isolated, temporary muster instances for each test scenario, ensuring:

- **Complete Test Isolation**: Each scenario runs against a fresh muster instance
- **No Resource Conflicts**: Tests cannot interfere with each other
- **Automatic Cleanup**: Instances and configuration are cleaned up after each test
- **Enhanced Debugging**: Instance logs are captured and available for analysis

### 2. Generate API Schema and Validate Scenarios

**🔧 Keep test scenarios synchronized with the API:**

```bash
# Generate API schema from current muster serve
./muster test --generate-schema --verbose

# Validate all scenarios against the schema
./muster test --validate-scenarios --verbose

# Both CLI and MCP server provide identical validation results
```

### 3. Run Your First Test

```bash
# Run a simple test to verify everything works
./muster test --scenario=serviceclass-basic-operations --verbose

# The framework will automatically:
# 1. Create a temporary muster instance on an available port
# 2. Generate mock MCP server configurations if needed
# 3. Execute the test scenario against that instance
# 4. Capture logs and results
# 5. Clean up the instance and configuration

# If successful, run all behavioral tests
./muster test --category=behavioral
```

### 3. Common Usage Patterns

```bash
# Test specific functionality you're working on
./muster test --concept=serviceclass          # Test all ServiceClass functionality
./muster test --concept=workflow              # Test all Workflow functionality
./muster test --concept=mcpserver             # Test all MCP Server functionality

# Schema generation and validation
./muster test --generate-schema               # Generate API schema
./muster test --validate-scenarios            # Validate against schema

# Run tests in parallel for faster execution (each gets its own instance)
./muster test --parallel=4 --base-port=18000

# Get detailed output for debugging (includes instance logs)
./muster test --verbose --debug

# Stop on first failure for quick feedback
./muster test --fail-fast
```

### 5. Schema Generation and Validation

```bash
# Generate API schema from live muster serve instance
./muster test --generate-schema --schema-output=current-schema.json --verbose

# Validate scenarios to catch API compatibility issues
./muster test --validate-scenarios --schema-input=current-schema.json --verbose

# Example validation output:
# 🔍 API Schema Validation Results
# Total scenarios: 131, Valid: 36, Invalid: 95, Errors: 330
# Error Types: unexpected_argument: 303, unknown_tool: 27
```

### 6. Enhanced Debugging with Instance Logs

```bash
# Instance logs are captured automatically and shown in debug mode
./muster test --scenario=serviceclass-basic-operations --debug

# Example debug output:
# 📋 Captured instance logs: stdout=7977 chars, stderr=0 chars
# 📄 Instance Logs:
#    STDOUT:
#       time=2025-06-19T11:07:19.565+02:00 level=INFO msg="Loaded configuration..."
#       time=2025-06-19T11:07:19.565+02:00 level=DEBUG msg="Registering service manager..."
```

## How to Execute Test Scenarios

### Command Line Interface

The primary way to run tests is through the CLI:

```bash
# Basic execution
./muster test

# With filters and options
./muster test --category=behavioral --concept=serviceclass --verbose
```

### MCP Server Mode

Tests can also be executed via MCP server mode for IDE integration and AI-powered testing:

```bash
# Start muster in MCP server mode for testing
./muster test --mcp-server

# The MCP server exposes these tools:
# - mcp_muster-test_test_run_scenarios      # Execute test scenarios
# - mcp_muster-test_test_list_scenarios     # List available scenarios  
# - mcp_muster-test_test_validate_scenario  # Validate YAML structure AND API schema
# - mcp_muster-test_test_get_results       # Get detailed test results
```

**🎯 Unified Functionality**: Both CLI and MCP server provide identical test execution and validation capabilities.

For detailed MCP usage, see [testing-via-mcp.md](testing-via-mcp.md).

### Filtering Tests

The framework organizes tests by **category** and **concept** to help you run exactly what you need:

```bash
# By Category - Type of testing
./muster test --category=behavioral      # User-facing functionality tests
./muster test --category=integration     # Component interaction tests

# By Concept - What you're testing  
./muster test --concept=serviceclass     # All ServiceClass tests
./muster test --concept=workflow         # All Workflow tests
./muster test --concept=mcpserver        # All MCP Server tests
./muster test --concept=capability       # All Capability tests
./muster test --concept=service          # All Service tests

# Specific scenario
./muster test --scenario=serviceclass-basic-crud-operations
```

### Execution Control

```bash
# Parallel execution (faster, but harder to debug)
./muster test --parallel=4              # Run up to 4 tests simultaneously
./muster test --parallel=1              # Single-threaded (default)

# Port management for parallel execution
./muster test --parallel=4 --base-port=18000  # Instances use ports 18000-18003

# Timeout control
./muster test --timeout=10m             # Set global timeout to 10 minutes
./muster test --timeout=1h              # Longer timeout for complex tests

# Failure handling
./muster test --fail-fast               # Stop immediately on first failure
./muster test                           # Continue running all tests even if some fail
```

### Output Control

```bash
# Verbosity levels
./muster test --verbose                 # Show detailed progress and results
./muster test --debug                   # Show MCP protocol traces and internal details
./muster test                           # Normal output (default)

# Output formats
./muster test --output-format=text      # Human-readable (default)
./muster test --output-format=json      # Machine-readable JSON
./muster test --output-format=junit     # JUnit XML for CI/CD

# Save results to file
./muster test --report-file=results.json --output-format=json
```

## Understanding Test Categories and Concepts

### What Are Test Categories?

Test categories organize tests by **testing approach**:

- **`behavioral`** - Tests that verify muster works as users expect it to work
  - Example: "When I create a ServiceClass, I can instantiate a Service from it"
  - Focus: API contracts, user workflows, expected behavior
  
- **`integration`** - Tests that verify components work together correctly
  - Example: "Workflow can orchestrate multiple Services with dependencies"
  - Focus: Component interactions, data flow, end-to-end scenarios

### What Are Test Concepts?

Test concepts organize tests by **what functionality** is being tested:

- **`serviceclass`** - Tests ServiceClass creation, validation, and Service instantiation
- **`workflow`** - Tests Workflow execution, arg templating, and step dependencies  
- **`mcpserver`** - Tests MCP server registration, tool aggregation, and connection management
- **`capability`** - Tests Capability definitions, operation mapping, and API abstractions
- **`service`** - Tests Service lifecycle, dependency management, and state transitions

### Practical Examples

```bash
# Test if ServiceClass feature works for users
./muster test --concept=serviceclass --category=behavioral

# Test if ServiceClasses integrate properly with other components
./muster test --concept=serviceclass --category=integration

# Test all user-facing functionality across all concepts
./muster test --category=behavioral

# Test specific workflow functionality
./muster test --concept=workflow
```

## How to Debug Failing Test Scenarios

When a test fails, follow this systematic approach to diagnose and fix the issue:

### Step 1: Get Detailed Information

```bash
# Run the specific failing test with maximum verbosity
./muster test --scenario=failing-scenario-name --verbose --debug

# Or run with fail-fast to focus on the first failure
./muster test --concept=serviceclass --fail-fast --verbose
```

This will show you:
- Which step failed and why
- The exact MCP tool call that was made
- The response received vs. what was expected
- Complete error messages and stack traces
- **Instance logs** from the isolated muster serve process

### Step 2: Analyze Instance Logs

With the `--debug` flag, you'll see captured logs from the muster instance:

```bash
./muster test --scenario=failing-scenario --debug

# Example output:
# 📋 Captured instance logs: stdout=7977 chars, stderr=0 chars
# 📄 Instance Logs:
#    STDOUT:
#       time=2025-06-19T11:07:19.565+02:00 level=INFO msg="Loaded configuration..."
#       time=2025-06-19T11:07:19.565+02:00 level=ERROR msg="Failed to register service..."
```

This helps identify:
- Configuration loading issues
- Service registration problems  
- Runtime errors during test execution
- Performance bottlenecks

### Step 3: Verify Test Environment

```bash
# Check if you can build muster
go build -o muster .

# Test with a simple scenario first
./muster test --scenario=serviceclass-basic-operations --debug

# Check available port range if you see port conflicts
./muster test --base-port=19000 --scenario=failing-scenario
```

### Step 4: Isolate the Problem

```bash
# Run just the problematic step manually by examining the scenario YAML
# Look at the test scenario to see what MCP tool and args are being used

# Example: If "core_serviceclass_create" is failing, check:
# - Is the YAML in the scenario valid?
# - Are there any resource conflicts (names already exist)?
# - Are all required args provided?
```

### Step 5: Common Issues and Solutions

#### Test Fails with "Port Already in Use"
**Problem**: Base port range is occupied by other processes

```bash
# Solution: Use a different base port range
./muster test --base-port=19000 --scenario=failing-scenario

# Or check what's using the ports
ss -tlnp | grep 18000
```

#### Test Fails with "Instance Startup Timeout"
**Problem**: muster instance takes too long to start

```bash
# Solution: Check instance logs for startup issues
./muster test --scenario=failing-scenario --debug

# Look for errors in the instance logs like:
# - Configuration file parsing errors
# - Missing dependencies
# - Permission issues
```

#### Test Fails with "Tool Not Found"
**Problem**: Required MCP tool is not available in the test instance

```bash
# Solution: Check if the tool should be available
# This usually indicates:
# - Missing MCP server registration in test configuration
# - Tool name typo in the scenario
# - Version mismatch between test and implementation
```

#### Test Fails with "Resource Already Exists"
**Problem**: Previous test run didn't clean up resources

```bash
# Solution: This shouldn't happen with isolated instances, but check:
# - Scenario cleanup steps are properly defined
# - Unique resource naming in the scenario
# - No hard-coded resource names that conflict
```

#### Test Fails with Validation Errors
**Problem**: Response doesn't match expected values

```bash
# Solution: Check the scenario's "expected" section
# Compare with actual response (shown in debug output)
# Update expectations if the behavior changed intentionally

# Use debug mode to see the exact response:
./muster test --scenario=failing-scenario --debug
```

### Step 6: Debug Individual Test Steps

You can examine what each test step is doing by looking at the scenario YAML file:

```yaml
# Example failing step
- name: "create-test-serviceclass"
  tool: "core_serviceclass_create"     # This is the MCP tool being called
  args:                          # These are the args sent
    yaml: |
      name: test-serviceclass
      # ... rest of YAML
  expected:                           # This is what the test expects
    success: true
    contains: ["created successfully"]
```

To debug this step:
1. Check if `core_serviceclass_create` tool should be available
2. Verify the YAML args are valid
3. Look at the instance logs for detailed error messages
4. Check if the response format has changed

### Step 7: Advanced Debugging with Multiple Parallel Tests

```bash
# If parallel tests are failing, run them sequentially for easier debugging
./muster test --parallel=1 --concept=serviceclass --debug

# If port conflicts occur in parallel execution
./muster test --parallel=2 --base-port=20000

# Run a single problematic scenario in isolation
./muster test --scenario=specific-scenario --debug
```

### Step 8: Update or Fix the Test

After identifying the issue:

- **If muster behavior changed**: Update the test scenario expectations
- **If muster has a bug**: Fix the bug in muster code  
- **If test scenario is wrong**: Fix the scenario YAML
- **If test environment issue**: Check build and dependency issues

## Parallel Execution

### Worker Pool Configuration

The test framework supports configurable parallel execution:

```bash
# Run with 4 parallel workers (recommended for development)
./muster test --parallel=4

# Run with 8 parallel workers (recommended for CI/CD)
./muster test --parallel=8

# Single-threaded execution (useful for debugging)
./muster test --parallel=1
```

### Best Practices

- **Development**: Use 2-4 workers to balance speed and resource usage
- **CI/CD**: Use 4-8 workers for faster execution

## Related Documentation

For comprehensive information about specific testing topics, see:

- **[API Schema Validation](api-schema-validation.md)** - Complete guide to schema generation and validation
- **[Testing via MCP](testing-via-mcp.md)** - MCP server integration for AI-powered testing  
- **[Test Scenarios](scenarios.md)** - Writing and structuring test scenarios
- **[Scenario Examples](examples/)** - Ready-to-use scenario templates
- **Debugging**: Use 1 worker to avoid concurrent execution issues
- **Resource Limits**: Monitor memory usage with large test suites

### Performance Considerations

- Tests are executed in isolation to prevent interference
- Connection pooling reduces MCP overhead
- Cleanup operations are parallelized where safe
- Resource usage scales linearly with worker count

## Reporting

### Output Formats

#### Text Format (Default)
```bash
./muster test --output-format=text
```

Provides human-readable output with:
- Progress indicators during execution
- Detailed step-by-step results
- Summary statistics
- Error details and stack traces

#### JSON Format
```bash
./muster test --output-format=json --report-file=results.json
```

Structured output suitable for:
- CI/CD pipeline integration
- Automated result processing
- External monitoring systems
- Result archiving and analysis

#### JUnit XML Format
```bash
./muster test --output-format=junit --report-file=results.xml
```

Industry-standard format for:
- Jenkins integration
- GitLab CI/CD pipelines
- GitHub Actions
- Test result visualization tools

### Report Structure

#### Test Results Summary
```json
{
  "summary": {
    "total_scenarios": 25,
    "passed": 23,
    "failed": 1,
    "errors": 1,
    "skipped": 0,
    "execution_time": "2m34s",
    "success_rate": 92.0
  },
  "scenarios": [...]
}
```

#### Detailed Scenario Results
```json
{
  "name": "serviceclass-basic-operations",
  "category": "behavioral",
  "concept": "serviceclass",
  "status": "passed",
  "execution_time": "45s",
  "steps": [
    {
      "name": "create-test-serviceclass",
      "status": "passed",
      "execution_time": "12s",
      "tool": "core_serviceclass_create",
      "response": {...}
    }
  ]
}
```

## Troubleshooting

### Common Issues

#### 1. Connection Refused
**Symptom**: `connection refused` errors when running tests
**Solution**: 
```bash
# Ensure muster aggregator is running
./muster serve

# Check if the service is healthy
systemctl --user status muster.service
```

#### 2. Tool Not Found
**Symptom**: `tool not found` errors during test execution
**Solution**:
```bash
# List available tools to verify they're registered
# (This would use mcp-debug when available)
```

#### 3. Timeout Errors
**Symptom**: Tests failing with timeout errors
**Solution**:
```bash
# Increase timeout
./muster test --timeout=60m

# Check system performance and resource usage
```

#### 4. Permission Denied
**Symptom**: Permission errors when creating/deleting resources
**Solution**:
```bash
# Check service account permissions
kubectl auth can-i '*' '*'

# Verify kubeconfig
kubectl config current-context
```

#### 5. Scenario Parse Errors
**Symptom**: YAML parsing errors when loading scenarios
**Solution**:
```bash
# Validate scenario syntax
./muster test --scenario=problematic-scenario --debug

# Check YAML syntax manually
```

### Debug Mode

Enable comprehensive debugging:

```bash
./muster test --debug --verbose
```

Debug mode provides:
- Detailed MCP protocol traces
- Step-by-step execution logs
- Arg and response dumps
- Timing information for each operation
- Resource usage statistics

### Log Analysis

Test execution logs include:
- Timestamp for each operation
- Tool invocation details
- Response validation results
- Error stack traces
- Performance metrics

Example log entry:
```
2024-01-15T10:30:45Z INFO  [serviceclass-basic] Step 'create-test-serviceclass' started
2024-01-15T10:30:45Z DEBUG [serviceclass-basic] Calling tool: core_serviceclass_create
2024-01-15T10:30:45Z DEBUG [serviceclass-basic] Args: {"yaml": "name: test-serviceclass..."}
2024-01-15T10:30:47Z DEBUG [serviceclass-basic] Response: {"success": true, "message": "created successfully"}
2024-01-15T10:30:47Z INFO  [serviceclass-basic] Step 'create-test-serviceclass' passed (2.1s)
```

## CI/CD Integration

### GitHub Actions Example

```yaml
name: muster Tests
on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      
      - name: Setup Go
        uses: actions/setup-go@v4
        with:
          go-version: '1.21'
          
      - name: Build muster
        run: go build -o muster .
        
      - name: Run Behavioral Tests
        run: |
          ./muster test --category=behavioral \
            --output-format=junit \
            --report-file=behavioral-results.xml \
            --parallel=4
            
      - name: Run Integration Tests  
        run: |
          ./muster test --category=integration \
            --output-format=junit \
            --report-file=integration-results.xml \
            --parallel=2
            
      - name: Upload Test Results
        uses: actions/upload-artifact@v3
        if: always()
        with:
          name: test-results
          path: "*-results.xml"
```

### Jenkins Pipeline Example

```groovy
pipeline {
    agent any
    
    stages {
        stage('Build') {
            steps {
                sh 'go build -o muster .'
            }
        }
        
        stage('Behavioral Tests') {
            steps {
                sh '''
                    ./muster test --category=behavioral \
                      --output-format=junit \
                      --report-file=behavioral-results.xml \
                      --parallel=4
                '''
            }
            post {
                always {
                    junit 'behavioral-results.xml'
                }
            }
        }
        
        stage('Integration Tests') {
            steps {
                sh '''
                    ./muster test --category=integration \
                      --output-format=junit \
                      --report-file=integration-results.xml \
                      --parallel=2
                '''
            }
            post {
                always {
                    junit 'integration-results.xml'
                }
            }
        }
    }
}
```

### Exit Codes

The test framework provides standard exit codes for automation:

- `0`: All tests passed successfully
- `1`: Some tests failed (expected failures)
- `2`: Test framework errors (unexpected failures)
- `3`: Configuration or setup errors

## Advanced Usage

### Custom Scenario Directories

```bash
# Load scenarios from custom directory
./muster test --config-path=/path/to/custom/scenarios

# Load scenarios from multiple directories
./muster test --config-path=/path/one,/path/two
```

### Filtering and Selection

```bash
# Run tests with specific tags
./muster test --tags=smoke,critical

# Exclude tests with specific tags
./muster test --exclude-tags=slow,external

# Run tests matching name pattern
./muster test --name-pattern="serviceclass-*"
```

### Environment-Specific Configuration

```bash
# Development environment
export MUSTER_TEST_PARALLEL=2
export MUSTER_TEST_TIMEOUT=10m
./muster test

# CI environment
export MUSTER_TEST_PARALLEL=8
export MUSTER_TEST_TIMEOUT=30m
export MUSTER_TEST_FAIL_FAST=true
./muster test
```

## Configuration Args

### Base Port Configuration

The framework automatically assigns ports starting from a base port:

```bash
# Default base port (18000)
./muster test

# Custom base port to avoid conflicts
./muster test --base-port=19000

# For parallel execution, each instance gets base-port + offset
./muster test --parallel=4 --base-port=18000
# Creates instances on ports: 18000, 18001, 18002, 18003
```

### Environment Variables

You can customize test execution through environment variables:

```bash
# Test execution settings  
export MUSTER_TEST_TIMEOUT="30m"                         # Default timeout
export MUSTER_TEST_PARALLEL="4"                          # Default parallel workers
export MUSTER_TEST_CONFIG="./scenarios"                  # Default scenario directory
export MUSTER_TEST_BASE_PORT="18000"                     # Default base port

# Then run tests
./muster test
```

Or use command-line flags to override settings per run:

```bash
./muster test --timeout=10m --parallel=2 --base-port=19000 --config-path=/custom/scenarios
```

## How to Create New Test Scenarios

Writing test scenarios helps you verify that muster functionality works correctly and catch regressions. Here's how to create effective test scenarios:

### Step 1: Plan Your Test

Before writing YAML, think through:

1. **What are you testing?** 
   - Which muster concept (ServiceClass, Workflow, MCP Server, etc.)
   - What specific functionality or behavior
   
2. **What's the user workflow?**
   - What steps would a user take?
   - What would they expect to happen?
   
3. **What could go wrong?**
   - Error conditions to test
   - Edge cases to validate

### Step 2: Choose Category and Concept

```yaml
name: "my-new-test-scenario"
category: "behavioral"           # or "integration"
concept: "serviceclass"          # serviceclass, workflow, mcpserver, capability, service
description: "Clear description of what this test verifies"
```

**Category Guidelines:**
- Use `behavioral` for testing user-facing functionality
- Use `integration` for testing component interactions

**Concept Guidelines:**
- Use the primary concept being tested
- If testing multiple concepts, choose the main focus

### Step 3: Define Test Steps

Each step should test one specific operation:

```yaml
steps:
  - name: "descriptive-step-name"
    description: "What this step accomplishes"
    tool: "core_serviceclass_create"     # MCP tool to call
    args:                          #  Args or the tool
      yaml: |
        name: test-resource
        # ... configuration
    expected:                           # What you expect to happen
      success: true
      contains: ["created successfully"]
    timeout: "30s"                     # Optional step timeout
```

### Step 4: Add Comprehensive Validation

Don't just check for success - validate the actual behavior:

```yaml
expected:
  success: true
  contains: ["created successfully", "test-resource"]  # Response must contain these
  not_contains: ["error", "failed"]                    # Response must not contain these
  json_path:                                           # Validate structured response
    name: "test-resource"
    status: "created"
    available: true
```

### Step 5: Include Cleanup

Always clean up resources your test creates:

```yaml
cleanup:
  - name: "cleanup-test-resource"
    description: "Remove test resource"
    tool: "core_serviceclass_delete"
    args:
      name: "test-resource"
    expected:
      success: true
    continue_on_failure: true          # Continue cleanup even if this fails
    
  - name: "verify-cleanup"
    description: "Verify resource was removed"
    tool: "core_serviceclass_get"
    args:
      name: "test-resource"
    expected:
      success: false                   # Should fail because resource is gone
      error_contains: ["not found"]
    continue_on_failure: true
```

### Step 6: Test Your Scenario

Before committing, validate your scenario works:

```bash
# Validate YAML syntax (when validation is implemented)
./muster test --validate-scenario=path/to/your-scenario.yaml

# Run your scenario
./muster test --scenario=my-new-test-scenario --verbose

# Debug any issues
./muster test --scenario=my-new-test-scenario --debug
```

### Example: Complete ServiceClass Test Scenario

```yaml
name: "serviceclass-arg-validation"
category: "behavioral"
concept: "serviceclass"
description: "Verify ServiceClass arg validation works correctly"
tags: ["serviceclass", "validation", "args"]
timeout: "5m"

steps:
  - name: "create-serviceclass-with-valid-args"
    description: "Create ServiceClass with all valid args"
    tool: "core_serviceclass_create"
    args:
      yaml: |
        name: test-validation-serviceclass
        description: "Test ServiceClass for arg validation"
        args:
          app_name:
            type: string
            required: true
            pattern: "^[a-z][a-z0-9-]*$"
          replicas:
            type: integer
            default: 1
            minimum: 1
            maximum: 10
        tools:
          - name: "core_service_create"
    expected:
      success: true
      contains: ["created successfully", "test-validation-serviceclass"]
    timeout: "1m"

  - name: "verify-serviceclass-available"
    description: "Verify ServiceClass is available for use"
    tool: "core_serviceclass_available"
    args:
      name: "test-validation-serviceclass"
    expected:
      success: true
      json_path:
        available: true
        name: "test-validation-serviceclass"

  - name: "test-valid-service-creation"
    description: "Create service with valid args"
    tool: "core_service_create"
    args:
      serviceClassName: "test-validation-serviceclass"
      label: "test-valid-service"
      args:
        app_name: "my-app"
        replicas: 3
    expected:
      success: true
      contains: ["created successfully", "test-valid-service"]

  - name: "test-invalid-app-name"
    description: "Verify invalid app_name is rejected"
    tool: "core_service_create"
    args:
      serviceClassName: "test-validation-serviceclass"
      label: "test-invalid-name"
      args:
        app_name: "My-App"  # Invalid: contains uppercase
        replicas: 2
    expected:
      success: false
      error_contains: ["invalid arg", "app_name", "pattern"]

  - name: "test-invalid-replicas"
    description: "Verify replicas outside valid range are rejected"
    tool: "core_service_create"
    args:
      serviceClassName: "test-validation-serviceclass"
      label: "test-invalid-replicas"
      args:
        app_name: "test-app"
        replicas: 15  # Invalid: exceeds maximum of 10
    expected:
      success: false
      error_contains: ["invalid arg", "replicas", "maximum"]

cleanup:
  - name: "delete-test-service"
    description: "Clean up valid test service"
    tool: "core_service_delete"
    args:
      label: "test-valid-service"
    expected:
      success: true
    continue_on_failure: true

  - name: "delete-test-serviceclass"
    description: "Clean up test ServiceClass"
    tool: "core_serviceclass_delete"
    args:
      name: "test-validation-serviceclass"
    expected:
      success: true
    continue_on_failure: true

  - name: "verify-serviceclass-deleted"
    description: "Verify ServiceClass was completely removed"
    tool: "core_serviceclass_get"
    args:
      name: "test-validation-serviceclass"
    expected:
      success: false
      error_contains: ["not found"]
    continue_on_failure: true
```

### Best Practices for Writing Tests

#### Use Descriptive Names
```yaml
# ✅ Good - describes what the test does
name: "serviceclass-arg-validation-with-constraints"

# ❌ Bad - generic and unclear  
name: "test-serviceclass-1"
```

#### Test Both Success and Failure Cases
```yaml
# Test successful operation
- name: "create-valid-serviceclass"
  # ... test valid creation

# Test error conditions
- name: "reject-duplicate-serviceclass-creation"
  # ... test duplicate name rejection
```

#### Use Unique Resource Names
```yaml
# ✅ Good - unique names prevent conflicts
args:
  yaml: |
    name: "test-scenario-unique-serviceclass"

# ❌ Bad - generic names cause conflicts
args:
  yaml: |
    name: "test-serviceclass"
```

#### Always Include Cleanup
```yaml
# Include cleanup section even for failed tests
cleanup:
  - name: "cleanup-resources"
    # ... cleanup steps
    continue_on_failure: true  # Don't fail the test if cleanup fails
```

#### Test Realistic Scenarios
```yaml
# ✅ Good - tests realistic user workflow
description: "User can create ServiceClass, instantiate Service, and scale it"

# ❌ Bad - tests internal implementation details
description: "Verify ServiceClass internal validation logic"
```

### Where to Put Your Test Scenarios

Organize scenarios by category and concept:

```
internal/testing/scenarios/
├── behavioral/
│   ├── serviceclass/
│   │   ├── basic-crud.yaml
│   │   ├── arg-validation.yaml          # ← Your new test here
│   │   └── tool-integration.yaml
│   ├── workflow/
│   │   ├── execution-flow.yaml
│   │   └── arg-templating.yaml
│   └── ...
└── integration/
    ├── end-to-end/
    └── component/
```

### Running Your New Test

```bash
# Run your specific test
./muster test --scenario=serviceclass-arg-validation

# Run all tests in your concept area
./muster test --concept=serviceclass

# Run with your changes included
./muster test --category=behavioral --concept=serviceclass
```

## Where to Find More Information

- **Scenario Authoring Details**: See [scenarios.md](scenarios.md) for complete YAML reference
- **Example Scenarios**: Check [examples/](examples/) directory for comprehensive examples  
- **Package Documentation**: See `internal/testing/doc.go` for implementation details

---

**Related Issues**: [#71](https://github.com/giantswarm/muster/issues/71), [#69](https://github.com/giantswarm/muster/issues/69) 