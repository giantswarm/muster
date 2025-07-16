# muster stop

Stop running resources in the Muster environment.

## Synopsis

```
muster stop [RESOURCE_TYPE] [NAME] [OPTIONS]
```

## Description

The `stop` command stops running services in Muster. It gracefully shuts down service instances while preserving their configuration for future restarts.

**Prerequisites**: The aggregator server must be running (`muster serve`) before using this command.

## Resource Types

| Resource Type | Description | Example |
|---------------|-------------|---------|
| `service` | Stop a running service by its name | `muster stop service my-app` |

## Options

### Output Control
- `--output`, `-o` (string): Output format (table\|json\|yaml)
  - Default: `table`
- `--quiet`, `-q`: Suppress non-essential output
  - Default: `false`

### Configuration
- `--config-path` (string): Custom configuration directory path
  - Default: `~/.config/muster`

## Examples

### Stopping Services
```bash
# Stop a running service
muster stop service my-web-app

# Example output:
# NAME          STATUS      ACTION
# my-web-app    Stopping    Stopped successfully
```

### Checking Status After Stop
```bash
# Stop service and verify
muster stop service my-app
muster get service my-app

# Status should change from Running to Stopping to Stopped
```

## Service Stop Behavior

### Graceful Shutdown
When stopping a service, Muster performs a graceful shutdown:

1. **Signal Termination**: Sends termination signal to the service process
2. **Grace Period**: Waits for the service to clean up resources
3. **Force Stop**: If needed, forcibly terminates after timeout
4. **State Update**: Updates service status to "Stopped"

```bash
# The stop process is automatic and graceful
muster stop service my-app

# Service will:
# 1. Receive shutdown signal
# 2. Complete current operations
# 3. Clean up resources
# 4. Exit cleanly
```

### Stop vs. Restart
Stopping a service preserves its configuration for future starts:

```bash
# Stop preserves configuration
muster stop service my-app
muster get service my-app  # Configuration still available

# Start uses the same configuration
muster start service my-app
```

## Service Lifecycle States

Services transition through these states during stop:

```bash
# Before stopping
muster get service my-app
# Status: Running

# During stop
muster stop service my-app
# Status: Stopping

# After stop
muster get service my-app
# Status: Stopped
```

**Service States:**
- `Running`: Service is active and processing
- `Stopping`: Service is in shutdown process
- `Stopped`: Service is completely stopped
- `Error`: Service encountered an error during shutdown

## Output Formats

### Table Format (Default)
```bash
muster stop service my-app
# NAME       TYPE      STATUS      ACTION
# my-app     service   Stopping    Stopped successfully
```

### JSON Format
```bash
muster stop service my-app --output json
# {
#   "name": "my-app",
#   "type": "service",
#   "status": "Stopping",
#   "action": "Stopped successfully",
#   "timestamp": "2024-01-07T10:00:00Z",
#   "previousStatus": "Running"
# }
```

### YAML Format
```bash
muster stop service my-app --output yaml
# name: my-app
# type: service
# status: Stopping
# action: "Stopped successfully"
# timestamp: "2024-01-07T10:00:00Z"
# previousStatus: Running
```

## Common Use Cases

### Maintenance Operations
```bash
# Stop service for maintenance
echo "Stopping service for maintenance..."
muster stop service my-app

# Perform maintenance tasks
echo "Performing maintenance..."
# ... maintenance operations ...

# Restart service
echo "Restarting service..."
muster start service my-app
echo "Maintenance complete"
```

### Resource Management
```bash
# Stop non-essential services to free resources
echo "Stopping development services..."
muster stop service dev-database
muster stop service test-cache
muster stop service debug-tools

echo "Resources freed for production workload"
```

### Deployment Scenarios
```bash
# Blue-green deployment pattern
echo "Stopping old version..."
muster stop service app-v1

echo "Starting new version..."
muster start service app-v2

echo "Deployment complete"
```

### Emergency Shutdown
```bash
# Emergency stop of problematic service
echo "Emergency shutdown of problematic service"
muster stop service problematic-app

# Check logs or status
muster get service problematic-app
```

## Batch Operations

### Stop Multiple Services
```bash
# Stop multiple related services
SERVICES=("frontend" "backend" "cache" "worker")

echo "Stopping application stack..."
for service in "${SERVICES[@]}"; do
  echo "Stopping $service..."
  muster stop service "$service"
done

echo "All services stopped"
```

### Stop by Pattern
```bash
# Stop all services matching a pattern (requires scripting)
DEV_SERVICES=$(muster list service --output json | jq -r '.services[] | select(.name | contains("dev-")) | .name')

for service in $DEV_SERVICES; do
  echo "Stopping development service: $service"
  muster stop service "$service"
done
```

### Conditional Stopping
```bash
# Stop service only if running
SERVICE_STATUS=$(muster get service my-app --output json | jq -r '.status')

if [ "$SERVICE_STATUS" = "Running" ]; then
  echo "Stopping running service..."
  muster stop service my-app
else
  echo "Service is not running (status: $SERVICE_STATUS)"
fi
```

## Error Handling

### Service Not Found
```bash
muster stop service non-existent
# Error: service 'non-existent' not found

# Solution: Check available services
muster list service
```

### Service Not Running
```bash
muster stop service my-app
# Error: service 'my-app' is not running

# Solution: Check current status
muster get service my-app
# Status might be: Stopped, Starting, Error
```

### Stop Timeout
```bash
muster stop service slow-service
# Warning: service took longer than expected to stop
# Status: Stopped (force terminated)

# This indicates the service didn't respond to graceful shutdown
# Check service logs for issues
```

### Permission Issues
```bash
muster stop service system-service
# Error: insufficient permissions to stop service

# Solution: Check if service requires special permissions
muster get service system-service
```

## Troubleshooting

### Service Won't Stop
```bash
# Check service status
muster get service stuck-service

# If service is stuck in "Stopping" state:
# 1. Wait a bit longer (some services need more time)
# 2. Check if service has active connections
# 3. Review service logs for shutdown issues
# 4. Consider force restart if necessary
```

### Stop Verification
```bash
# Verify service actually stopped
muster stop service my-app

# Wait a moment for shutdown to complete
sleep 5

# Check final status
FINAL_STATUS=$(muster get service my-app --output json | jq -r '.status')
if [ "$FINAL_STATUS" = "Stopped" ]; then
  echo "Service stopped successfully"
else
  echo "Service stop may have failed: $FINAL_STATUS"
fi
```

### Dependencies Handling
```bash
# When stopping services with dependencies,
# stop dependent services first

echo "Stopping dependent services first..."
muster stop service frontend-app    # Depends on backend
muster stop service backend-api     # Depends on database
muster stop service database        # Core dependency

echo "All services stopped in correct order"
```

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Service stopped successfully |
| 1 | General error or invalid arguments |
| 2 | Service not found |
| 3 | Service not running |
| 4 | Stop timeout or force termination required |
| 5 | Permission denied |
| 6 | Connection error (server not running) |

## Auto-Completion

The stop command supports tab completion:

```bash
# Resource types
muster stop [TAB]
# Suggestions: service

# Running service names
muster stop service [TAB]
# Suggestions: running-app, active-service, ...
# (Only shows services that are currently running)
```

## Integration Patterns

### Graceful Application Shutdown
```bash
#!/bin/bash
# Graceful shutdown script for entire application

APP_SERVICES=("load-balancer" "web-frontend" "api-backend" "worker-queue" "database")

echo "Initiating graceful application shutdown..."

# Stop services in reverse dependency order
for service in "${APP_SERVICES[@]}"; do
  STATUS=$(muster get service "$service" --output json | jq -r '.status')
  
  if [ "$STATUS" = "Running" ]; then
    echo "Stopping $service..."
    muster stop service "$service"
    
    # Wait for service to stop
    while [ "$(muster get service "$service" --output json | jq -r '.status')" = "Stopping" ]; do
      echo "Waiting for $service to stop..."
      sleep 2
    done
    
    echo "$service stopped successfully"
  else
    echo "$service is not running (status: $STATUS)"
  fi
done

echo "Application shutdown complete"
```

### Rolling Restart
```bash
#!/bin/bash
# Rolling restart of service instances

SERVICE_NAME="$1"
if [ -z "$SERVICE_NAME" ]; then
  echo "Usage: $0 <service-name>"
  exit 1
fi

echo "Performing rolling restart of $SERVICE_NAME..."

# Stop the service
echo "Stopping $SERVICE_NAME..."
muster stop service "$SERVICE_NAME"

# Wait for complete stop
while [ "$(muster get service "$SERVICE_NAME" --output json | jq -r '.status')" != "Stopped" ]; do
  echo "Waiting for service to stop completely..."
  sleep 3
done

# Start the service again
echo "Starting $SERVICE_NAME..."
muster start service "$SERVICE_NAME"

# Wait for service to be healthy
echo "Waiting for service to become healthy..."
while [ "$(muster get service "$SERVICE_NAME" --output json | jq -r '.health.status')" != "Healthy" ]; do
  sleep 5
done

echo "Rolling restart completed successfully"
```

### Resource Cleanup
```bash
#!/bin/bash
# Stop and cleanup development environment

echo "Cleaning up development environment..."

# Get all development services
DEV_SERVICES=$(muster list service --output json | \
  jq -r '.services[] | select(.name | startswith("dev-") or contains("-dev")) | .name')

if [ -z "$DEV_SERVICES" ]; then
  echo "No development services found"
  exit 0
fi

echo "Found development services: $DEV_SERVICES"

# Stop each development service
for service in $DEV_SERVICES; do
  echo "Stopping development service: $service"
  muster stop service "$service"
done

echo "Development environment cleanup complete"
```

## Related Commands

- **[start](start.md)** - Start stopped services
- **[get](get.md)** - Check service status before/after stopping
- **[list](list.md)** - List services to see which are running
- **[create](create.md)** - Create services before starting them

## Advanced Usage

### Stop with Status Monitoring
```bash
# Stop service with real-time status monitoring
SERVICE_NAME="my-app"

echo "Stopping $SERVICE_NAME with monitoring..."
muster stop service "$SERVICE_NAME" &
STOP_PID=$!

# Monitor status while stopping
while kill -0 $STOP_PID 2>/dev/null; do
  STATUS=$(muster get service "$SERVICE_NAME" --output json | jq -r '.status')
  echo "Current status: $STATUS"
  sleep 1
done

wait $STOP_PID
echo "Stop operation completed"
```

### Conditional Stop with Dependency Check
```bash
# Stop service only if no other services depend on it
SERVICE_TO_STOP="database"

# Check if any services are still running that might depend on this service
RUNNING_SERVICES=$(muster list service --output json | \
  jq -r '.services[] | select(.status=="Running") | .name')

echo "Checking dependencies before stopping $SERVICE_TO_STOP..."

SAFE_TO_STOP=true
for service in $RUNNING_SERVICES; do
  if [ "$service" != "$SERVICE_TO_STOP" ]; then
    # In a real scenario, you'd check actual dependencies
    echo "Warning: $service is still running"
    SAFE_TO_STOP=false
  fi
done

if [ "$SAFE_TO_STOP" = true ]; then
  echo "Safe to stop $SERVICE_TO_STOP"
  muster stop service "$SERVICE_TO_STOP"
else
  echo "Cannot stop $SERVICE_TO_STOP - other services may depend on it"
fi
``` 