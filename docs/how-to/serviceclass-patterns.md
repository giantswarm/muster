# ServiceClass Patterns

Learn common design patterns for creating reusable service templates.

## Create Reusable Service Templates

### Goal
Design ServiceClasses that can be easily reused across different environments and use cases.

### Basic ServiceClass Structure

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: web-application
  namespace: default
spec:
  description: "Generic web application service with lifecycle management"
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
      type: string
      default: "development"
      description: "Deployment environment"
  serviceConfig:
    defaultName: "webapp-{{.environment}}-{{.timestamp}}"
    dependencies: []
    lifecycleTools:
      start:
        tool: "x_k8s_deploy_app"
        args:
          image: "{{.image}}"
          port: "{{.port}}"
          replicas: "{{.replicas}}"
          environment: "{{.environment}}"
        outputs:
          serviceUrl: "result.service_url"
          podNames: "result.pod_names"
      stop:
        tool: "x_k8s_delete_app"
        args:
          namespace: "{{.environment}}"
          app_name: "{{.name}}"
      status:
        tool: "x_k8s_get_app_status"
        args:
          namespace: "{{.environment}}"
          app_name: "{{.name}}"
        expect:
          jsonPath:
            status: "running"
            ready_replicas: "{{.replicas}}"
    healthCheck:
      enabled: true
      interval: "30s"
      failureThreshold: 3
      successThreshold: 1
    timeout:
      create: "10m"
      delete: "5m"
      healthCheck: "30s"
    outputs:
      endpoint: "{{.serviceUrl}}"
      replicas: "{{.replicas}}"
      environment: "{{.environment}}"
```

## Database Service Pattern

### Goal
Create a robust database service with backup and recovery capabilities.

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: postgresql-database
  namespace: default
spec:
  description: "PostgreSQL database with automated backup and monitoring"
  args:
    database_name:
      type: string
      required: true
      description: "Name of the database to create"
    username:
      type: string
      default: "postgres"
      description: "Database username"
    password:
      type: string
      required: true
      description: "Database password (should be secret)"
    storage_size:
      type: string
      default: "10Gi"
      description: "Storage size for the database"
    backup_schedule:
      type: string
      default: "0 2 * * *"
      description: "Cron schedule for backups"
    environment:
      type: string
      default: "development"
      description: "Environment (affects resource allocation)"
  serviceConfig:
    defaultName: "postgres-{{.database_name}}-{{.environment}}"
    dependencies: []
    lifecycleTools:
      start:
        tool: "x_postgres_deploy"
        args:
          database_name: "{{.database_name}}"
          username: "{{.username}}"
          password: "{{.password}}"
          storage_size: "{{.storage_size}}"
          environment: "{{.environment}}"
          # Environment-specific resource allocation
          resources: "{{ if eq .environment \"production\" }}{\"cpu\": \"2000m\", \"memory\": \"4Gi\"}{{ else }}{\"cpu\": \"500m\", \"memory\": \"1Gi\"}{{ end }}"
        outputs:
          connectionString: "result.connection_string"
          host: "result.host"
          port: "result.port"
      stop:
        tool: "x_postgres_destroy"
        args:
          database_name: "{{.database_name}}"
          environment: "{{.environment}}"
          backup_before_destroy: "{{ if eq .environment \"production\" }}true{{ else }}false{{ end }}"
      status:
        tool: "x_postgres_status"
        args:
          database_name: "{{.database_name}}"
          environment: "{{.environment}}"
        expect:
          jsonPath:
            status: "running"
            connections: ">= 0"
    # Custom lifecycle hooks
    lifecycleHooks:
      postStart:
        - tool: "x_postgres_setup_backup"
          args:
            database_name: "{{.database_name}}"
            schedule: "{{.backup_schedule}}"
            environment: "{{.environment}}"
        - tool: "x_postgres_create_monitoring"
          args:
            database_name: "{{.database_name}}"
            environment: "{{.environment}}"
      preStop:
        - tool: "x_postgres_final_backup"
          args:
            database_name: "{{.database_name}}"
            environment: "{{.environment}}"
    healthCheck:
      enabled: true
      interval: "60s"
      failureThreshold: 3
      successThreshold: 1
      tool: "x_postgres_health_check"
      args:
        connection_string: "{{.connectionString}}"
    timeout:
      create: "15m"
      delete: "10m"
      healthCheck: "30s"
    outputs:
      connection_string: "{{.connectionString}}"
      host: "{{.host}}"
      port: "{{.port}}"
      database_name: "{{.database_name}}"
```

## Microservice with Dependencies Pattern

### Goal
Create a microservice that depends on other services (database, cache, messaging).

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: microservice-with-deps
  namespace: default
spec:
  description: "Microservice with database and cache dependencies"
  args:
    service_name:
      type: string
      required: true
      description: "Name of the microservice"
    image:
      type: string
      required: true
      description: "Container image"
    database_required:
      type: boolean
      default: true
      description: "Whether database is required"
    cache_required:
      type: boolean
      default: false
      description: "Whether Redis cache is required"
    environment:
      type: string
      default: "development"
      description: "Deployment environment"
  serviceConfig:
    defaultName: "{{.service_name}}-{{.environment}}"
    # Define service dependencies
    dependencies:
      - name: "{{.service_name}}-db"
        serviceClassName: "postgresql-database"
        condition: "{{.database_required}}"
        args:
          database_name: "{{.service_name}}"
          environment: "{{.environment}}"
        waitFor: "running"
      - name: "{{.service_name}}-cache"
        serviceClassName: "redis-cache"
        condition: "{{.cache_required}}"
        args:
          cache_name: "{{.service_name}}"
          environment: "{{.environment}}"
        waitFor: "running"
    lifecycleTools:
      start:
        tool: "x_k8s_deploy_microservice"
        args:
          name: "{{.service_name}}"
          image: "{{.image}}"
          environment: "{{.environment}}"
          # Use dependency outputs
          database_url: "{{ if .database_required }}{{.dependencies.db.connection_string}}{{ else }}{{ end }}"
          redis_url: "{{ if .cache_required }}{{.dependencies.cache.connection_string}}{{ else }}{{ end }}"
        outputs:
          serviceUrl: "result.service_url"
          healthEndpoint: "result.health_endpoint"
      stop:
        tool: "x_k8s_delete_microservice"
        args:
          name: "{{.service_name}}"
          environment: "{{.environment}}"
      status:
        tool: "x_k8s_get_microservice_status"
        args:
          name: "{{.service_name}}"
          environment: "{{.environment}}"
        expect:
          jsonPath:
            status: "running"
            health_check: "healthy"
    healthCheck:
      enabled: true
      interval: "30s"
      failureThreshold: 3
      successThreshold: 1
      tool: "x_http_health_check"
      args:
        url: "{{.healthEndpoint}}"
        expected_status: 200
    timeout:
      create: "15m"  # Longer timeout due to dependencies
      delete: "10m"
      healthCheck: "60s"
    outputs:
      service_url: "{{.serviceUrl}}"
      health_endpoint: "{{.healthEndpoint}}"
```

## Environment-Specific Configuration Pattern

### Goal
Create ServiceClasses that adapt to different environments automatically.

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: environment-aware-app
  namespace: default
spec:
  description: "Application that adapts configuration based on environment"
  args:
    app_name:
      type: string
      required: true
      description: "Application name"
    image:
      type: string
      required: true
      description: "Container image"
    environment:
      type: string
      required: true
      description: "Target environment"
      enum: ["development", "staging", "production"]
  serviceConfig:
    defaultName: "{{.app_name}}-{{.environment}}"
    lifecycleTools:
      start:
        tool: "x_k8s_deploy_app"
        args:
          name: "{{.app_name}}"
          image: "{{.image}}"
          environment: "{{.environment}}"
          # Environment-specific configuration
          replicas: |
            {{ if eq .environment "production" }}5
            {{ else if eq .environment "staging" }}2
            {{ else }}1{{ end }}
          resources: |
            {{ if eq .environment "production" }}
            {"requests": {"cpu": "1000m", "memory": "2Gi"}, "limits": {"cpu": "2000m", "memory": "4Gi"}}
            {{ else if eq .environment "staging" }}
            {"requests": {"cpu": "500m", "memory": "1Gi"}, "limits": {"cpu": "1000m", "memory": "2Gi"}}
            {{ else }}
            {"requests": {"cpu": "100m", "memory": "256Mi"}, "limits": {"cpu": "500m", "memory": "1Gi"}}
            {{ end }}
          storage_class: |
            {{ if eq .environment "production" }}fast-ssd
            {{ else if eq .environment "staging" }}standard-ssd
            {{ else }}standard{{ end }}
          monitoring_enabled: "{{ if eq .environment \"production\" }}true{{ else }}false{{ end }}"
          debug_mode: "{{ if eq .environment \"development\" }}true{{ else }}false{{ end }}"
        outputs:
          serviceUrl: "result.service_url"
          monitoringUrl: "result.monitoring_url"
      stop:
        tool: "x_k8s_delete_app"
        args:
          name: "{{.app_name}}"
          environment: "{{.environment}}"
      status:
        tool: "x_k8s_get_app_status"
        args:
          name: "{{.app_name}}"
          environment: "{{.environment}}"
    # Environment-specific health checks
    healthCheck:
      enabled: true
      interval: "{{ if eq .environment \"production\" }}15s{{ else }}30s{{ end }}"
      failureThreshold: "{{ if eq .environment \"production\" }}2{{ else }}3{{ end }}"
      successThreshold: 1
    # Environment-specific timeouts
    timeout:
      create: "{{ if eq .environment \"production\" }}20m{{ else }}10m{{ end }}"
      delete: "5m"
      healthCheck: "60s"
    outputs:
      service_url: "{{.serviceUrl}}"
      monitoring_url: "{{.monitoringUrl}}"
      environment: "{{.environment}}"
```

## Batch Job Pattern

### Goal
Create a ServiceClass for running batch jobs and data processing tasks.

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: batch-job
  namespace: default
spec:
  description: "Batch job processing with retry and monitoring"
  args:
    job_name:
      type: string
      required: true
      description: "Name of the batch job"
    image:
      type: string
      required: true
      description: "Container image for the job"
    command:
      type: array
      description: "Command to run"
    args:
      type: array
      description: "Arguments for the command"
    input_data:
      type: string
      description: "Input data location"
    output_data:
      type: string
      description: "Output data location"
    max_retries:
      type: integer
      default: 3
      description: "Maximum number of retries"
    timeout:
      type: string
      default: "1h"
      description: "Job timeout"
    parallelism:
      type: integer
      default: 1
      description: "Number of parallel executions"
  serviceConfig:
    defaultName: "job-{{.job_name}}-{{.timestamp}}"
    lifecycleTools:
      start:
        tool: "x_k8s_create_job"
        args:
          name: "{{.job_name}}"
          image: "{{.image}}"
          command: "{{.command}}"
          args: "{{.args}}"
          input_data: "{{.input_data}}"
          output_data: "{{.output_data}}"
          max_retries: "{{.max_retries}}"
          timeout: "{{.timeout}}"
          parallelism: "{{.parallelism}}"
        outputs:
          jobId: "result.job_id"
          logPath: "result.log_path"
      stop:
        tool: "x_k8s_cancel_job"
        args:
          job_id: "{{.jobId}}"
      status:
        tool: "x_k8s_get_job_status"
        args:
          job_id: "{{.jobId}}"
        expect:
          jsonPath:
            phase: "Succeeded"
    # Custom status monitoring for batch jobs
    statusMonitoring:
      enabled: true
      interval: "60s"
      progressTracking: true
      tool: "x_k8s_get_job_progress"
      args:
        job_id: "{{.jobId}}"
    # No traditional health checks for batch jobs
    healthCheck:
      enabled: false
    timeout:
      create: "{{.timeout}}"
      delete: "5m"
    outputs:
      job_id: "{{.jobId}}"
      log_path: "{{.logPath}}"
      status: "completed"
```

## Multi-Region Service Pattern

### Goal
Deploy services across multiple regions with failover capabilities.

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: multi-region-service
  namespace: default
spec:
  description: "Service deployed across multiple regions with failover"
  args:
    service_name:
      type: string
      required: true
      description: "Name of the service"
    image:
      type: string
      required: true
      description: "Container image"
    primary_region:
      type: string
      required: true
      description: "Primary deployment region"
    backup_regions:
      type: array
      default: []
      description: "Backup regions for failover"
    traffic_split:
      type: object
      default: {"primary": 100}
      description: "Traffic distribution across regions"
  serviceConfig:
    defaultName: "{{.service_name}}-multi-region"
    # Deploy to multiple regions
    multiRegionConfig:
      primary:
        region: "{{.primary_region}}"
        traffic_percentage: "{{.traffic_split.primary}}"
      backups:
        - region: "{{.backup_regions}}"
          traffic_percentage: "{{.traffic_split.backup}}"
          failover_enabled: true
    lifecycleTools:
      start:
        tool: "x_multi_region_deploy"
        args:
          name: "{{.service_name}}"
          image: "{{.image}}"
          primary_region: "{{.primary_region}}"
          backup_regions: "{{.backup_regions}}"
          traffic_split: "{{.traffic_split}}"
        outputs:
          globalEndpoint: "result.global_endpoint"
          regionEndpoints: "result.region_endpoints"
          loadBalancerIp: "result.load_balancer_ip"
      stop:
        tool: "x_multi_region_destroy"
        args:
          name: "{{.service_name}}"
          regions: "{{.all_regions}}"
      status:
        tool: "x_multi_region_status"
        args:
          name: "{{.service_name}}"
          primary_region: "{{.primary_region}}"
        expect:
          jsonPath:
            primary_status: "healthy"
            failover_ready: true
    # Global health monitoring
    healthCheck:
      enabled: true
      interval: "30s"
      failureThreshold: 2
      successThreshold: 1
      tool: "x_global_health_check"
      args:
        endpoint: "{{.globalEndpoint}}"
        regions: "{{.all_regions}}"
    # Failover configuration
    failoverConfig:
      enabled: true
      health_check_threshold: 3
      failover_time: "60s"
      automatic_failback: true
    timeout:
      create: "20m"  # Multi-region deployment takes longer
      delete: "15m"
      healthCheck: "60s"
    outputs:
      global_endpoint: "{{.globalEndpoint}}"
      region_endpoints: "{{.regionEndpoints}}"
      load_balancer_ip: "{{.loadBalancerIp}}"
```

## Best Practices

### 1. Argument Design
- Use clear, descriptive argument names
- Provide sensible defaults for optional arguments
- Include detailed descriptions for all arguments
- Use appropriate types and validation

### 2. Template Usage
- Leverage Go templates for dynamic configuration
- Use conditional logic for environment-specific behavior
- Reference dependency outputs properly
- Keep templates readable and maintainable

### 3. Lifecycle Management
- Design idempotent start/stop operations
- Implement proper cleanup in stop tools
- Use appropriate timeouts for each operation
- Include meaningful status checks

### 4. Health Monitoring
- Enable health checks for long-running services
- Set appropriate intervals and thresholds
- Use meaningful health check endpoints
- Consider environment-specific monitoring needs

### 5. Error Handling
- Implement graceful degradation
- Provide clear error messages
- Use appropriate retry strategies
- Consider rollback scenarios

### 6. Resource Management
- Set environment-appropriate resource limits
- Use resource quotas and limits
- Monitor resource utilization
- Implement autoscaling where appropriate

## Testing ServiceClass Patterns

### 1. Validation Testing
```bash
# Create and test ServiceClass
muster create serviceclass web-application.yaml

# Test with different argument combinations
muster create service test-web web-application --image nginx:latest --environment test
```

### 2. Dependency Testing
```bash
# Check serviceclass availability
muster check serviceclass microservice-with-deps

# Get serviceclass details
muster get serviceclass microservice-with-deps
```

### 3. Environment Testing
```bash
# Test across environments
for env in development staging production; do
  muster create service "test-$env" environment-aware-app --app_name test --image nginx:latest --environment "$env"
done
```

## Related Documentation
- [Workflow Creation](workflow-creation.md)
- [ServiceClass Reference](../reference/mcp-tools.md#serviceclass-tools) 