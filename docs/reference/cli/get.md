# muster get

Get detailed information about a specific resource.

## Synopsis

```
muster get [RESOURCE_TYPE] [NAME] [OPTIONS]
```

## Description

The `get` command retrieves detailed information about a specific resource in Muster, providing comprehensive status, configuration, and metadata about individual resources.

**Prerequisites**: The aggregator server must be running (`muster serve`) before using this command.

## Resource Types

| Resource Type | Description | Example |
|---------------|-------------|---------|
| `service` | Get the status of a static service (aggregator or MCPServer wrapper) | `muster get service mcp-aggregator` |
| `mcpserver` | Get MCP server details and configuration | `muster get mcpserver kubernetes` |
| `workflow` | Get workflow definition and details | `muster get workflow deploy-app` |
| `workflow-execution` | Get workflow execution details and results | `muster get workflow-execution abc123` |

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

### Getting Service Information
```bash
# Get service details
muster get service mcp-aggregator

# Example output:
# Name:              mcp-aggregator
# Status:            Running
# Type:              Aggregator
# Health:            Healthy
```

### Getting MCP Server Information
```bash
# Get MCP server details
muster get mcpserver kubernetes

# Example output:
# Name:              kubernetes
# Description:        Kubernetes management tools
# Type:               stdio
# AutoStart:          true
# Command:            ["mcp-kubernetes"]
# Environment:        KUBECONFIG=/path/to/config
# State:              Running
# Tools:              15
# Health:             Healthy
# Last Error:         None
```

### Getting Workflow Information
```bash
# Get workflow definition
muster get workflow deploy-application

# Example output:
# Name:              deploy-application
# Description:       Deploy application to Kubernetes
# Created:           2024-01-07 08:30:00
# Steps:             5
# Parameters:
#   app_name:        (required) Application name
#   environment:     (required) Target environment
#   image:           (required) Container image
#   replicas:        (optional) Number of replicas (default: 1)
# Executions:        23 total
# Success Rate:      95.7%
# Last Execution:    1h ago (Success)
# Average Duration:  45 seconds
```

### Getting Workflow Execution Information
```bash
# Get execution details
muster get workflow-execution abc123-def456-789

# Example output:
# ID:                abc123-def456-789
# Workflow:          deploy-application
# Status:            Success
# Started:           2024-01-07 12:00:00
# Completed:         2024-01-07 12:00:45
# Duration:          45 seconds
# Parameters:
#   app_name:        my-web-app
#   environment:     production
#   image:           myapp:v1.2.3
#   replicas:        5
# Steps Executed:
#   1. validate_parameters    ✓ Success (2s)
#   2. prepare_deployment     ✓ Success (5s)
#   3. apply_kubernetes       ✓ Success (30s)
#   4. wait_for_rollout      ✓ Success (8s)
#   5. verify_health         ✓ Success (0s)
# Output:
#   deployment.apps/my-web-app created
#   service/my-web-app created
#   ingress.networking.k8s.io/my-web-app created
```

## Output Formats

### Table Format (Default)
Human-readable formatted output with sections:

```bash
muster get service mcp-aggregator
# Name:              mcp-aggregator
# Status:            Running
# Type:              Aggregator
# Health:            Healthy
```

### JSON Format

```bash
muster get service mcp-aggregator --output json
# {
#   "name": "mcp-aggregator",
#   "type": "Aggregator",
#   "state": "running",
#   "health": "healthy"
# }
```

### YAML Format

```bash
muster get mcpserver kubernetes --output yaml
# apiVersion: muster.giantswarm.io/v1alpha1
# kind: MCPServer
# metadata:
#   name: kubernetes
# spec:
#   type: stdio
#   command: mcp-kubernetes
#   autoStart: true
```

## Detailed Information Sections

### Service Details
When getting service information, you'll see:

- **Basic Info**: Name, type (Aggregator or MCPServer), state, health
- **Runtime Info**: Last error, last connection time
- **Health**: Whether the service is healthy

### MCP Server Details
MCP server information shows:

- **Process Info**: PID, command, uptime
- **Connection Status**: Health, tool count, responsiveness
- **Configuration**: Environment variables, command arguments
- **Tool Inventory**: List of available tools
- **Auto-start Settings**: Startup behavior configuration

### Workflow Details
Workflow information includes:

- **Definition**: Steps, parameters, description
- **Execution Statistics**: Success rate, average duration
- **Parameter Schema**: Required and optional workflow parameters
- **Recent History**: Latest execution results
- **Tool Dependencies**: Required tools for execution

### Workflow Execution Details
Execution information provides:

- **Execution Metadata**: ID, start/end times, duration
- **Parameter Values**: Actual parameters used
- **Step-by-Step Results**: Each step's status and output
- **Failure Information**: Error details if execution failed
- **Output Logs**: Complete execution output

## Use Cases

### Service Debugging
```bash
# Check why a service isn't working
muster get service problematic-app

# Look for:
# - Status: Error, Starting, Stopped
# - Health: Unhealthy, Unknown
# - Last Restart: Recent restarts indicate issues
# - Parameters: Incorrect configuration
```

### Workflow Analysis
```bash
# Analyze workflow performance
muster get workflow deploy-app

# Review:
# - Success rate trends
# - Average execution duration
# - Parameter requirements
# - Recent execution status
```

### Execution Troubleshooting
```bash
# Debug failed workflow execution
muster get workflow-execution failed-execution-id

# Examine:
# - Which step failed
# - Error messages and output
# - Parameter values used
# - Execution environment
```

## Error Handling

### Resource Not Found
```bash
muster get service non-existent
# Error: service 'non-existent' not found

# Solution: List available resources
muster list service
```

### Invalid Resource Type
```bash
muster get invalid-type my-resource
# Error: unknown resource type 'invalid-type'

# Valid types: service, mcpserver, workflow, workflow-execution
```

### Connection Issues
```bash
muster get service my-app
# Error: failed to connect to aggregator

# Solution: Ensure server is running
muster serve  # In another terminal
```

### Incomplete Information
```bash
muster get mcpserver kubernetes
# Status: Error - Unable to retrieve tool list

# This indicates the MCP server is not responding
# Check server logs or restart the server
```

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Resource information retrieved successfully |
| 1 | General error or invalid arguments |
| 2 | Resource not found |
| 3 | Invalid resource type |
| 4 | Connection error (server not running) |

## Auto-Completion

The get command supports tab completion:

```bash
# Resource types
muster get [TAB]
# Suggestions: service, mcpserver, workflow, workflow-execution

# Resource names (context-aware)
muster get service [TAB]
# Suggestions: my-app, database, monitoring, ...

muster get workflow [TAB]
# Suggestions: deploy-app, backup-db, scale-service, ...
```

## Integration with Other Commands

### Typical Workflow
```bash
# 1. List resources to find what you want
muster list service

# 2. Get detailed information
muster get service interesting-service

# 3. Take action based on status
if [ "$(muster get service my-app --output json | jq -r '.status')" = "Stopped" ]; then
  muster start service my-app
fi
```

### Debugging Workflow
```bash
# 1. Check service status
muster get service my-app

# 2. Verify MCP servers are running
muster get mcpserver kubernetes

# 4. Check recent workflow executions
muster list workflow-execution | head -5
```

## Related Commands

- **[list](list.md)** - List multiple resources to find the one you want
- **[start](start.md)** - Start services or execute workflows
- **[stop](stop.md)** - Stop running services
- **[check](check.md)** - Check resource availability
- **[create](create.md)** - Create new resources

## Advanced Usage

### Monitoring Scripts
```bash
#!/bin/bash
# Check service health and restart if needed
SERVICE_STATUS=$(muster get service my-app --output json | jq -r '.health.status')
if [ "$SERVICE_STATUS" != "Healthy" ]; then
  echo "Service unhealthy, restarting..."
  muster stop service my-app
  muster start service my-app
fi
```

### Configuration Extraction
```bash
# Extract MCPServer configurations for backup
for s in $(muster list mcpserver --output json | jq -r '.mcpServers[].name'); do
  muster get mcpserver "$s" --output yaml > "mcpserver-$s.yaml"
done
```

### Performance Analysis
```bash
# Analyze workflow performance over time
WORKFLOW_NAME="deploy-app"
echo "Workflow: $WORKFLOW_NAME"
muster get workflow "$WORKFLOW_NAME" --output json | \
  jq '{successRate, averageDuration, totalExecutions}'
```
