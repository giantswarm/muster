// Package workflow provides workflow management and execution capabilities for muster.
//
// This package manages workflow definitions that can be stored as YAML files and executed
// as multi-step operations. Workflows are automatically registered as MCP tools when loaded,
// enabling programmatic access through the aggregator API.
//
// # Workflow Definition Structure
//
// Workflows are defined in YAML format with the following structure:
//
//	name: "my-workflow"
//	description: "A sample workflow that demonstrates multi-step operations"
//	steps:
//	- id: "step1"
//	  tool: "some_tool"
//	  args:
//	    key: "value"
//	  continueOnError: false
//	- id: "step2"
//	  tool: "another_tool"
//	  args:
//	    input: "{{step1.result}}"
//
// # Storage and Loading
//
// Workflows are stored as YAML files and can be placed in:
//   - User configuration directory: ~/.config/muster/workflows/
//   - Project configuration directory: .muster/workflows/
//
// Project workflows take precedence over user workflows with the same name.
// All workflows are automatically loaded on startup and when files are modified.
//
// # Tool Integration
//
// Each workflow is automatically registered as an MCP tool with the name pattern:
// "action_workflow_{workflow_name}"
//
// This allows workflows to be executed through the MCP aggregator API or other
// MCP clients. The tool registration happens immediately when workflows are loaded.
//
// # Workflow Execution
//
// Workflows are executed step by step in the defined order. Each step:
//   - Calls the specified tool with the provided arguments
//   - Can reference outputs from previous steps using {{stepId.field}} syntax
//   - Can optionally continue on error if continueOnError is true
//   - Has access to the workflow's execution context
//
// # Error Handling
//
// The workflow manager provides comprehensive error handling:
//   - Invalid workflow files are logged but don't prevent other workflows from loading
//   - Missing tools are detected and reported during validation
//   - Execution errors can be configured to stop or continue the workflow
//
// # Usage Example
//
//	// Create a workflow manager
//	storage := config.NewStorage()
//	manager, err := workflow.NewWorkflowManager(storage, toolCaller, toolChecker)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Load workflow definitions
//	if err := manager.LoadDefinitions(); err != nil {
//	    log.Printf("Failed to load workflows: %v", err)
//	}
//
//	// Workflows are now available as MCP tools
//	// They can be executed through the MCP aggregator API
//
// # File Management
//
// Workflows can be created, updated, and deleted at runtime:
//   - Create: Save workflow YAML to the appropriate directory
//   - Update: Modify existing workflow files
//   - Delete: Remove workflow files
//
// The manager automatically detects file changes and updates the available tools
// accordingly.
package workflow
