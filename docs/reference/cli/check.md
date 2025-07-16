# muster check

Check if resources are available and properly configured.

## Synopsis

```
muster check [RESOURCE_TYPE] [NAME] [OPTIONS]
```

## Description

The `check` command verifies if resources are available and properly configured in Muster. It validates that all dependencies are met and that resources can be used effectively.

**Prerequisites**: The aggregator server must be running (`muster serve`) before using this command.

## Resource Types

| Resource Type | Description | Example |
|---------------|-------------|---------|
| `serviceclass` | Check if a ServiceClass is available for use | `muster check serviceclass web-app` |
| `mcpserver` | Check MCP server status and connectivity | `muster check mcpserver kubernetes` |
| `workflow` | Check if a workflow is available (all required tools present) | `muster check workflow deploy-app` |

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

### Checking ServiceClass Availability
```bash
# Check if a ServiceClass is ready to use
muster check serviceclass web-application

# Example output:
# NAME              STATUS      TOOLS_AVAILABLE   ISSUES
# web-application   Available   3/3               None

# If there are issues:
# NAME              STATUS        TOOLS_AVAILABLE   ISSUES
# web-application   Unavailable   2/3               x_kubernetes_scale missing
```

### Checking MCP Server Status
```bash
# Check MCP server health
muster check mcpserver kubernetes

# Example output:
# NAME         STATUS    RESPONSIVE   TOOLS   LAST_CHECK
# kubernetes   Healthy   Yes          15      30s ago

# If server has issues:
# NAME         STATUS    RESPONSIVE   TOOLS   LAST_CHECK
# kubernetes   Error     No           0       2m ago
```

### Checking Workflow Availability
```bash
# Check if workflow can be executed
muster check workflow deploy-application

# Example output:
# NAME                 STATUS      TOOLS_AVAILABLE   DEPENDENCIES
# deploy-application   Available   5/5               All met

# If dependencies are missing:
# NAME                 STATUS        TOOLS_AVAILABLE   DEPENDENCIES
# deploy-application   Unavailable   4/5               x_helm_install missing
```

## Check Results

### ServiceClass Availability
When checking a ServiceClass, the command verifies:

- **Tool Availability**: All required MCP tools are accessible
- **MCP Server Health**: Required MCP servers are running and responsive
- **Parameter Schema**: ServiceClass definition is valid
- **Dependencies**: All external dependencies are met

```bash
muster check serviceclass database-service
# Checks:
# ✓ MCP server 'kubernetes' is running
# ✓ Tool 'x_kubernetes_apply' is available
# ✓ Tool 'x_kubernetes_get_status' is available
# ✓ ServiceClass definition is valid
# → Status: Available
```

### MCP Server Health
When checking an MCP server, the command verifies:

- **Process Status**: Server process is running
- **Connectivity**: Server responds to health checks
- **Tool Inventory**: All expected tools are exposed
- **Performance**: Response times are acceptable

```bash
muster check mcpserver prometheus
# Checks:
# ✓ Process is running (PID: 1234)
# ✓ Server responds to ping (250ms)
# ✓ Tools are available (8 tools)
# ✓ Recent activity detected
# → Status: Healthy
```

### Workflow Dependencies
When checking a workflow, the command verifies:

- **Step Validation**: All workflow steps are defined
- **Tool Requirements**: Required tools are available
- **Parameter Schema**: Workflow parameters are valid
- **Execution Environment**: Environment is ready

```bash
muster check workflow backup-database
# Checks:
# ✓ Workflow definition is valid (3 steps)
# ✓ Tool 'x_database_backup' is available
# ✓ Tool 'x_storage_upload' is available
# ✓ Required parameters are defined
# → Status: Available
```

## Output Formats

### Table Format (Default)
Human-readable status with clear indicators:

```bash
muster check serviceclass web-app
# NAME      STATUS      TOOLS   ISSUES
# web-app   Available   3/3     None
```

### JSON Format
Detailed status information for programmatic use:

```bash
muster check serviceclass web-app --output json
# {
#   "name": "web-app",
#   "type": "serviceclass",
#   "status": "Available",
#   "checks": {
#     "toolsAvailable": {
#       "required": 3,
#       "available": 3,
#       "missing": []
#     },
#     "mcpServers": [
#       {
#         "name": "kubernetes",
#         "status": "Healthy",
#         "tools": ["x_kubernetes_apply", "x_kubernetes_get_status"]
#       }
#     ],
#     "issues": []
#   },
#   "lastChecked": "2024-01-07T10:00:00Z"
# }
```

### YAML Format
Configuration-friendly output:

```bash
muster check workflow deploy-app --output yaml
# name: deploy-app
# type: workflow
# status: Available
# checks:
#   stepsValid: true
#   toolsRequired: 5
#   toolsAvailable: 5
#   missingTools: []
#   parametersValid: true
# dependencies:
#   - name: kubernetes
#     status: Available
#   - name: helm
#     status: Available
# lastChecked: "2024-01-07T10:00:00Z"
```

## Status Indicators

### Availability Status
Resources can have these availability statuses:

- **Available**: Resource is ready to use
- **Unavailable**: Resource has missing dependencies
- **Degraded**: Resource is partially available
- **Unknown**: Resource status cannot be determined
- **Error**: Resource has configuration errors

### Health Status (MCP Servers)
MCP servers have specific health indicators:

- **Healthy**: Server is running and responsive
- **Unhealthy**: Server is running but not responding correctly
- **Error**: Server has errors or is not running
- **Starting**: Server is in startup phase
- **Unknown**: Server status cannot be determined

## Use Cases

### Pre-Deployment Validation
```bash
# Validate environment before deployment
echo "Validating deployment environment..."

# Check required ServiceClasses
muster check serviceclass web-application
muster check serviceclass database-service

# Check required workflows
muster check workflow deploy-application
muster check workflow rollback-deployment

# Check MCP servers
muster check mcpserver kubernetes
muster check mcpserver helm

echo "Environment validation complete"
```

### Troubleshooting Dependencies
```bash
# Debug why a service creation failed
echo "Checking dependencies for web-app service..."

# Check the ServiceClass first
SC_STATUS=$(muster check serviceclass web-application --output json | jq -r '.status')

if [ "$SC_STATUS" != "Available" ]; then
  echo "ServiceClass is not available:"
  muster check serviceclass web-application
  
  # Check specific MCP servers
  muster check mcpserver kubernetes
  muster check mcpserver prometheus
else
  echo "ServiceClass is available - issue may be elsewhere"
fi
```

### Health Monitoring
```bash
# Regular health check script
echo "Performing system health check..."

CRITICAL_SERVERS=("kubernetes" "prometheus" "github")
CRITICAL_WORKFLOWS=("deploy-app" "backup-db" "scale-service")

echo "Checking critical MCP servers..."
for server in "${CRITICAL_SERVERS[@]}"; do
  STATUS=$(muster check mcpserver "$server" --output json | jq -r '.status')
  echo "  $server: $STATUS"
  
  if [ "$STATUS" != "Healthy" ]; then
    echo "    WARNING: $server is not healthy!"
  fi
done

echo "Checking critical workflows..."
for workflow in "${CRITICAL_WORKFLOWS[@]}"; do
  STATUS=$(muster check workflow "$workflow" --output json | jq -r '.status')
  echo "  $workflow: $STATUS"
  
  if [ "$STATUS" != "Available" ]; then
    echo "    WARNING: $workflow is not available!"
  fi
done
```

### CI/CD Integration
```bash
#!/bin/bash
# Pre-deployment environment check for CI/CD

REQUIRED_SERVICECLASSES=("web-app" "database" "cache")
REQUIRED_WORKFLOWS=("deploy-app" "rollback" "health-check")

echo "Running pre-deployment checks..."

ALL_CHECKS_PASSED=true

# Check ServiceClasses
for sc in "${REQUIRED_SERVICECLASSES[@]}"; do
  echo "Checking ServiceClass: $sc"
  if ! muster check serviceclass "$sc" --quiet; then
    echo "FAIL: ServiceClass $sc is not available"
    ALL_CHECKS_PASSED=false
  else
    echo "PASS: ServiceClass $sc is available"
  fi
done

# Check Workflows
for wf in "${REQUIRED_WORKFLOWS[@]}"; do
  echo "Checking Workflow: $wf"
  if ! muster check workflow "$wf" --quiet; then
    echo "FAIL: Workflow $wf is not available"
    ALL_CHECKS_PASSED=false
  else
    echo "PASS: Workflow $wf is available"
  fi
done

if [ "$ALL_CHECKS_PASSED" = true ]; then
  echo "All checks passed - deployment can proceed"
  exit 0
else
  echo "Some checks failed - deployment blocked"
  exit 1
fi
```

## Error Handling

### Resource Not Found
```bash
muster check serviceclass non-existent
# Error: serviceclass 'non-existent' not found

# Solution: List available resources
muster list serviceclass
```

### Missing Dependencies
```bash
muster check serviceclass web-app
# NAME     STATUS        TOOLS   ISSUES
# web-app  Unavailable   2/3     x_kubernetes_scale missing

# Solution: Check MCP server providing missing tool
muster check mcpserver kubernetes
muster list mcpserver  # Find which server should provide the tool
```

### MCP Server Issues
```bash
muster check mcpserver kubernetes
# NAME         STATUS   RESPONSIVE   TOOLS   ISSUES
# kubernetes   Error    No           0       Connection timeout

# Solution: Check server status and restart if needed
muster get mcpserver kubernetes
# Restart the MCP server if necessary
```

### Connectivity Issues
```bash
muster check workflow deploy-app
# Error: failed to connect to aggregator

# Solution: Ensure aggregator is running
muster serve  # In another terminal
```

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Resource is available and healthy |
| 1 | General error or invalid arguments |
| 2 | Resource not found |
| 3 | Resource is unavailable or unhealthy |
| 4 | Partial availability (degraded) |
| 5 | Connection error (server not running) |

## Auto-Completion

The check command supports tab completion:

```bash
# Resource types
muster check [TAB]
# Suggestions: serviceclass, mcpserver, workflow

# Resource names (context-aware)
muster check serviceclass [TAB]
# Suggestions: web-app, database, monitoring, ...

muster check mcpserver [TAB]
# Suggestions: kubernetes, prometheus, github, ...
```

## Performance Considerations

### Batch Checking
```bash
# Check multiple resources efficiently
RESOURCES=("web-app" "database" "cache")

echo "Batch checking ServiceClasses..."
for resource in "${RESOURCES[@]}"; do
  muster check serviceclass "$resource" --quiet || echo "ISSUE: $resource"
done
```

### Caching Results
```bash
# Cache check results for scripting
CHECK_CACHE="/tmp/muster_checks.json"

# Perform checks and cache results
{
  echo "{"
  echo "  \"serviceClasses\": {"
  for sc in $(muster list serviceclass --output json | jq -r '.serviceClasses[].name'); do
    STATUS=$(muster check serviceclass "$sc" --output json | jq -r '.status')
    echo "    \"$sc\": \"$STATUS\","
  done | sed '$ s/,$//'
  echo "  },"
  echo "  \"timestamp\": \"$(date -Iseconds)\""
  echo "}"
} > "$CHECK_CACHE"

# Use cached results
cat "$CHECK_CACHE" | jq '.serviceClasses'
```

## Related Commands

- **[list](list.md)** - List resources to find what to check
- **[get](get.md)** - Get detailed information after check fails
- **[create](create.md)** - Create resources after verifying dependencies
- **[start](start.md)** - Start resources after confirming availability

## Advanced Usage

### Deep Health Check
```bash
#!/bin/bash
# Comprehensive system health check

echo "Performing deep health check..."

# Check all MCP servers
echo "=== MCP Server Health ==="
muster list mcpserver --output json | jq -r '.mcpServers[].name' | while read server; do
  echo "Checking $server..."
  muster check mcpserver "$server" --output json | \
    jq -r 'if .status == "Healthy" then "✓ \(.name): OK" else "✗ \(.name): \(.status)" end'
done

# Check all ServiceClasses
echo "=== ServiceClass Availability ==="
muster list serviceclass --output json | jq -r '.serviceClasses[].name' | while read sc; do
  echo "Checking $sc..."
  muster check serviceclass "$sc" --output json | \
    jq -r 'if .status == "Available" then "✓ \(.name): OK" else "✗ \(.name): \(.status)" end'
done

# Check all workflows
echo "=== Workflow Readiness ==="
muster list workflow --output json | jq -r '.workflows[].name' | while read wf; do
  echo "Checking $wf..."
  muster check workflow "$wf" --output json | \
    jq -r 'if .status == "Available" then "✓ \(.name): OK" else "✗ \(.name): \(.status)" end'
done

echo "Health check complete"
``` 