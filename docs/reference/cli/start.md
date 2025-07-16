# muster start

Start resources or execute workflows in the Muster environment.

## Synopsis

```
muster start [RESOURCE_TYPE] [NAME] [OPTIONS]
```

## Description

The `start` command starts services or executes workflows in Muster. For services, it activates the service instance. For workflows, it executes the workflow with optional parameters.

**Prerequisites**: The aggregator server must be running (`muster serve`) before using this command.

## Resource Types

| Resource Type | Description | Example |
|---------------|-------------|---------|
| `service` | Start a service by its name | `muster start service my-app` |
| `workflow` | Execute a workflow with optional parameters | `muster start workflow deploy-app` |

## Options

### Output Control
- `--output`, `-o` (string): Output format (table\|json\|yaml)
  - Default: `table`
- `--quiet`, `-q`: Suppress non-essential output
  - Default: `false`

### Configuration
- `--config-path` (string): Custom configuration directory path
  - Default: `~/.config/muster`

### Workflow Parameters
When starting workflows, additional parameters can be passed:
- `--param=value`: Set workflow parameters
- Any unknown flags are treated as workflow parameters

## Examples

### Starting Services
```bash
# Start a service
muster start service my-web-app

# Example output:
# NAME          STATUS      ACTION
# my-web-app    Starting    Started successfully
```

### Executing Workflows
```bash
# Execute a basic workflow
muster start workflow deploy-app

# Execute workflow with parameters
muster start workflow deploy-app \
  --environment=production \
  --replicas=3 \
  --image=myapp:v1.2.3

# Execute workflow with complex parameters
muster start workflow backup-database \
  --database=production-db \
  --retention=30d \
  --compression=true \
  --notification-email=admin@company.com
```

## Service Management

### Service Lifecycle
```bash
# Check service status first
muster get service my-app

# Start the service
muster start service my-app

# Verify it started
muster get service my-app
# Status should change from Stopped to Starting to Running
```

### Service Dependencies
When starting a service, Muster automatically:
1. Checks if the ServiceClass is available
2. Verifies all required MCP tools are accessible
3. Validates service configuration
4. Starts the service process

```bash
# If dependencies are missing:
muster start service my-app
# Error: ServiceClass 'web-app' requires tool 'x_kubernetes_apply' which is not available

# Solution: Check MCP server status
muster list mcpserver
muster get mcpserver kubernetes
```

## Workflow Execution

### Basic Workflow Execution
```bash
# List available workflows
muster list workflow

# Get workflow details to see required parameters
muster get workflow deploy-app

# Execute with required parameters
muster start workflow deploy-app \
  --app-name=my-application \
  --environment=staging
```

### Parameter Passing
Workflow parameters can be passed in multiple ways:

```bash
# Flag-style parameters (recommended)
muster start workflow deploy-app \
  --app-name=my-app \
  --replicas=3 \
  --environment=production

# Mixed parameter styles
muster start workflow complex-deployment \
  --cluster=production \
  --namespace=apps \
  --debug=true \
  --features=feature1,feature2,feature3
```

### Parameter Validation
Parameters are validated against the workflow definition:

```bash
# Missing required parameter
muster start workflow deploy-app
# Error: required parameter 'app-name' not provided

# Invalid parameter value
muster start workflow deploy-app --replicas=invalid
# Error: parameter 'replicas' must be a number

# Correct usage
muster start workflow deploy-app --app-name=test --replicas=2
```

## Output Formats

### Table Format (Default)
```bash
muster start service my-app
# NAME       TYPE      STATUS      ACTION
# my-app     service   Starting    Started successfully

muster start workflow deploy-app --app=test
# NAME         TYPE      STATUS     EXECUTION_ID
# deploy-app   workflow  Running    abc123-def456-789
```

### JSON Format
```bash
muster start service my-app --output json
# {
#   "name": "my-app",
#   "type": "service",
#   "status": "Starting",
#   "action": "Started successfully",
#   "timestamp": "2024-01-07T10:00:00Z"
# }

muster start workflow deploy-app --app=test --output json
# {
#   "name": "deploy-app",
#   "type": "workflow",
#   "status": "Running",
#   "executionId": "abc123-def456-789",
#   "parameters": {
#     "app": "test"
#   },
#   "started": "2024-01-07T10:00:00Z"
# }
```

### YAML Format
```bash
muster start workflow deploy-app --app=test --output yaml
# name: deploy-app
# type: workflow
# status: Running
# executionId: abc123-def456-789
# parameters:
#   app: test
# started: "2024-01-07T10:00:00Z"
```

## Workflow Execution Tracking

### Monitoring Execution
```bash
# Start workflow and get execution ID
EXEC_ID=$(muster start workflow deploy-app --app=test --output json | jq -r '.executionId')

# Monitor execution progress
muster get workflow-execution "$EXEC_ID"

# List recent executions
muster list workflow-execution
```

### Execution Status
Workflow executions go through these states:
- `Running`: Workflow is currently executing
- `Success`: Workflow completed successfully
- `Failed`: Workflow encountered an error
- `Timeout`: Workflow exceeded maximum execution time

### Long-Running Workflows
```bash
# For long-running workflows, start in background monitoring
muster start workflow long-deployment --app=complex-app &

# Check status periodically
while true; do
  STATUS=$(muster list workflow-execution --output json | jq -r '.executions[0].status')
  echo "Current status: $STATUS"
  if [ "$STATUS" != "Running" ]; then
    break
  fi
  sleep 30
done
```

## Common Patterns

### Service Startup Sequence
```bash
# Typical service startup workflow
echo "Starting application stack..."

# 1. Start database service
muster start service production-db
echo "Database starting..."

# 2. Wait for database to be healthy
while [ "$(muster get service production-db --output json | jq -r '.health.status')" != "Healthy" ]; do
  echo "Waiting for database..."
  sleep 5
done

# 3. Start application services
muster start service backend-api
muster start service frontend-app

echo "All services started successfully"
```

### Environment Deployment
```bash
# Deploy to specific environment
ENV="staging"
APP_VERSION="v1.2.3"

echo "Deploying $APP_VERSION to $ENV environment"

muster start workflow deploy-application \
  --environment="$ENV" \
  --version="$APP_VERSION" \
  --replicas=2 \
  --enable-monitoring=true

echo "Deployment initiated"
```

### Batch Service Management
```bash
# Start multiple related services
SERVICES=("api-gateway" "user-service" "payment-service" "notification-service")

for service in "${SERVICES[@]}"; do
  echo "Starting $service..."
  muster start service "$service"
done

echo "All microservices started"
```

## Error Handling

### Service Start Failures
```bash
muster start service my-app
# Error: service 'my-app' not found

# Solution: Check if service exists
muster list service
muster get service my-app
```

```bash
muster start service my-app
# Error: service 'my-app' is already running

# Solution: Check current status
muster get service my-app
# If you need to restart:
muster stop service my-app
muster start service my-app
```

### Workflow Execution Failures
```bash
muster start workflow deploy-app
# Error: required parameter 'app-name' not provided

# Solution: Check workflow requirements
muster get workflow deploy-app
muster start workflow deploy-app --app-name=my-app
```

```bash
muster start workflow deploy-app --app-name=test
# Error: workflow execution failed: tool 'x_kubernetes_apply' not available

# Solution: Check MCP server status
muster list mcpserver
muster get mcpserver kubernetes
```

### ServiceClass Dependencies
```bash
muster start service my-app
# Error: ServiceClass 'web-app' is not available

# Solution: Check ServiceClass status
muster get serviceclass web-app
muster check serviceclass web-app
```

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Resource started successfully |
| 1 | General error or invalid arguments |
| 2 | Resource not found |
| 3 | Resource already running (for services) |
| 4 | Missing required parameters (for workflows) |
| 5 | Dependency not available |
| 6 | Connection error (server not running) |

## Auto-Completion

The start command supports tab completion:

```bash
# Resource types
muster start [TAB]
# Suggestions: service, workflow

# Service names
muster start service [TAB]
# Suggestions: my-app, database, monitoring, ...

# Workflow names
muster start workflow [TAB]
# Suggestions: deploy-app, backup-db, scale-service, ...
```

## Integration Patterns

### Deployment Automation
```bash
#!/bin/bash
# Automated deployment script

APP_NAME="$1"
VERSION="$2"
ENVIRONMENT="$3"

if [ -z "$APP_NAME" ] || [ -z "$VERSION" ] || [ -z "$ENVIRONMENT" ]; then
  echo "Usage: $0 <app-name> <version> <environment>"
  exit 1
fi

echo "Deploying $APP_NAME:$VERSION to $ENVIRONMENT"

# Execute deployment workflow
EXEC_ID=$(muster start workflow deploy-application \
  --app-name="$APP_NAME" \
  --version="$VERSION" \
  --environment="$ENVIRONMENT" \
  --output json | jq -r '.executionId')

echo "Deployment started with execution ID: $EXEC_ID"

# Monitor deployment
while true; do
  STATUS=$(muster get workflow-execution "$EXEC_ID" --output json | jq -r '.status')
  echo "Deployment status: $STATUS"
  
  if [ "$STATUS" = "Success" ]; then
    echo "Deployment completed successfully!"
    break
  elif [ "$STATUS" = "Failed" ]; then
    echo "Deployment failed!"
    muster get workflow-execution "$EXEC_ID"
    exit 1
  fi
  
  sleep 30
done
```

### Service Health Monitoring
```bash
#!/bin/bash
# Start service and wait for health check

SERVICE_NAME="$1"
MAX_WAIT=300  # 5 minutes

echo "Starting service: $SERVICE_NAME"
muster start service "$SERVICE_NAME"

echo "Waiting for service to become healthy..."
ELAPSED=0
while [ $ELAPSED -lt $MAX_WAIT ]; do
  HEALTH=$(muster get service "$SERVICE_NAME" --output json | jq -r '.health.status')
  
  if [ "$HEALTH" = "Healthy" ]; then
    echo "Service is healthy!"
    break
  elif [ "$HEALTH" = "Error" ]; then
    echo "Service failed to start!"
    muster get service "$SERVICE_NAME"
    exit 1
  fi
  
  sleep 10
  ELAPSED=$((ELAPSED + 10))
done

if [ $ELAPSED -ge $MAX_WAIT ]; then
  echo "Service failed to become healthy within $MAX_WAIT seconds"
  exit 1
fi
```

## Related Commands

- **[stop](stop.md)** - Stop running services
- **[get](get.md)** - Check service/workflow status
- **[list](list.md)** - List available services and workflows
- **[create](create.md)** - Create services and workflows before starting
- **[check](check.md)** - Check resource availability before starting

## Advanced Usage

### Conditional Execution
```bash
# Start service only if not already running
if [ "$(muster get service my-app --output json | jq -r '.status')" != "Running" ]; then
  muster start service my-app
else
  echo "Service already running"
fi
```

### Parallel Service Startup
```bash
# Start multiple services in parallel
muster start service service1 &
muster start service service2 &
muster start service service3 &

# Wait for all to complete
wait

echo "All services startup initiated"
```

### Workflow with Dynamic Parameters
```bash
# Generate parameters dynamically
TIMESTAMP=$(date +%Y%m%d-%H%M%S)
BRANCH=$(git rev-parse --abbrev-ref HEAD)
COMMIT=$(git rev-parse --short HEAD)

muster start workflow deploy-with-metadata \
  --app-name=my-app \
  --build-id="$TIMESTAMP" \
  --git-branch="$BRANCH" \
  --git-commit="$COMMIT" \
  --environment=staging
``` 