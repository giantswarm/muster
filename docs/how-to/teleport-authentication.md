# Teleport Authentication for Private Installations

This guide explains how to configure Muster to access MCP servers on private installations via Teleport Application Access.

## Overview

Private installations are deployed in networks without public endpoints and can only be accessed through Teleport Application Access. Muster supports Teleport Machine ID certificates (tbot) for authenticating to these private endpoints.

The core principle is: **OAuth for identity, Teleport for access**. User identity remains managed through OAuth/Dex, while Teleport provides the network-level access to private endpoints.

## Prerequisites

1. **Teleport cluster** with Application Access enabled
2. **tbot** configured to generate Machine ID certificates
3. **Teleport application** registered for your MCP server

## Configuration

### Filesystem Mode (Local Development)

When running Muster locally, certificates are loaded from the tbot output directory:

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: remote-mcp-kubernetes
spec:
  type: streamable-http
  url: https://mcp-kubernetes.teleport.example.com/mcp
  auth:
    type: teleport
    teleport:
      # Path to tbot identity output directory
      identityDir: /var/run/tbot/identity
      # Teleport application name for routing
      appName: mcp-kubernetes
```

### Kubernetes Mode

When running Muster in Kubernetes, certificates are loaded from a Kubernetes Secret:

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: remote-mcp-kubernetes
  namespace: muster-system
spec:
  type: streamable-http
  url: https://mcp-kubernetes.teleport.example.com/mcp
  auth:
    type: teleport
    teleport:
      # Secret containing tbot identity files
      identitySecretName: tbot-identity-output
      # Namespace where the secret is located (optional, defaults to MCPServer namespace)
      identitySecretNamespace: teleport-system
      # Teleport application name for routing
      appName: mcp-kubernetes
```

## Identity File Format

tbot outputs identity files that Muster expects in the following format:

| File | Description |
|------|-------------|
| `tls.crt` | Client certificate |
| `tls.key` | Client private key |
| `ca.crt` | Teleport CA certificate |

### Creating the Kubernetes Secret

If tbot doesn't directly create a Kubernetes Secret, you can create one manually:

```bash
kubectl create secret generic tbot-identity-output \
  --from-file=tls.crt=/var/run/tbot/identity/tls.crt \
  --from-file=tls.key=/var/run/tbot/identity/tls.key \
  --from-file=ca.crt=/var/run/tbot/identity/ca.crt \
  -n teleport-system
```

Or use a YAML manifest:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: tbot-identity-output
  namespace: teleport-system
type: Opaque
data:
  tls.crt: <base64-encoded-cert>
  tls.key: <base64-encoded-key>
  ca.crt: <base64-encoded-ca>
```

## Configuration Options

| Field | Description | Required |
|-------|-------------|----------|
| `identityDir` | Filesystem path to tbot identity files | One of identityDir or identitySecretName |
| `identitySecretName` | Kubernetes Secret name | One of identityDir or identitySecretName |
| `identitySecretNamespace` | Secret namespace | No (defaults to MCPServer namespace) |
| `appName` | Teleport application name | No (but recommended for routing) |

## Certificate Hot-Reloading

When using filesystem mode (`identityDir`), Muster automatically watches for certificate file changes. When tbot renews the certificates, Muster reloads them without requiring a restart.

In Kubernetes mode, certificate updates require restarting the affected MCP server connections. You can trigger this by updating the MCPServer resource or restarting Muster.

## Troubleshooting

### "teleport client handler not registered"

This error means the Teleport adapter was not initialized. Ensure Muster is running with the correct configuration. The adapter is automatically registered during startup.

### "secret missing tls.crt/tls.key/ca.crt"

The Kubernetes Secret doesn't contain the required certificate files. Verify the secret contents:

```bash
kubectl get secret tbot-identity-output -n teleport-system -o yaml
```

### "failed to get Teleport HTTP client"

Check that:
1. The identity directory or secret exists
2. Certificate files are readable
3. Certificates are valid (not expired)

### Connection refused or timeout

1. Verify the Teleport application is accessible
2. Check that the `appName` matches the registered Teleport application
3. Ensure the URL points to the correct Teleport proxy

## Security Considerations

- **Certificate Storage**: Identity files contain sensitive private keys. Use Kubernetes Secrets with appropriate RBAC.
- **Certificate Rotation**: tbot handles certificate renewal. Muster automatically reloads updated certificates.
- **OAuth Tokens**: User identity is still managed through OAuth. Teleport only provides network access.

## Example: Complete Setup

1. **Register the Teleport application**:

```yaml
# teleport-app.yaml
kind: app
version: v3
metadata:
  name: mcp-kubernetes
spec:
  uri: http://mcp-kubernetes.internal:8080
  public_addr: mcp-kubernetes.teleport.example.com
```

2. **Configure tbot**:

```yaml
# tbot.yaml
version: v2
onboarding:
  join_method: kubernetes
  token: tbot-token
storage:
  type: kubernetes-secret
  name: tbot-identity-output
  namespace: teleport-system
outputs:
  - type: application
    app_name: mcp-kubernetes
    destination:
      type: kubernetes-secret
      name: tbot-identity-output
      namespace: teleport-system
```

3. **Create the MCPServer**:

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: remote-mcp-kubernetes
spec:
  type: streamable-http
  url: https://mcp-kubernetes.teleport.example.com/mcp
  auth:
    type: teleport
    teleport:
      identitySecretName: tbot-identity-output
      identitySecretNamespace: teleport-system
      appName: mcp-kubernetes
```

## Related Documentation

- [Teleport Application Access](https://goteleport.com/docs/application-access/)
- [Teleport Machine ID](https://goteleport.com/docs/machine-id/)
- [tbot Configuration](https://goteleport.com/docs/machine-id/reference/configuration/)
