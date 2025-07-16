# Advanced Platform Engineering Scenarios

Complex real-world scenarios that demonstrate advanced Muster capabilities for experienced platform engineers.

## Scenario 1: Multi-Cluster Debugging and Comparison

### Goal
Set up cross-cluster investigation capabilities for debugging issues across development, staging, and production environments.

### Components Required

#### Cluster Access ServiceClass
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: cluster-access
  namespace: default
spec:
  description: "Secure cluster access with authentication"
  args:
    cluster_name:
      type: string
      required: true
      description: "Name of the target cluster"
    namespace:
      type: string
      default: "default"
      description: "Default namespace for operations"
    auth_method:
      type: string
      default: "kubeconfig"
      enum: ["kubeconfig", "teleport", "oidc"]
      description: "Authentication method"
  serviceConfig:
    defaultName: "cluster-{{.cluster_name}}"
    lifecycleTools:
      start:
        tool: "x_cluster_authenticate"
        args:
          cluster: "{{.cluster_name}}"
          method: "{{.auth_method}}"
          namespace: "{{.namespace}}"
        outputs:
          kubeconfig: "result.kubeconfig_path"
          context: "result.context_name"
      stop:
        tool: "x_cluster_logout"
        args:
          cluster: "{{.cluster_name}}"
      status:
        tool: "x_cluster_status"
        args:
          cluster: "{{.cluster_name}}"
        expect:
          jsonPath:
            authenticated: true
            accessible: true
```

#### Cross-Cluster Comparison Workflow
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: cross-cluster-investigation
  namespace: default
spec:
  name: cross-cluster-investigation
  description: "Compare resources and configurations across multiple clusters"
  args:
    resource_type:
      type: string
      required: true
      enum: ["pods", "services", "deployments", "configmaps"]
      description: "Type of Kubernetes resource to compare"
    namespace:
      type: string
      default: "default"
      description: "Namespace to investigate"
    resource_name:
      type: string
      description: "Specific resource name (optional)"
    include_logs:
      type: boolean
      default: false
      description: "Include pod logs in comparison"
  steps:
    # Establish cluster connections
    - id: connect_staging
      tool: core_service_create
      args:
        name: "staging-access"
        serviceClassName: "cluster-access"
        args:
          cluster_name: "staging"
          namespace: "{{.namespace}}"
      store: true
      
    - id: connect_production
      tool: core_service_create
      args:
        name: "production-access"
        serviceClassName: "cluster-access"
        args:
          cluster_name: "production"
          namespace: "{{.namespace}}"
      store: true
      
    # Gather resource information
    - id: get_staging_resources
      tool: x_k8s_get_resources
      args:
        cluster: "staging"
        resource_type: "{{.resource_type}}"
        namespace: "{{.namespace}}"
        resource_name: "{{.resource_name}}"
      store: true
      
    - id: get_production_resources
      tool: x_k8s_get_resources
      args:
        cluster: "production"
        resource_type: "{{.resource_type}}"
        namespace: "{{.namespace}}"
        resource_name: "{{.resource_name}}"
      store: true
      
    # Gather logs if requested
    - id: get_staging_logs
      tool: x_k8s_get_logs
      args:
        cluster: "staging"
        namespace: "{{.namespace}}"
        resource_name: "{{.resource_name}}"
      condition:
        and:
          - template: "{{ .include_logs }}"
          - template: "{{ eq .resource_type \"pods\" }}"
      store: true
      
    - id: get_production_logs
      tool: x_k8s_get_logs
      args:
        cluster: "production"
        namespace: "{{.namespace}}"
        resource_name: "{{.resource_name}}"
      condition:
        and:
          - template: "{{ .include_logs }}"
          - template: "{{ eq .resource_type \"pods\" }}"
      store: true
      
    # Compare and analyze
    - id: compare_resources
      tool: x_compare_k8s_resources
      args:
        staging_data: "{{.results.get_staging_resources}}"
        production_data: "{{.results.get_production_resources}}"
        resource_type: "{{.resource_type}}"
      store: true
      
    - id: analyze_differences
      tool: x_analyze_resource_diff
      args:
        comparison: "{{.results.compare_resources}}"
        staging_logs: "{{.results.get_staging_logs}}"
        production_logs: "{{.results.get_production_logs}}"
      store: true
      
    # Generate investigation report
    - id: generate_report
      tool: x_generate_investigation_report
      args:
        resource_type: "{{.resource_type}}"
        namespace: "{{.namespace}}"
        resource_name: "{{.resource_name}}"
        comparison: "{{.results.compare_resources}}"
        analysis: "{{.results.analyze_differences}}"
        timestamp: "{{.execution_time}}"
      store: true
      
    # Cleanup connections
    - id: cleanup_staging
      tool: core_service_delete
      args:
        name: "staging-access"
      allowFailure: true
      
    - id: cleanup_production
      tool: core_service_delete
      args:
        name: "production-access"
      allowFailure: true
```

## Scenario 2: Complete Observability Stack Setup

### Goal
Deploy and configure a complete monitoring and observability stack with Prometheus, Grafana, and custom dashboards.

### Observability ServiceClass
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: observability-stack
  namespace: default
spec:
  description: "Complete observability stack with monitoring and alerting"
  args:
    environment:
      type: string
      required: true
      enum: ["development", "staging", "production"]
      description: "Deployment environment"
    retention_period:
      type: string
      default: "15d"
      description: "Data retention period"
    alert_webhook:
      type: string
      description: "Webhook URL for critical alerts"
    custom_dashboards:
      type: array
      default: []
      description: "List of custom Grafana dashboard configurations"
  serviceConfig:
    defaultName: "observability-{{.environment}}"
    dependencies:
      # Prometheus for metrics collection
      - name: "prometheus-{{.environment}}"
        serviceClassName: "prometheus-server"
        args:
          environment: "{{.environment}}"
          retention: "{{.retention_period}}"
          storage_size: "{{ if eq .environment \"production\" }}100Gi{{ else }}20Gi{{ end }}"
        waitFor: "running"
        
      # Grafana for visualization
      - name: "grafana-{{.environment}}"
        serviceClassName: "grafana-server"
        args:
          environment: "{{.environment}}"
          prometheus_url: "{{.dependencies.prometheus.endpoint}}"
        waitFor: "running"
        
      # AlertManager for alerting
      - name: "alertmanager-{{.environment}}"
        serviceClassName: "alertmanager"
        args:
          environment: "{{.environment}}"
          webhook_url: "{{.alert_webhook}}"
        waitFor: "running"
        condition: "{{ ne .alert_webhook \"\" }}"
        
    lifecycleTools:
      start:
        tool: x_setup_observability_stack
        args:
          environment: "{{.environment}}"
          prometheus_endpoint: "{{.dependencies.prometheus.endpoint}}"
          grafana_endpoint: "{{.dependencies.grafana.endpoint}}"
          alertmanager_endpoint: "{{.dependencies.alertmanager.endpoint}}"
          custom_dashboards: "{{.custom_dashboards}}"
        outputs:
          stack_url: "result.grafana_url"
          prometheus_url: "result.prometheus_url"
          alert_rules: "result.configured_rules"
      stop:
        tool: x_teardown_observability_stack
        args:
          environment: "{{.environment}}"
      status:
        tool: x_check_observability_health
        args:
          environment: "{{.environment}}"
        expect:
          jsonPath:
            prometheus_healthy: true
            grafana_healthy: true
            dashboards_loaded: true
    healthCheck:
      enabled: true
      interval: "60s"
      failureThreshold: 2
      successThreshold: 1
    timeout:
      create: "15m"
      delete: "10m"
    outputs:
      grafana_url: "{{.stack_url}}"
      prometheus_url: "{{.prometheus_url}}"
      monitoring_enabled: true
```

### Investigation Setup Workflow
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: setup-investigation-environment
  namespace: default
spec:
  name: setup-investigation-environment
  description: "Set up complete investigation environment with monitoring and debugging tools"
  args:
    environment:
      type: string
      required: true
      description: "Target environment for investigation"
    enable_advanced_monitoring:
      type: boolean
      default: true
      description: "Enable advanced monitoring features"
  steps:
    # Set up core observability
    - id: deploy_observability
      tool: core_service_create
      args:
        name: "investigation-monitoring"
        serviceClassName: "observability-stack"
        args:
          environment: "{{.environment}}"
          retention_period: "7d"
          custom_dashboards:
            - name: "investigation-dashboard"
              type: "custom"
              queries:
                - "rate(http_requests_total[5m])"
                - "kubernetes_pod_restart_total"
                - "node_memory_usage_bytes"
      store: true
      
    # Configure MCP servers for debugging
    - id: setup_prometheus_mcp
      tool: core_mcpserver_create
      args:
        name: "prometheus-tools"
        type: "localCommand"
        command: ["mcp-server-prometheus"]
        autoStart: true
        env:
          PROMETHEUS_URL: "{{.results.deploy_observability.prometheus_url}}"
        
    - id: setup_grafana_mcp
      tool: core_mcpserver_create
      args:
        name: "grafana-tools"
        type: "localCommand"
        command: ["mcp-server-grafana"]
        autoStart: true
        env:
          GRAFANA_URL: "{{.results.deploy_observability.grafana_url}}"
          
    # Set up log aggregation
    - id: configure_log_aggregation
      tool: x_setup_log_aggregation
      args:
        environment: "{{.environment}}"
        prometheus_endpoint: "{{.results.deploy_observability.prometheus_url}}"
      condition:
        template: "{{ .enable_advanced_monitoring }}"
      store: true
      
    # Create investigation workspace
    - id: create_workspace
      tool: x_create_investigation_workspace
      args:
        environment: "{{.environment}}"
        monitoring_url: "{{.results.deploy_observability.grafana_url}}"
        tools_available:
          - "prometheus-tools"
          - "grafana-tools"
          - "kubernetes-tools"
      store: true
      
    # Generate access instructions
    - id: generate_access_guide
      tool: x_generate_access_guide
      args:
        workspace: "{{.results.create_workspace}}"
        grafana_url: "{{.results.deploy_observability.grafana_url}}"
        prometheus_url: "{{.results.deploy_observability.prometheus_url}}"
        environment: "{{.environment}}"
```

## Scenario 3: Automated Incident Response

### Goal
Create an automated incident response system that can detect issues, gather diagnostic information, and execute remediation procedures.

### Incident Response Workflow
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: automated-incident-response
  namespace: default
spec:
  name: automated-incident-response
  description: "Automated incident detection and response workflow"
  args:
    alert_source:
      type: string
      required: true
      description: "Source of the alert (prometheus, grafana, external)"
    severity:
      type: string
      required: true
      enum: ["critical", "warning", "info"]
      description: "Incident severity level"
    affected_service:
      type: string
      required: true
      description: "Name of the affected service"
    environment:
      type: string
      required: true
      description: "Environment where incident occurred"
    alert_details:
      type: object
      required: true
      description: "Detailed alert information"
  steps:
    # Initial assessment and logging
    - id: log_incident
      tool: x_log_incident
      args:
        severity: "{{.severity}}"
        service: "{{.affected_service}}"
        environment: "{{.environment}}"
        details: "{{.alert_details}}"
        timestamp: "{{.execution_time}}"
      store: true
      
    # Gather system information
    - id: collect_diagnostics
      tool: x_collect_system_diagnostics
      args:
        service: "{{.affected_service}}"
        environment: "{{.environment}}"
        include_logs: true
        include_metrics: true
        time_range: "1h"
      store: true
      
    # Check service health and dependencies
    - id: check_service_health
      tool: x_comprehensive_health_check
      args:
        service: "{{.affected_service}}"
        environment: "{{.environment}}"
        check_dependencies: true
      store: true
      
    # Determine remediation strategy
    - id: analyze_issue
      tool: x_analyze_incident
      args:
        diagnostics: "{{.results.collect_diagnostics}}"
        health_check: "{{.results.check_service_health}}"
        alert_details: "{{.alert_details}}"
        severity: "{{.severity}}"
      store: true
      
    # Execute automated remediation (if safe)
    - id: attempt_auto_remediation
      tool: x_execute_remediation
      args:
        strategy: "{{.results.analyze_issue.recommended_action}}"
        service: "{{.affected_service}}"
        environment: "{{.environment}}"
        safety_level: "{{ if eq .severity \"critical\" }}conservative{{ else }}standard{{ end }}"
      condition:
        and:
          - jsonPath:
              "results.analyze_issue.auto_remediation_safe": true
          - template: "{{ ne .environment \"production\" }}"
      store: true
      allowFailure: true
      
    # Notify incident response team
    - id: notify_team
      tool: x_send_incident_notification
      args:
        incident_id: "{{.results.log_incident.incident_id}}"
        severity: "{{.severity}}"
        service: "{{.affected_service}}"
        environment: "{{.environment}}"
        analysis: "{{.results.analyze_issue}}"
        auto_remediation_attempted: "{{ ne .results.attempt_auto_remediation nil }}"
        auto_remediation_success: "{{.results.attempt_auto_remediation.success}}"
        
    # Create incident report
    - id: generate_incident_report
      tool: x_generate_incident_report
      args:
        incident_id: "{{.results.log_incident.incident_id}}"
        diagnostics: "{{.results.collect_diagnostics}}"
        analysis: "{{.results.analyze_issue}}"
        remediation_actions: "{{.results.attempt_auto_remediation}}"
        timestamp: "{{.execution_time}}"
      store: true
      
    # Follow-up monitoring
    - id: schedule_follow_up
      tool: x_schedule_monitoring
      args:
        incident_id: "{{.results.log_incident.incident_id}}"
        service: "{{.affected_service}}"
        environment: "{{.environment}}"
        monitoring_duration: "{{ if eq .severity \"critical\" }}4h{{ else }}1h{{ end }}"
        check_interval: "{{ if eq .severity \"critical\" }}5m{{ else }}15m{{ end }}"
```

## Scenario 4: Multi-Environment Deployment Pipeline

### Goal
Create a sophisticated deployment pipeline that automatically promotes applications through multiple environments with appropriate testing and validation.

### Environment Promotion Workflow
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: multi-environment-deployment
  namespace: default
spec:
  name: multi-environment-deployment
  description: "Complete deployment pipeline with automatic environment promotion"
  args:
    application_name:
      type: string
      required: true
      description: "Name of the application to deploy"
    image_tag:
      type: string
      required: true
      description: "Container image tag to deploy"
    target_environment:
      type: string
      default: "development"
      enum: ["development", "staging", "production"]
      description: "Target environment for deployment"
    auto_promote:
      type: boolean
      default: true
      description: "Enable automatic promotion to next environment"
    rollback_on_failure:
      type: boolean
      default: true
      description: "Automatically rollback on deployment failure"
  steps:
    # Validate deployment prerequisites
    - id: validate_deployment
      tool: x_validate_deployment_prerequisites
      args:
        application: "{{.application_name}}"
        image_tag: "{{.image_tag}}"
        environment: "{{.target_environment}}"
      store: true
      
    # Get current deployment information
    - id: get_current_deployment
      tool: x_get_current_deployment
      args:
        application: "{{.application_name}}"
        environment: "{{.target_environment}}"
      store: true
      allowFailure: true
      
    # Deploy to target environment
    - id: deploy_application
      tool: core_service_create
      args:
        name: "{{.application_name}}-{{.target_environment}}"
        serviceClassName: "environment-aware-app"
        args:
          app_name: "{{.application_name}}"
          image: "{{.application_name}}:{{.image_tag}}"
          environment: "{{.target_environment}}"
      store: true
      
    # Wait for deployment to be ready
    - id: wait_for_ready
      tool: x_wait_for_deployment_ready
      args:
        service_name: "{{.application_name}}-{{.target_environment}}"
        timeout: "{{ if eq .target_environment \"production\" }}10m{{ else }}5m{{ end }}"
      store: true
      
    # Run environment-specific tests
    - id: run_smoke_tests
      tool: x_run_smoke_tests
      args:
        application: "{{.application_name}}"
        environment: "{{.target_environment}}"
        endpoint: "{{.results.deploy_application.service_url}}"
      store: true
      
    - id: run_integration_tests
      tool: x_run_integration_tests
      args:
        application: "{{.application_name}}"
        environment: "{{.target_environment}}"
        endpoint: "{{.results.deploy_application.service_url}}"
      condition:
        template: "{{ ne .target_environment \"development\" }}"
      store: true
      
    - id: run_performance_tests
      tool: x_run_performance_tests
      args:
        application: "{{.application_name}}"
        environment: "{{.target_environment}}"
        endpoint: "{{.results.deploy_application.service_url}}"
      condition:
        template: "{{ eq .target_environment \"production\" }}"
      store: true
      
    # Validate deployment success
    - id: validate_deployment_success
      tool: x_validate_deployment_success
      args:
        smoke_tests: "{{.results.run_smoke_tests}}"
        integration_tests: "{{.results.run_integration_tests}}"
        performance_tests: "{{.results.run_performance_tests}}"
        environment: "{{.target_environment}}"
      store: true
      
    # Handle deployment failure
    - id: rollback_deployment
      tool: x_rollback_deployment
      args:
        application: "{{.application_name}}"
        environment: "{{.target_environment}}"
        previous_deployment: "{{.results.get_current_deployment}}"
      condition:
        and:
          - template: "{{ .rollback_on_failure }}"
          - jsonPath:
              "results.validate_deployment_success.success": false
      store: true
      
    # Promote to next environment (if applicable)
    - id: determine_next_environment
      tool: x_get_next_environment
      args:
        current_environment: "{{.target_environment}}"
        auto_promote: "{{.auto_promote}}"
        deployment_success: "{{.results.validate_deployment_success.success}}"
      store: true
      
    - id: promote_to_next_environment
      tool: workflow_multi_environment_deployment
      args:
        application_name: "{{.application_name}}"
        image_tag: "{{.image_tag}}"
        target_environment: "{{.results.determine_next_environment.next_env}}"
        auto_promote: "{{.auto_promote}}"
        rollback_on_failure: "{{.rollback_on_failure}}"
      condition:
        jsonPath:
          "results.determine_next_environment.should_promote": true
      store: true
      
    # Generate deployment report
    - id: generate_deployment_report
      tool: x_generate_deployment_report
      args:
        application: "{{.application_name}}"
        image_tag: "{{.image_tag}}"
        environment: "{{.target_environment}}"
        deployment_success: "{{.results.validate_deployment_success.success}}"
        test_results:
          smoke: "{{.results.run_smoke_tests}}"
          integration: "{{.results.run_integration_tests}}"
          performance: "{{.results.run_performance_tests}}"
        promotion_attempted: "{{ ne .results.promote_to_next_environment nil }}"
```

## Best Practices for Advanced Scenarios

### 1. Error Handling and Recovery
- Always include rollback procedures for critical operations
- Use `allowFailure: true` for non-critical steps
- Implement comprehensive logging and audit trails
- Design for graceful degradation

### 2. Security Considerations
- Use least-privilege access for all operations
- Implement proper secrets management
- Audit all privileged operations
- Use secure communication channels

### 3. Performance Optimization
- Leverage parallel execution where possible
- Implement appropriate timeouts and retries
- Use resource limits and monitoring
- Cache frequently used data

### 4. Monitoring and Observability
- Include comprehensive logging in all workflows
- Implement health checks and status monitoring
- Use distributed tracing for complex workflows
- Set up alerting for critical operations

### 5. Testing and Validation
- Test workflows in non-production environments first
- Use dry-run modes for validation
- Implement comprehensive test coverage
- Validate all configuration templates

## Related Documentation
- [Workflow Creation](workflow-creation.md)
- [ServiceClass Patterns](serviceclass-patterns.md)