# API Schema Generation and Validation

This document explains how to use `muster test` to generate API schemas and validate test scenarios against the current muster serve API.

## Overview

The schema generation and validation functionality helps ensure test scenarios stay in sync with the actual muster serve API. When the API changes, you can regenerate the schema and validate scenarios to catch compatibility issues.

**🎯 Unified Functionality**: Both CLI (`muster test --validate-scenarios`) and MCP server (`mcp_muster-test_test_validate_scenario` with `schema_path`) provide identical validation functionality with the same detailed results and error reporting.

## Generate API Schema

Generate a JSON schema from a running muster serve instance:

```bash
# Generate schema with default settings
muster test --generate-schema

# Generate with verbose output and custom file name
muster test --generate-schema --verbose --schema-output=api-schema.json

# Use different port range to avoid conflicts
muster test --generate-schema --base-port=19000
```

The generated schema contains:
- All 43+ `core_*` API tools from muster serve
- Arg schemas inferred from tool names and error analysis
- Proper JSON Schema format for validation tools

### Example Schema Structure

```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "muster Core API Schema",
  "description": "Generated schema for muster core API tools",
  "properties": {
    "tools": {
      "properties": {
        "core_serviceclass_create": {
          "type": "object",
          "properties": {
            "name": { "type": "string", "description": "Name of the resource to create" },
            "type": { "type": "string", "description": "ServiceClass type" },
            "version": { "type": "string", "description": "ServiceClass version" },
            "serviceConfig": { "type": "object", "description": "ServiceClass configuration" }
          }
        }
      }
    }
  },
  "generated_at": "2025-06-22T15:55:42+02:00",
  "version": "1.0.0"
}
```

## Validate Test Scenarios

Validate existing test scenarios against the generated schema using CLI or MCP server:

### CLI Validation
```bash
# Validate scenarios with default schema
muster test --validate-scenarios

# Use custom schema file and show verbose output
muster test --validate-scenarios --schema-input=api-schema.json --verbose

# Validate scenarios from custom directory
muster test --validate-scenarios --config=path/to/scenarios
```

### MCP Server Validation
```bash
# Start MCP server
muster test --mcp-server

# Then call the validation tool:
# mcp_muster-test_test_validate_scenario with args:
# - scenario_path: "/path/to/scenarios" (required)
# - schema_path: "schema.json" (optional, enables API validation)
# - category: "behavioral" (optional)
# - concept: "serviceclass" (optional)
```

**Note**: Both methods provide identical validation results and error reporting.

### Validation Output

The validator provides detailed reports:

```
📊 Validation Results
═══════════════════
Total scenarios: 207
Valid scenarios: 15
Invalid scenarios: 192
Total errors: 354

Error Summary:
  unknown_tool: 29
  unexpected_argument: 325

Detailed Results:

❌ serviceclass-create
   unexpected_argument: Step create-test-serviceclass: Argument 'description' not expected for tool 'core_serviceclass_create'

❌ capability-check-available
   unknown_tool: Step check-is-available: Tool 'core_capability_available' not found in API schema
      💡 Check available tools in the schema
```

## Error Types

| Error Type | Description | Action Required |
|------------|-------------|-----------------|
| `unknown_tool` | Tool name not found in API schema or invalid prefix | Check if tool name changed, was removed, or has invalid prefix |
| `unexpected_argument` | Argument not defined in tool schema | Remove argument or check if arg name changed |
| `missing_required_argument` | Required arg not provided | Add the missing required arg |

### Tool Validation Rules

The validation system handles different tool prefixes according to their purpose:

1. **`core_*` tools** - Core muster API tools
   - ✅ **Validated against API schema**:  Args nd tool existence are checked
   - ❌ **Fails if**: Tool doesn't exist in current API or has invalid args
   - 📝 **Example**: `core_serviceclass_create`, `core_service_start`

2. **`x_*` tools** - Mock MCP server tools  
   - ✅ **Always valid**: Part of test scenario setup (mock servers)
   - ⚠️ **Not validated**:  Args an't be verified (scenario-specific)
   - 📝 **Example**: `x_kubernetes-mock_k8s_pod_list`, `x_storage-mock_create_volume`

3. **`workflow_*` tools** - Workflow execution tools
   - ✅ **Always valid**: Workflow execution calls
   - ⚠️ **Not validated**:  Args epend on workflow definition  
   - 📝 **Example**: `workflow_deploy-app`, `workflow_setup-environment`

4. **`api_*` tools** - API tools
   - ✅ **Always valid**: API operation tools
   - ⚠️ **Not validated**:  Args re API-specific
   - 📝 **Example**: `api_create_resource`, `api_update_config`

5. **All other prefixes** - Invalid tools
   - ❌ **Always fails**: Unknown tool type
   - 📝 **Fix**: Use proper prefix (`core_`, `x_`, `workflow_`, or `api_`)

### Validation Examples

```yaml
steps:
  # ✅ VALID: Core tool - will be validated against API schema
  - id: "create-serviceclass"
    tool: "core_serviceclass_create"
    args:
      name: "my-service"
      type: "web"
    
  # ✅ VALID: Mock tool - accepted but not arg-validated  
  - id: "setup-mock"
    tool: "x_kubernetes-mock_create_pod"
    args:
      namespace: "test"
      
  # ✅ VALID: Workflow execution - accepted but not arg-validated
  - id: "run-workflow"
    tool: "workflow_deploy-application"
    args:
      environment: "staging"
      
  # ❌ INVALID: Unknown prefix
  - id: "bad-tool"
    tool: "custom_my_tool"  # Will fail validation
    args: {}
```

## Integration Workflow

### 1. API Development Workflow

```bash
# 1. Develop new API features
# 2. Generate updated schema
muster test --generate-schema --schema-output=schema-v2.json

# 3. Validate existing scenarios
muster test --validate-scenarios --schema-input=schema-v2.json

# 4. Fix any validation errors in scenarios
# 5. Commit both schema and updated scenarios
```

### 2. CI/CD Integration

```bash
# In CI pipeline after API changes
muster test --generate-schema --schema-output=current-schema.json
muster test --validate-scenarios --schema-input=current-schema.json

# Fail build if validation errors exist
```

### 3. Documentation Updates

When the API changes:
1. Generate new schema: `muster test --generate-schema`
2. Validate scenarios: `muster test --validate-scenarios`
3. Update scenario files to fix validation errors
4. Update documentation to reflect API changes
5. Commit updated schema for future validation

## Schema Evolution

### Regenerate Schema After API Changes

```bash
# After adding new core tools or changing args
muster test --generate-schema --verbose

# Compare with previous schema
diff schema.json schema-previous.json

# Validate all scenarios against new schema
muster test --validate-scenarios --verbose
```

### Common Validation Fixes

1. **Unknown tools**: Check if tool was renamed or moved
2. **Missing arguments**: Add required args from schema
3. **Extra arguments**: Remove deprecated or renamed args
4. **Mock tools**: Ensure mock configurations match expected tools

## Advanced Usage

### Custom Schema Analysis

The generated schema can be used with external JSON schema validators:

```bash
# Use with jsonschema CLI tool
pip install jsonschema
jsonschema -i scenario.json schema.json
```

### Schema-Driven Test Generation

Use the schema to generate new test scenarios:

```python
import json

# Load schema
with open('schema.json') as f:
    schema = json.load(f)

# Generate test cases for each tool
for tool_name, tool_schema in schema['properties']['tools']['properties'].items():
    print(f"Tool: {tool_name}")
    print(f"Args: {list(tool_schema.get('properties', {}).keys())}")
```

## Troubleshooting

### Schema Generation Issues

1. **Port conflicts**: Use `--base-port` with different range
2. **Instance startup timeout**: Increase `--timeout` duration
3. **Connection refused**: Check if other muster instances are running

### Validation Issues

1. **Schema file not found**: Check `--schema-input` path
2. **Invalid JSON**: Regenerate schema file
3. **Too many errors**: Use `--verbose` to see detailed error information

## Examples

### Full Workflow Example

```bash
# 1. Generate current API schema
muster test --generate-schema --verbose

# 2. Validate all scenarios (CLI method)
muster test --validate-scenarios --verbose

# 3. Fix identified issues in scenario files
# 4. Re-validate to confirm fixes
muster test --validate-scenarios

# 5. Generate tests for specific concept
muster test --concept=serviceclass --verbose

# 6. Update schema after API changes
muster test --generate-schema --schema-output=schema-v$(date +%Y%m%d).json
```

### Alternative MCP Workflow
```bash
# Start MCP server for AI-powered validation
muster test --mcp-server

# Use MCP tools for validation:
# - mcp_muster-test_test_validate_scenario: Validate YAML structure or against API schema 
# - mcp_muster-test_test_run_scenarios: Execute test scenarios
# - mcp_muster-test_test_list_scenarios: Discover available scenarios
```

This workflow ensures your test scenarios stay synchronized with the actual muster serve API, catching breaking changes early and maintaining test reliability. Both CLI and MCP server provide identical functionality for maximum flexibility. 