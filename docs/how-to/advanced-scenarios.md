# Advanced Platform Engineering Scenarios

Complex real-world scenarios that demonstrate advanced Muster capabilities for experienced platform engineers.

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
      tool: x_kubernetes_apply
      args:
        manifest: |
          apiVersion: apps/v1
          kind: Deployment
          metadata:
            name: "{{.application_name}}-{{.target_environment}}"
          spec:
            template:
              spec:
                containers:
                  - name: app
                    image: "{{.application_name}}:{{.image_tag}}"
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
