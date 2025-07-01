// Package workflow provides workflow management and execution capabilities for muster.
//
// This package manages workflow definitions that can be stored as YAML files and executed
// as multi-step operations. Workflows are automatically registered as MCP tools when loaded,
// enabling programmatic access through the aggregator API with the "action_" prefix.
//
// # Workflow Definition Structure
//
// Workflows are defined in YAML format with the following structure:
//
//	name: "my-workflow"
//	description: "A sample workflow that demonstrates multi-step operations"
//	inputSchema:
//	  type: "object"
//	  properties:
//	    environment:
//	      type: "string"
//	      description: "Target environment"
//	      default: "development"
//	  required: ["environment"]
//	steps:
//	- id: "step1"
//	  tool: "some_tool"
//	  args:
//	    key: "value"
//	    env: "{{.environment}}"
//	- id: "step2"
//	  tool: "another_tool"
//	  args:
//	    input: "{{step1.result}}"
//	    environment: "{{.environment}}"
//
// # Storage and Loading
//
// Workflows are stored as YAML files and can be placed in:
//   - **User configuration directory**: ~/.config/muster/workflows/
//   - **Project configuration directory**: .muster/workflows/
//
// Project workflows take precedence over user workflows with the same name.
// All workflows are automatically loaded on startup and when files are modified.
//
// # Tool Integration
//
// Each workflow is automatically registered as an MCP tool with the name pattern:
// **"action_{workflow_name}"**
//
// This allows workflows to be executed through:
//   - MCP aggregator API
//   - Other MCP clients
//   - AI assistants via MCP protocol
//   - Direct API calls through the workflow handler
//
// The tool registration happens immediately when workflows are loaded.
//
// # Workflow Execution
//
// Workflows are executed step by step in the defined order. Each step:
//   - Calls the specified tool with the provided arguments
//   - Can reference outputs from previous steps using {{stepId.field}} syntax
//   - Can reference input args using {{.argumentName}} syntax
//   - Has access to the workflow's execution context
//   - Supports arg templating for dynamic argument construction
//
// ## Arg Templating
//
// Workflows support Go template syntax for dynamic arg substitution:
//   - **Input arguments**: {{.argumentName}}
//   - **Step outputs**: {{stepId.result}} or {{stepId.specificField}}
//   - **Default values**: Via input schema default properties
//
// # Workflow Manager
//
// The WorkflowManager provides comprehensive workflow lifecycle management:
//
//   - **Definition Loading**: Load workflows from YAML files
//   - **Validation**: Comprehensive validation of workflow definitions
//   - **CRUD Operations**: Create, read, update, and delete workflows
//   - **Execution**: Execute workflows with arg validation
//   - **Tool Integration**: Automatic registration as MCP tools
//   - **Availability Checking**: Dynamic tool availability validation
//
// # Input Schema Support
//
// Workflows can define input schemas for arg validation:
//
//	inputSchema:
//	  type: "object"
//	  properties:
//	    environment:
//	      type: "string"
//	      description: "Deployment environment"
//	      default: "development"
//	    version:
//	      type: "string"
//	      description: "Application version"
//	    replicas:
//	      type: "number"
//	      description: "Number of replicas"
//	      default: 3
//	  required: ["environment", "version"]
//
// This enables:
//   - **Arg validation** before execution
//   - **Default value** assignment for optional arguments
//   - **Type checking** for input arguments
//   - **Documentation** for workflow consumers
//
// # Error Handling
//
// The workflow manager provides comprehensive error handling:
//   - Invalid workflow files are logged but don't prevent other workflows from loading
//   - Missing tools are detected and reported during validation
//   - Execution errors can be configured to stop or continue the workflow
//   - Arg validation errors prevent workflow execution
//   - Tool execution errors are propagated with context
//
// # Dynamic Tool Availability
//
// Workflows use dynamic tool availability checking:
//   - Tools are validated against the aggregator at execution time
//   - Workflows automatically become available when all required tools are present
//   - No caching of tool availability to ensure real-time accuracy
//   - Integration with tool update events for logging
//
// # Usage Examples
//
// ## Manager Setup
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
// ## Creating Workflows
//
//	workflow := api.Workflow{
//	    Name:        "deploy-app",
//	    Description: "Deploy application to environment",
//	    InputSchema: api.WorkflowInputSchema{
//	        Type: "object",
//	        Args: map[string]api.SchemaProperty{
//	            "environment": {
//	                Type:        "string",
//	                Description: "Target environment",
//	                Default:     "development",
//	            },
//	            "version": {
//	                Type:        "string",
//	                Description: "Application version",
//	            },
//	        },
//	        Required: []string{"version"},
//	    },
//	    Steps: []api.WorkflowStep{
//	        {
//	            ID:   "validate",
//	            Tool: "validate_environment",
//	            Args: map[string]interface{}{
//	                "environment": "{{.environment}}",
//	            },
//	        },
//	        {
//	            ID:   "deploy",
//	            Tool: "deploy_application",
//	            Args: map[string]interface{}{
//	                "environment": "{{.environment}}",
//	                "version":     "{{.version}}",
//	            },
//	        },
//	    },
//	}
//
//	if err := manager.CreateWorkflow(workflow); err != nil {
//	    log.Fatal(err)
//	}
//
// ## Executing Workflows
//
//	// Execute through manager
//	result, err := manager.ExecuteWorkflow(ctx, "deploy-app", map[string]interface{}{
//	    "environment": "production",
//	    "version":     "v1.2.3",
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Execute as MCP tool (through aggregator)
//	// Tool name: "action_deploy-app"
//	result, err := toolCaller.CallTool(ctx, "action_deploy-app", map[string]interface{}{
//	    "environment": "production",
//	    "version":     "v1.2.3",
//	})
//
// ## Querying Workflows
//
//	// List all workflows
//	workflows := manager.ListDefinitions()
//	for _, wf := range workflows {
//	    fmt.Printf("Workflow: %s - %s\n", wf.Name, wf.Description)
//	}
//
//	// List available workflows (with all tools present)
//	available := manager.ListAvailableDefinitions()
//	fmt.Printf("Available workflows: %d\n", len(available))
//
//	// Check specific workflow availability
//	if manager.IsAvailable("deploy-app") {
//	    fmt.Println("Deploy workflow is ready to execute")
//	}
//
//	// Get workflows as MCP tools
//	tools := manager.GetWorkflows()
//	for _, tool := range tools {
//	    fmt.Printf("MCP Tool: %s - %s\n", tool.Name, tool.Description)
//	}
//
// # File Management
//
// Workflows can be created, updated, and deleted at runtime:
//   - **Create**: Save workflow YAML to the appropriate directory
//   - **Update**: Modify existing workflow files
//   - **Delete**: Remove workflow files
//
// The manager automatically detects file changes and updates the available tools
// accordingly.
//
// # API Integration
//
// The workflow package integrates with muster's API layer:
//   - **WorkflowHandler**: API interface for workflow management
//   - **Tool provider**: Exposes workflows as MCP tools
//   - **Event integration**: Subscribes to tool update events
//   - **Registration pattern**: Proper API layer registration
//
// # Thread Safety
//
// All workflow operations are thread-safe:
//   - Concurrent workflow execution
//   - Thread-safe definition management
//   - Protected access to workflow registry
//   - Safe tool availability checking
//
// # Validation
//
// Comprehensive validation ensures workflow quality:
//   - **Name validation**: Unique and valid workflow names
//   - **Step validation**: Required fields and step ID uniqueness
//   - **Tool validation**: Tool names cannot be empty
//   - **Schema validation**: Input schema structure and types
//   - **Arg validation**: Required and optional arg checking
//
// # Performance Characteristics
//
//   - **Dynamic checking**: Tool availability checked at execution time
//   - **No caching**: Ensures real-time tool availability
//   - **Efficient loading**: Workflows loaded only when needed
//   - **Template caching**: Template compilation cached for performance
//
// This package enables powerful workflow orchestration capabilities within muster,
// allowing users to define complex multi-step operations that can be executed
// programmatically or through AI assistant interactions.
package workflow
