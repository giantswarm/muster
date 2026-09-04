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

## Authorization servers muster cannot discover or register with: GitHub

Some authorization servers go further than skipping RFC 9728: they publish no
[RFC 8414][rfc8414] discovery document at all, accept neither Client ID
Metadata Documents nor [RFC 7591][rfc7591] dynamic registration, and issue
tokens that belong to the person rather than to one login session. GitHub is
the prompting case -- the hosted GitHub MCP server at
`https://api.githubcopilot.com/mcp/` names `https://github.com/login/oauth` as
its authorization server, and any GitHub user token is accepted as its bearer.
Three optional fields on `spec.auth.authorizationServer` describe such a
server completely:

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: github
  namespace: agent-platform
spec:
  type: streamable-http
  url: https://api.githubcopilot.com/mcp/
  auth:
    type: oauth
    authorizationServer:
      issuer: https://github.com/login/oauth
      # No discovery document: pin the endpoints. Both or neither.
      authorizationEndpoint: https://github.com/login/oauth/authorize
      tokenEndpoint: https://github.com/login/oauth/access_token
      scopes: "repo read:org project"
      # A client registered with GitHub out of band (a GitHub App or OAuth
      # App whose callback URL is muster's /oauth/proxy/callback).
      clientCredentialsSecretRef:
        name: github-oauth-client       # keys: client-id, client-secret
      # The grant belongs to the person, not to one login session.
      grantScope: subject
```

- **`authorizationEndpoint` / `tokenEndpoint`** replace discovery for this
  issuer. muster performs no metadata request and assumes S256 PKCE support
  (it keeps sending `code_challenge`; an AS that ignores it is unaffected). The
  operator pinning the endpoints vouches for them, which is why the RFC 8414
  §3.3 self-check of the plain issuer override does not apply here.
- **`clientCredentialsSecretRef`** makes muster identify itself with the
  pre-registered client instead of its CIMD URL or a dynamic registration:
  `core_auth_login` reports `clientIdMethod: preregistered`, and token refresh
  presents the same client. Rotate the secret and the next login picks it up;
  no restart needed.
- **`grantScope: subject`** files every token from this issuer under the
  user's identity (the `sub` muster derives from the caller's token) as well
  as under the session. A later session of the same person -- another MCP
  client, a re-login, a front-end whose bearer rotates on every token refresh
  -- reuses the grant: its `core_auth_login` connects at once and answers
  "Successfully connected" without a sign-in link. `core_auth_logout` on that
  server removes the person's grant, as does "sign out everywhere"; logging
  one session out of muster does not. Use it only for external accounts the
  person owns; a token that carries session-specific authority stays
  `session`, the default.

Several MCPServers may point at the same issuer (GitHub's hosted MCP server
and an in-house server that verifies GitHub tokens, say): the grant is stored
per issuer, so one consent serves them all -- the usual SSO reuse.

## When not to use this

`authorizationServer` is mutually exclusive with `forwardToken: true` and
`tokenExchange.enabled: true`. The CRD admission rules will reject any
`MCPServer` that combines them — those features have their own issuer
configuration.

The override does **not** change the [RFC 8707][rfc8707] `resource` parameter
sent on auth/token requests. It remains the configured MCP server URL with the
query and the fragment dropped, which is also what mcp-go sends on token
refresh.

## Reporting non-compliant backends

If you find a hosted MCP that doesn't publish RFC 9728 PRM, please file an
issue with that backend's vendor — the [MCP authorization spec][mcp-auth]
mandates publishing PRM. The override exists as an operator escape hatch, not
as a substitute for spec compliance.

[rfc9728]: https://datatracker.ietf.org/doc/html/rfc9728
[rfc8414]: https://datatracker.ietf.org/doc/html/rfc8414
[rfc7591]: https://datatracker.ietf.org/doc/html/rfc7591
[rfc8414-33]: https://datatracker.ietf.org/doc/html/rfc8414#section-3.3
[rfc8707]: https://www.rfc-editor.org/rfc/rfc8707.html
[mcp-auth]: https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization
