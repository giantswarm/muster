# Testing muster via MCP

## Overview

This guide provides comprehensive documentation for testing muster using its Model Context Protocol (MCP) server integration. The testing framework exposes MCP tools that enable LLM agents and IDE integrations to execute, manage, and validate muster functionality through standardized protocols.

**Key Benefits:**
- **AI-Powered Testing**: LLM agents can autonomously execute test scenarios and validate functionality
- **IDE Integration**: Direct testing from development environments with MCP-enabled tools  
- **Standardized Interface**: Consistent tool-based approach across different testing contexts
- **Automated Validation**: Comprehensive scenario execution with built-in result verification
- **API Schema Validation**: Generate and validate against live muster serve API schemas
- **Unified Functionality**: CLI and MCP server provide identical testing and validation capabilities
- **Isolated Test Execution**: Each test scenario runs against a fresh, isolated muster instance
- **Complete Log Capture**: Instance logs are automatically captured and included in all MCP responses

## Test Architecture

### Managed Instance Approach

Unlike CLI usage where you might need to manage muster services manually, **the MCP testing tools automatically create and manage isolated muster instances** for each test scenario:

1. **Automatic Instance Creation**: Each test scenario gets a fresh muster serve process on a unique port
2. **Port Management**: Instances are assigned unique ports to avoid conflicts (starting from base port 18000)
3. **Configuration Isolation**: Each instance has its own temporary configuration directory
4. **Mock Server Integration**: Mock MCP servers are automatically configured and started within each instance
5. **Log Collection**: All stdout/stderr from instances is captured and included in responses
6. **Automatic Cleanup**: Instances and configurations are cleaned up after test completion

### Enhanced Debugging 

**Instance logs are always included in MCP responses**, providing comprehensive debugging information:

```json
{
  "summary": {...},
  "scenarios": [
    {
      "name": "serviceclass-basic-operations",
      "status": "passed", 
      "instance_logs": {
        "stdout": "time=2025-06-19T11:07:19.565+02:00 level=INFO msg=\"Loaded configuration...\"",
        "stderr": "",
        "combined": "=== STDOUT ===\ntime=2025-06-19T11:07:19.565+02:00 level=INFO..."
      }
    }
  ]
}
```

### Mock MCP Server Integration

**Testing muster's Core MCP Server Management**: When test scenarios include mock MCP servers in their `pre_configuration`, the framework tests muster serve's MCP server management and tool aggregation capabilities:

1. **Generates Mock Server Configs**: Creates individual configuration files for each mock MCP server
2. **Creates MCPServer Definitions**: Generates MCP server definition files for muster serve
3. **Updates muster Config**: the main muster configuration with the port for the aggregator
4. **Starts muster serve**: So we have a clean instance to test against for each test scenario
5. **Tool Aggregation**: mcpserver tools are  aggregated and exposed through muster's aggregated MCP server

**Why This Architecture Matters**: muster's other concepts (workflows, serviceclasses, capabilities, services) depend on MCP server tools being available through the aggregated MCP server. Mock servers enable testing these concepts and the complete integration without requiring actual external services.

**Mock Server Tool Naming**: Mock MCP server tools become available through the muster MCP interface with a specific naming convention:

**Naming Pattern**: `x_<mcpserver-name>_<tool-name>`

**Example**: If you have a mocked MCP server named "kubernetes-mock" with a tool called "get_pods", it becomes available as:
- **Tool Name**: `x_kubernetes-mock_get_pods`
- **Access**: Available through the same muster serve MCP interface alongside core tools

**Usage in Test Scenarios**:
```yaml
# Pre-configuration defines mock server:
pre_configuration:
  mcp_servers:
    - name: "kubernetes-mock"
      config:
        tools:
          - name: "get_pods"        # Simple name in mock config
            # ... tool definition

# Test steps reference with prefix:
steps:
  - id: test-mock-tool
    description: "Use mocked Kubernetes tool"
    tool: x_kubernetes-mock_get_pods  # Note the x_ prefix
    args:
      namespace: default
    expected:
      success: true
      contains: ["pod-1", "pod-2"]
```

**Key Points**:
- Mock server tools are prefixed with `x_` followed by the server name
- These tools are available during test execution through the same MCP interface
- Mock responses are defined in the scenario configuration
- This enables testing complex integrations without requiring actual external services

### Workflow Tool Integration

**Workflow Naming**: Workflows defined in test scenarios are exposed with the `workflow_` prefix:

```yaml
# Pre-configuration defines workflow:
pre_configuration:
  workflows:
    - name: "deploy-app"
      config:
        # workflow definition

# Test steps reference workflows:
steps:
  - id: "run-deployment"
    tool: "workflow_deploy-app"   # workflow_ prefix (NOT action_)
    args:
      app_name: "test-app"
```

**Important**: The old `action_<workflow-name>` naming is deprecated and no longer works. Always use `workflow_<workflow-name>`.

## MCP Tools Overview

The muster testing framework exposes four primary MCP tools through the aggregator:

### 1. `mcp_muster-test_test_run_scenarios`
**Purpose**: Execute test scenarios with comprehensive configuration options and automatic instance management

**Args**:
- `category` (string, optional): Filter by category ("behavioral", "integration")
- `concept` (string, optional): Filter by concept ("serviceclass", "workflow", "mcpserver", "service")
- `scenario` (string, optional): Run specific scenario by name
- `config_path` (string, optional): Path to scenario files (default: `internal/testing/scenarios`)
- `parallel` (number, optional): Number of parallel workers (default: 1)
- `fail_fast` (boolean, optional): Stop on first failure (default: false)
- `verbose` (boolean, optional): Enable verbose output (default: false)

**Response Format**:
```json
{
  "summary": {
    "total_scenarios": 25,
    "passed": 23,
    "failed": 1,
    "errors": 1,
    "skipped": 0,
    "execution_time": "2m34s",
    "success_rate": 92.0,
    "base_port": 18000
  },
  "scenarios": [
    {
      "name": "serviceclass-basic-operations",
      "status": "passed",
      "execution_time": "45s",
      "instance_logs": {
        "stdout": "time=2025-06-19T11:07:19.565+02:00 level=INFO msg=\"Loaded configuration...\"",
        "stderr": "",
        "combined": "=== STDOUT ===\ntime=2025-06-19T11:07:19.565+02:00 level=INFO..."
      },
      "steps": [
        {
          "id": "create-test-serviceclass",
          "status": "passed",
          "execution_time": "12s",
          "tool": "core_serviceclass_create",
          "response": {...}
        }
      ]
    }
  ]
}
```

### 2. `mcp_muster-test_test_list_scenarios`
**Purpose**: Discover available test scenarios with filtering capabilities

**Args**:
- `category` (string, optional): Filter by category
- `concept` (string, optional): Filter by concept  
- `config_path` (string, optional): Path to scenario files (default: `internal/testing/scenarios`)

**Response Format**:
```json
{
  "scenarios": [
    {
      "name": "serviceclass-basic-operations",
      "category": "behavioral", 
      "concept": "serviceclass",
      "description": "Basic ServiceClass management operations",
      "tags": ["basic", "crud", "serviceclass"],
      "step_count": 6,
      "cleanup_count": 2,
      "estimated_duration": "5m"
    }
  ],
  "total_count": 15,
  "categories": ["behavioral", "integration"],
  "concepts": ["serviceclass", "workflow", "mcpserver"]
}
```

### 3. `mcp_muster-test_test_validate_scenario`
**Purpose**: Validate YAML scenario files for syntax and completeness, with optional API schema validation

**Args**:
- `scenario_path` (string, required): Path to scenario file or directory
- `schema_path` (string, optional): Path to API schema file for API validation
- `category` (string, optional): Filter by category when using schema validation ("behavioral", "integration")  
- `concept` (string, optional): Filter by concept when using schema validation ("serviceclass", "workflow", "mcpserver", "service")

**Response Format (YAML validation)**:
```json
{
  "validation_type": "yaml_structure",
  "valid": true,
  "scenario_count": 3,
  "scenarios": [
    {
      "name": "serviceclass-basic-operations",
      "valid": true,
      "errors": [],
      "warnings": [
        "Step 'create-test-service' has no timeout specified"
      ],
      "step_count": 4,
      "cleanup_count": 1
    }
  ],
  "path": "internal/testing/scenarios"
}
```

**Response Format (API schema validation)**:
```json
{
  "validation_type": "api_schema",
  "schema_path": "schema.json",
  "total_scenarios": 131,
  "valid_scenarios": 36,
  "total_errors": 330,
  "scenario_results": [
    {
      "scenario_name": "serviceclass-basic-operations",
      "valid": false,
      "errors": [
        {
          "type": "unexpected_argument",
          "message": "Step create-test-serviceclass: Argument 'description' not expected for tool 'core_serviceclass_create'",
          "field": "description",
          "suggestion": "Remove argument or check if arg name changed"
        }
      ],
      "step_results": [
        {
          "step_id": "create-test-serviceclass",
          "tool": "core_serviceclass_create",
          "valid": false,
          "errors": [...]
        }
      ]
    }
  ],
  "validation_summary": {
    "valid_steps": 159,
    "invalid_steps": 207,
    "unexpected_argument": 303,
    "unknown_tool": 27
  }
}
```

### 4. `mcp_muster-test_test_get_results`
**Purpose**: Retrieve detailed results from the last test execution

**Args**:
- `random_string` (string, required): Dummy arg (use any value)

**Response Format**:
```json
{
  "last_execution": {
    "start_time": "2024-01-15T10:30:00Z",
    "end_time": "2024-01-15T10:35:30Z", 
    "duration": "5m30s",
    "base_port": 18000,
    "configuration": {
      "parallel": 4,
      "category": "behavioral",
      "verbose": true
    },
    "detailed_results": {
      "scenarios": [...],
      "summary": {...}
    }
  },
  "available": true
}
```

## Usage Patterns

### Basic Test Execution

#### Run All Tests
```json
{
  "tool": "mcp_muster-test_test_run_scenarios",
  "args": {
    "verbose": true
  }
}
```

#### Run Category-Specific Tests
```json
{
  "tool": "mcp_muster-test_test_run_scenarios", 
  "args": {
    "category": "behavioral",
    "verbose": true
  }
}
```

#### Run Concept-Specific Tests
```json
{
  "tool": "mcp_muster-test_test_run_scenarios",
  "args": {
    "concept": "serviceclass",
    "parallel": 2
  }
}
```

#### Run Single Scenario
```json
{
  "tool": "mcp_muster-test_test_run_scenarios",
  "args": {
    "scenario": "serviceclass-basic-operations",
    "verbose": true
  }
}
```

### Advanced Filtering and Configuration

#### Parallel Execution with Fail-Fast
```json
{
  "tool": "mcp_muster-test_test_run_scenarios",
  "args": {
    "category": "integration",
    "parallel": 4,
    "fail_fast": true,
    "verbose": true
  }
}
```

#### Custom Scenario Path
```json
{
  "tool": "mcp_muster-test_test_run_scenarios",
  "args": {
    "config_path": "internal/testing/scenarios",
    "concept": "workflow"
  }
}
```

### Scenario Discovery and Validation

#### List Available Scenarios
```json
{
  "tool": "test_list_scenarios",
  "args": {
    "concept": "serviceclass"
  }
}
```

#### Validate Scenario Files (YAML Structure)
```json
{
  "tool": "test_validate_scenario",
  "args": {
    "scenario_path": "internal/testing/scenarios/"
  }
}
```

#### Validate Scenarios Against API Schema
```json
{
  "tool": "test_validate_api_schema",
  "args": {
    "schema_path": "schema.json",
    "category": "behavioral"
  }
}
```

#### Validate Scenarios Against API Schema
```json
{
  "tool": "mcp_muster-test_test_validate_scenario",
  "args": {
    "scenario_path": "internal/testing/scenarios/",
    "schema_path": "schema.json",
    "concept": "serviceclass"
  }
}
```

#### Get Latest Results
```json
{
  "tool": "mcp_muster-test_test_get_results",
  "args": {
    "random_string": "get_results"
  }
}
```

## Workflow Examples

### Development Workflow

#### Pre-Commit Testing Validation
```json
{
  "tool": "mcp_muster-test_test_run_scenarios",
  "args": {
    "category": "behavioral",
    "parallel": 2,
    "fail_fast": true
  }
}
```

#### API Schema Validation Workflow
```json
{
  "tool": "mcp_muster-test_test_validate_scenario",
  "args": {
    "scenario_path": "internal/testing/scenarios/",
    "schema_path": "schema.json",
    "category": "behavioral"
  }
}
```

#### Local Development Testing Pattern
```json
{
  "tool": "mcp_muster-test_test_run_scenarios",
  "args": {
    "concept": "workflow",
    "verbose": true
  }
}
```

#### Quick Feedback Loop
```json
{
  "tool": "mcp_muster-test_test_run_scenarios",
  "args": {
    "scenario": "serviceclass-basic-operations",
    "verbose": true
  }
}
```

### Scenario Authoring Workflow

#### 1. Create New Test Scenario
```yaml
# Create: internal/testing/scenarios/my-new-test.yaml
name: "my-new-feature-test"
category: "behavioral"
concept: "serviceclass"
description: "Test my new ServiceClass feature"

steps:
  - id: test-new-feature
    tool: core_serviceclass_create
    args:
      yaml: |
        name: test-new-feature
        # ... rest of YAML
    expected:
      success: true
      contains: ["created successfully"]
```

#### 2. Validate Scenario Syntax
```json
{
  "tool": "mcp_muster-test_test_validate_scenario",
  "args": {
    "scenario_path": "internal/testing/scenarios/my-new-test.yaml"
  }
}
```

#### 3. Validate Against API Schema
```json
{
  "tool": "mcp_muster-test_test_validate_scenario",
  "args": {
    "scenario_path": "internal/testing/scenarios/my-new-test.yaml",
    "schema_path": "schema.json"
  }
}
```

#### 4. Test New Scenario
```json
{
  "tool": "mcp_muster-test_test_run_scenarios",
  "args": {
    "scenario": "my-new-feature-test",
    "verbose": true
  }
}
```

#### 5. Iterate Based on Results
```json
{
  "tool": "mcp_muster-test_test_get_results",
  "args": {
    "random_string": "check_results"
  }
}
```

### Debugging Workflow

#### 1. Identify Failing Tests
```json
{
  "tool": "mcp_muster-test_test_run_scenarios",
  "args": {
    "concept": "serviceclass",
    "fail_fast": true,
    "verbose": true
  }
}
```

#### 2. Validate Against API Schema
```json
{
  "tool": "mcp_muster-test_test_validate_scenario",
  "args": {
    "scenario_path": "internal/testing/scenarios/",
    "schema_path": "schema.json",
    "concept": "serviceclass"
  }
}
```

#### 3. Analyze Instance Logs
Check the `instance_logs` in the test results for detailed debugging information:
```json
{
  "scenario": {
    "name": "failing-scenario",
    "status": "failed",
    "instance_logs": {
      "stdout": "time=2025-06-19T11:07:19.565+02:00 level=INFO msg=\"Starting muster...\"",
      "stderr": "time=2025-06-19T11:07:20.123+02:00 level=ERROR msg=\"Failed to initialize service\"",
      "combined": "=== STDOUT ===\n... === STDERR ===\n..."
    }
  }
}
```

#### 4. Get Detailed Test Results
```json
{
  "tool": "mcp_muster-test_test_get_results",
  "args": {
    "random_string": "debug_analysis"
  }
}
```

#### 5. Test Single Failing Scenario
```json
{
  "tool": "mcp_muster-test_test_run_scenarios",
  "args": {
    "scenario": "specific-failing-scenario",
    "verbose": true
  }
}
```

## Best Practices

### Test Execution Strategies

#### 1. **Layered Testing Approach**
```json
// Start with behavioral tests
{
  "tool": "mcp_muster-test_test_run_scenarios",
  "args": {
    "category": "behavioral",
    "fail_fast": true
  }
}

// Then run integration tests
{
  "tool": "mcp_muster-test_test_run_scenarios", 
  "args": {
    "category": "integration",
    "parallel": 2
  }
}
```

#### 2. **Concept-Driven Development**
```json
// Test the concept you're actively developing
{
  "tool": "mcp_muster-test_test_run_scenarios",
  "args": {
    "concept": "workflow",
    "verbose": true
  }
}
```

#### 3. **Fast Feedback with Fail-Fast**
```json
{
  "tool": "mcp_muster-test_test_run_scenarios",
  "args": {
    "fail_fast": true,
    "parallel": 4,
    "verbose": true
  }
}
```

### Scenario Organization Patterns

#### 1. **Naming Conventions**
- Use descriptive names: `serviceclass-crud-operations`
- Include complexity level: `workflow-basic-arg-templating`
- Group by functionality: `mcpserver-connection-management`

#### 2. **Category Usage**
- **`behavioral`**: User-facing functionality, API contracts, expected behaviors
- **`integration`**: Component interactions, end-to-end workflows, system integration

#### 3. **Concept Organization**
- Group related functionality together
- Use tags for cross-cutting concerns
- Maintain clear dependency hierarchies

#### 4. **Test Isolation**
- Ensure each scenario can run independently
- Include proper cleanup steps
- Use unique resource names

### Error Handling and Recovery

#### 1. **Graceful Failure Handling**
```json
{
  "tool": "mcp_muster-test_test_run_scenarios",
  "args": {
    "fail_fast": false,  // Continue execution
    "verbose": true      // Get detailed error info
  }
}
```

#### 2. **Result Analysis Pattern**
```json
// Always check results after execution
{
  "tool": "mcp_muster-test_test_get_results",
  "args": {
    "random_string": "post_execution_check"
  }
}
```

#### 3. **Validation Before Execution**
```json
// Validate scenarios before running
{
  "tool": "mcp_muster-test_test_validate_scenario",
  "args": {
    "scenario_path": "internal/testing/scenarios/"
  }
}
```

## Troubleshooting

### Common Issues and Solutions

#### 1. **Build or Binary Issues**
**Symptoms**: 
```json
{
  "error": "failed to start muster process: executable file not found",
  "tool": "mcp_muster-test_test_run_scenarios"
}
```

**Solutions**:
The testing framework expects to find the `muster` binary in the current working directory. Ensure the binary is built and available:

```bash
# Ensure muster is built
go build -o muster .

# Verify binary exists and is executable
ls -la ./muster
chmod +x ./muster

# Test basic muster functionality
./muster --help
```

**Note**: The testing framework automatically manages muster serve processes - you don't need to start muster manually.

#### 2. **Port Conflicts**
**Symptoms**:
```json
{
  "error": "failed to find available port: no available ports found starting from 18000",
  "tool": "mcp_muster-test_test_run_scenarios"
}
```

**Solutions**:
The testing framework automatically finds available ports starting from 18000. If this error occurs, check what's using the port range:

```bash
# Check what's using the default port range
ss -tlnp | grep 18000

# Kill conflicting processes or wait for them to finish
```

The framework will automatically retry with different ports, but if the entire range is occupied, you may need to free up some ports or reduce parallel execution:

```json
{
  "tool": "mcp_muster-test_test_run_scenarios",
  "args": {
    "parallel": 1,
    "verbose": true
  }
}
```

#### 3. **Instance Startup Failures**
**Symptoms**:
```json
{
  "error": "timeout waiting for muster instance to be ready",
  "tool": "mcp_muster-test_test_run_scenarios"
}
```

**Solutions**:
Check the instance logs in the response for startup errors:
```json
{
  "scenario": {
    "instance_logs": {
      "stderr": "error: failed to load configuration: config file not found"
    }
  }
}
```

Common startup issues:
- Configuration generation errors
- Missing dependencies
- File permission issues
- Resource exhaustion (memory/disk)

#### 4. **Scenario Validation Errors**
**Symptoms**:
```json
{
  "tool": "mcp_muster-test_test_validate_scenario",
  "result": {
    "valid": false,
    "errors": ["step 'invalid-step' references unknown tool 'invalid_tool'"]
  }
}
```

**Solutions**:
```json
{
  "tool": "mcp_muster-test_test_validate_scenario",
  "args": {
    "scenario_path": "internal/testing/scenarios/"
  }
}
```

#### 5. **Resource Exhaustion**
**Symptoms**: Tests fail with out-of-memory or disk space errors

**Solutions**:
```bash
# Check system resources
df -h        # Disk space
free -h      # Memory usage
```

Reduce parallel execution:
```json
{
  "tool": "mcp_muster-test_test_run_scenarios",
  "args": {
    "parallel": 1,
    "concept": "serviceclass"
  }
}
```

## Integration Examples

### IDE Integration with MCP

#### VS Code with MCP Extension
```json
// Configure MCP connection to muster (for accessing testing tools)
{
  "mcp.servers": {
    "muster-test": {
      "command": "muster",
      "args": ["serve", "--mcp"],
      "env": {}
    }
  }
}
```

**Note**: The testing tools accessed through this MCP server will automatically manage their own muster instances for test execution.

#### Cursor with Built-in MCP
```typescript
// Use MCP testing tools directly in Cursor
const testResult = await mcp.callTool("mcp_muster-test_test_run_scenarios", {
  concept: "serviceclass",
  verbose: true
});
```

## Test Scenario Structure

### Basic Structure

```yaml
---
name: scenario-name
description: "Description of what this scenario tests"
category: behavioral  # or integration
concept: serviceclass  # serviceclass, workflow, mcpserver, service

# Pre-configuration for isolated muster instance
pre_configuration:
  mcp_servers:                    # Mock MCP servers (optional)
    - name: "mock-server"
      config:
        tools:
          - name: "mock-tool"     # Simple name in config
  workflows:                      # Workflows (optional)
    - name: "test-workflow"
      config:
        # workflow definition

steps:
  - id: step-identifier
    description: "What this step does"
    tool: "core_tool_name"        # Core tools: direct name
    # tool: "x_mock-server_mock-tool"  # Mock tools: x_<server>_<tool>
    # tool: "workflow_test-workflow"   # Workflows: workflow_<name>
    args:
      param1: value1
      param2: value2
    expected:
      success: true
      contains: ["expected text"]
cleanup:
  - id: cleanup-step
    tool: "core_cleanup_tool"
    args:
      name: resource-to-cleanup
    expected:
      success: true
```

### Step Definition

Each test step follows the same structure as workflow steps for consistency:

- **`id`**: Unique identifier for the step (aligns with workflow step format)
- **`description`**: Human-readable explanation of what the step does
- **`tool`**: The MCP tool to invoke (core, mock, or workflow - see naming conventions below)
- **`args`**: Tool arguments as key-value pairs (aligns with workflow step format)
- **`expected`**: Expected outcome validation

### Tool Naming Conventions in Scenarios

- **Core Tools**: Use direct names like `core_serviceclass_create`
- **Mock Tools**: Use `x_<server-name>_<tool-name>` pattern
- **Workflows**: Use `workflow_<workflow-name>` pattern (NOT `action_<name>`)

### Example Scenarios

#### Basic ServiceClass Operations

```yaml
---
name: serviceclass-basic-operations
description: "Tests basic ServiceClass CRUD operations"
category: behavioral
concept: serviceclass
steps:
  - id: list-initial-serviceclasses
    description: "List ServiceClasses before creating any"
    tool: core_serviceclass_list
    args: {}
    expected:
      success: true

  - id: create-serviceclass
    description: "Create a new ServiceClass"
    tool: core_serviceclass_create
    args:
      name: test-basic-serviceclass
      definition:
        description: "Test ServiceClass for basic operations"
        tools:
          - core_service_create
          - core_service_delete
        lifecycleTools:
          start: "mock_start"
          stop: "mock_stop"
        args: []
    expected:
      success: true

cleanup:
  - id: delete-serviceclass
    description: "Delete the test ServiceClass"
    tool: core_serviceclass_delete
    args:
      name: test-basic-serviceclass
    expected:
      success: true
```

#### Basic Workflow Operations

```yaml
---
name: workflow-basic-operations
description: "Tests basic Workflow CRUD operations"
category: behavioral
concept: workflow

pre_configuration:
  workflows:
    - name: "test-basic-workflow"
      config:
        description: "Test workflow for basic operations"
        steps:
          - id: step1
            tool: core_serviceclass_list
            args: {}

steps:
  - id: verify-workflow-available
    description: "Verify the workflow is available"
    tool: core_workflow_list
    args: {}
    expected:
      success: true
      contains: ["test-basic-workflow"]

  - id: execute-workflow
    description: "Execute the test workflow"
    tool: workflow_test-basic-workflow  # Note: workflow_ prefix, NOT action_
    args: {}
    expected:
      success: true

  - id: validate-workflow
    description: "Validate the workflow definition"
    tool: core_workflow_validate
    args:
      name: test-basic-workflow
    expected:
      success: true
      contains:
        - "Workflow definition is valid"

cleanup:
  - id: delete-workflow
    description: "Delete the test workflow"
    tool: core_workflow_delete
    args:
      name: test-basic-workflow
    expected:
      success: true
```

## Meta-Tools Wrapping

The test framework transparently wraps all tool calls through the `call_tool` meta-tool. Test scenarios continue to reference tools by their simple names (e.g., `core_service_list`), and the test client handles the wrapping internally.

**What this means for test authors:**
- Write scenarios using direct tool names: `tool: core_serviceclass_create`
- The framework automatically wraps this as: `call_tool(name="core_serviceclass_create", arguments={...})`
- Response unwrapping is also handled automatically

This architecture matches how AI agents interact with Muster in production - they also use meta-tools to access all functionality.

---

## Summary

The muster MCP testing framework provides a powerful, standardized way to execute comprehensive tests through AI-powered workflows. Key takeaways:

- **Four Core Tools**: `test_run_scenarios`, `test_list_scenarios`, `test_validate_scenario`, `test_get_results`
- **Flexible Filtering**: By category, concept, or specific scenario
- **Parallel Execution**: Configurable worker pools for faster testing
- **Comprehensive Results**: Detailed execution reports with step-by-step analysis
- **Integration Ready**: Works with IDEs, CI/CD pipelines, and LLM agents

**Best Practices Summary**:
1. Build muster binary before running tests: `go build -o muster .`
2. Use fail-fast for quick feedback during development
3. Analyze instance logs in test responses for debugging
4. Validate scenarios before execution with `test_validate_scenario`
5. Use verbose mode to get detailed execution information
6. Test concept-specific functionality during development
7. Use parallel execution for comprehensive test suites
8. Leverage automatic instance management - no manual muster service management needed

This MCP-based testing approach enables seamless integration between muster development and AI-powered development workflows, providing both automated validation and intelligent debugging capabilities. 