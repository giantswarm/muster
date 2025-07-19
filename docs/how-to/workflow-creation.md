# Workflow Creation

Learn how to build effective multi-step workflows for platform automation.

## Build Multi-Step Deployment Workflows

### Goal
Create workflows that automate complex deployment processes with proper error handling and validation.

### Prerequisites
- Understanding of your deployment process
- Required tools available via MCP servers
- ServiceClasses defined for your services

### Basic Workflow Structure

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: deployment-workflow
  namespace: default
spec:
  description: "Multi-step deployment with validation"
  args:
    app_name:
      type: string
      required: true
      description: "Application name"
    version:
      type: string
      required: true
      description: "Version to deploy"
    environment:
      type: string
      default: "staging"
      description: "Target environment"
  steps:
    - id: validate_prerequisites
      tool: x_deployment_validate_prerequisites
      args:
        app_name: "{{.app_name}}"
        environment: "{{.environment}}"
      
    - id: create_deployment
      tool: core_service_create
      args:
        name: "{{.app_name}}-{{.environment}}"
        serviceClassName: "web-application"
        args:
          image: "{{.app_name}}:{{.version}}"
          environment: "{{.environment}}"
      store: true
      
    - id: wait_for_ready
      tool: x_kubernetes_wait_for_deployment
      args:
        deployment_name: "{{.results.create_deployment.name}}"
        timeout: "300s"
      
    - id: run_health_checks
      tool: x_monitoring_health_check
      args:
        endpoint: "{{.results.create_deployment.endpoint}}"
        
    - id: notify_team
      tool: x_slack_send_notification
      args:
        message: "✅ {{.app_name}} {{.version}} deployed to {{.environment}}"
        channel: "#deployments"
```

### Advanced Features

#### Error Handling with allowFailure
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: resilient-deployment
  namespace: default
spec:
  name: resilient-deployment
  description: "Deployment with optional steps"
  args:
    app_name:
      type: string
      required: true
  steps:
    - id: optional_migration
      tool: x_database_run_migration
      args:
        version: "{{.version}}"
      allowFailure: true  # Continue workflow even if this fails
      
    - id: deploy_app
      tool: core_service_create
      args:
        name: "{{.app_name}}"
        serviceClassName: "web-application"
```

#### Conditional Steps
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: conditional-deployment
  namespace: default
spec:
  name: conditional-deployment
  description: "Deployment with environment-specific steps"
  args:
    environment:
      type: string
      required: true
  steps:
    - id: check_environment
      tool: core_config_get
      args:
        environment: "{{.environment}}"
      store: true
      
    - id: production_validation
      tool: x_security_run_production_checks
      condition:
        jsonPath:
          "results.check_environment.is_production": true
          
    - id: staging_fast_deploy
      tool: x_deployment_quick_deploy
      condition:
        jsonPath:
          "results.check_environment.is_staging": true
```

## Handle Conditional Workflow Steps

### Goal
Create workflows that execute different paths based on conditions.

### Condition Types

#### 1. JSONPath Conditions
```yaml
# Check result values from previous steps
- id: conditional_step
  tool: x_security_production_specific_tool
  condition:
    jsonPath:
      "results.environment_check.type": "production"
      "results.environment_check.ready": true
```

#### 2. Template-Based Conditions
```yaml
# Use template expressions
- id: environment_specific
  tool: x_deployment_deploy_to_environment
  condition:
    template: "{{ eq .environment \"production\" }}"
```

#### 3. Complex Conditions
```yaml
# Multiple conditions with logic
- id: complex_validation
  tool: x_monitoring_advanced_validation
  condition:
    and:
      - jsonPath:
          "results.health_check.status": "healthy"
      - template: "{{ gt .replicas 1 }}"
      - jsonPath:
          "results.security_scan.passed": true
```

### Real-World Example: Environment-Specific Deployment

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: smart-deployment
  namespace: default
spec:
  name: smart-deployment
  description: "Intelligent deployment based on environment"
  args:
    app_name:
      type: string
      required: true
    environment:
      type: string
      required: true
    force_production:
      type: boolean
      default: false
  steps:
    - id: analyze_environment
      tool: get_environment_config
      args:
        environment: "{{.environment}}"
      store: true
      
    # Development: Quick deployment
    - id: dev_quick_deploy
      tool: quick_deploy
      args:
        app: "{{.app_name}}"
        env: "{{.environment}}"
      condition:
        jsonPath:
          "results.analyze_environment.type": "development"
          
    # Staging: Deploy with basic tests
    - id: staging_deploy
      tool: deploy_with_tests
      args:
        app: "{{.app_name}}"
        env: "{{.environment}}"
        test_suite: "basic"
      condition:
        jsonPath:
          "results.analyze_environment.type": "staging"
          
    # Production: Full validation pipeline
    - id: prod_security_scan
      tool: security_scan
      args:
        app: "{{.app_name}}"
      condition:
        or:
          - jsonPath:
              "results.analyze_environment.type": "production"
          - template: "{{ .force_production }}"
      store: true
      
    - id: prod_deploy
      tool: production_deploy
      args:
        app: "{{.app_name}}"
        security_report: "{{.results.prod_security_scan.report}}"
      condition:
        and:
          - jsonPath:
              "results.prod_security_scan.passed": true
          - or:
            - jsonPath:
                "results.analyze_environment.type": "production"
            - template: "{{ .force_production }}"
```

## Template Workflow Arguments

### Goal
Use Go templates to create dynamic, reusable workflows.

### Basic Templating

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: templated-workflow
  namespace: default
spec:
  name: templated-workflow
  description: "Workflow with dynamic configuration"
  args:
    app_name:
      type: string
      required: true
    replicas:
      type: integer
      default: 1
    environment:
      type: string
      default: "development"
  steps:
    - id: deploy_app
      tool: core_service_create
      args:
        name: "{{.app_name}}-{{.environment}}"
        serviceClassName: "web-application"
        args:
          # Template expressions
          image: "{{.app_name}}:{{ if eq .environment \"production\" }}stable{{ else }}latest{{ end }}"
          replicas: "{{.replicas}}"
          # Conditional configuration
          resources: "{{ if eq .environment \"production\" }}{\"cpu\": \"1000m\", \"memory\": \"2Gi\"}{{ else }}{\"cpu\": \"100m\", \"memory\": \"256Mi\"}{{ end }}"
```

### Advanced Template Functions

```yaml
# Complex templating with helper functions
args:
  database_url: "postgresql://user:pass@{{.database_host}}:{{ add .database_port 1000 }}/{{.app_name}}"
  feature_flags: "{{ join .enabled_features \",\" }}"
  config_json: |
    {
      "app": "{{.app_name}}",
      "env": "{{.environment}}",
      "debug": {{ if eq .environment "development" }}true{{ else }}false{{ end }},
      "replicas": {{.replicas}},
      "created_at": "{{ now.Format \"2006-01-02T15:04:05Z07:00\" }}"
    }
```

### Template with Result References

```yaml
steps:
  - id: get_cluster_info
    tool: get_kubernetes_cluster_info
    args:
      cluster: "{{.target_cluster}}"
    store: true
    
  - id: deploy_to_cluster
    tool: deploy_application
    args:
      cluster_endpoint: "{{.results.get_cluster_info.endpoint}}"
      cluster_version: "{{.results.get_cluster_info.version}}"
      # Use template logic with results
      namespace: "{{ if gt .results.get_cluster_info.node_count 5 }}production{{ else }}staging{{ end }}"
```

## Debug Workflow Execution

### Goal
Effectively troubleshoot and debug workflow execution issues.

### Debugging Techniques

#### 1. Add Debug Steps
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: debuggable-workflow
  namespace: default
spec:
  name: debuggable-workflow
  steps:
    - id: debug_input
      tool: debug_log
      args:
        message: "Starting workflow with args: {{.}}"
        level: "info"
        
    - id: main_task
      tool: complex_operation
      args:
        input: "{{.complex_input}}"
      store: true
      
    - id: debug_result
      tool: debug_log
      args:
        message: "Main task completed: {{.results.main_task}}"
        level: "info"
```

#### 2. Validation Steps
```yaml
steps:
  - id: validate_input
    tool: validate_workflow_input
    args:
      schema: |
        {
          "type": "object",
          "required": ["app_name", "environment"],
          "properties": {
            "app_name": {"type": "string", "minLength": 1},
            "environment": {"type": "string", "enum": ["dev", "staging", "production"]}
          }
        }
      input: "{{.}}"
```

#### 3. Checkpoint Steps
```yaml
steps:
  - id: checkpoint_1
    tool: create_checkpoint
    args:
      name: "pre-deployment"
      state: "{{.}}"
      results: "{{.results}}"
      
  - id: risky_operation
    tool: complex_deployment
    args:
      config: "{{.deployment_config}}"
    store: true
    
  - id: checkpoint_2
    tool: create_checkpoint
    args:
      name: "post-deployment"
      state: "{{.}}"
      results: "{{.results}}"
```

### Workflow Monitoring

#### Real-time Execution Tracking
```bash
# Monitor workflow execution
muster get workflow-execution deployment-workflow-20240107-123456 --watch

# Get detailed execution status
muster describe workflow-execution deployment-workflow-20240107-123456

# View execution logs
muster logs workflow-execution deployment-workflow-20240107-123456
```

#### Error Analysis
```bash
# Get failed step details
muster get workflow-execution deployment-workflow-20240107-123456 -o json | jq '.status.steps[] | select(.status == "failed")'

# View step-specific logs
muster logs workflow-execution deployment-workflow-20240107-123456 --step validate_prerequisites

# Export execution trace
muster get workflow-execution deployment-workflow-20240107-123456 --export-trace trace.json
```

### Testing Strategies

#### 1. Dry Run Mode
```yaml
# Add dry-run capability to workflows
steps:
  - id: deploy_service
    tool: core_service_create
    args:
      name: "{{.app_name}}"
      serviceClassName: "web-application"
      dryRun: "{{ .dry_run | default false }}"
```

#### 2. Mock Steps for Testing
```yaml
# Test workflow with mock tools
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: test-deployment-workflow
  namespace: default
spec:
  name: test-deployment-workflow
  description: "Test version with mocks"
  args:
    use_mocks:
      type: boolean
      default: true
  steps:
    - id: deploy_app
      tool: "{{ if .use_mocks }}mock_deploy{{ else }}real_deploy{{ end }}"
      args:
        app: "{{.app_name}}"
```

## Advanced Workflow Patterns

### 1. Parallel Execution
```yaml
# Execute steps in parallel
steps:
  - id: parallel_group_1
    parallel:
      - id: deploy_frontend
        tool: deploy_service
        args:
          service: "frontend"
      - id: deploy_backend
        tool: deploy_service
        args:
          service: "backend"
      - id: deploy_database
        tool: deploy_service
        args:
          service: "database"
  
  - id: verify_all_services
    tool: verify_deployment
    args:
      services: ["frontend", "backend", "database"]
```

### 2. Loop Patterns
```yaml
# Process multiple items
steps:
  - id: deploy_microservices
    forEach:
      items: "{{.microservices}}"
      step:
        id: deploy_service
        tool: deploy_microservice
        args:
          name: "{{.item.name}}"
          version: "{{.item.version}}"
          config: "{{.item.config}}"
```

### 3. Rollback Workflows
```yaml
# Built-in rollback capability
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: deployment-with-rollback
  namespace: default
spec:
  name: deployment-with-rollback
  onFailure:
    - id: rollback_deployment
      tool: rollback_to_previous_version
      args:
        service: "{{.app_name}}"
        environment: "{{.environment}}"
    - id: notify_failure
      tool: send_alert
      args:
        message: "Deployment failed, rolled back {{.app_name}}"
  steps:
    # ... normal deployment steps
```

## Best Practices

### 1. Workflow Design
- Keep workflows focused on single objectives
- Use descriptive step IDs and descriptions
- Implement proper error handling
- Add validation steps early

### 2. Configuration Management
- Use templates for dynamic configuration
- Externalize environment-specific values
- Validate inputs before processing
- Store sensitive data securely

### 3. Monitoring and Observability
- Add logging steps for debugging
- Include health checks and validation
- Monitor execution times and success rates
- Implement alerting for critical failures

### 4. Testing and Validation
- Test workflows in non-production environments
- Use dry-run modes for validation
- Implement rollback procedures
- Version control workflow definitions

## Related Documentation
- [ServiceClass Patterns](serviceclass-patterns.md)
- [Workflow Reference](../reference/mcp-tools.md#workflow-tools) 