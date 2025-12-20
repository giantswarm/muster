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
| `service` | Get detailed status of a service | `muster get service my-app` |
| `serviceclass` | Get ServiceClass details and configuration | `muster get serviceclass web-app` |
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
muster get service my-web-app

# Example output:
# Name:              my-web-app
# Status:            Running
# ServiceClass:      web-application
# Created:           2024-01-07 10:00:00
# Uptime:           2h 15m
# Parameters:
#   image:           nginx:latest
#   replicas:        3
#   environment:     production
# Health:           Healthy
# Last Restart:     Never
```

### Getting ServiceClass Information
```bash
# Get ServiceClass configuration
muster get serviceclass web-application

# Example output:
# Name:              web-application
# Description:       Scalable web application service
# Created:           2024-01-07 08:00:00
# Tools Required:
#   - x_kubernetes_apply
#   - x_kubernetes_get_status
#   - x_kubernetes_scale
# Parameters:
#   image:           (required) Container image
#   replicas:        (optional) Number of replicas (default: 1)
#   environment:     (optional) Environment name
# Instances:         2 services using this class
# Status:           Available
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
muster get service my-app
# Name:              my-app
# Status:            Running
# ServiceClass:      web-service
# Created:           2h ago
# Parameters:
#   image:           nginx:latest
#   replicas:        3
```

### JSON Format
Complete structured data:

```bash
muster get service my-app --output json
# {
#   "name": "my-app",
#   "status": "Running",
#   "serviceClass": "web-service",
#   "created": "2024-01-07T10:00:00Z",
#   "uptime": "2h15m",
#   "parameters": {
#     "image": "nginx:latest",
#     "replicas": 3,
#     "environment": "production"
#   },
#   "health": {
#     "status": "Healthy",
#     "lastCheck": "2024-01-07T12:14:30Z"
#   },
#   "metadata": {
#     "lastRestart": null,
#     "pid": 5678
#   }
# }
```

### YAML Format
Configuration-friendly output:

```bash
muster get serviceclass web-app --output yaml
# apiVersion: muster.giantswarm.io/v1alpha1
# kind: ServiceClass
# metadata:
#   name: web-app
#   created: "2024-01-07T08:00:00Z"
# spec:
#   description: "Web application service"
#   toolsRequired:
#   - x_kubernetes_apply
#   - x_kubernetes_get_status
#   parameters:
#     image:
#       type: string
#       required: true
#       description: "Container image"
#     replicas:
#       type: integer
#       required: false
#       default: 1
# status:
#   available: true
#   instances: 2
```

## Detailed Information Sections

### Service Details
When getting service information, you'll see:

- **Basic Info**: Name, status, ServiceClass, creation time
- **Runtime Info**: Uptime, PID, health status
- **Configuration**: All parameters passed during creation
- **Lifecycle**: Start/stop history, restart information
- **Dependencies**: Required tools and their availability

### ServiceClass Details
ServiceClass information includes:

- **Template Info**: Name, description, creation time
- **Tool Requirements**: List of required MCP tools
- **Parameter Schema**: Required and optional parameters with types
- **Usage Statistics**: Number of service instances
- **Availability**: Whether all required tools are available

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

### Configuration Verification
```bash
# Verify ServiceClass configuration
muster get serviceclass my-template --output yaml

# Check:
# - Tool requirements are met
# - Parameter schema is correct
# - Instance count matches expectations
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

# Valid types: service, serviceclass, mcpserver, workflow, workflow-execution
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
# Suggestions: service, serviceclass, mcpserver, workflow, workflow-execution

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

# 2. If unhealthy, check the ServiceClass
muster get serviceclass web-application

# 3. Verify MCP servers are running
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
# Extract service configuration for backup
muster get service my-app --output yaml > my-app-backup.yaml

# Extract all ServiceClass configurations
for sc in $(muster list serviceclass --output json | jq -r '.serviceClasses[].name'); do
  muster get serviceclass "$sc" --output yaml > "serviceclass-$sc.yaml"
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
