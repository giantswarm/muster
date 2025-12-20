# Muster Helm Chart

A Helm chart for [Muster](https://github.com/giantswarm/muster) - Universal Control Plane for AI Agents built on the Model Context Protocol (MCP).

## Description

Muster is an MCP aggregator that manages multiple MCP servers, orchestrates service lifecycles, and provides unified tool access for AI agents. It enables AI assistants to interact with your infrastructure through a single interface.

## Prerequisites

- Kubernetes 1.21+
- Helm 3.0+

## Installation

```bash
# Add the Giant Swarm catalog
helm repo add giantswarm https://giantswarm.github.io/giantswarm-catalog/
helm repo update

# Install muster
helm install muster giantswarm/muster

# Or install from local source
helm install muster ./helm/muster
```

## Configuration

### Key Configuration Parameters

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of replicas | `1` |
| `image.registry` | Container registry | `gsoci.azurecr.io` |
| `image.repository` | Container repository | `giantswarm/muster` |
| `image.tag` | Image tag (defaults to appVersion) | `""` |
| `service.type` | Kubernetes service type | `ClusterIP` |
| `service.port` | Service port | `8090` |
| `muster.aggregator.port` | Aggregator HTTP port | `8090` |
| `muster.aggregator.transport` | MCP transport (streamable-http, sse) | `streamable-http` |
| `muster.namespace` | Namespace for CRD discovery | Release namespace |
| `muster.debug` | Enable debug logging | `false` |
| `rbac.create` | Create RBAC resources | `true` |
| `rbac.profile` | RBAC profile (minimal, readonly, standard) | `standard` |
| `crds.install` | Install CRDs with the chart | `true` |
| `ciliumNetworkPolicy.enabled` | Enable CiliumNetworkPolicy | `false` |

### RBAC Profiles

Muster supports three RBAC profiles:

- **minimal**: Only muster CRDs (MCPServer, ServiceClass, Workflow)
- **readonly**: Read-only K8s resources + muster CRDs  
- **standard**: Read + write for full muster functionality

```yaml
rbac:
  create: true
  profile: "standard"  # or "minimal", "readonly"
```

### Resource Limits

```yaml
resources:
  limits:
    cpu: 500m
    memory: 512Mi
  requests:
    cpu: 100m
    memory: 128Mi
```

### Ingress

```yaml
ingress:
  enabled: true
  className: "nginx"
  annotations:
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
  hosts:
    - host: muster.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: muster-tls
      hosts:
        - muster.example.com
```

### CiliumNetworkPolicy

For clusters using Cilium:

```yaml
ciliumNetworkPolicy:
  enabled: true
```

## Usage

After installation, access muster:

```bash
# Port forward
kubectl port-forward svc/muster 8090:8090

# Test health
curl http://localhost:8090/health

# List tools
curl -X POST http://localhost:8090/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc": "2.0", "method": "tools/list", "id": 1}'
```

### AI Assistant Integration

Configure your AI assistant (Cursor/VSCode) to connect to muster:

```json
{
  "mcpServers": {
    "muster": {
      "command": "muster",
      "args": ["agent", "--mcp-server", "--endpoint", "http://localhost:8090/mcp"]
    }
  }
}
```

## CRDs

Muster uses three Custom Resource Definitions:

- **MCPServer**: Define MCP servers (stdio, streamable-http, sse)
- **ServiceClass**: Template for service instances
- **Workflow**: Multi-step automated workflows

When `crds.install: true`, CRDs are installed automatically. For GitOps workflows, you may want to install CRDs separately.

## Upgrading

```bash
helm repo update
helm upgrade muster giantswarm/muster
```

## Uninstallation

```bash
helm uninstall muster

# Optional: Remove CRDs (will delete all muster resources)
kubectl delete crd mcpservers.muster.giantswarm.io
kubectl delete crd serviceclasses.muster.giantswarm.io
kubectl delete crd workflows.muster.giantswarm.io
```

## Related Documentation

- [Muster Documentation](https://github.com/giantswarm/muster/tree/main/docs)
- [MCP Protocol](https://modelcontextprotocol.io/)

## License

Apache 2.0 - see [LICENSE](https://github.com/giantswarm/muster/blob/main/LICENSE)

