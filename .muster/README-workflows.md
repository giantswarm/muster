# Workflow Configuration

This directory contains example workflow definitions that demonstrate how to create sequences of MCP tool calls for complex operations.

## Quick Start

```bash
# Create the workflows directory
mkdir -p ~/.config/muster/workflows

# Copy the examples you want to use
cp .muster/workflows/workflow-example-auth.yaml ~/.config/muster/workflows/auth.yaml
cp .muster/workflows/workflow-example-portforward.yaml ~/.config/muster/workflows/portforward.yaml
```

## Available Example Workflows

### `workflow-example-auth.yaml`

Demonstrates authentication workflow that logs into Teleport and sets the kubectl context.

### `workflow-example-portforward.yaml`

Shows how to create a port forward with validation steps.

### `workflow-example-discovery.yaml`

Illustrates resource discovery and formatting for cluster exploration.

## Creating Custom Workflows

Workflow files define sequences of MCP tool calls that can be executed together:

```yaml
name: my-workflow
description: "Custom workflow description"
version: 1
args:
  param1:
    type: string
    required: true
    description: "First arg"
  param2:
    type: string
    description: "Second arg"
    default: "default-value"
steps:
  - id: step1
    tool: api_some_tool
    args:
      arg1: "{{ .param1 }}"
      arg2: "{{ .param2 }}"
    store: "step1_result"
  - id: step2
    tool: api_another_tool
    args:
      input: "{{ .step1_result.output }}"
```

## Workflow Schema

- **name**: Unique identifier for the workflow
- **description**: Human-readable description
- **version**: Version number for tracking changes
- **args**: Argument definitions for workflow inputs
- **steps**: Array of workflow steps to execute

### Step Schema

- **id**: Unique identifier for the step
- **tool**: MCP tool name to execute
- **args**: Arguments to pass to the tool (supports templating)
- **store**: Optional variable name to store the result

## Using Workflows

Workflows can be executed through the muster MCP interface:

```bash
# List available workflows
muster agent --mcp-server
# then use: core_workflow_list

# Execute a workflow
# Use: core_workflow_execute with workflow name and arguments
```

## Template Variables

Workflows support Go template syntax for dynamic values:

- `{{ .argName }}` - Access input arguments
- `{{ .stepId.field }}` - Access results from previous steps
- `{{ .stepId.output }}` - Access the main output from a step

## File Organization

- **Project workflows**: `.muster/workflows/` (override user workflows)
- **User workflows**: `~/.config/muster/workflows/` (global defaults)
- **Legacy support**: `agent_workflows.yaml` files are still supported

Project workflows take precedence over user workflows when names match. 