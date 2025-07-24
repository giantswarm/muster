# Muster Helm Chart

A Helm chart for [Muster](https://github.com/giantswarm/muster) - Universal Control Plane for AI Agents built on the Model Context Protocol (MCP).

## Description

Muster is a sophisticated MCP aggregator that manages multiple MCP servers, orchestrates service lifecycles, and provides unified tool access for AI agents. It acts as a central control plane for platform engineering operations and enables AI assistants to interact with your entire infrastructure through a single interface.

## Installation

### Prerequisites

- Kubernetes 1.19+
- Helm 3.0+
- Optional: Muster CRDs for advanced features (MCPServer, ServiceClass, Workflow)

### Basic Installation

```bash
# Add the Giant Swarm catalog
helm repo add giantswarm https://giantswarm.github.io/giantswarm-catalog/
helm repo update

# Install Muster
helm install muster giantswarm/muster
```

### Custom Installation

```bash
# Install with custom values
helm install muster giantswarm/muster \
  --set muster.aggregator.port=9090 \
  --set muster.debug=true \
  --set ingress.enabled=true \
  --set ingress.hosts[0].host=muster.example.com

# Install from source
git clone https://github.com/giantswarm/muster.git
cd muster
helm install muster ./helm/muster
```

### Production Installation

```bash
# Production deployment with security hardening
helm install muster giantswarm/muster \
  --set replicaCount=3 \
  --set resources.limits.cpu=1000m \
  --set resources.limits.memory=1Gi \
  --set resources.requests.cpu=200m \
  --set resources.requests.memory=256Mi
```

## Configuration

### Core Configuration Parameters

| Parameter | Description | Default | Required |
|-----------|-------------|---------|----------|
| `replicaCount` | Number of replicas | `1` | No |
| `image.registry` | Container registry | `gsoci.azurecr.io` | Yes |
| `image.repository` | Container repository | `giantswarm/muster` | Yes |
| `image.tag` | Image tag (overrides appVersion) | `""` | No |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` | Yes |

### Service Configuration

| Parameter | Description | Default | Required |
|-----------|-------------|---------|----------|
| `service.type` | Kubernetes service type | `ClusterIP` | Yes |
| `service.port` | Service port | `8090` | Yes |

### Security Configuration

| Parameter | Description | Default | Required |
|-----------|-------------|---------|----------|
| `serviceAccount.create` | Create service account | `true` | No |
| `serviceAccount.automount` | Auto-mount SA token | `true` | No |
| `rbac.create` | Create RBAC resources | `true` | No |
| `podSecurityContext.runAsUser` | User ID to run as | `1000` | No |
| `podSecurityContext.runAsNonRoot` | Run as non-root | `true` | No |
| `securityContext.readOnlyRootFilesystem` | Read-only root filesystem | `true` | No |

### Muster Application Configuration

| Parameter | Description | Default | Required |
|-----------|-------------|---------|----------|
| `muster.aggregator.port` | Aggregator HTTP port | `8090` | Yes |
| `muster.aggregator.host` | Bind address | `"0.0.0.0"` | Yes |
| `muster.aggregator.transport` | MCP transport protocol | `"streamable-http"` | Yes |
| `muster.aggregator.enabled` | Enable aggregator | `true` | Yes |
| `muster.aggregator.musterPrefix` | Tool prefix | `"x"` | No |
| `muster.configPath` | Config directory in container | `"/config"` | Yes |
| `muster.debug` | Enable debug logging | `false` | No |
| `muster.yolo` | Disable safety restrictions | `false` | No |

### Resource Configuration

| Parameter | Description | Default | Required |
|-----------|-------------|---------|----------|
| `resources.limits.cpu` | CPU limit | `500m` | Yes |
| `resources.limits.memory` | Memory limit | `512Mi` | Yes |
| `resources.requests.cpu` | CPU request | `100m` | Yes |
| `resources.requests.memory` | Memory request | `128Mi` | Yes |

### Networking Configuration

| Parameter | Description | Default | Required |
|-----------|-------------|---------|----------|
| `ingress.enabled` | Enable ingress | `false` | No |
| `ingress.className` | Ingress class | `""` | No |
| `ingress.hosts[0].host` | Hostname | `muster.local` | No |
| `ingress.hosts[0].paths[0].path` | Path | `/` | No |

## Usage Examples

### Basic Deployment

```yaml
# values.yaml
muster:
  aggregator:
    port: 8090
  debug: false

service:
  type: ClusterIP
  port: 8090

ingress:
  enabled: false
```

### Development Environment

```yaml
# values-dev.yaml
muster:
  debug: true
  yolo: true  # ⚠️ Use with caution in dev only

ingress:
  enabled: true
  hosts:
    - host: muster-dev.local
      paths:
        - path: /
          pathType: Prefix

resources:
  limits:
    cpu: 200m
    memory: 256Mi
  requests:
    cpu: 50m
    memory: 128Mi
```

### Production Environment

```yaml
# values-prod.yaml
replicaCount: 3

muster:
  debug: false
  yolo: false
  aggregator:
    port: 8090
    transport: "streamable-http"

service:
  type: ClusterIP
  port: 8090

ingress:
  enabled: true
  className: "nginx"
  annotations:
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
    nginx.ingress.kubernetes.io/rate-limit: "100"
  hosts:
    - host: muster.company.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: muster-tls
      hosts:
        - muster.company.com

resources:
  limits:
    cpu: 1000m
    memory: 1Gi
  requests:
    cpu: 200m
    memory: 256Mi
```

## RBAC Permissions

Muster requires comprehensive Kubernetes API access to manage its resources effectively:

### Core Permissions
- **Pods, Services, Endpoints**: Service discovery and management
- **Nodes, Namespaces**: Cluster-wide operations
- **ConfigMaps, Secrets**: Configuration management

### Application Resources
- **Deployments, ReplicaSets**: Application lifecycle
- **StatefulSets, DaemonSets**: Stateful application management

### Muster CRDs
- **MCPServers**: MCP server lifecycle management
- **ServiceClasses**: Service template management
- **Workflows**: Multi-step process orchestration

### Custom Resource Access
- **CustomResourceDefinitions**: CRD discovery

## AI Assistant Integration

### Cursor/VSCode Integration

Add this to your IDE's MCP configuration:

```json
{
  "mcpServers": {
    "muster": {
      "command": "kubectl",
      "args": [
        "port-forward",
        "svc/muster",
        "8090:8090",
        "--",
        "muster",
        "agent",
        "--mcp-server",
        "--endpoint",
        "http://localhost:8090/mcp"
      ]
    }
  }
}
```

### Direct Connection

```bash
# Port forward to Muster
kubectl port-forward svc/muster 8090:8090

# Test MCP endpoint
curl -X POST http://localhost:8090/mcp \
  -H "Content-Type: application/json" \
  -d '{"method": "tools/list", "params": {}}'
```

## Health Checks and Monitoring

### Health Endpoints

- **Liveness Probe**: `GET /health` (port 8090)
- **Readiness Probe**: `GET /health` (port 8090)

### Monitoring Commands

```bash
# Check pod status
kubectl get pods -l app.kubernetes.io/name=muster

# View logs
kubectl logs -l app.kubernetes.io/name=muster -f

# Check service endpoints
kubectl get endpoints muster

# Test API connectivity
kubectl exec -it deployment/muster -- curl http://localhost:8090/health
```

### Metrics and Observability

```bash
# Resource usage
kubectl top pods -l app.kubernetes.io/name=muster

# Events
kubectl get events --field-selector involvedObject.name=muster

# Service status
kubectl describe service muster
```

## Troubleshooting

### Common Issues

#### 1. Pod Fails to Start

**Symptoms**: Pod in `CrashLoopBackOff` or `Error` state

**Diagnostics**:
```bash
kubectl describe pod -l app.kubernetes.io/name=muster
kubectl logs -l app.kubernetes.io/name=muster --previous
```

**Solutions**:
- Check resource limits vs. actual usage
- Verify image availability and pull secrets
- Review security context configuration
- Ensure CRDs are installed if using advanced features

#### 2. Health Check Failures

**Symptoms**: Pod shows as not ready, health checks failing

**Diagnostics**:
```bash
kubectl describe pod -l app.kubernetes.io/name=muster | grep -A 10 "Events:"
```

**Solutions**:
- Verify `/health` endpoint responds: `curl http://pod-ip:8090/health`
- Check aggregator port configuration matches health check
- Review startup time vs. `initialDelaySeconds`

#### 3. RBAC Permission Denied

**Symptoms**: Muster logs show permission errors

**Diagnostics**:
```bash
kubectl auth can-i list pods --as=system:serviceaccount:default:muster
kubectl describe clusterrolebinding muster
```

**Solutions**:
- Ensure `rbac.create=true`
- Verify ClusterRole includes required resources
- Check service account is correctly bound

#### 4. Configuration Issues

**Symptoms**: Muster starts but doesn't function correctly

**Diagnostics**:
```bash
kubectl exec -it deployment/muster -- cat /config/config.yaml
kubectl logs -l app.kubernetes.io/name=muster | grep -i error
```

**Solutions**:
- Verify ConfigMap content matches expected format
- Check muster-specific configuration values
- Review environment variables

### Debug Mode

Enable debug logging for detailed troubleshooting:

```bash
helm upgrade muster giantswarm/muster \
  --set muster.debug=true \
  --reuse-values
```

### Support and Resources

- **GitHub Issues**: [https://github.com/giantswarm/muster/issues](https://github.com/giantswarm/muster/issues)
- **Documentation**: [https://github.com/giantswarm/muster/tree/main/docs](https://github.com/giantswarm/muster/tree/main/docs)
- **Giant Swarm Support**: Contact through your support channels

## Upgrading

### Standard Upgrade

```bash
helm repo update
helm upgrade muster giantswarm/muster
```

### Upgrade with Value Changes

```bash
helm upgrade muster giantswarm/muster \
  --set muster.aggregator.port=9090 \
  --reuse-values
```

### Rollback

```bash
helm rollback muster 1
```

## Uninstallation

```bash
# Remove Helm release
helm uninstall muster

# Optional: Remove CRDs (if installed separately)
kubectl delete crd mcpservers.muster.giantswarm.io
kubectl delete crd serviceclasses.muster.giantswarm.io
kubectl delete crd workflows.muster.giantswarm.io
```

## Contributing

See the [Muster repository](https://github.com/giantswarm/muster) for contribution guidelines.

## License

This chart is licensed under the Apache 2.0 License. See the [LICENSE](https://github.com/giantswarm/muster/blob/main/LICENSE) file for details.
