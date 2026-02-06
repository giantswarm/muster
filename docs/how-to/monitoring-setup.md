# Set Up Service Monitoring

This guide covers how to monitor MCP servers, services, and workflows in muster using built-in health checks, events, and CLI commands.

## Overview

Muster provides several monitoring capabilities out of the box:

- **Health checks** for MCP servers and service instances
- **Event tracking** for resource lifecycle changes and failures
- **CLI commands** for inspecting status, connectivity, and tool availability

## Prerequisites

- Muster installed and running
- At least one MCP server or service configured

## Quick Start

### 1. Check Resource Health

Use `muster check` to verify the health of any resource:

```bash
# Check a specific MCP server
muster check mcpserver kubernetes

# Check a ServiceClass (validates tool availability and schema)
muster check serviceclass web-app

# Check a workflow (validates steps, tools, and parameters)
muster check workflow deploy-app
```

Exit codes indicate health status:
- `0` -- Available and healthy
- `3` -- Unavailable or unhealthy
- `4` -- Degraded / partial availability
- `5` -- Connection error

### 2. List Resources with Status

```bash
# List all MCP servers with their current status
muster list mcpserver

# Wide output for more details (tool counts, connection state)
muster list mcpserver -o wide
```

### 3. Monitor Events

```bash
# View recent events
muster events

# Filter to warnings from the last hour
muster events --type Warning --since 1h

# Filter by resource
muster events --resource-type mcpserver --resource-name kubernetes
```

## Configure MCP Server Health Checks

Enable periodic health checks in the MCPServer spec to detect failures automatically:

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: kubernetes
  namespace: default
spec:
  type: stdio
  autoStart: true
  command: ["mcp-kubernetes"]
  description: "Kubernetes cluster management MCP server"
  healthCheck:
    enabled: true
    interval: "30s"
    timeout: "10s"
```

Health check status values:

| Status | Meaning |
|--------|---------|
| `healthy` | Running and responsive |
| `unhealthy` | Running but not responding correctly |
| `error` | Not running or returning errors |
| `starting` | In startup phase |
| `unknown` | Status cannot be determined |

## Configure Service Health Checks

ServiceClasses can define health check tools that run periodically against service instances:

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: ServiceClass
metadata:
  name: service-k8s-connection
spec:
  description: "Kubernetes cluster connection service"
  serviceConfig:
    lifecycleTools:
      healthCheck:
        tool: "api_kubernetes_connection_status"
        args:
          connectionId: "{{ .service_id }}"
        expect:
          success: true
          jsonPath:
            health: true
    healthCheck:
      enabled: true
      interval: "60s"
      failureThreshold: 3
      successThreshold: 2
    timeout:
      healthCheck: "30s"
```

Key configuration options:

- **`interval`** -- How often the health check runs
- **`failureThreshold`** -- Consecutive failures before marking unhealthy
- **`successThreshold`** -- Consecutive successes before marking healthy again
- **`timeout.healthCheck`** -- Maximum time for a single health check

## Monitor with Events

Muster emits events for key lifecycle transitions. Use these for alerting and diagnostics.

### MCP Server Events

| Event | Meaning |
|-------|---------|
| `MCPServerHealthCheckFailed` | Health checks are consistently failing |
| `MCPServerRecoveryStarted` | Automatic recovery process began |
| `MCPServerRecoverySucceeded` | Recovery restored the server |
| `MCPServerRecoveryFailed` | Recovery failed |
| `MCPServerToolsDiscovered` | Tools were discovered from the server |
| `MCPServerToolsUnavailable` | Tools became unavailable |
| `MCPServerReconnected` | Connection restored after a failure |

### Service Instance Events

| Event | Meaning |
|-------|---------|
| `ServiceInstanceHealthy` | Health checks passing |
| `ServiceInstanceUnhealthy` | Health checks failing |
| `ServiceInstanceHealthCheckFailed` | Individual health check failed |
| `ServiceInstanceHealthCheckRecovered` | Recovered after failures |

### Example: Watch for Warnings

```bash
# Stream warning events
muster events --type Warning --since 5m

# JSON output for scripting
muster events --type Warning --output json
```

## Monitoring Scripts

### Check All MCP Servers

```bash
#!/bin/bash
echo "=== MCP Server Health ==="
muster list mcpserver --output json | jq -r '.[].name' | while read server; do
  STATUS=$(muster check mcpserver "$server" --output json | jq -r '.status')
  echo "  $server: $STATUS"
done
```

### Pre-Deployment Validation

```bash
#!/bin/bash
# Verify all critical servers are healthy before deployment
CRITICAL_SERVERS=("kubernetes" "prometheus" "github")

for server in "${CRITICAL_SERVERS[@]}"; do
  STATUS=$(muster check mcpserver "$server" --output json | jq -r '.status')
  if [ "$STATUS" != "Healthy" ]; then
    echo "FAIL: $server is $STATUS"
    exit 1
  fi
  echo "OK: $server"
done
echo "All critical servers healthy."
```

## Prometheus Integration

If you have a Prometheus MCP server configured, AI agents can query metrics directly:

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: prometheus
  namespace: default
spec:
  type: stdio
  autoStart: true
  command: ["mcp-prometheus", "--config", "/etc/prometheus/config.yaml"]
  env:
    PROMETHEUS_URL: "http://localhost:9090"
  description: "Prometheus metrics collection and querying"
```

Once configured, agents can query Prometheus metrics through natural language (e.g., "show me the error rate for the API service over the last hour").

## Troubleshooting

### Health Check Failing

```bash
# Get detailed server info
muster get mcpserver <server-name>

# Check recent events for the server
muster events --resource-type mcpserver --resource-name <server-name>

# Verify the server binary is available
which <server-command>
```

### Events Not Appearing

- Verify the resource name and type are correct
- Check time range: `muster events --since 24h`
- Use `--output json` for machine-readable diagnostics

### Service Stuck in Unhealthy State

- Check if the health check tool exists: `muster check serviceclass <name>`
- Verify health check thresholds aren't too aggressive (low `failureThreshold` with short `interval`)
- Review service events: `muster events --resource-type service --resource-name <name>`

## Related Documentation

- [MCP Server Management](mcp-server-management.md) - Server configuration and health monitoring
- [Service Configuration](service-configuration.md) - ServiceClass health check setup
- [Troubleshooting](troubleshooting.md) - General troubleshooting guide
- [Events Reference](../reference/events.md) - Complete event reference
- [CLI Check Reference](../reference/cli/check.md) - Detailed `muster check` documentation
