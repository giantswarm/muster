# Test Scenario Authoring Guide

## Overview

This guide provides comprehensive documentation for authoring YAML-based test scenarios for the muster test framework. Test scenarios define the complete lifecycle of a test, including setup, execution, validation, and cleanup.

**Key Architecture Points:**
- Each test scenario runs against its own isolated muster serve instance
- Mock MCP servers are essential for testing muster's core MCP server management and tool aggregation features
- Mock servers enable testing muster concepts (workflows, serviceclasses, capabilities, services) that depend on MCP server tools
- Mock servers are managed as separate processes by muster serve, so we can test the complete functionality of muster serve through the scenarios
- Tools follow specific naming conventions based on their source (core vs. mock)

## YAML Schema Reference

### Complete Scenario Structure

```yaml
# Required fields
name: "scenario-unique-name"           # Unique identifier for the scenario
category: "behavioral"                 # "behavioral" or "integration"  
concept: "serviceclass"                # Core muster concept being tested
description: "Human-readable description of what this scenario tests"

# Optional metadata
tags: ["basic", "crud", "smoke"]       # Tags for filtering and organization
timeout: "5m"                          # Global scenario timeout (default: 30m)

# Pre-configuration for the isolated muster instance
# This generates the necessary configs and definitions before starting muster serve
pre_configuration:
  mcp_servers:                         # Mock MCP servers (uses muster's standard MCP server management)
    - name: "mock-server-name"
      config:
        tools:
          - name: "tool-name"          # Simple name in mock config
            description: "Tool description"
            input_schema:
              type: "object"
              properties:
                param1:
                  type: "string"
            responses:
              - response:
                  status: "success"
                  
  service_classes:                     # ServiceClasses to pre-create
    - name: "test-serviceclass"
      config:
        # ServiceClass definition
        
  workflows:                           # Workflows to pre-create  
    - name: "test-workflow"
      config:
        # Workflow definition

# Test execution steps
steps:
  - id: "step-unique-name"             # Unique step identifier
    description: "What this step does" # Human-readable step description
    tool: "core_serviceclass_create"   # MCP tool name to invoke
    args:                              # Tool args (renamed from 'args')
      yaml: |                          # YAML content (for tools that accept YAML)
        name: test-resource
        description: "Test resource"
    expected:                          # Validation rules
      success: true                    # Expected success/failure
      contains: ["created", "success"] # Response must contain these strings
      json_path:                       # JSON path assertions
        status: "created"
        available: true
    timeout: "1m"                      # Step-specific timeout

# Required cleanup steps (always run, even on failure)
cleanup:
  - id: "cleanup-resources"            # Changed from 'name' to 'id'
    description: "Remove test resources"
    tool: "core_serviceclass_delete"
    args:                              # Changed from 'args' to 'args'
      name: "test-resource"
    expected:
      success: true
    timeout: "30s"
```

### Key Schema Changes

#### Updated Field Names
- Step identifiers use `id` instead of `name` (aligns with workflow step format)
- Tool args use `args` instead of `args` (aligns with workflow step format)
- Cleanup steps also use `id` and `args` for consistency

#### Tool Naming Conventions

**Core muster Tools**: Use standard names
```yaml
steps:
  - id: "create-serviceclass"
    tool: "core_serviceclass_create"   # Standard core tool
```

**Mock MCP Server Tools**: Use `x_<server-name>_<tool-name>` pattern
```yaml
# Pre-configuration defines mock server:
pre_configuration:
  mcp_servers:
    - name: "kubernetes-mock"
      config:
        tools:
          - name: "get_pods"           # Simple name in config

# Steps reference with prefix:
steps:
  - id: "test-k8s-pods"
    tool: "x_kubernetes-mock_get_pods" # Prefixed name in usage
```

**Workflow Tools**: Use `workflow_<workflow-name>` pattern  
```yaml
# Pre-configuration defines workflow:
pre_configuration:
  workflows:
    - name: "backup-data"
      config:
        # workflow definition

# Steps reference workflows:
steps:
  - id: "run-backup"
    tool: "workflow_backup-data"     # workflow_ prefix (NOT action_)
```

### Schema Validation Rules

#### Required Fields

- **name**: Must be unique across all scenarios, use kebab-case
- **category**: Must be either "behavioral" or "integration"
- **concept**: Must be one of the supported concepts (serviceclass, workflow, mcpserver, service)
- **description**: Human-readable description of the test purpose
- **steps**: At least one test step must be defined

#### Optional Fields

- **tags**: Array of strings for categorization and filtering
- **timeout**: Global timeout in Go duration format (e.g., "5m", "30s", "1h")
- **pre_configuration**: Setup for the isolated muster instance
- **cleanup**: Teardown steps run after test completion

#### Step Schema

Each step must define:
- **id**: Unique identifier within the scenario
- **tool**: Valid MCP tool name (core, mock, or workflow)
- **expected**: At least one validation rule (success, contains, json_path, etc.)

## Authoring Best Practices

### 1. Naming Conventions

#### Scenario Names
Use descriptive, kebab-case names:

```yaml
# ✅ Good examples
name: "serviceclass-basic-crud-operations"
name: "workflow-arg-templating-validation"
name: "mcpserver-connection-recovery-handling"

# ❌ Bad examples
name: "test1"
name: "ServiceClass_Test"
```

#### Step Names
Use action-oriented names with `id` field:

```yaml
# ✅ Good examples
  - id: "create-test-serviceclass"
  - id: "verify-serviceclass-availability"  
  - id: "instantiate-service-from-class"

# ❌ Bad examples
  - id: "step1"
  - id: "test-stuff"
```

### 2. Tool Reference Patterns

#### Core Tools
```yaml
steps:
  - id: "list-serviceclasses"
    tool: "core_serviceclass_list"    # Direct core tool usage
```

#### Mock Server Tools  
```yaml
# Define in pre_configuration:
pre_configuration:
  mcp_servers:
    - name: "storage-mock"
      config:
        tools:
          - name: "create_volume"      # Simple name in mock config

# Reference in steps:
steps:
  - id: "test-storage"
    tool: "x_storage-mock_create_volume"  # x_<server>_<tool> pattern
```

#### Workflow Tools
```yaml
# Define in pre_configuration:
pre_configuration:
  workflows:
    - name: "backup-data"
      config:
        # workflow definition

# Reference in steps:
steps:
  - id: "run-backup"
    tool: "workflow_backup-data"     # workflow_<name> pattern (NOT action_)
```

### 3. Arg Patterns

#### YAML Args
For tools that accept YAML configurations:

```yaml
args:
  yaml: |
    name: test-serviceclass
    description: "Test ServiceClass for scenario"
    args:
      replicas:
        type: integer
        default: 1
      image:
        type: string
        required: true
    tools:
      - name: "core_service_create"
```

#### Key-Value Args
For simple arg passing:

```yaml
args:
  name: "test-workflow"
  timeout: "5m"
  parallel: true
```

### 4. Validation Patterns

#### Success Validation
Basic validation - ensure the operation succeeded:

```yaml
expected:
  success: true
```

#### Content Validation
Verify response contains expected content:

```yaml
expected:
  success: true
  contains: ["created successfully", "test-serviceclass"]
```

#### JSON Path Validation
For structured responses:

```yaml
expected:
  success: true
  json_path:
    status: "running"
    available: true
    metadata.name: "test-serviceclass"
```

#### Error Validation
For testing error conditions:

```yaml
expected:
  success: false
  error_contains: ["not found", "resource does not exist"]
```

### 5. Mock Server Configuration

#### Complete Mock Server Example
```yaml
pre_configuration:
  mcp_servers:
    - name: "database-mock"
      config:
        tools:
          - name: "create_table"
            description: "Create database table"
            input_schema:
              type: "object"
              properties:
                table_name:
                  type: "string"
                  required: true
                columns:
                  type: "array"
                  items:
                    type: "object"
            responses:
              - condition:
                  table_name: "users"
                response:
                  status: "created"
                  table_id: "tbl_users_123"
                  rows: 0
                delay: "2s"
              - error: "table '{{ .table_name }}' already exists"

# Usage in steps:
steps:
  - id: "create-users-table"
    tool: "x_database-mock_create_table"  # Note the x_ prefix
    args:
      table_name: "users"
      columns:
        - name: "id"
          type: "integer"
        - name: "email"
          type: "string"
    expected:
      success: true
      contains: ["created", "tbl_users_123"]
```

### 6. Resource Management

#### Unique Resource Names
Always use unique names to avoid conflicts:

```yaml
args:
  yaml: |
    name: "test-serviceclass-{{ scenario.name }}"  # Use scenario name for uniqueness
```

#### Comprehensive Cleanup
Always clean up resources:

```yaml
cleanup:
  - id: "delete-test-serviceclass"
    tool: "core_serviceclass_delete"  
    args:
      name: "test-serviceclass"
    expected:
      success: true
    continue_on_failure: true
```

## Common Anti-Patterns

### ❌ What to Avoid

#### 1. Incorrect Tool Naming
```yaml
# ❌ Bad: Old workflow naming
steps:
  - id: "run-workflow"
    tool: "action_my-workflow"  # Old naming, doesn't work

# ✅ Good: Current workflow naming  
steps:
  - id: "run-workflow"
    tool: "workflow_my-workflow"  # Correct workflow_ prefix
```

#### 2. Missing Mock Tool Prefix
```yaml
# ❌ Bad: Direct mock tool name
steps:
  - id: "test-mock"
    tool: "create_resource"  # Missing x_ prefix

# ✅ Good: Proper mock tool reference
steps:
  - id: "test-mock"  
    tool: "x_resource-mock_create_resource"  # Correct x_<server>_<tool> pattern
```

#### 3. Inconsistent Field Names
```yaml
# ❌ Bad: Mixing old and new field names
steps:
  - name: "test-step"         # Should be 'id'
    tool: "core_test"
    args:               # Should be 'args'
      test: true
      
# ✅ Good: Consistent field naming
steps:
  - id: "test-step"
    tool: "core_test"  
    args:
      test: true
```

#### 4. Missing Cleanup
```yaml
# ❌ Bad: No cleanup section
steps:
  - id: "create-resource"
    # ... create something but never clean it up
```

```yaml
# ✅ Good: Always include cleanup
cleanup:
  - id: "delete-resource"
    # ... proper cleanup
```

## Validation and Testing

### Schema Validation

Use the built-in validation to check scenario syntax:

```bash
# Validate a single scenario
./muster test --validate-scenario=path/to/scenario.yaml

# Validate all scenarios in a directory  
./muster test --validate-scenarios=path/to/scenarios/
```

### Testing Your Scenarios

Test scenarios automatically run against isolated muster instances:

```bash
# Test a specific scenario (creates fresh muster instance automatically)
./muster test --scenario=my-scenario --verbose

# Test with debugging to see instance logs
./muster test --scenario=my-scenario --debug

# Test all scenarios in a concept category
./muster test --concept=serviceclass --verbose
```

**Benefits of Managed Instances:**
- Each scenario runs against a fresh muster instance
- Mock MCP servers are automatically configured and integrated
- No interference between test scenarios
- Automatic cleanup of instances and configurations
- Complete isolation ensures reliable test results

---

For complete examples implementing these patterns, see the [examples/](examples/) directory.  
For framework documentation, see [README.md](README.md).  
For package details, see `internal/testing/doc.go`.
