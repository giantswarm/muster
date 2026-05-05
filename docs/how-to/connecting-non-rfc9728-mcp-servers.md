# Connecting to MCP servers that don't publish RFC 9728 metadata

Some hosted MCP servers (Atlassian's Remote MCP at `mcp.atlassian.com` is the
prompting case) require OAuth but don't publish [RFC 9728][rfc9728] Protected
Resource Metadata at any well-known path. By default muster's `core_auth_login`
flow can't discover the authorization server for such backends and fails with:

```
Cannot authenticate to '<server>': RFC 9728 protected resource metadata not found.
```

Pin the issuer manually with `spec.auth.authorizationServer`:

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: atlassian
spec:
  type: streamable-http
  url: https://mcp.atlassian.com/v1/mcp
  auth:
    type: oauth
    authorizationServer:
      issuer: https://cf.mcp.atlassian.com
      scopes: "openid offline_access"
```

When the override is set, muster skips PRM probing and fetches AS metadata
directly from `<issuer>/.well-known/oauth-authorization-server`. The discovered
`issuer` is verified against your pinned value per [RFC 8414 §3.3][rfc8414-33]
— a typo or stale URL fails closed instead of driving an OAuth flow against
the wrong AS.

## What you'll see in the UI

The override applies only to `muster auth login --server <name>`
(`core_auth_login`). It does **not** bypass the connect-time PRM probe that
runs when muster first reaches the backend, so:

1. On first reconciliation the server enters **Auth Required**.
2. Run `muster auth login --server <name>`. The override skips PRM and the
   OAuth browser flow opens against the pinned issuer.
3. After the token is cached the server transitions to **Connected**;
   subsequent reconnects use the bearer header without rediscovery.

## When not to use this

`authorizationServer` is mutually exclusive with `forwardToken: true` and
`tokenExchange.enabled: true`. The CRD admission rules will reject any
`MCPServer` that combines them — those features have their own issuer
configuration.

The override does **not** change the [RFC 8707][rfc8707] `resource` parameter
sent on auth/token requests; that remains the canonical MCP server URL.

## Reporting non-compliant backends

If you find a hosted MCP that doesn't publish RFC 9728 PRM, please file an
issue with that backend's vendor — the [MCP authorization spec][mcp-auth]
mandates publishing PRM. The override exists as an operator escape hatch, not
as a substitute for spec compliance.

[rfc9728]: https://datatracker.ietf.org/doc/html/rfc9728
[rfc8414-33]: https://datatracker.ietf.org/doc/html/rfc8414#section-3.3
[rfc8707]: https://www.rfc-editor.org/rfc/rfc8707.html
[mcp-auth]: https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization
