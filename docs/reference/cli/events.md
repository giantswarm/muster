# muster events

List and filter events for muster resources in both Kubernetes and filesystem modes.

## Synopsis

```
muster events [OPTIONS]
```

## Description

The `events` command provides access to event history for all muster components including MCPServers, ServiceClasses, Workflows, and Service instances. Events are automatically generated during resource lifecycle operations and can be queried with various filters.

Events provide visibility into:
- Resource creation, updates, and deletions
- Service state transitions (starting, running, stopped, failed)
- Tool availability changes
- Workflow execution progress and results
- Health check status and recovery operations

**Prerequisites**: The aggregator server must be running (`muster serve`) before using this command.

## Filtering Options

### Resource Filtering
- `--resource-type` (string): Filter by resource type
  - Options: `mcpserver`, `serviceclass`, `workflow`, `service`
- `--resource-name` (string): Filter by specific resource name
- `--namespace` (string): Filter by namespace (default: all namespaces)

### Event Filtering
- `--type` (string): Filter by event type
  - Options: `Normal`, `Warning`
- `--since` (string): Show events after this time
  - Formats: `1h`, `30m`, `2024-01-15T10:00:00Z`, `2024-01-15`, `2024-01-15 10:00:00`
- `--until` (string): Show events before this time
  - Same formats as `--since`
- `--limit` (int): Limit number of events returned
  - Default: `50`

### Output Control
- `--output`, `-o` (string): Output format (table|json|yaml)
  - Default: `table`
- `--quiet`, `-q`: Suppress non-essential output
  - Default: `false`

### Configuration
- `--config-path` (string): Custom configuration directory path
  - Default: `~/.config/muster`

## Examples

### Basic Usage
```bash
# List all recent events
muster events

# Example output:
# TIMESTAMP             NAMESPACE  RESOURCE      NAME           TYPE     REASON                 MESSAGE
# 2024-01-15T14:30:15Z  default   MCPServer     github-server   Normal   MCPServerStarted      MCPServer service started successfully
# 2024-01-15T14:30:10Z  default   MCPServer     github-server   Normal   MCPServerStarting     MCPServer service beginning startup
# 2024-01-15T14:29:45Z  default   MCPServer     github-server   Normal   MCPServerCreated      MCPServer successfully created
```

### Resource-Specific Filtering
```bash
# Filter by resource type
muster events --resource-type mcpserver
muster events --resource-type serviceclass
muster events --resource-type workflow
muster events --resource-type service

# Filter by specific resource
muster events --resource-type mcpserver --resource-name prometheus
muster events --resource-type workflow --resource-name deploy-app

# Filter by namespace
muster events --namespace default
muster events --namespace muster-system
```

### Time-Based Filtering
```bash
# Show events from the last hour
muster events --since 1h

# Show events from the last 30 minutes
muster events --since 30m

# Show events between specific times
muster events --since 2024-01-15T10:00:00Z --until 2024-01-15T18:00:00Z

# Show events from a specific date
muster events --since 2024-01-15

# Show events with relative time format
muster events --since "2 hours ago"
```

### Event Type Filtering
```bash
# Show only warning events
muster events --type Warning

# Show only normal events
muster events --type Normal

# Combine with other filters
muster events --resource-type mcpserver --type Warning --since 1h
```

### Output Formats
```bash
# JSON format for programmatic processing
muster events --output json --limit 10

# YAML format for detailed inspection
muster events --output yaml --resource-name my-service

# Table format with specific filters
muster events --resource-type workflow --limit 20
```

### Advanced Filtering Combinations
```bash
# Recent warning events for MCPServers
muster events --resource-type mcpserver --type Warning --since 2h

# All events for a specific service in the last day
muster events --resource-type service --resource-name my-app --since 24h

# Workflow execution events with detailed output
muster events --resource-type workflow --output yaml --since 1h
```

## Event Types Overview

### MCPServer Events
- **Lifecycle**: Created, Updated, Deleted, Starting, Started, Stopped, Failed
- **Tools**: ToolsDiscovered, ToolsUnavailable, Reconnected
- **Health**: HealthCheckFailed, RecoveryStarted, RecoverySucceeded, RecoveryFailed

### ServiceClass Events
- **Configuration**: Created, Updated, Deleted, Validated, ValidationFailed
- **Availability**: Available, Unavailable, ToolsDiscovered, ToolsMissing, ToolsRestored

### Workflow Events
- **Configuration**: Created, Updated, Deleted, ValidationFailed, ValidationSucceeded
- **Execution**: ExecutionStarted, ExecutionCompleted, ExecutionFailed, ExecutionTracked
- **Steps**: StepStarted, StepCompleted, StepFailed, StepSkipped, StepConditionEvaluated
- **Tools**: Available, Unavailable, ToolsDiscovered, ToolsMissing, ToolRegistered, ToolUnregistered

### Service Instance Events
- **Lifecycle**: Created, Starting, Started, Stopping, Stopped, Restarting, Restarted, Deleted, Failed
- **Health**: Healthy, Unhealthy, HealthCheckFailed, HealthCheckRecovered, StateChanged
- **Tools**: ToolExecutionStarted, ToolExecutionCompleted, ToolExecutionFailed

## Event Severity

### Normal Events
Events that indicate successful operations or expected state changes:
- Resource creation, updates, deletions
- Successful service starts and stops
- Tool discovery and availability
- Workflow execution completion
- Health check recovery

### Warning Events
Events that may require attention or indicate problems:
- Service failures and crashes
- Tool unavailability
- Validation failures
- Health check failures
- Workflow execution failures

## Backend Modes

### Kubernetes Mode
When running with Kubernetes backend:
- Events are stored as Kubernetes Event objects
- Can be viewed with `kubectl get events`
- Events include standard Kubernetes metadata (count, firstTime, lastTime)
- Events respect Kubernetes TTL and cleanup policies

### Filesystem Mode
When running with filesystem backend:
- Events are logged to console and `events.log` file
- Events are stored as JSON entries for machine readability
- No automatic cleanup (events persist until manually removed)
- Useful for development and standalone deployments

## Troubleshooting

### No Events Displayed
```bash
# Check if aggregator is running
muster list mcpserver

# Verify event generation by performing an action
muster create service test-service web-app
muster events --resource-type service --resource-name test-service
```

### Time Filter Issues
```bash
# Use explicit timezone for UTC times
muster events --since 2024-01-15T10:00:00Z

# Use relative time for recent events
muster events --since 1h30m
```

### Large Result Sets
```bash
# Use limit to reduce output
muster events --limit 10

# Use time filters to narrow results
muster events --since 1h --until 30m

# Filter by specific resource
muster events --resource-type mcpserver --resource-name specific-server
```

## Related Commands

- [`muster list`](list.md) - List multiple resources
- [`muster get`](get.md) - Get detailed resource information
- [`muster serve`](serve.md) - Start the aggregator server (required for events)

## See Also

- [Event Reference Guide](../events.md) - Complete event types and troubleshooting
- [Configuration Reference](../configuration.md) - Server configuration options 