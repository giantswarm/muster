# Integrate with Kubernetes

This guide covers how to integrate muster with Kubernetes clusters, enabling AI agents to interact with Kubernetes resources.

## Overview

Muster can connect to Kubernetes clusters through an MCP server, providing AI agents with tools to:

- List and inspect Kubernetes resources (pods, deployments, services, etc.)
- Monitor cluster health and status
- Execute kubectl-like operations through natural language

## Prerequisites

- Muster installed and running
- Access to a Kubernetes cluster with valid credentials
- The `mcp-kubernetes` server binary (or a compatible Kubernetes MCP server)

## Quick Start

### 1. Create the Kubernetes MCP Server

Create an MCP server configuration for Kubernetes:

```yaml
# kubernetes-mcp.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: kubernetes
  namespace: default
spec:
  type: stdio
  autoStart: true
  command: mcp-kubernetes
  description: "Kubernetes cluster management MCP server"
```

### 2. Register the Server

```bash
muster create mcpserver kubernetes-mcp.yaml
```

### 3. Verify the Integration

```bash
# Check server status
muster get mcpserver kubernetes

# Test tool availability
muster agent --repl
# In REPL:
filter tools k8s
```

## Using Kubernetes Tools

Once configured, AI agents can access Kubernetes tools through the aggregator. Common tools include:

- `x_kubernetes_list_pods` - List pods in a namespace
- `x_kubernetes_get_pod` - Get details of a specific pod
- `x_kubernetes_list_deployments` - List deployments
- `x_kubernetes_apply` - Apply Kubernetes manifests
- `x_kubernetes_logs` - Get pod logs

### Example: List Pods

Ask your AI agent:
```
List all pods in the default namespace
```

The agent will use the Kubernetes tools to retrieve and display pod information.

## SSO Authentication with Kubernetes

For production environments, muster supports Single Sign-On with Kubernetes OIDC authentication. This allows users to authenticate once and access Kubernetes clusters without separate authentication flows.

### Configure Token Forwarding

Enable SSO for the Kubernetes MCP server:

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: mcp-kubernetes
spec:
  description: "Kubernetes MCP with SSO"
  toolPrefix: "k8s"
  type: streamable-http
  url: "https://mcp-kubernetes.example.com/mcp"
  auth:
    forwardToken: true
    requiredAudiences:
      - "dex-k8s-authenticator"
```

**How it works:**

1. User authenticates to muster via `muster auth login`
2. Muster requests tokens with Kubernetes OIDC audiences
3. On MCP requests, muster forwards the token to the Kubernetes server
4. Users can immediately access Kubernetes without additional authentication

For detailed SSO configuration, see the [MCP Server Management Guide](mcp-server-management.md#sso-authentication).

## Multi-Cluster Configuration

To connect to multiple Kubernetes clusters, create separate MCP server configurations:

```yaml
# production-cluster.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: k8s-production
spec:
  type: stdio
  autoStart: true
  command: mcp-kubernetes
  toolPrefix: "k8s-prod"
  description: "Production Kubernetes cluster"
  env:
    KUBECONFIG: "/path/to/production-kubeconfig"

---
# staging-cluster.yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: k8s-staging
spec:
  type: stdio
  autoStart: true
  command: mcp-kubernetes
  toolPrefix: "k8s-staging"
  description: "Staging Kubernetes cluster"
  env:
    KUBECONFIG: "/path/to/staging-kubeconfig"
```

Using different tool prefixes (`k8s-prod`, `k8s-staging`) allows AI agents to distinguish between clusters.

## Troubleshooting

### Server Not Starting

```bash
# Check if the command is available
which mcp-kubernetes

# Verify server status
muster get mcpserver kubernetes

# Check for errors in server output
muster check mcpserver kubernetes
```

### Authentication Errors

- Verify your kubeconfig is valid: `kubectl cluster-info`
- Check KUBECONFIG environment variable is set correctly
- For SSO, ensure the IdP supports cross-client authentication

### Tools Not Appearing

```bash
# Verify server is running
muster list mcpserver

# Test tool discovery
muster agent --repl
# In REPL:
list tools
filter tools kubernetes
```

## Related Documentation

- [MCP Server Management](mcp-server-management.md) - Detailed MCP server configuration
- [SSO Authentication](mcp-server-management.md#sso-authentication) - Single Sign-On setup
- [Teleport Authentication](teleport-authentication.md) - Teleport integration for cluster access
- [Configuration Reference](../reference/configuration.md) - Complete configuration options
