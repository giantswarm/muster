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
  -- reuses the grant without calling `core_auth_login`: its first tool call
  to the server connects with the grant and runs, and `list_tools` shows the
  server's tools instead of listing the server under `auth_required`. A
  `core_auth_login` from such a session still works and answers "Successfully
  connected" without a sign-in link. A session of a person without a grant
  keeps failing with `user not authenticated to server <name>` until that
  person logs in once. Use it only for external accounts the person owns; a
  token that carries session-specific authority stays `session`, the default.

Several MCPServers may point at the same issuer (GitHub's hosted MCP server
and an in-house server that verifies GitHub tokens, say): the grant is stored
per issuer, so one consent serves them all -- the usual SSO reuse.

### Signing out of a subject-scoped issuer

The grant belongs to the person, so `core_auth_logout` on **any** server of a
subject-scoped issuer revokes it for the person, not for one server or one
session -- even when other MCPServers share the issuer, where a logout from a
session-scoped shared issuer would leave the token alone so the other servers
keep working. Concretely:

- The tokens for that issuer are deleted from every session of the person and
  from the person's grant itself. `muster auth status` shows every server of
  the issuer as not authenticated.
- Every server of the issuer is disconnected for the calling session and for
  the person's other live sessions muster knows about: their tools are hidden
  and their pooled connections are closed. The tool result names the sibling
  servers it disconnected. A session muster has not seen since it started (or
  one on another replica) finds the token gone on its next request and
  disconnects itself; no session can adopt a revoked grant.
- The next `core_auth_login` on any of those servers starts a fresh sign-in.
  Whether the browser sees a consent prompt is the authorization server's
  call: GitHub redirects straight back while the person's authorization of the
  App still exists on the GitHub side, so revoke it there too if the account
  must be cut off from muster for good.

"Sign out everywhere" removes the grant as well; logging one session out of
muster does not.

### Keeping the grant alive: refresh

Access tokens from such an authorization server are short-lived (a GitHub
App's user access token lives eight hours) and come with a refresh token
(GitHub's lives six months and is rotated on every use). muster refreshes a
subject-scoped grant itself, so the person connects once and the grant then
lives as long as the refresh token does, across every session and muster
restart:

- The grant filed under the person is the canonical copy. Every lookup that
  serves a tool call, a `core_auth_login` or a broker release checks it
  first; when its access token has expired or will within the refresh margin
  (five minutes, capped at half the token's lifetime), muster redeems the
  refresh token at the pinned `tokenEndpoint` with the pre-registered client
  from `clientCredentialsSecretRef`, stores the rotated tokens under the
  person and under every session copy, and serves the new access token. A
  running connection presents the new bearer on its next request; nothing
  reconnects.
- Refreshes are single-flighted per person and issuer, and the store is
  re-read inside the flight, so many sessions and tool calls arriving at once
  cost one token request -- which matters because a rotating refresh token
  can be redeemed exactly once.
- A refresh the authorization server rejects (`invalid_grant`, GitHub's
  `bad_refresh_token`: the person revoked the App, the refresh token
  expired) removes the grant for the person; the next use asks for a
  sign-in, which GitHub answers with a consent page again. A transient
  failure (network, 5xx) keeps the grant and the still-valid access token
  and is retried on the next lookup. Both are logged as
  `subject_grant_refresh_rejected` / `subject_grant_refresh_failed`; a
  refresh that went through logs `subject_grant_refreshed`.

Session-scoped tokens (the default) are not touched by this: their refresh
stays with the MCP transport as before.

## Releasing a person's grant to a trusted relying party

A front-end that talks to the same external account with its own client
libraries -- a developer portal whose GitHub plugins carry their own GitHub
clients -- needs the person's access token in its own hands, not a tool call
through muster. The token broker (`tokenExchangeBroker`, see the
[configuration reference](../reference/configuration.md#brokered-token-exchange-tokenexchangebroker))
can release a subject-scoped grant to such a party through the standard
RFC 8693 token exchange it already serves, with a *grant target*:

```yaml
aggregator:
  oauth:
    server:
      trustedIssuers:
        - issuer: https://dex.example.com          # the portal's login IdP
          jwksUrl: https://dex.example.com/keys
          allowedAudiences: ["portal-client-id"]
      tokenExchangeBroker:
        clientAudiences:
          portal-backend: ["github"]
        brokerClients:
          portal-backend:
            clientCredentialsSecretRef:
              name: muster-broker-clients
        targets:
          github:
            # The issuer the MCPServer above pins with grantScope: subject.
            grantIssuer: https://github.com/login/oauth
```

The relying party's backend (a confidential broker client) POSTs to
`/oauth/token` with `grant_type=urn:ietf:params:oauth:grant-type:token-exchange`,
the person's ID token as `subject_token` and `audience=github`, and receives:

```json
{"access_token": "ghu_…", "issued_token_type": "urn:ietf:params:oauth:token-type:access_token",
 "token_type": "Bearer", "expires_in": 27540}
```

- The person is the subject token's own `sub` -- the Dex subject the grant
  was filed under when the person connected the server -- regardless of a
  `subjectClaim` mapping the trusted issuer may carry for on-behalf-of
  exchanges.
- The access token is the person's, refreshed first when it is due;
  `expires_in` is its remaining lifetime. The refresh token never leaves
  muster: the relying party re-exchanges when the token runs out, and each
  release is audited (`subject_grant_released`) like every exchange.
- A person without a grant gets `invalid_target`. The relying party then
  sends the person through muster's connect once: `core_auth_login` for the
  server answers the sign-in URL, and the start endpoint's `redirect=`
  parameter (`oauth.mcpClient.postLoginRedirectAllowlist`) brings the
  browser back to the page that needed the token -- for an App the person
  already authorized at their login, GitHub redirects straight back without
  a prompt.
- `clientAudiences` gates which client may ask for which grant; a grant
  target takes none of the Dex exchange settings (`connectorId`, `scopes`,
  `clientCredentialsSecretRef`), and a target is either a grant target or a
  Dex exchange target, never both.
- Signing out (`core_auth_logout` on any server of the issuer, "sign out
  everywhere") revokes the grant for the relying party as well: its next
  exchange answers `invalid_target`.

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
