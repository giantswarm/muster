# Muster Custom Resource Definitions (CRDs)

Complete reference for muster's Kubernetes Custom Resource Definitions. These CRDs define the core resources managed by the muster system for orchestrating MCP servers, service lifecycle management, and workflow execution.

## Overview

Muster provides three primary CRDs:

| CRD | API Version | Kind | Short Name | Purpose |
|-----|-------------|------|------------|---------|
| **MCPServer** | `muster.giantswarm.io/v1alpha1` | `MCPServer` | `mcps` | Manages MCP (Model Context Protocol) servers that provide tools |
| **ServiceClass** | `muster.giantswarm.io/v1alpha1` | `ServiceClass` | `sc` | Defines reusable service templates with lifecycle management |
| **Workflow** | `muster.giantswarm.io/v1alpha1` | `Workflow` | `wf` | Defines multi-step processes for automated task execution |

## MCPServer

MCPServer resources define and manage MCP (Model Context Protocol) servers that provide tools for various operations like Git, filesystem, database management, etc.

### Resource Definition

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: <server-name>
  namespace: <namespace>
spec:
  # Required: Server execution type
  type: local|remote
  
  # Optional: Tool name prefix (applies to both local and remote)
  toolPrefix: "<prefix>"
  
  # Optional: Human-readable description
  description: "<description>"
  
  # Configuration for local MCP servers (when type: local)
  local:
    # Optional: Auto-start behavior
    autoStart: false
    
    # Required for local: Command to execute
    command: ["<executable>", "<arg1>", "<arg2>"]
    
    # Optional: Environment variables
    env:
      KEY1: "value1"
      KEY2: "value2"
  
  # Configuration for remote MCP servers (when type: remote)
  remote:
    # Required: Remote server endpoint
    endpoint: "https://api.example.com/mcp"
    
    # Required: Transport protocol
    transport: "http|sse"
    
    # Optional: Connection timeout in seconds
    timeout: 30

# Status is managed automatically by muster
status:
  state: running          # unknown|starting|running|stopping|stopped|failed
  health: healthy         # unknown|healthy|unhealthy|checking
  availableTools: []      # List of tool names provided by this server
  lastError: ""           # Any error from recent operations
  conditions: []          # Kubernetes standard conditions
```

### Field Reference

#### Spec Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `type` | `string` | Yes | Execution method for the MCP server | Must be `local` or `remote` |
| `toolPrefix` | `string` | No | Prefix for all tool names from this server | Pattern: `^[a-zA-Z][a-zA-Z0-9_-]*$` |
| `description` | `string` | No | Human-readable description | Max 500 characters |
| `local` | `LocalConfig` | Yes* | Configuration for local MCP servers | Required when `type` is `local` |
| `remote` | `RemoteConfig` | Yes* | Configuration for remote MCP servers | Required when `type` is `remote` |

#### LocalConfig Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `autoStart` | `boolean` | No | Auto-start when system initializes | Default: `false` |
| `command` | `[]string` | Yes | Command line to execute the server | Min 1 item |
| `env` | `map[string]string` | No | Environment variables for the process | - |

#### RemoteConfig Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `endpoint` | `string` | Yes | Remote server endpoint URL | Must be valid HTTP/HTTPS URL |
| `transport` | `string` | Yes | Transport protocol | `http`, `sse`, `websocket` |
| `timeout` | `integer` | No | Connection timeout in seconds | Min: 1, Max: 300, Default: 30 |

#### Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `state` | `string` | Current operational state: `unknown`, `starting`, `running`, `stopping`, `stopped`, `failed` |
| `health` | `string` | Health status: `unknown`, `healthy`, `unhealthy`, `checking` |
| `availableTools` | `[]string` | List of tool names provided by this MCP server |
| `lastError` | `string` | Error message from the most recent operation |
| `conditions` | `[]metav1.Condition` | Standard Kubernetes conditions |

### Examples

#### Basic Git Tools Server (Local)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: git-tools
  namespace: default
spec:
  type: local
  description: "Git tools MCP server for repository operations"
  local:
    autoStart: true
    command: ["npx", "@modelcontextprotocol/server-git"]
    env:
      GIT_ROOT: "/workspace"
      LOG_LEVEL: "info"
```

#### Python Tools with Prefix (Local)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: python-tools
  namespace: default
spec:
  type: local
  toolPrefix: "py"
  description: "Python-based MCP server with custom tools"
  local:
    autoStart: true
    command: ["python", "-m", "mcp_server.custom"]
    env:
      PYTHONPATH: "/usr/local/lib/python3.9/site-packages"
      DEBUG: "true"
```

#### Remote HTTP Server (Remote)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: api-tools
  namespace: default
spec:
  type: remote
  description: "Remote API tools server"
  remote:
    endpoint: "https://api.example.com/mcp"
    transport: "http"
    timeout: 30
```

#### SSE Remote Server (Remote)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: sse-tools
  namespace: default
spec:
  type: remote
  toolPrefix: "remote"
  description: "Server-Sent Events MCP server"
  remote:
    endpoint: "https://mcp.example.com/sse"
    transport: "sse"
    timeout: 60
```

### CLI Usage

```bash
# List all MCP servers
kubectl get mcpservers
# or using short name
kubectl get mcps

# Get detailed information
kubectl describe mcpserver git-tools

# Check logs
kubectl logs mcpserver/git-tools

# Create from file
kubectl apply -f mcpserver.yaml
```

---

## ServiceClass

ServiceClass resources define reusable templates for service instances with complete lifecycle management including start, stop, health checking, and dependency management.

### Resource Definition

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: <serviceclass-name>
  namespace: <namespace>
spec:
  # Optional: Human-readable description
  description: "<description>"
  
  # Optional: Argument schema for service instantiation
  args:
    <arg_name>:
      type: string|integer|boolean|number|object|array
      required: true|false
      default: <default_value>
      description: "<description>"
  
  # Required: Service configuration template
  serviceConfig:
    # Optional: Default name template for instances
    defaultName: "<name_template>"
    
    # Optional: ServiceClass dependencies
    dependencies: ["<serviceclass1>", "<serviceclass2>"]
    
    # Required: Lifecycle tool definitions
    lifecycleTools:
      # Required: Start tool
      start:
        tool: "<tool_name>"
        args:
          <key>: <value_template>
        outputs:
          <var_name>: "<json_path>"
      
      # Required: Stop tool  
      stop:
        tool: "<tool_name>"
        args:
          <key>: <value_template>
        outputs:
          <var_name>: "<json_path>"
      
      # Optional: Restart tool
      restart:
        tool: "<tool_name>"
        args:
          <key>: <value_template>
        outputs:
          <var_name>: "<json_path>"
      
      # Optional: Health check tool
      healthCheck:
        tool: "<tool_name>"
        args:
          <key>: <value_template>
        expect:
          success: true|false
          jsonPath:
            <path>: <expected_value>
        expectNot:
          success: true|false
          jsonPath:
            <path>: <unexpected_value>
      
      # Optional: Status tool
      status:
        tool: "<tool_name>"
        args:
          <key>: <value_template>
        outputs:
          <var_name>: "<json_path>"
    
    # Optional: Health check configuration
    healthCheck:
      enabled: true|false
      interval: "<duration>"
      failureThreshold: <number>
      successThreshold: <number>
    
    # Optional: Operation timeouts
    timeout:
      create: "<duration>"
      delete: "<duration>"
      healthCheck: "<duration>"
    
    # Optional: Output templates
    outputs:
      <output_name>: "<template>"

# Status is managed automatically by muster
status:
  available: true|false              # All required tools available
  requiredTools: []                  # List of required tool names
  missingTools: []                   # List of unavailable tools
  toolAvailability:                  # Detailed tool availability
    startToolAvailable: true|false
    stopToolAvailable: true|false
    restartToolAvailable: true|false
    healthCheckToolAvailable: true|false
    statusToolAvailable: true|false
  conditions: []                     # Kubernetes standard conditions
```

### Field Reference

#### Spec Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `description` | `string` | No | Human-readable description | Max 1000 characters |
| `args` | `map[string]ArgDefinition` | No | Argument schema for service instances | - |
| `serviceConfig` | `ServiceConfig` | Yes | Core service configuration template | - |

#### ArgDefinition Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `type` | `string` | Yes | Data type of the argument | `string`, `integer`, `boolean`, `number`, `object`, `array` |
| `required` | `boolean` | No | Whether argument is mandatory | Default: `false` |
| `default` | `any` | No | Default value if not provided | - |
| `description` | `string` | No | Usage explanation | Max 500 characters |

#### ServiceConfig Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `defaultName` | `string` | No | Template for generating instance names | Supports templating |
| `dependencies` | `[]string` | No | Required ServiceClasses | - |
| `lifecycleTools` | `LifecycleTools` | Yes | Tool definitions for lifecycle management | - |
| `healthCheck` | `HealthCheckConfig` | No | Health monitoring configuration | - |
| `timeout` | `TimeoutConfig` | No | Operation timeout settings | - |
| `outputs` | `map[string]string` | No | Output templates for instances | Supports templating |

#### LifecycleTools Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `start` | `ToolCall` | Yes | Tool for starting service instances |
| `stop` | `ToolCall` | Yes | Tool for stopping service instances |
| `restart` | `ToolCall` | No | Tool for restarting service instances |
| `healthCheck` | `HealthCheckToolCall` | No | Tool for health checking |
| `status` | `ToolCall` | No | Tool for querying service status |

#### ToolCall Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `tool` | `string` | Yes | Name of the tool to execute |
| `args` | `map[string]any` | No | Arguments for tool execution (supports templating) |
| `outputs` | `map[string]string` | No | Maps tool result paths to variable names |

#### HealthCheckToolCall Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `tool` | `string` | Yes | Name of the health check tool |
| `args` | `map[string]any` | No | Arguments for tool execution |
| `expect` | `HealthCheckExpectation` | No | Positive health check expectations |
| `expectNot` | `HealthCheckExpectation` | No | Negative health check expectations |

#### HealthCheckExpectation Fields

| Field | Type | Description |
|-------|------|-------------|
| `success` | `boolean` | Whether the tool call should succeed |
| `jsonPath` | `map[string]any` | JSON path conditions to check in results |

#### HealthCheckConfig Fields

| Field | Type | Default | Description | Constraints |
|-------|------|---------|-------------|-------------|
| `enabled` | `boolean` | `false` | Whether health checking is active | - |
| `interval` | `string` | `"30s"` | How often to perform health checks | Duration format: `^[0-9]+(ns\|us\|ms\|s\|m\|h)$` |
| `failureThreshold` | `integer` | `3` | Failures before marking unhealthy | Min: 1 |
| `successThreshold` | `integer` | `1` | Successes to mark healthy | Min: 1 |

#### TimeoutConfig Fields

| Field | Type | Description | Constraints |
|-------|------|-------------|-------------|
| `create` | `string` | Timeout for service creation | Duration format: `^[0-9]+(ns\|us\|ms\|s\|m\|h)$` |
| `delete` | `string` | Timeout for service deletion | Duration format: `^[0-9]+(ns\|us\|ms\|s\|m\|h)$` |
| `healthCheck` | `string` | Timeout for individual health checks | Duration format: `^[0-9]+(ns\|us\|ms\|s\|m\|h)$` |

### Examples

#### PostgreSQL Database ServiceClass
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: postgres-database
  namespace: default
spec:
  description: "PostgreSQL database service with lifecycle management"
  args:
    database_name:
      type: string
      required: true
      description: "Name of the database to create"
    port:
      type: integer
      required: false
      default: 5432
      description: "Port number for the database"
    replicas:
      type: integer
      required: false
      default: 1
      description: "Number of database replicas"
  serviceConfig:
    defaultName: "postgres-{{.database_name}}"
    dependencies: []
    lifecycleTools:
      start:
        tool: "docker_run"
        args:
          image: "postgres:13"
          env:
            POSTGRES_DB: "{{.database_name}}"
            POSTGRES_PORT: "{{.port}}"
        outputs:
          containerId: "result.container_id"
      stop:
        tool: "docker_stop"
        args:
          container_id: "{{.containerId}}"
      healthCheck:
        tool: "postgres_health_check"
        args:
          port: "{{.port}}"
        expect:
          success: true
          jsonPath:
            status: "healthy"
    healthCheck:
      enabled: true
      interval: "30s"
      failureThreshold: 3
      successThreshold: 1
    timeout:
      create: "5m"
      delete: "2m"
      healthCheck: "10s"
    outputs:
      connection_string: "postgresql://user:pass@localhost:{{.port}}/{{.database_name}}"
      port: "{{.port}}"
```

#### Web Application ServiceClass
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: web-application
  namespace: default
spec:
  description: "Generic web application service"
  args:
    image:
      type: string
      required: true
      description: "Container image to deploy"
    port:
      type: integer
      default: 8080
      description: "Application port"
    replicas:
      type: integer
      default: 1
      description: "Number of replicas"
    environment:
      type: object
      default: {}
      description: "Environment variables"
  serviceConfig:
    defaultName: "web-{{.image | replace '/' '-' | replace ':' '-'}}"
    lifecycleTools:
      start:
        tool: "kubernetes_deploy"
        args:
          image: "{{.image}}"
          port: "{{.port}}"
          replicas: "{{.replicas}}"
          env: "{{.environment}}"
        outputs:
          deploymentName: "metadata.name"
          serviceName: "service.metadata.name"
      stop:
        tool: "kubernetes_delete"
        args:
          deployment: "{{.deploymentName}}"
          service: "{{.serviceName}}"
      status:
        tool: "kubernetes_status"
        args:
          deployment: "{{.deploymentName}}"
        outputs:
          readyReplicas: "status.readyReplicas"
      healthCheck:
        tool: "http_check"
        args:
          url: "http://{{.serviceName}}:{{.port}}/health"
        expect:
          success: true
    healthCheck:
      enabled: true
      interval: "30s"
    outputs:
      url: "http://{{.serviceName}}:{{.port}}"
      deployment: "{{.deploymentName}}"
```

### CLI Usage

```bash
# List all ServiceClasses
kubectl get serviceclasses
# or using short name
kubectl get sc

# Get detailed information
kubectl describe serviceclass postgres-database

# Create from file
kubectl apply -f serviceclass.yaml
```

---

## Workflow

Workflow resources define multi-step processes that can be executed to automate complex tasks like application deployment, data migration, or system configuration.

### Resource Definition

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: <workflow-name>
  namespace: <namespace>
spec:
  # Optional: Human-readable description
  description: "<description>"
  
  # Optional: Argument schema for workflow execution
  args:
    <arg_name>:
      type: string|integer|boolean|number|object|array
      required: true|false
      default: <default_value>
      description: "<description>"
  
  # Required: Workflow steps
  steps:
    - id: "<step_id>"
      tool: "<tool_name>"
      args:
        <key>: <value_template>
      condition:
        tool: "<condition_tool>"
        args:
          <key>: <value>
        fromStep: "<step_id>"
        expect:
          success: true|false
          jsonPath:
            <path>: <expected_value>
        expectNot:
          success: true|false
          jsonPath:
            <path>: <unexpected_value>
      store: true|false
      allowFailure: true|false
      outputs:
        <var_name>: <output_template>
      description: "<step_description>"

# Status is managed automatically by muster
status:
  available: true|false              # All required tools available
  requiredTools: []                  # List of required tool names  
  missingTools: []                   # List of unavailable tools
  stepValidation: []                 # Validation results per step
  conditions: []                     # Kubernetes standard conditions
```

### Field Reference

#### Spec Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `description` | `string` | No | Human-readable description | Max 1000 characters |
| `args` | `map[string]ArgDefinition` | No | Argument schema for execution validation | - |
| `steps` | `[]WorkflowStep` | Yes | Sequence of workflow steps | Min 1 item |

#### WorkflowStep Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `id` | `string` | Yes | Unique step identifier within workflow | Pattern: `^[a-zA-Z0-9_-]+$`, Max 63 chars |
| `tool` | `string` | Yes | Name of the tool to execute | Min 1 character |
| `args` | `map[string]any` | No | Arguments for tool execution (supports templating) | - |
| `condition` | `WorkflowCondition` | No | Optional execution condition | - |
| `store` | `boolean` | No | Store step result for later steps | Default: `false` |
| `allowFailure` | `boolean` | No | Continue on step failure | Default: `false` |
| `outputs` | `map[string]any` | No | Output mappings for subsequent steps | - |
| `description` | `string` | No | Human-readable step documentation | Max 500 characters |

#### WorkflowCondition Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `tool` | `string` | No* | Tool for condition evaluation |
| `args` | `map[string]any` | No | Arguments for condition tool |
| `fromStep` | `string` | No* | Reference step for condition evaluation |
| `expect` | `WorkflowConditionExpectation` | No | Positive expectations |
| `expectNot` | `WorkflowConditionExpectation` | No | Negative expectations |

*Note: Either `tool` or `fromStep` should be specified

#### WorkflowConditionExpectation Fields

| Field | Type | Description |
|-------|------|-------------|
| `success` | `boolean` | Whether the tool call should succeed |
| `jsonPath` | `map[string]any` | JSON path conditions to check |

#### Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `available` | `boolean` | All required tools available |
| `requiredTools` | `[]string` | List of required tool names |
| `missingTools` | `[]string` | List of unavailable tools |
| `stepValidation` | `[]StepValidationResult` | Validation results for each step |
| `conditions` | `[]metav1.Condition` | Standard Kubernetes conditions |

#### StepValidationResult Fields

| Field | Type | Description |
|-------|------|-------------|
| `stepId` | `string` | Identifies the workflow step |
| `valid` | `boolean` | Whether the step passed validation |
| `toolAvailable` | `boolean` | Whether the required tool is available |
| `validationErrors` | `[]string` | Any validation error messages |

### Examples

#### Application Deployment Workflow
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: deploy-application
  namespace: default
spec:
  name: deploy-application
  description: "Deploy application to production environment with validation"
  args:
    app_name:
      type: string
      required: true
      description: "Name of the application to deploy"
    environment:
      type: string
      default: "production"
      description: "Target deployment environment"
    replicas:
      type: integer
      default: 3
      description: "Number of application replicas"
  steps:
    - id: build_image
      tool: docker_build
      args:
        name: "{{.app_name}}"
        tag: "{{.environment}}-latest"
      store: true
      description: "Build container image for deployment"
    
    - id: deploy_service
      tool: core_service_create
      args:
        name: "{{.app_name}}-{{.environment}}"
        serviceClassName: "web-application"
        args:
          image: "{{.results.build_image.image_id}}"
          replicas: "{{.replicas}}"
      store: true
      description: "Create service instance"
    
    - id: verify_deployment
      tool: health_check
      args:
        service_name: "{{.results.deploy_service.name}}"
        timeout: "5m"
      store: true
      description: "Verify deployment health"
```

#### Conditional Database Migration Workflow
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: database-migration
  namespace: default
spec:
  name: database-migration
  description: "Perform database migration with rollback capability"
  args:
    database_name:
      type: string
      required: true
      description: "Target database name"
    migration_version:
      type: string
      required: true
      description: "Target migration version"
    dry_run:
      type: boolean
      default: false
      description: "Perform dry run without applying changes"
  steps:
    - id: check_database
      tool: postgres_status
      args:
        database: "{{.database_name}}"
      store: true
      description: "Verify database connectivity"
    
    - id: backup_database
      tool: postgres_backup
      args:
        database: "{{.database_name}}"
        backup_name: "pre-migration-{{.migration_version}}"
      condition:
        fromStep: "check_database"
        expect:
          success: true
          jsonPath:
            status: "connected"
      store: true
      description: "Create backup before migration"
    
    - id: run_migration
      tool: postgres_migrate
      args:
        database: "{{.database_name}}"
        version: "{{.migration_version}}"
        dry_run: "{{.dry_run}}"
      condition:
        fromStep: "backup_database"
        expect:
          success: true
      allowFailure: false
      store: true
      description: "Execute database migration"
    
    - id: verify_migration
      tool: postgres_verify
      args:
        database: "{{.database_name}}"
        expected_version: "{{.migration_version}}"
      condition:
        fromStep: "run_migration"
        expect:
          success: true
      description: "Verify migration completed successfully"
    
    - id: rollback_migration
      tool: postgres_restore
      args:
        database: "{{.database_name}}"
        backup_name: "{{.results.backup_database.backup_name}}"
      condition:
        fromStep: "verify_migration"
        expectNot:
          success: true
      description: "Rollback on migration failure"
```

#### Multi-Environment Deployment Workflow
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: progressive-deployment
  namespace: default
spec:
  name: progressive-deployment
  description: "Deploy application progressively across environments"
  args:
    app_name:
      type: string
      required: true
      description: "Application name"
    version:
      type: string
      required: true
      description: "Application version"
    environments:
      type: array
      default: ["staging", "production"]
      description: "Target environments in order"
  steps:
    - id: deploy_staging
      tool: core_service_create
      args:
        name: "{{.app_name}}-staging"
        serviceClassName: "web-application"
        args:
          image: "{{.app_name}}:{{.version}}"
          environment: "staging"
      store: true
      description: "Deploy to staging environment"
    
    - id: test_staging
      tool: integration_test
      args:
        service_name: "{{.results.deploy_staging.name}}"
        test_suite: "smoke-tests"
      condition:
        fromStep: "deploy_staging"
        expect:
          success: true
      store: true
      description: "Run integration tests on staging"
    
    - id: deploy_production
      tool: core_service_create
      args:
        name: "{{.app_name}}-production"
        serviceClassName: "web-application"
        args:
          image: "{{.app_name}}:{{.version}}"
          environment: "production"
          replicas: 5
      condition:
        fromStep: "test_staging"
        expect:
          success: true
          jsonPath:
            test_result: "passed"
      store: true
      description: "Deploy to production environment"
    
    - id: monitor_production
      tool: health_monitor
      args:
        service_name: "{{.results.deploy_production.name}}"
        duration: "10m"
      condition:
        fromStep: "deploy_production"
        expect:
          success: true
      description: "Monitor production deployment"
```

### CLI Usage

```bash
# List all workflows
kubectl get workflows
# or using short name
kubectl get wf

# Get detailed information
kubectl describe workflow deploy-application

# Create from file
kubectl apply -f workflow.yaml
```

---

## Templating

All CRDs support Go template syntax for dynamic values. Templates can reference:

### Available Variables

| Context | Variables | Description |
|---------|-----------|-------------|
| **ServiceClass** | `.` | All args passed during service creation |
| | `.<arg_name>` | Specific argument values |
| | `.containerId`, `.deploymentName`, etc. | Outputs from previous tools |
| **Workflow** | `.` | All args passed during workflow execution |
| | `.<arg_name>` | Specific argument values |
| | `.results.<step_id>.<output>` | Results from previous steps |

### Template Functions

| Function | Description | Example |
|----------|-------------|---------|
| `replace` | String replacement | `{{.image | replace "/" "-"}}` |
| `lower` | Convert to lowercase | `{{.name | lower}}` |
| `upper` | Convert to uppercase | `{{.env | upper}}` |
| `trim` | Remove whitespace | `{{.value | trim}}` |

### Examples

```yaml
# ServiceClass templating
defaultName: "{{.app_name}}-{{.environment}}"
args:
  image: "{{.registry}}/{{.app_name}}:{{.version}}"
  env:
    APP_NAME: "{{.app_name | upper}}"
    DATABASE_URL: "{{.database_connection_string}}"

# Workflow templating  
args:
  service_name: "{{.app_name}}-{{.environment}}"
  image: "{{.results.build_image.image_id}}"
  replicas: "{{.replicas}}"
```

---

## Tool Availability and Dependencies

### Tool Discovery

Muster automatically discovers available tools from:
1. **Core Tools**: Built-in muster tools (`core_service_*`, `core_workflow_*`, etc.)
2. **MCP Server Tools**: Tools provided by registered MCPServer resources
3. **Dynamic Tools**: Workflow execution tools (`workflow_<name>`)

### Dependency Resolution

#### ServiceClass Dependencies
```yaml
spec:
  serviceConfig:
    dependencies: ["database", "cache"]  # Must be available ServiceClasses
```

#### Tool Dependencies
The system automatically validates that all referenced tools are available:
- ServiceClass lifecycle tools must be available for the ServiceClass to be marked `available: true`
- Workflow step tools must be available for the Workflow to be marked `available: true`

### Status Monitoring

All CRDs provide status information about tool availability:

```bash
# Check ServiceClass tool availability
kubectl get serviceclass postgres-database -o jsonpath='{.status.toolAvailability}'

# Check Workflow tool availability  
kubectl get workflow deploy-app -o jsonpath='{.status.missingTools}'

# Check MCP Server status
kubectl get mcpserver git-tools -o jsonpath='{.status.state}'
```

---

## Best Practices

### Naming Conventions

| Resource | Pattern | Examples |
|----------|---------|----------|
| **MCPServer** | `<tool-category>-tools` | `git-tools`, `filesystem-tools`, `database-tools` |
| **ServiceClass** | `<service-type>` | `postgres-database`, `web-application`, `message-queue` |
| **Workflow** | `<action>-<target>` | `deploy-application`, `backup-database`, `scale-service` |

### Resource Organization

```yaml
# Use consistent namespacing
metadata:
  namespace: muster-system    # For system resources
  namespace: applications     # For application resources
  namespace: default         # For development resources

# Use labels for grouping
metadata:
  labels:
    component: database
    environment: production
    version: v1.2.3
```

### Security Considerations

```yaml
# MCPServer environment variables
env:
  # Avoid storing secrets directly
  DATABASE_URL: "postgresql://user:pass@localhost:5432/db"  # ❌ BAD
  
  # Use references to secrets
  DATABASE_URL_FROM_SECRET: "database-credentials"         # ✅ GOOD

# ServiceClass security
serviceConfig:
  lifecycleTools:
    start:
      args:
        # Use secure defaults
        security_context:
          runAsNonRoot: true
          readOnlyRootFilesystem: true
```

### Error Handling

```yaml
# Workflow error handling
steps:
  - id: risky_operation
    tool: external_api_call
    allowFailure: true    # Continue on failure
    
  - id: cleanup
    tool: cleanup_resources
    condition:
      fromStep: "risky_operation"
      expectNot:
        success: true     # Only run if previous step failed
```

### Performance Optimization

```yaml
# Health check tuning
healthCheck:
  enabled: true
  interval: "30s"        # Balance between responsiveness and load
  failureThreshold: 3    # Avoid false positives
  successThreshold: 1    # Quick recovery

# Timeout configuration
timeout:
  create: "5m"          # Generous for complex deployments
  delete: "2m"          # Quick cleanup
  healthCheck: "10s"    # Fast feedback
```

---

## Related Documentation

- **[CLI Reference](cli/)** - Command-line tools for managing CRDs
- **[MCP Tools Reference](mcp-tools.md)** - Available tools for use in ServiceClasses and Workflows
- **[Workflow Creation Guide](../how-to/workflow-creation.md)** - Step-by-step workflow development
- **[Service Configuration Guide](../how-to/service-configuration.md)** - ServiceClass development patterns
- **[Architecture Overview](../explanation/architecture.md)** - How CRDs fit into the muster system 
