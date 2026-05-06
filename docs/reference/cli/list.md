# muster list

List resources in the Muster environment.

## Synopsis

```
muster list [RESOURCE_TYPE] [OPTIONS]
```

## Description

The `list` command displays resources managed by Muster, providing an overview of their current state and configuration. It supports multiple resource types and output formats.

**Prerequisites**: The aggregator server must be running (`muster serve`) before using this command.

## Resource Types

| Resource Type | Description | Example |
|---------------|-------------|---------|
| `service` | List static services with their status | `muster list service` |
| `mcpserver` | List all MCP server definitions | `muster list mcpserver` |
| `workflow` | List all workflow definitions | `muster list workflow` |
| `workflow-execution` | List all workflow execution history | `muster list workflow-execution` |

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

### Listing Services
```bash
# List static services (the aggregator and per-MCPServer wrappers)
muster list service

# Example output:
# NAME             TYPE        STATE     HEALTH
# mcp-aggregator   Aggregator  Running   Healthy
# kubernetes       MCPServer   Running   Healthy
# prometheus       MCPServer   Stopped   Unknown
```

### Listing MCP Servers
```bash
# List all MCP server configurations
muster list mcpserver

# Example output (when authenticated):
# NAME         STATE          SESSION         TYPE
# kubernetes   Connected      Authenticated   streamable-http
# prometheus   Auth Required  Pending Auth    streamable-http
# github       Failed         -               stdio

# Use wide output for more details:
muster list mcpserver -o wide
```

The output shows:
- **STATE**: Infrastructure state from CRD status
  - `Connected`: Server is reachable and authenticated
  - `Auth Required`: Server is reachable but needs authentication (returned 401)
  - `Connecting`: Attempting to connect
  - `Failed`: Server cannot be reached
  - `Running`/`Stopped`: For stdio (local) servers
- **SESSION**: Per-user authentication state (only shown when logged in)
  - `Authenticated`: Successfully authenticated to this server
  - `Pending Auth`: Server requires authentication
  - `Expired`: Token has expired
  - `-`: No session state available

### Listing Workflows
```bash
# List all workflow definitions
muster list workflow

# Example output:
# NAME           DESCRIPTION              STEPS   LAST_EXECUTED
# deploy-app     Application deployment   5       1h ago
# backup-db      Database backup          3       6h ago
# scale-service  Service scaling          2       Never
```

### Listing Workflow Executions
```bash
# List workflow execution history
muster list workflow-execution

# Example output:
# ID                    WORKFLOW     STATUS     STARTED        DURATION
# abc123-def456-789     deploy-app   Success    2h ago         45s
# def456-789abc-123     backup-db    Success    6h ago         2m30s
# 789abc-123def-456     deploy-app   Failed     1d ago         15s
```

## Output Formats

### Table Format (Default)
Clean, human-readable tabular output:

```bash
muster list service
# NAME             TYPE        STATE     HEALTH
# mcp-aggregator   Aggregator  Running   Healthy
# kubernetes       MCPServer   Running   Healthy
```

### JSON Format
Structured data for programmatic processing:

```bash
muster list service --output json
# {
#   "services": [
#     {
#       "name": "my-app",
#       "status": "Running",
#       "serviceClass": "web-service",
#       "created": "2024-01-07T10:00:00Z",
#       "parameters": {
#         "image": "nginx:latest",
#         "replicas": 3
#       }
#     }
#   ]
# }
```

### YAML Format
YAML output for configuration management:

```bash
muster list mcpserver --output yaml
# mcpServers:
# - name: kubernetes
#   type: stdio
#   command: mcp-kubernetes
#   autoStart: true
```

## Filtering and Information

### Service Status Information
Services are displayed with comprehensive status information:

```bash
muster list service
# NAME             TYPE        STATE       HEALTH
# mcp-aggregator   Aggregator  Running     Healthy
# kubernetes       MCPServer   Running     Healthy
# prometheus       MCPServer   Stopped     Unknown
```

**State Values:**
- `Running`: Service is active and healthy
- `Starting`: Service is in startup phase
- `Stopped`: Service is intentionally stopped
- `Failed`: Service encountered an error
- `Unknown`: Service state cannot be determined

### MCP Server Information
MCP servers display connection and tool information:

```bash
muster list mcpserver --show-details
# Name          Type    State     Tools   AutoStart   Port
# kubernetes    local   Running   15      true        1234
# prometheus    local   Running   8       true        1235
# github        local   Stopped   12      false       -
# local-tools   local   Error     0       true        -
```

### Workflow Information
Workflows show execution statistics and metadata:

```bash
muster list workflow
# NAME           STEPS   EXECUTIONS   SUCCESS_RATE   LAST_RUN
# deploy-app     5       23           95.7%          1h ago
# backup-db      3       156          100%           6h ago
# scale-service  2       0            -              Never
```

## Resource Relationships

### Resource Relationships

```bash
# List services to see runtime state of the aggregator and MCPServer wrappers
muster list service

# List MCPServer definitions to see what's configured to be loaded
muster list mcpserver
```

## Common Use Cases

### System Overview
```bash
# Get complete system overview
muster list service
muster list mcpserver
muster list workflow
```

### Health Check
```bash
# Check system health
muster list service | grep -v Running  # Find non-running services
muster list mcpserver | grep -v Running  # Find failed MCP servers
```

### Resource Planning
```bash
# Understand resource usage
muster list workflow --output json | jq '.workflows[] | {name, executions, successRate}'
```

### Automation Scripting
```bash
# Get running services for scripts
RUNNING_SERVICES=$(muster list service --output json | jq -r '.services[] | select(.status=="Running") | .name')

# Get available tools count
TOOL_COUNT=$(muster list mcpserver --output json | jq '[.mcpServers[] | .tools] | add')
```

## Troubleshooting

### Empty Results
```bash
muster list service
# No services found

# Possible causes:
# 1. No services created yet
muster create service test-service web-app

# 2. Server not running
muster serve  # In another terminal

# 3. Configuration issue
muster list service --config-path ~/.config/muster
```

### Connection Issues
```bash
muster list service
# Error: failed to connect to aggregator

# Solution: Verify server is running
ps aux | grep "muster serve"
curl http://localhost:8080/api/v1/status
```

### Permission Issues
```bash
muster list service
# Error: permission denied

# Solution: Check configuration permissions
ls -la ~/.config/muster/
chmod 755 ~/.config/muster
```

## Exit Codes

| Code | Meaning |
|------|---------|
| 0 | Resources listed successfully |
| 1 | General error or invalid arguments |
| 2 | Invalid resource type |
| 3 | Configuration error |
| 4 | Connection error (server not running) |

## Auto-Completion

The list command supports tab completion:

```bash
# Resource types
muster list [TAB]
# Suggestions: service, mcpserver, workflow, workflow-execution
```

## Performance Considerations

### Large Environments
For environments with many resources:

```bash
# Use quiet mode for faster output
muster list service --quiet

# Use JSON for programmatic processing
muster list service --output json | jq '.services | length'

# Filter results programmatically
muster list service --output json | jq '.services[] | select(.status=="Running")'
```

## Integration Patterns

### Monitoring Scripts
```bash
#!/bin/bash
# Check for failed services
FAILED_SERVICES=$(muster list service --output json | jq -r '.services[] | select(.status=="Error") | .name')
if [ -n "$FAILED_SERVICES" ]; then
  echo "Failed services detected: $FAILED_SERVICES"
  exit 1
fi
```

### Workflow Monitoring
```bash
# Check recent workflow executions
muster list workflow-execution --output json | \
  jq '.executions[] | select(.started > "2024-01-07T00:00:00Z")'
```

## Related Commands

- **[get](get.md)** - Get detailed information about specific resources
- **[create](create.md)** - Create new resources
- **[start](start.md)** - Start services or execute workflows
- **[check](check.md)** - Check resource availability
- **[agent](agent.md)** - Interactive exploration with REPL mode

## Advanced Usage

### Custom Queries
```bash
# Count services by state
muster list service --output json | jq 'group_by(.state) | map({state: .[0].state, count: length})'

# List workflows by success rate
muster list workflow --output json | jq '.workflows | sort_by(.successRate) | reverse'
```
