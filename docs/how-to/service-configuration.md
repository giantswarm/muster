# Service Configuration

Complete guide for configuring and managing service lifecycle in Muster.

## Overview

Services in Muster are runtime instances created from ServiceClass templates. This guide covers:
- Service creation and configuration
- Lifecycle management (start, stop, restart)
- Health monitoring and status checking
- Parameter customization and environment-specific settings

## Basic Service Configuration

### Creating a Service

```bash
# Create service from ServiceClass
muster create service my-app web-application

# Create with custom parameters
muster create service database postgres-db \
  --param db_name=production_db \
  --param max_connections=100
```

### Service Definition Structure

Services are created from ServiceClass templates with instance-specific parameters:

```yaml
# Service instance configuration
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceInstance
metadata:
  name: my-web-app
  namespace: default
spec:
  serviceClassName: web-application
  parameters:
    image: nginx:1.21
    port: "8080"
    replicas: "3"
    environment: production
  persistence:
    enabled: true
    path: /data/my-web-app
```

## Service Lifecycle Management

### Starting Services

```bash
# Start a service
muster start service my-app

# Start with runtime parameters
muster start service my-app \
  --env DATABASE_URL=postgres://... \
  --env LOG_LEVEL=debug

# Start multiple services
muster start service web-app database cache
```

### Stopping Services

```bash
# Stop a service gracefully
muster stop service my-app

# Force stop (immediate termination)
muster stop service my-app --force

# Stop all services matching pattern
muster stop service --pattern "web-*"
```

### Restarting Services

```bash
# Restart service (stop + start)
muster restart service my-app

# Restart with new parameters
muster restart service my-app \
  --param image=nginx:1.22 \
  --param replicas=5
```

## Service Status and Health

### Checking Service Status

```bash
# Get detailed service information
muster get service my-app

# Check service health
muster check service my-app

# List all services with status
muster list service --output table
```

### Service Status States

Services can be in one of several states:

| State | Description | Actions Available |
|-------|-------------|-------------------|
| `stopped` | Service is not running | start |
| `starting` | Service is initializing | stop (force), status |
| `running` | Service is active and healthy | stop, restart, status |
| `stopping` | Service is shutting down | status |
| `failed` | Service encountered an error | start, restart, logs |
| `unknown` | Service state cannot be determined | start, stop (force) |

### Health Check Configuration

Services inherit health check configuration from their ServiceClass:

```yaml
# ServiceClass with health checks
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: web-application
spec:
  healthCheck:
    enabled: true
    httpGet:
      path: /health
      port: 8080
    initialDelaySeconds: 30
    periodSeconds: 10
    timeoutSeconds: 5
    failureThreshold: 3
```

## Parameter Configuration

### Static Parameters

Set during service creation and persist across restarts:

```bash
# Create service with static parameters
muster create service api-server web-application \
  --param image=myapp:v1.2.0 \
  --param replicas=3 \
  --param memory_limit=512Mi
```

### Runtime Parameters

Provided during service start, override static parameters:

```bash
# Start with runtime environment variables
muster start service api-server \
  --env DEBUG=true \
  --env LOG_LEVEL=debug \
  --env FEATURE_FLAGS=experimental
```

### Parameter Templates

Use templating for dynamic parameter values:

```yaml
# ServiceClass with templated parameters
spec:
  parameters:
    app_name:
      type: string
      required: true
    image:
      type: string
      default: "{{.app_name}}:latest"
    service_url:
      type: string
      default: "https://{{.app_name}}.example.com"
```

## Environment-Specific Configuration

### Development Environment

```bash
# Development service configuration
muster create service my-app-dev web-application \
  --param environment=development \
  --param debug=true \
  --param replicas=1 \
  --param resources=minimal

# Start with development overrides
muster start service my-app-dev \
  --env LOG_LEVEL=debug \
  --env HOT_RELOAD=true
```

### Production Environment

```bash
# Production service configuration
muster create service my-app-prod web-application \
  --param environment=production \
  --param replicas=5 \
  --param resources=standard \
  --param monitoring=enabled

# Start with production settings
muster start service my-app-prod \
  --env LOG_LEVEL=warn \
  --env METRICS_ENABLED=true \
  --env HEALTH_CHECK_INTERVAL=30s
```

## Persistence Configuration

### Enabling Persistence

```bash
# Create service with persistence
muster create service database postgres \
  --persistence \
  --persist-path /data/postgres

# Create with custom persistence settings
muster create service app web-application \
  --persistence \
  --persist-path /app/data \
  --persist-mode shared
```

### Persistence Modes

| Mode | Description | Use Case |
|------|-------------|----------|
| `exclusive` | Data is isolated per service instance | Databases, unique state |
| `shared` | Data is shared across service restarts | Configuration, caches |
| `readonly` | Service can only read persistent data | Static assets, templates |

### Managing Persistent Data

```bash
# View persistent data location
muster get service database --output yaml | grep persistencePath

# Backup persistent data
cp -r ~/.config/muster/persistence/database /backup/

# Restore persistent data
cp -r /backup/database ~/.config/muster/persistence/
```

## Service Dependencies

### Defining Dependencies

Services can depend on other services or external resources:

```yaml
# ServiceClass with dependencies
spec:
  dependencies:
    - name: database
      type: service
      required: true
    - name: cache
      type: service
      required: false
    - name: external-api
      type: external
      healthCheck:
        url: https://api.example.com/health
```

### Dependency Resolution

```bash
# Muster automatically resolves dependencies
muster start service web-app
# This will start database and cache first, then web-app

# Check dependency status
muster get service web-app --show-dependencies

# Start with dependency override
muster start service web-app --skip-dependencies
```

## Advanced Configuration

### Custom Resource Limits

```bash
# Create service with resource constraints
muster create service heavy-app worker-class \
  --param cpu_limit=2 \
  --param memory_limit=4Gi \
  --param disk_limit=10Gi
```

### Network Configuration

```bash
# Service with custom networking
muster create service api gateway-class \
  --param port=8080 \
  --param host=0.0.0.0 \
  --param protocol=https \
  --param cert_path=/certs/api.crt
```

### Security Configuration

```bash
# Service with security settings
muster create service secure-app web-application \
  --param security_enabled=true \
  --param auth_provider=oauth2 \
  --param tls_enabled=true
```

## Configuration Validation

### Validation During Creation

```bash
# Validate service configuration before creation
muster create service my-app web-application --dry-run

# Validate with specific parameters
muster create service my-app web-application \
  --param invalid_param=value \
  --validate-only
```

### Runtime Validation

```bash
# Check if service configuration is valid
muster check service my-app

# Validate all services
muster check service --all
```

## Configuration Updates

### Updating Service Parameters

```bash
# Update service with new parameters
muster update service my-app \
  --param image=myapp:v1.3.0 \
  --param replicas=4

# Update requires restart to take effect
muster restart service my-app
```

### Rolling Updates

```bash
# Perform rolling update (if supported by ServiceClass)
muster update service my-app \
  --param image=myapp:v1.3.0 \
  --strategy rolling

# Monitor update progress
muster get service my-app --watch
```

## Troubleshooting Service Configuration

### Common Configuration Issues

#### Service Won't Start

```bash
# Check service status and logs
muster get service my-app
muster logs service my-app

# Validate ServiceClass exists
muster check serviceclass web-application

# Check for parameter validation errors
muster create service my-app web-application --dry-run
```

#### Parameter Validation Errors

```bash
# List required parameters for ServiceClass
muster get serviceclass web-application --show-parameters

# Validate parameter types and values
muster validate service my-app web-application \
  --param port=invalid_port_number
```

#### Health Check Failures

```bash
# Check health check configuration
muster get service my-app --show-health

# Test health check manually
curl http://localhost:8080/health

# Disable health checks temporarily
muster update service my-app --disable-health-checks
```

### Debugging Service Issues

```bash
# Enable debug logging for service
muster restart service my-app --env LOG_LEVEL=debug

# Check service resource usage
muster get service my-app --show-resources

# View service events
muster get service my-app --show-events
```

## Configuration Best Practices

### Service Naming

```bash
# Use descriptive, hierarchical names
muster create service frontend-web-prod web-application
muster create service backend-api-staging api-service
muster create service worker-queue-dev worker-class
```

### Parameter Management

- **Use defaults**: Define sensible defaults in ServiceClass
- **Environment-specific**: Use different parameter sets per environment
- **Validation**: Always validate parameters before deployment
- **Documentation**: Document required and optional parameters

### Resource Planning

- **Right-size resources**: Start with minimal resources, scale up as needed
- **Monitor usage**: Use resource monitoring to optimize allocations
- **Set limits**: Always set resource limits to prevent resource exhaustion

### Security Considerations

- **Least privilege**: Grant minimal required permissions
- **Secrets management**: Use secure secret storage, never hardcode secrets
- **Network isolation**: Isolate services that don't need to communicate
- **Regular updates**: Keep service images and dependencies up to date

## Integration with Workflows

Services can be managed through workflows for complex orchestration:

```yaml
# Workflow that manages service lifecycle
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: deploy-application
spec:
  steps:
    - id: create-database
      tool: core_service_create
      args:
        name: "{{.app_name}}-db"
        serviceClassName: postgres
        
    - id: wait-for-database
      tool: core_service_wait_healthy
      args:
        name: "{{.app_name}}-db"
        timeout: 300s
        
    - id: create-application
      tool: core_service_create
      args:
        name: "{{.app_name}}"
        serviceClassName: web-application
        parameters:
          database_url: "postgres://{{.app_name}}-db:5432/app"
```

## Next Steps

- [Learn about ServiceClass patterns](serviceclass-patterns.md)
- [Create custom workflows](workflow-creation.md)
- [Troubleshoot service issues](troubleshooting.md)

## Related Documentation

- [ServiceClass Templates](serviceclass-patterns.md)
- [Workflow Integration](workflow-creation.md)
- [CLI Reference](../reference/cli/) 