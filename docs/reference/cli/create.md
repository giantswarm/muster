# muster create

Create resources in the Muster environment.

## Synopsis

```
muster create [RESOURCE_TYPE] [NAME] [OPTIONS]
```

## Description

The `create` command creates new resources in Muster. It supports creating service classes, workflows, and service instances with flexible argument passing and configuration options.

**Prerequisites**: The aggregator server must be running (`muster serve`) before using this command.

## Resource Types

| Resource Type | Description | Example |
|---------------|-------------|---------|
| `serviceclass` | Service template definition | `muster create serviceclass web-app` |
| `workflow` | Workflow definition | `muster create workflow deploy-flow` |
| `service` | Service instance from a ServiceClass | `muster create service my-app web-app` |

## Options

### Output Control
- `--output`, `-o` (string): Output format (table\|json\|yaml)
  - Default: `table`
- `--quiet`, `-q`: Suppress non-essential output
  - Default: `false`

### Configuration
- `--config-path` (string): Custom configuration directory path
  - Default: `~/.config/muster`

### Service-Specific Parameters
When creating services, additional parameters can be passed as arguments:
- `--param=value`: Set service parameters
- Any unknown flags are treated as service parameters

## Examples

### Creating ServiceClasses
```bash
# Create a basic service class
muster create serviceclass web-app

# With structured output
muster create serviceclass web-app --output yaml
```

### Creating Workflows
```bash
# Create a basic workflow
muster create workflow deploy-app

# Create workflow with JSON output
muster create workflow backup-db --output json
```

### Creating Services
```bash
# Create service from service class
muster create service my-app web-service

# Create service with parameters
muster create service my-portal mimir-port-forward \
  --managementCluster=gazelle \
  --localPort=18009

# Complex service with multiple parameters
muster create service monitoring-stack prometheus-stack \
  --namespace=monitoring \
  --retention=30d \
  --replicas=3 \
  --storage=100Gi
```

## Service Creation Patterns

### Basic Service Creation
```bash
# Pattern: muster create service [service-name] [serviceclass-name]
muster create service my-web-app web-application

# The service inherits configuration from the serviceclass
muster get service my-web-app
```

### Parameterized Service Creation
```bash
# Pass parameters to customize the service
muster create service custom-app web-application \
  --image=nginx:1.21 \
  --replicas=5 \
  --environment=production
```

### Port-Forward Services
```bash
# Create port-forward service for development
muster create service local-prometheus prometheus-port-forward \
  --cluster=management \
  --namespace=monitoring \
  --localPort=9090 \
  --remotePort=9090
```

## Output Formats

### Table Format (Default)
```bash
muster create service my-app web-service
# NAME     TYPE           STATUS    SERVICECLASS
# my-app   service        Created   web-service
```

### JSON Format
```bash
muster create service my-app web-service --output json
# {
#   "name": "my-app",
#   "type": "service", 
#   "status": "Created",
#   "serviceClass": "web-service",
#   "created": "2024-01-07T10:00:00Z"
# }
```

### YAML Format
```bash
muster create serviceclass web-app --output yaml
# apiVersion: muster.giantswarm.io/v1alpha1
# kind: ServiceClass
# metadata:
#   name: web-app
#   created: "2024-01-07T10:00:00Z"
# spec:
#   description: "Web application service class"
```

## Resource Creation Workflow

### 1. ServiceClass Creation
ServiceClasses define reusable service templates:

```bash
# Create the template
muster create serviceclass database-service

# Verify creation
muster get serviceclass database-service

# List all service classes
muster list serviceclass
```

### 2. Service Instance Creation
Create concrete service instances from ServiceClasses:

```bash
# Create instance from template
muster create service prod-db database-service \
  --size=large \
  --backup=enabled

# Check service status
muster get service prod-db
```

### 3. Workflow Creation
Create workflow definitions for automation:

```bash
# Create deployment workflow
muster create workflow app-deployment

# Verify workflow
muster get workflow app-deployment

# Execute the workflow
muster start workflow app-deployment --app=my-app
```

## Parameter Passing

### Service Parameters
When creating services, parameters are passed to the underlying ServiceClass:

```bash
# All unknown flags become service parameters
muster create service my-service web-app \
  --image=myapp:v1.0 \
  --replicas=3 \
  --memory=512Mi \
  --custom-config=value

# These parameters are available to the ServiceClass template
```

### Parameter Validation
Parameters are validated against the ServiceClass definition:

```bash
# If ServiceClass requires 'image' parameter
muster create service test-app web-app
# Error: required parameter 'image' not provided

# Correct usage
muster create service test-app web-app --image=nginx:latest
```

## Error Handling

### Resource Already Exists
```bash
muster create service my-app web-service
# Error: service 'my-app' already exists

# Solution: Use different name or delete existing
muster get service my-app  # Check if it exists
muster delete service my-app  # Delete if needed
```

### ServiceClass Not Found
```bash
muster create service my-app non-existent
# Error: serviceclass 'non-existent' not found

# Solution: List available service classes
muster list serviceclass
muster create service my-app existing-serviceclass
```

### Missing Required Parameters
```bash
muster create service my-app complex-service
# Error: required parameter 'cluster' not provided

# Solution: Provide required parameters
muster create service my-app complex-service --cluster=production
```

### Configuration Issues
```bash
muster create serviceclass my-class
# Error: failed to connect to aggregator

# Solution: Ensure server is running
muster serve  # In another terminal
```

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Resource created successfully |
| 1 | General error or invalid arguments |
| 2 | Resource already exists |
| 3 | Required parameter missing |
| 4 | Connection error (server not running) |

## Auto-Completion

The create command supports tab completion for:

```bash
# Resource types
muster create [TAB]
# Suggestions: service, serviceclass, workflow

# ServiceClass names (when creating services)
muster create service my-app [TAB]
# Suggestions: web-app, database, monitoring, ...
```

## Integration with Other Commands

### Typical Workflow
```bash
# 1. Create service class template
muster create serviceclass web-app

# 2. Create service instance
muster create service my-app web-app --image=nginx

# 3. Start the service
muster start service my-app

# 4. Check status
muster get service my-app

# 5. Create workflow for automation
muster create workflow deploy-my-app
```

## Related Commands

- **[get](get.md)** - Retrieve created resource details
- **[list](list.md)** - List all resources of a type
- **[start](start.md)** - Start created services/workflows
- **[check](check.md)** - Check resource availability
- **[delete](#)** - Delete created resources

## Advanced Usage

### Batch Creation
```bash
# Create multiple services with different parameters
for env in dev staging prod; do
  muster create service "app-$env" web-service \
    --environment="$env" \
    --replicas=$([ "$env" = "prod" ] && echo 5 || echo 2)
done
```

### Template-Based Creation
```bash
# Create service with complex configuration
muster create service complex-app enterprise-service \
  --database-url="postgres://localhost:5432/myapp" \
  --redis-url="redis://localhost:6379" \
  --feature-flags="feature1,feature2,feature3" \
  --monitoring=enabled \
  --backup-schedule="0 2 * * *"
```

### Development Patterns
```bash
# Quick development setup
muster create service dev-env development-stack \
  --debug=true \
  --hot-reload=enabled \
  --local-storage=./data
``` 