# muster create

Create resources in the Muster environment.

## Synopsis

```
muster create [RESOURCE_TYPE] [NAME] [OPTIONS]
```

## Description

The `create` command creates new resources in Muster. It supports creating workflows and service instances with flexible argument passing and configuration options.

**Prerequisites**: The aggregator server must be running (`muster serve`) before using this command.

## Resource Types

| Resource Type | Description | Example |
|---------------|-------------|---------|
| `workflow` | Workflow definition | `muster create workflow deploy-flow` |
| `service` | Service instance | `muster create service my-app` |

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

### Creating Workflows
```bash
# Create a basic workflow
muster create workflow deploy-app

# Create workflow with JSON output
muster create workflow backup-db --output json
```

### Creating Services
```bash
# Create service with parameters
muster create service my-portal \
  --managementCluster=gazelle \
  --localPort=18009

# Complex service with multiple parameters
muster create service monitoring-stack \
  --namespace=monitoring \
  --retention=30d \
  --replicas=3 \
  --storage=100Gi
```

## Output Formats

### Table Format (Default)
```bash
muster create service my-app
# NAME     TYPE      STATUS
# my-app   service   Created
```

### JSON Format
```bash
muster create service my-app --output json
# {
#   "name": "my-app",
#   "type": "service",
#   "status": "Created",
#   "created": "2024-01-07T10:00:00Z"
# }
```

### YAML Format
```bash
muster create workflow deploy-app --output yaml
# apiVersion: muster.giantswarm.io/v1alpha1
# kind: Workflow
# metadata:
#   name: deploy-app
#   created: "2024-01-07T10:00:00Z"
# spec:
#   description: "Application deployment workflow"
```

## Resource Creation Workflow

### Workflow Creation
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
When creating services, parameters are passed as arguments:

```bash
# All unknown flags become service parameters
muster create service my-service \
  --image=myapp:v1.0 \
  --replicas=3 \
  --memory=512Mi \
  --custom-config=value
```

## Error Handling

### Resource Already Exists
```bash
muster create service my-app
# Error: service 'my-app' already exists

# Solution: Use different name or delete existing
muster get service my-app  # Check if it exists
muster delete service my-app  # Delete if needed
```

### Missing Required Parameters
```bash
muster create service my-app
# Error: required parameter 'cluster' not provided

# Solution: Provide required parameters
muster create service my-app --cluster=production
```

### Configuration Issues
```bash
muster create workflow my-workflow
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
# Suggestions: service, workflow
```

## Integration with Other Commands

### Typical Workflow
```bash
# 1. Create service instance
muster create service my-app --image=nginx

# 2. Start the service
muster start service my-app

# 3. Check status
muster get service my-app

# 4. Create workflow for automation
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
  muster create service "app-$env" \
    --environment="$env" \
    --replicas=$([ "$env" = "prod" ] && echo 5 || echo 2)
done
```

### Template-Based Creation
```bash
# Create service with complex configuration
muster create service complex-app \
  --database-url="postgres://localhost:5432/myapp" \
  --redis-url="redis://localhost:6379" \
  --feature-flags="feature1,feature2,feature3" \
  --monitoring=enabled \
  --backup-schedule="0 2 * * *"
```

### Development Patterns
```bash
# Quick development setup
muster create service dev-env \
  --debug=true \
  --hot-reload=enabled \
  --local-storage=./data
```
