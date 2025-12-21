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
| `muster.oauth.enabled` | Enable OAuth proxy for remote MCP auth | `false` |
| `muster.oauth.publicUrl` | Public URL for OAuth callbacks | `""` |
| `muster.oauth.clientId` | OAuth client ID (CIMD URL) | Giant Swarm hosted |
| `muster.oauth.callbackPath` | OAuth callback endpoint path | `/oauth/callback` |
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

### OAuth Proxy for Remote MCP Servers

When connecting to remote MCP servers that require authentication (e.g., `mcp-kubernetes`), you can enable the OAuth proxy. This allows Muster to handle OAuth flows on behalf of users without exposing sensitive tokens to the Muster Agent.

```yaml
muster:
  oauth:
    enabled: true
    publicUrl: "https://muster.example.com"  # Must be publicly accessible
    clientId: "https://giantswarm.github.io/muster/oauth-client.json"
    callbackPath: "/oauth/callback"

# Ingress is required for OAuth callbacks
ingress:
  enabled: true
  hosts:
    - host: muster.example.com
      paths:
        - path: /
          pathType: Prefix
```

When a remote MCP server returns a 401, Muster will:
1. Present a synthetic `authenticate_{servername}` tool to the user
2. When called, return an authorization URL for the user to visit
3. After browser authentication, store the token and allow the user to retry

#### OAuth Client ID (CIMD) Configuration

The default `clientId` points to the Giant Swarm hosted [Client ID Metadata Document (CIMD)](https://giantswarm.github.io/muster/oauth-client.json), which has a fixed redirect URI of `https://muster.giantswarm.io/oauth/callback`. This works for deployments at that domain.

**For custom deployments**, you have two options:

1. **Host your own CIMD file** with your deployment's redirect URI:
   ```json
   {
     "client_id": "https://your-domain.com/oauth-client.json",
     "client_name": "Muster MCP Aggregator",
     "redirect_uris": ["https://your-domain.com/oauth/callback"],
     "grant_types": ["authorization_code", "refresh_token"],
     "response_types": ["code"],
     "token_endpoint_auth_method": "none"
   }
   ```
   Then set `clientId` to your CIMD URL.

2. **Use an IdP with dynamic client registration** (RFC 7591) that accepts new redirect URIs at runtime.

Note: The Identity Provider must trust the CIMD URL and allow the specified redirect URI.

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

