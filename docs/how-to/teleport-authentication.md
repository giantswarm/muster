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

Muster uses tbot's `application` output type files. When you configure tbot with an application output, it produces:

| File | Description |
|------|-------------|
| `tlscert` | Client certificate |
| `key` | Client private key |
| `teleport-application-ca.pem` | Teleport CA certificate |

### tbot Configuration

Configure tbot with an `application` output type:

```yaml
outputs:
  - type: application
    app_name: mcp-kubernetes
    destination:
      type: directory
      path: /var/run/teleport/identity
```

### Creating the Kubernetes Secret

If tbot doesn't directly create a Kubernetes Secret, you can create one manually:

```bash
kubectl create secret generic tbot-identity-output \
  --from-file=tlscert=/var/run/tbot/identity/tlscert \
  --from-file=key=/var/run/tbot/identity/key \
  --from-file=teleport-application-ca.pem=/var/run/tbot/identity/teleport-application-ca.pem \
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
  tlscert: <base64-encoded-cert>
  key: <base64-encoded-key>
  teleport-application-ca.pem: <base64-encoded-ca>
```

## Configuration Options

| Field | Description | Required |
|-------|-------------|----------|
| `identityDir` | Filesystem path to tbot identity files | Exactly one of identityDir or identitySecretName (mutually exclusive) |
| `identitySecretName` | Kubernetes Secret name | Exactly one of identityDir or identitySecretName (mutually exclusive) |
| `identitySecretNamespace` | Secret namespace | No (defaults to MCPServer namespace) |
| `appName` | Teleport application name | No (but recommended for routing) |

**Note:** `identityDir` and `identitySecretName` are mutually exclusive. You must specify exactly one of them.

## Certificate Hot-Reloading

When using filesystem mode (`identityDir`), Muster automatically watches for certificate file changes. When tbot renews the certificates, Muster reloads them without requiring a restart.

In Kubernetes mode, certificate updates require restarting the affected MCP server connections. You can trigger this by updating the MCPServer resource or restarting Muster.

## Troubleshooting

### "teleport client handler not registered"

This error means the Teleport adapter was not initialized. Ensure Muster is running with the correct configuration. The adapter is automatically registered during startup.

### "secret missing tlscert/key/teleport-application-ca.pem"

The Kubernetes Secret doesn't contain the required certificate files. Verify the secret contents:

```bash
kubectl get secret tbot-identity-output -n teleport-system -o yaml
```

Ensure tbot is configured with `type: application` output, which produces these files.

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

### Certificate Storage and Handling

- **In-Memory Loading (Kubernetes)**: When loading certificates from Kubernetes Secrets, Muster loads them directly into memory without writing to temporary files. This eliminates the risk of private key exposure through filesystem access.
- **Filesystem Mode**: When using `identityDir`, ensure the directory has appropriate permissions (0700) and the certificate files are readable only by the Muster process user (0600).
- **RBAC**: Use Kubernetes RBAC to restrict access to identity secrets. Only grant `get` permissions on the specific secret to the Muster service account.

### Certificate Lifecycle and Revocation

- **Short-Lived Certificates**: Teleport Machine ID certificates are intentionally short-lived (typically 1 hour). This design provides revocation-like behavior - if access needs to be revoked, certificates naturally expire without requiring explicit revocation checks (CRL/OCSP).
- **Automatic Rotation**: tbot handles certificate renewal automatically. Muster watches for file changes and reloads certificates without restart.
- **Expiry Monitoring**: Muster tracks certificate expiration and logs warnings when certificates are expiring soon.

### Namespace Restrictions

For defense-in-depth, Muster restricts which namespaces identity secrets can be loaded from. By default, only the following namespaces are allowed:

- `teleport-system`
- `muster-system`

This prevents misconfigured MCPServer resources from accessing secrets in unauthorized namespaces.

### Input Validation

- **App Name Validation**: The `appName` field is validated to contain only alphanumeric characters, hyphens, underscores, and dots. This prevents potential header injection attacks.
- **Path Validation**: The `identityDir` field must be an absolute path and cannot contain path traversal sequences (`..`).
- **Secret Name Validation**: Secret names are validated against Kubernetes naming conventions.

### OAuth Token Separation

User identity remains managed through OAuth/Dex tokens. Teleport certificates only provide network-level access to private endpoints - they do not grant any user-level permissions. Authorization decisions are still made based on OAuth tokens.

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
proxy_server: teleport.example.com:443
onboarding:
  join_method: kubernetes
  token: tbot-token
storage:
  type: memory
outputs:
  - type: application
    app_name: mcp-kubernetes
    destination:
      type: directory
      path: /var/run/teleport/identity
renewal_interval: 20m
certificate_ttl: 1h
```

This configuration produces files: `tlscert`, `key`, and `teleport-application-ca.pem` in the output directory.

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
