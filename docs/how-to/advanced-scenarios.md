# Advanced Platform Engineering Scenarios

Complex real-world scenarios that demonstrate advanced Muster capabilities for experienced platform engineers.

## Automated Incident Response

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
  description: "Automated incident detection and response workflow"
  # Supported arg keys: type, required, default, description.
  args:
    alert_source:
      type: string
      required: true
      description: "Source of the alert (prometheus, grafana, external)"
    severity:
      type: string
      required: true
      description: "Incident severity level: critical, warning, or info"
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
  # Reference args as {{ .input.<arg> }} and stored results as
  # {{ .results.<step-id> }} (the engine renders with missingkey=error).
  steps:
    # Initial assessment and logging
    - id: log_incident
      tool: x_log_incident
      args:
        severity: "{{ .input.severity }}"
        service: "{{ .input.affected_service }}"
        environment: "{{ .input.environment }}"
        details: "{{ .input.alert_details }}"
        timestamp: "{{ now | date \"2006-01-02T15:04:05Z07:00\" }}"
      store: true

    # Gather system information
    - id: collect_diagnostics
      tool: x_collect_system_diagnostics
      args:
        service: "{{ .input.affected_service }}"
        environment: "{{ .input.environment }}"
        include_logs: true
        include_metrics: true
        time_range: "1h"
      store: true

    # Check service health and dependencies
    - id: check_service_health
      tool: x_comprehensive_health_check
      args:
        service: "{{ .input.affected_service }}"
        environment: "{{ .input.environment }}"
        check_dependencies: true
      store: true

    # Determine remediation strategy
    - id: analyze_issue
      tool: x_analyze_incident
      args:
        diagnostics: "{{ .results.collect_diagnostics }}"
        health_check: "{{ .results.check_service_health }}"
        alert_details: "{{ .input.alert_details }}"
        severity: "{{ .input.severity }}"
      store: true

    # Execute automated remediation only when the analysis says it is safe
    # and we are not in production. A single boolean template gate can combine
    # both checks (it sees both .results and .input).
    - id: attempt_auto_remediation
      tool: x_execute_remediation
      args:
        strategy: "{{ .results.analyze_issue.recommended_action }}"
        service: "{{ .input.affected_service }}"
        environment: "{{ .input.environment }}"
        safety_level: "{{ if eq .input.severity \"critical\" }}conservative{{ else }}standard{{ end }}"
      condition:
        template: "{{ and .results.analyze_issue.auto_remediation_safe (ne .input.environment \"production\") }}"
      store: true
      allowFailure: true

    # Notify incident response team
    - id: notify_team
      tool: x_send_incident_notification
      args:
        incident_id: "{{ .results.log_incident.incident_id }}"
        severity: "{{ .input.severity }}"
        service: "{{ .input.affected_service }}"
        environment: "{{ .input.environment }}"
        analysis: "{{ .results.analyze_issue }}"

    # Create incident report
    - id: generate_incident_report
      tool: x_generate_incident_report
      args:
        incident_id: "{{ .results.log_incident.incident_id }}"
        diagnostics: "{{ .results.collect_diagnostics }}"
        analysis: "{{ .results.analyze_issue }}"
        timestamp: "{{ now | date \"2006-01-02T15:04:05Z07:00\" }}"
      store: true

    # Follow-up monitoring
    - id: schedule_follow_up
      tool: x_schedule_monitoring
      args:
        incident_id: "{{ .results.log_incident.incident_id }}"
        service: "{{ .input.affected_service }}"
        environment: "{{ .input.environment }}"
        monitoring_duration: "{{ if eq .input.severity \"critical\" }}4h{{ else }}1h{{ end }}"
        check_interval: "{{ if eq .input.severity \"critical\" }}5m{{ else }}15m{{ end }}"
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
- Validate availability with `muster check workflow <name>` (and rely on the
  CRD's server-side validation at `kubectl apply` time)
- Cover workflows with BDD scenarios (`muster test`)

## Related Documentation
- [Workflow Creation](workflow-creation.md)
