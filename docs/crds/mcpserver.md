# MCPServer CRD

The MCPServer Custom Resource Definition (CRD) enables Kubernetes-native management of Model Context Protocol servers within muster.

## Overview

MCPServer resources define MCP server instances that provide tools and capabilities to the muster system. With the CRD-based approach, MCP servers are managed as first-class Kubernetes resources, enabling GitOps workflows, RBAC integration, and native Kubernetes tooling.

## API Version

- **API Group**: `muster.giantswarm.io`
- **API Version**: `v1alpha1`
- **Kind**: `MCPServer`
- **Scope**: Namespaced

## Schema

### MCPServerSpec

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | Yes | Unique identifier for the MCP server (max 63 chars, DNS-compatible) |
| `type` | string | Yes | Server execution type (currently only "localCommand") |
| `autoStart` | boolean | No | Whether to start automatically (default: false) |
| `toolPrefix` | string | No | Prefix for tool names to avoid conflicts |
| `command` | []string | No | Command and arguments for localCommand type (required for localCommand) |
| `env` | map[string]string | No | Environment variables for the server process |
| `description` | string | No | Human-readable description (max 500 chars) |

### MCPServerStatus

| Field | Type | Description |
|-------|------|-------------|
| `state` | string | Current operational state (unknown, starting, running, stopping, stopped, failed) |
| `health` | string | Health status (unknown, healthy, unhealthy, checking) |
| `availableTools` | []string | List of tools provided by this server |
| `lastError` | string | Most recent error message |
| `conditions` | []Condition | Standard Kubernetes conditions |

## Examples

### Basic Git Tools Server

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: git-tools
  namespace: default
spec:
  name: git-tools
  type: localCommand
  autoStart: true
  toolPrefix: git
  command: ["npx", "@modelcontextprotocol/server-git"]
  env:
    GIT_ROOT: "/workspace"
    LOG_LEVEL: "info"
  description: "Git tools MCP server for repository operations"
```

### Python-based Custom Server

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: python-tools
  namespace: development
spec:
  name: python-tools
  type: localCommand
  autoStart: false
  command: ["python", "-m", "mcp_server.custom"]
  env:
    PYTHONPATH: "/usr/local/lib/python3.9/site-packages"
    DEBUG: "true"
  description: "Custom Python MCP server"
```

## Usage

### Creating MCPServers

```bash
# Create from YAML file
kubectl apply -f mcpserver.yaml

# Create from examples
kubectl apply -f examples/mcpserver-example.yaml
```

### Listing MCPServers

```bash
# List all MCPServers
kubectl get mcpservers

# List with custom columns
kubectl get mcpservers -o custom-columns=NAME:.metadata.name,TYPE:.spec.type,AUTOSTART:.spec.autoStart,STATE:.status.state

# Short form using alias
kubectl get mcps
```

### Describing MCPServers

```bash
# Get detailed information
kubectl describe mcpserver git-tools

# Get YAML output
kubectl get mcpserver git-tools -o yaml
```

### Updating MCPServers

```bash
# Edit interactively
kubectl edit mcpserver git-tools

# Apply updates from file
kubectl apply -f updated-mcpserver.yaml
```

### Deleting MCPServers

```bash
# Delete specific server
kubectl delete mcpserver git-tools

# Delete all servers in namespace
kubectl delete mcpservers --all
```

## Validation

The CRD includes comprehensive validation rules:

- **Name validation**: Must be DNS-compatible (lowercase, alphanumeric, hyphens)
- **Type validation**: Must be "localCommand"
- **Command validation**: Required for localCommand type, minimum 1 item
- **Tool prefix validation**: Must start with letter, contain only alphanumeric, underscore, hyphen
- **Description validation**: Maximum 500 characters

## Migration from YAML Files

The CRD-based approach **replaces** the file-based YAML configuration. There is no automatic migration tool, and the new system does not maintain backward compatibility.

### Migration Steps

1. **Review existing configurations**: Examine your current `mcpservers/*.yaml` files
2. **Create CRD manifests**: Convert each YAML file to a MCPServer CRD
3. **Apply to cluster**: Use `kubectl apply` to create the resources
4. **Verify operation**: Ensure servers are working correctly
5. **Remove old files**: Delete the old YAML configuration files

### Example Conversion

**Old YAML file** (`mcpservers/git.yaml`):
```yaml
name: git-tools
type: localCommand
autoStart: true
command: ["npx", "@modelcontextprotocol/server-git"]
env:
  GIT_ROOT: "/workspace"
```

**New CRD** (`git-tools-mcpserver.yaml`):
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: git-tools
  namespace: default
spec:
  name: git-tools
  type: localCommand
  autoStart: true
  command: ["npx", "@modelcontextprotocol/server-git"]
  env:
    GIT_ROOT: "/workspace"
```

## Local Development

For local development without Kubernetes, muster automatically falls back to filesystem mode using the unified client. In this mode:

- MCPServer CRDs are stored as YAML files in `./mcpservers/<namespace>/`
- The same API and tooling work seamlessly
- No Kubernetes cluster required

## Status Management

MCPServer status is managed automatically by muster:

- **State**: Reflects the current operational state of the server process
- **Health**: Indicates whether the server is responding correctly
- **Available Tools**: Lists tools discovered from the server
- **Conditions**: Standard Kubernetes condition patterns for detailed status

## RBAC Integration

MCPServer resources integrate with Kubernetes RBAC:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: mcpserver-manager
rules:
- apiGroups: ["muster.giantswarm.io"]
  resources: ["mcpservers"]
  verbs: ["get", "list", "create", "update", "patch", "delete"]
- apiGroups: ["muster.giantswarm.io"]
  resources: ["mcpservers/status"]
  verbs: ["get", "update", "patch"]
```

## Troubleshooting

### Common Issues

1. **Validation errors**: Check the CRD schema requirements
2. **Command failures**: Verify the command path and arguments
3. **Environment issues**: Ensure required environment variables are set
4. **Permission errors**: Check RBAC permissions for the namespace

### Debugging

```bash
# Check server status
kubectl get mcpserver git-tools -o jsonpath='{.status}'

# View server logs (if available)
kubectl describe mcpserver git-tools

# Check events
kubectl get events --field-selector involvedObject.name=git-tools
```

## Best Practices

1. **Naming**: Use descriptive, DNS-compatible names
2. **Namespaces**: Organize servers by environment or team
3. **Tool prefixes**: Use prefixes to avoid tool name conflicts
4. **Resource limits**: Consider adding resource constraints in the future
5. **Monitoring**: Implement monitoring for server health and performance
6. **GitOps**: Store CRD manifests in version control for declarative management

## Future Enhancements

The v1alpha1 API is subject to change. Future versions may include:

- Additional server types beyond localCommand
- Resource limits and requests
- Health check configuration
- Advanced lifecycle management
- Integration with Kubernetes operators 