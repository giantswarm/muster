# Configuration reference

Complete reference for configuring muster system settings, resources, and behavior.

## Overview

muster uses a file-based configuration system with YAML files organized in a structured directory hierarchy. Configuration is loaded from `~/.config/muster/` by default, or from a custom path specified with `--config-path`.

### Configuration Philosophy

- **Simple**: Direct YAML files that are easy to edit
- **Predictable**: Standard directory structure with clear separation
- **Flexible**: Support for both API-driven and manual file editing

## Configuration Directory Structure

```
~/.config/muster/
├── config.yaml              # Main system configuration
├── mcpservers/              # MCP server definitions
│   ├── kubernetes.yaml
│   ├── github.yaml
│   └── prometheus.yaml
├── workflows/               # Workflow definitions
│   ├── deploy-app.yaml
│   └── backup-database.yaml
└── status/                  # Written by muster: the status it records per definition
    ├── mcpservers/          # status/mcpservers/<name>.yaml for mcpservers/<name>.yaml
    └── workflows/
```

The definition files are yours: muster reads them and writes them only through
`core_mcpserver_create`/`core_mcpserver_update` and their workflow counterparts. The status it
observes for a definition (state, last error, last connection) goes to `status/`, at the
definition's own relative path, never into the definition file -- so an edit you make is
never overwritten by a status update, and a `status:` block still present in a definition
file from an older muster is ignored. `status/` is muster's; leave it out of version control.

## Main Configuration File

### Location
- **Default**: `~/.config/muster/config.yaml`
- **Custom**: Specified via `--config-path` flag

### Structure

```yaml
# ~/.config/muster/config.yaml
aggregator:
  port: 8090                    # Server port (default: 8090)
  host: "localhost"             # Bind address (default: localhost)
  transport: "streamable-http"  # MCP transport (default: streamable-http)
  enabled: true                 # Enable aggregator (default: true)

namespace: "default"            # Kubernetes namespace for CR discovery (default: default)
```

## Configuration Fields Reference

### Top-Level Configuration

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `namespace` | `string` | `"default"` | Kubernetes namespace for discovering MCPServer and Workflow CRs: muster's namespace |
| `mcpServersInAllNamespaces` | `bool` | `false` | In Kubernetes mode, watch MCPServer CRs of every namespace and serve those of `namespace` and of the organization namespaces (names that start with `org-`); a server in any other namespace is not served. Workflow CRs, events and the broker's default Secret namespace stay in `namespace`. A server of another namespace is keyed `<namespace>/<name>`, its tools are exposed as `x_<namespace>_<toolPrefix or name>_<tool>`, and it may not set `auth.requiredAudiences`, `auth.forwardToken`, `auth.forwardIdentity`, `auth.tokenExchange`, `family` or `auth.type: sigv4`. In `namespace`, a server name or `toolPrefix` may not start with `org-`. Such a server signs in through `/oauth/ns/<namespace>/callback` with a client of its own namespace, and its sign-in is filed under its namespace and issuer and goes when the namespace's servers go; its pin's client Secret is read through a Role in its namespace with `get` on that Secret alone ([Servers outside muster's namespace](../how-to/connecting-non-rfc9728-mcp-servers.md#servers-outside-musters-namespace)). A person sees a server only when Kubernetes RBAC lets them `list` `mcpservers.muster.giantswarm.io` in its namespace, `namespace` included ([Who sees which MCP server](#who-sees-which-mcp-server)). Needs `aggregator.oauth.server` with the `dex` provider; muster refuses to start otherwise. Helm: `muster.mcpServersInAllNamespaces`. |
| `kubernetes` | `bool` | `false` | Enable Kubernetes CRD mode. When `true`, uses Kubernetes CRDs for resource storage and requires the apiserver: if the Kubernetes client cannot be created at startup (apiserver unreachable, muster CRDs not installed), `muster serve` retries for about half a minute and then exits with an error instead of falling back to the filesystem, so the kubelet restarts it. When `false`, uses filesystem YAML files. The Helm chart sets this to `true` by default. |
| `aggregator` | `AggregatorConfig` | see below | Aggregator service configuration |
| `auth` | `AuthConfig` | see below | Authentication settings for CLI |
| `toolsetPresets` | `map[string]Preset` | `{}` | Toolset presets an agent's `X-muster-Toolset` header can name as `preset:<name>`; `read-only`, `none` and `full` are built in and cannot be redefined. See [Toolsets](toolsets.md). |
| `secretBinding` | `SecretBindingConfig` | `{}` | Secrets muster never reads for an MCPServer. See [Protected Secrets](#protected-secrets). |

### Protected Secrets

muster never reads for an MCPServer (`headersFrom`, `auth.tokenExchange` and `auth.authorizationServer` `clientCredentialsSecretRef`) a Secret listed here, whoever writes the MCPServer. Which Secrets an MCPServer may name at all is the chart's admission policy: those its writer may `get` ([CRD reference](crds.md#secret-references)).

```yaml
secretBinding:
  protectedSecrets: [muster-oauth]
```

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `protectedSecrets` | `[]string` | `[]` | Secrets of `namespace` no MCPServer may read. The chart lists every Secret it mounts and the ingress TLS Secrets. muster also refuses Secrets of the Helm release, TLS, ServiceAccount token, bootstrap token and image pull types. Secrets named here in `aggregator.providerApps` or `tokenExchangeBroker` are not protected: an MCPServer may share one. Helm: `muster.secretBinding.protectedSecrets` (added to the chart's list). |

A change takes effect when muster restarts: credentials already pinned stay for the process's lifetime.

### Who sees which MCP server

With `mcpServersInAllNamespaces`, muster lists the MCPServer objects as the
caller, with the session's own Dex ID token, once per namespace that holds
servers, and keeps the answer for a few seconds per token. The caller sees the
servers of every namespace where the Kubernetes API lets them `list`
`mcpservers.muster.giantswarm.io`: their organizations' namespaces, and
`namespace` through the binding the installation gives every signed-in person
(agent-platform's `agent-platform:mcpserver-viewer`). muster's ServiceAccount
gets no new right.

- A server the caller may not see is absent: from the tool, resource and prompt
  listings, the servers that wait for a sign-in, `auth://status` and
  `core_auth_status`, `core_mcpserver_list` and `_get`, `core_service_list` and
  `_status`, a family's members and `core_events`. A call to its tools,
  `core_service_start`, `_stop`, `_restart` and `core_auth_login` for it answer
  as for a server that does not exist.
- muster never connects a session to such a server: the SSO fan-out, the
  on-demand connect of a tool call and the adoption of a person's grant skip it,
  so no token is forwarded to it or exchanged for it.
- A session without an ID token, or whose token the API server does not accept,
  sees no server at all, not even those of `namespace`, and its calls say to sign
  in again. muster's login asks for `writesAsCaller.kubernetesAudience`, so a new
  login carries the audience the API server needs. Where no server in
  `namespace` asked for that audience before, every session signs in to muster
  again once.
- Using a server still needs the server's own authorization: a sign-in, or SSO
  for a server in `namespace`.
- A server outside `namespace` that the caller sees but whose sign-in cannot
  start yet (muster may not read its pin's client Secret, or has not connected
  to it) is answered by `core_auth_login` with "the server's sign-in is not
  ready" and the reason, not as a missing server. muster reads such a Secret
  only through a Role in the server's namespace that gives its ServiceAccount
  `get` on that Secret alone ([Its client Secret](../how-to/connecting-non-rfc9728-mcp-servers.md#servers-outside-musters-namespace)).
- The sign-ins of such a server go with its namespace's servers: a server that
  appears later in a namespace of the same name starts with none of the old
  namespace's tokens, grants, client registrations or flows.

### Toolset Presets

Presets are named selections of the tool catalogue that a request's `X-muster-Toolset` header
(or the `filter_tools` `toolset` argument) references as `preset:<name>`. They are evaluated
against the live catalogue on every request. Each rule sets exactly one of `tool`, `pattern`,
`server`, `workflow`, `readOnly: true`, `preset` (composition, include only) or `label`
(presets only; see [Toolsets](toolsets.md)).

```yaml
toolsetPresets:
  infrastructure:
    description: Every infrastructure server, without deletes
    include:
      - server: mcp-kubernetes
      - server: mcp-prometheus
    exclude:
      - pattern: "*_delete"
```

`read-only`, `none` and `full` are built in; configuration that redefines one, sets a rule with
no key or more than one, uses an invalid `pattern`, composes an unknown preset or cycles fails
`muster serve` at startup with the preset named. The Helm chart exposes this block as
`muster.toolsetPresets`.

### Aggregator Configuration

The aggregator manages the unified MCP interface and tool aggregation.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `port` | `int` | `8090` | Port for the aggregator HTTP/WebSocket server |
| `host` | `string` | `"localhost"` | Host address to bind the server to |
| `transport` | `string` | `"streamable-http"` | MCP transport protocol |
| `enabled` | `bool` | `true` | Whether to enable the aggregator service |

#### Transport Options

| Transport | Description | Use Case |
|-----------|-------------|----------|
| `streamable-http` | HTTP with streaming support | **Recommended** - Most compatible |
| `sse` | Server-Sent Events | Real-time updates |
| `stdio` | Standard I/O | Command-line clients |

#### Admin Listener

`aggregator.admin` starts a small web UI for sessions on a separate listener. It has no
authentication of its own, so it is bound to the loopback address and reached with
`kubectl port-forward` or from the host. See [HTTP endpoints](api.md#admin-listener) for
the pages it serves.

```yaml
aggregator:
  admin:
    enabled: true          # default: false
    port: 9999             # default: 9999
    bindAddress: 127.0.0.1 # default: 127.0.0.1; a wider bind exposes an unauthenticated UI
```

#### Credential Provider for Substrate's Egress Gateway

`aggregator.credentialProvider` makes muster a credential provider of Substrate's egress
gateway: a sandbox's git and gh requests to a provider's hosts are sent with the access
token of the person who started the Session, without the token ever reaching the sandbox.
The data path stays the gateway's; muster only answers whose token, and only to the gateway.

- **Session bindings.** The component that starts a Session calls muster as the person
  (their bearer, validated like every request on the aggregator port):
  `PUT /session-bindings` with `{"actor": "spiffe://substrate-actor.local/actor/<atespace>/<actor>",
  "session": "<namespace>/<name>", "providers": ["github"], "toolset": ["preset:read-only"]}`
  binds the Session's sandbox actor to the caller (`204`; `403` when the actor is bound to
  another person; `400` for a provider that is not releasable or a toolset that is not one;
  `toolset` is optional, see [the toolset bound to a Session](toolsets.md#the-toolset-bound-to-a-session)), and `DELETE /session-bindings?actor=<id>` removes the caller's
  binding (`204`, also when there is none; `403` for another person's). A binding lives for
  `bindingTTL` after its last bind or answered fetch, in Valkey when muster runs on Valkey.
- **FetchSecret.** On its own gRPC port, muster serves `CredentialProvider.FetchSecret` over
  mutual TLS and answers only a client certificate that chains to `clientCAFile` and names
  `gatewayIdentity`. For `ate-secret://<authority>/<provider>/git` it answers
  `base64("<user>:<token>")` (the gateway's rule adds `Basic `), `<user>` the provider's
  `gitUsers` entry or `x-access-token`, GitHub's; a GitLab instance sets `oauth2`, for
  `.../<provider>/api` the token itself (the rule adds `Bearer `), when the actor is bound,
  the provider is in its binding and listed in `releasable`, and the person holds the grant.
  Anything else is refused (`PERMISSION_DENIED`, or `NOT_FOUND` without a grant). Only the
  access token leaves muster, never the refresh token. Every answer and refusal is logged
  (`credential_released`, `credential_refused`: person, Session, actor, provider, use).
- **Freshness.** muster refreshes a subject-scoped grant 15 minutes before it expires, so a
  token the gateway caches (five minutes per actor and URI) is never used past its expiry.
  A disconnected grant or a removed binding is refused at the gateway's next fetch.
- **Actor bearer.** With `actorBearer.enabled`, a bound Session's sandbox calls muster itself
  as its person, with no credential in the sandbox: the gateway's rule for the muster host
  injects the actor's JWT (`actor_jwt`, audience `actorBearer.audience`), and muster serves an
  MCP request (`/mcp`, `/sse`, `/message`) whose bearer names `actorBearer.issuer` as the
  person the actor is bound to. The JWT must be signed by a key of the issuer's key set
  (`jwksUrl`, default `<issuer>/openid/v1/jwks`), unexpired, for that audience, with the
  subject `actor/<atespace>/<actor>`; the binding must list the reserved provider `muster`,
  which a bind accepts only with the actor bearer on and `FetchSecret` never releases. A bad
  token is `401`, an unbound actor or a binding without `muster` is `403`, a binding store
  outage is `503`. Every accepted or refused request is an audit event
  (`[AUDIT] action=actor_bearer outcome=success|failure`) naming the person's truncated subject,
  the actor, the bound Session and its bound toolset, and, on a refusal, the reason. All requests of one actor are
  one muster session, and every accepted request restarts the binding's TTL. The actor JWT is
  never forwarded downstream. Servers that forward (`forwardToken`, `forwardIdentity`) or
  exchange (`tokenExchange`) the person's ID token get the person's own login ID token instead:
  the one that lives longest among the person's live muster logins (the CLI's, the portal's
  forwarded one), read on every request, so a login's sign-out or revocation takes its token
  out at once. When none is live, muster refreshes the person's logins it serves at the
  identity provider, at most once every 30 seconds per person; without any, those servers
  stay unconnected for the actor.

```yaml
aggregator:
  credentialProvider:
    enabled: true                    # default: false; needs oauth.server
    listenAddress: ":50051"          # default
    serverCredBundle: /run/servicedns.podcert.ate.dev/credential-bundle.pem
    clientCAFile: /run/podidentity.podcert.ate.dev/trust-bundle.pem
    gatewayIdentity: spiffe://cluster.local/ns/substrate/sa/substrate-atenet-egress
    authority: muster.giantswarm.io  # default
    bindingTTL: 336h                 # default: 14 days, the Session idle TTL
    releasable:                      # provider instance -> grant issuer (grantScope: subject)
      github: https://github.com/apps/my-workspaces-app
      gitlab: https://gitlab.example.com
    gitUsers:                        # releasable instance -> git user name (default: x-access-token)
      gitlab: oauth2                 # GitLab accepts an OAuth token over git only as oauth2
    actorBearer:
      enabled: true                  # default: false
      issuer: https://idp.substrate.svc   # ate-api's --actor-jwt-issuer
      audience: https://muster.example.com
      caFile: /run/servicedns.podcert.ate.dev/trust-bundle.pem
      allowPrivateIPJWKSHosts: [idp.substrate.svc]
```

The Helm chart renders this from `muster.credentialProvider` and mounts Substrate's pod
certificates; register muster with Substrate as
`credentialProvider.additionalProviders: [{uriAuthority: muster.giantswarm.io, host: <muster service>.<namespace>.svc:50051}]`.

#### Provider Apps

`aggregator.providerApps` lists the OAuth App of each provider instance a person grants once
in muster, apart from any MCPServer: a GitHub App, a GitLab OAuth application, one per
instance. GitHub and GitLab, or github.com and a GitHub Enterprise Server, can be granted
side by side at one installation.

- **Granting.** `core_auth_login app=<name>` answers the App's sign-in link, or that muster
  already holds the person's grant. The grant belongs to the person (`grantScope: subject`):
  one sign-in serves every session and every server that receives it. `core_auth_logout
  app=<name>` revokes the grant at the App's revocation endpoint and drops it; no other grant
  is touched.
- **Attaching.** An MCPServer in muster's namespace that connects as the person
  (`forwardToken: true` or `forwardIdentity: true`) names the Apps whose grants it receives in
  `spec.auth.providerApps`. On every request muster sends the access token of each named App
  the person granted in the header `X-Muster-Grant-<app>`; an App the person has not granted
  sends no header.
- **Asking.** A tool result whose `structuredContent` is `{"status": "grant_required",
  "apps": ["gitlab"]}` is answered `auth_required` with each named App's sign-in link
  (`structuredContent.apps[].authUrl`). Every other result is returned unchanged.
- **Releasing.** Every App is releasable to the egress gateway under its name, with its
  `gitUser`, next to any `credentialProvider.releasable` entry (one that names the App
  differently fails the start). Each App's issuer is pinned from muster's start, so a grant is
  released whether or not any MCPServer has answered.
- **Existing grants.** Grants are filed under the App's `issuer`, as an MCPServer pinned with
  `grantScope: subject` files them. An App that names the issuer of such a registration finds
  the grants people already hold; its pin wins over the MCPServer's.

```yaml
aggregator:
  providerApps:
    - name: github                   # lower-case letters, digits, dashes
      issuer: https://github.com/apps/my-workspaces-app
      authorizationEndpoint: https://github.com/login/oauth/authorize
      tokenEndpoint: https://github.com/login/oauth/access_token
      expectedIssuer: https://github.com/login/oauth
      clientCredentialsSecretRef:
        name: github-workspaces-app  # namespace defaults to muster's
    - name: gitlab
      issuer: https://gitlab.example.com   # discovered
      revocationEndpoint: https://gitlab.example.com/oauth/revoke
      clientCredentialsSecretRef:
        name: gitlab-workspaces-app
      scopes: api read_user
      gitUser: oauth2
    - name: chatgpt
      flow: chatgptDeviceCode        # OpenAI's device-code sign-in
```

The Helm chart renders this from `muster.providerApps`; it needs `muster.oauth.mcpClient`.

##### A person's ChatGPT subscription (`flow: chatgptDeviceCode`)

An App with `flow: chatgptDeviceCode` connects a person's ChatGPT subscription with
OpenAI's device-code sign-in, the one `codex login --device-auth` runs. A server cannot take
codex's browser sign-in, which redirects to a listener on the person's machine.

- **Defaults.** `issuer: https://auth.openai.com`, the Codex CLI's public client
  (`clientID: app_EMoamEEZ73f0CkXaXp7hrann`, no secret), the endpoints
  `<issuer>/oauth/authorize` and `<issuer>/oauth/token`, and the scope codex signs in with.
  A `clientCredentialsSecretRef` is refused: the client is public.
- **Connecting.** `core_auth_login app=chatgpt` asks OpenAI for a one-time code and answers
  it with OpenAI's device page (`structuredContent`: `status: pending`, `userCode`,
  `verificationUrl`, `expiresAt`). muster polls OpenAI at the interval OpenAI names, redeems
  the authorization code it is handed with its PKCE verifier, and files the tokens as the
  person's grant. A code not entered within 15 minutes is given up. While a code is
  outstanding, `core_auth_login` answers the same code.
- **Status.** `auth://status` and `core_auth_status` report the App `pending` with the code,
  the page and the code's expiry while one is outstanding, `connected` once the grant is
  filed, and `auth_required` otherwise, with `last_failure` saying why the last sign-in ended
  without a grant (the code expired; device-code sign-in is not enabled for the account,
  which OpenAI answers with 404 and the person enables in ChatGPT under Settings → Security,
  or their workspace admin in the workspace permissions). Reading the status never starts a
  sign-in. With `oauth.server.storage.type: valkey` the outstanding code, its poll lease
  and the last failure are in the shared store (the code encrypted with the store's key):
  every replica answers the same code, one replica polls it under a lease it renews every
  10 seconds, and when that replica stops, the next one asked about the code
  (`core_auth_login` or the status) polls it once the lease has lapsed, within 30 seconds. The grant is in the shared token store,
  where every replica finds it.
- **Refreshing and releasing.** The grant is refreshed with the same client under the
  cross-replica refresh lock: OpenAI rotates the refresh token on every use, so muster must
  be the login's only refresher. The access token's lifetime is read from its `exp`, since
  OpenAI's answer carries no `expires_in`. The credential provider releases the access token
  as `ate-secret://<authority>/chatgpt/api`; the refresh token never leaves muster. The
  ChatGPT account id is in the grant's ID token (`https://api.openai.com/auth` →
  `chatgpt_account_id`).
- **Disconnecting.** `core_auth_logout app=chatgpt` ends an outstanding code, on every
  replica, and drops the grant; OpenAI publishes no revocation endpoint for this client.
- **Encryption.** The chart refuses the App without `muster.oauth.server.encryptionKey`: a
  person's ChatGPT login is stored only encrypted.

### Auth Configuration

#### Session Duration

The `sessionDuration` field controls how long a user's session remains valid before
re-authentication is required. This sets the server-side refresh token TTL (the muster
refresh token's lifetime).

```yaml
aggregator:
  oauth:
    server:
      sessionDuration: "720h"  # 30 days (default)
```

| Value | Duration | Notes |
|-------|----------|-------|
| `720h` | 30 days | Default, aligned with Dex's `absoluteLifetime` |
| `168h` | 7 days | More restrictive for high-security environments |
| `2160h` | 90 days | Longer sessions (ensure Dex `absoluteLifetime` matches) |

> **Important:** muster uses a **rolling** refresh token TTL (reset on each token
> rotation), while Dex's `absoluteLifetime` is an **absolute** limit measured from the
> original login that does **not** reset on rotation. If you increase `sessionDuration`
> beyond Dex's `absoluteLifetime`, the effective session will still be limited by Dex --
> the next token refresh after the absolute lifetime expires will fail, forcing
> re-authentication even if muster's session estimate shows time remaining.
>
> Always ensure `sessionDuration` does not exceed Dex's `absoluteLifetime`.

#### Provider Token Refresh Threshold

muster holds each signed-in person's provider (Dex) token and hands it out as
is while it has more than `providerTokenRefreshThreshold` of lifetime left; a
token closer to its expiry is refreshed at the provider first. The threshold
therefore sets the minimum lifetime of the token muster forwards to downstream
servers (`forwardToken`, `forwardIdentity`, token exchange) and of the one a
client's refresh returns.

```yaml
aggregator:
  oauth:
    server:
      providerTokenRefreshThreshold: "25m"  # empty: the mcp-oauth default, 5m
```

Raise it when a caller needs a forwarded token that outlives a long operation:
an agent turn of 20 minutes whose tool calls all carry the token of its first
call needs a threshold of at least 20 minutes, or a refresh can return a token
with only a few minutes left and every tool fails mid-turn once it expires.

| Value | Behaviour |
|-------|-----------|
| empty | 5 minutes, the mcp-oauth default |
| `25m` | A handed-out token has at least 25 of Dex's 30 minutes left; muster refreshes at Dex at most every 5 minutes per person |

The value is a Go duration of at least `1s`; anything else fails startup.
Keep it below the provider's token lifetime (Dex `expiry.idTokens`, 30m by
default): at or above it, every token muster holds counts as expiring and
every request refreshes at the provider.

#### Access Token TTL

The access token TTL is not directly configurable; it defaults to 30 minutes
(`DefaultAccessTokenTTL`), matching Dex's `idTokens` expiry. The mcp-oauth library's
`capTokenExpiry` function automatically caps the effective access token lifetime to never
exceed the provider's token lifetime, so even if the default were higher, the actual
expiry would be capped to what Dex issues.

Access tokens are refreshed automatically using the refresh token -- users do not need to
re-authenticate when they expire. The CLI's `muster auth status` shows the current access
token expiry under "Expires" and the session duration under "Session".

For the full token lifecycle and how muster and Dex tokens interact, see the
[Security Configuration](../operations/security.md#token-lifecycle) guide.

The CLI's `muster auth status` displays an approximate session estimate based on the
default 30-day duration. Custom server-side values are not yet reflected in the CLI
estimate.

#### Resource Identifier

muster issues only opaque access tokens. muster is not an identity provider:
it never signs tokens, and downstream MCP servers validate identity against
dex (or their own IdP), never against muster.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `resourceIdentifier` | `string` | `""` | RFC 8707 canonical URI for this muster instance as a resource server. Access tokens carry this value in their `aud` claim; tokens bound to a different resource are rejected, preventing replay across resource servers sharing the same IdP. Defaults to `baseUrl` when empty. |

#### DPoP and Trusted Proxies

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `trustedProxyCIDRs` | `[]string` | `[]` | CIDRs from which `X-Forwarded-Proto` and `X-Forwarded-Host` headers are trusted for DPoP htu URL reconstruction. Required when muster runs behind a reverse proxy that terminates TLS. |

#### RFC 8693 Token Exchange

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `trustedIssuers` | `[]TrustedIssuerConfig` | `[]` | Trusted Dex issuers: their ID tokens are accepted as bearer tokens and as the broker's subject tokens. |

**TrustedIssuerConfig fields:**

| Field | Type | Description |
|-------|------|-------------|
| `issuer` | `string` | Expected `iss` claim value. |
| `jwksUrl` | `string` | JWKS endpoint. Independent of `issuer`. |
| `allowedAudiences` | `[]string` | Accepted `aud` values. Empty accepts any audience. |
| `allowedScopes` | `[]string` | Scope ceiling for tokens from this issuer. Nil means no restriction. |
| `allowPrivateIPJWKS` | `bool` | Allow `jwksUrl` to resolve to a private or loopback address, for a Dex on a private network. Prefer `allowPrivateIPJWKSHosts`. Emits a startup warning when set. Default: `false`. |
| `allowPrivateIPJWKSHosts` | `[]string` | Host-scoped alternative to `allowPrivateIPJWKS`: `jwksUrl` may resolve to a private IP only when its hostname is listed; every other host keeps the SSRF guard. |
| `subjectClaim` | `string` | Verified claim whose value becomes the canonical subject. Empty keeps `sub`. Use `email` for Dex, whose `sub` is opaque. A token without the claim is rejected. |
| `acceptedTypHeaders` | `[]string` | Accepted JWT `typ` header values. Empty keeps the RFC 9068 default (`at+jwt`). Dex sets no `typ` on ID tokens: use `[""]`. |

#### Brokered Token Exchange (`tokenExchangeBroker`)

Exposes muster's RFC 8693 token exchange to external confidential clients: a broker client POSTs a token-exchange request with an `audience` parameter to `/oauth/token` and receives a token minted by the audience's downstream Dex (muster issues no JWTs). Subject tokens are validated against `trustedIssuers`, so at least one issuer entry covering the broker client's tokens is required.

Policy enforced by the broker path (mcp-oauth): client authentication is mandatory and only confidential clients are accepted; audiences are gated by the per-client allowlist; no refresh tokens are issued (`expires_in` is bounded by the downstream token's expiry — clients re-exchange); DPoP is rejected.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `clientAudiences` | `map[string][]string` | `{}` | Per-client audience allowlist: broker client ID → audiences it may request. A miss returns `invalid_target`. |
| `targets` | `map[string]BrokerTargetConfig` | `{}` | Audience name (e.g. a cluster name) → downstream target: a Dex exchange (`dexTokenEndpoint`) or the release of the person's own grant (`grantIssuer`). |
| `brokerClients` | `map[string]BrokerClientConfig` | `{}` | Confidential broker clients seeded at startup, keyed by client id: `clientCredentialsSecretRef` (a Kubernetes Secret with `client-id`/`client-secret`) or `clientSecretFile` (a file holding the secret, for deployments without a Kubernetes Secret store), plus optional informational `scopes`. |
| `allowPrivateIP` | `bool` | `false` | Allow downstream token endpoints to resolve to private/loopback IPs. Reduces SSRF protection; internal deployments only. |

**BrokerTargetConfig fields (Dex exchange target):**

| Field | Type | Description |
|-------|------|-------------|
| `dexTokenEndpoint` | `string` | Downstream Dex token endpoint URL (HTTPS). Required unless `grantIssuer` is set. |
| `expectedIssuer` | `string` | Expected `iss` claim of the exchanged token. Derived from `dexTokenEndpoint` when empty. |
| `connectorId` | `string` | Downstream Dex OIDC connector that trusts the subject token's issuer. Required. |
| `scopes` | `string` | Space-separated downstream scopes (default: `openid profile email groups`). Kubernetes-bound audiences must include the Dex cross-client scope for the apiserver's client, e.g. `audience:server:client_id:dex-k8s-authenticator` — without it the exchanged token's `aud` is the exchange client only, which the kube-apiserver rejects. The client-supplied RFC 8693 `scope` parameter is intentionally ignored. |
| `clientCredentialsSecretRef` | `object` | Kubernetes Secret with the downstream exchange client credentials: `name` (required), `namespace` (defaults to the muster namespace), `clientIdKey` (default `client-id`), `clientSecretKey` (default `client-secret`). |

**BrokerTargetConfig fields (grant target):**

| Field | Type | Description |
|-------|------|-------------|
| `grantIssuer` | `string` | HTTPS issuer of an authorization server an MCPServer pins with `spec.auth.authorizationServer` and `grantScope: subject` (e.g. `https://github.com/login/oauth`). The exchange answers the person's own access token from that issuer -- the grant filed under the subject token's `sub` when the person connected the server -- refreshed first when it is due, with `expires_in` its remaining lifetime and never the refresh token. A person without a grant gets `invalid_target`. Mutually exclusive with `dexTokenEndpoint`; takes none of the exchange fields. See [Releasing a person's grant to a trusted relying party](../how-to/connecting-non-rfc9728-mcp-servers.md#releasing-a-persons-grant-to-a-trusted-relying-party). |

Example:

```yaml
aggregator:
  oauth:
    server:
      trustedIssuers:
        - issuer: https://dex.main.example.com
          jwksUrl: https://dex.main.example.com/keys
          allowedAudiences: ["portal-frontend"]
      tokenExchangeBroker:
        clientAudiences:
          portal-backend: ["cluster-a", "github"]
        targets:
          cluster-a:
            dexTokenEndpoint: https://dex.cluster-a.example.com/token
            connectorId: main-dex
            scopes: "openid profile email groups audience:server:client_id:dex-k8s-authenticator"
            clientCredentialsSecretRef:
              name: muster-token-exchange-cluster-a
          github:
            grantIssuer: https://github.com/login/oauth
```

#### Private-IP OIDC Discovery (Dex)

By default the SSRF guard rejects an OIDC issuer URL that resolves to a private or loopback address. On clusters where the public Dex hostname resolves to an RFC 1918 address (e.g. an Azure internal load balancer, or air-gapped environments), discovery fails with `context deadline exceeded` and the server starts in degraded mode.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `dex.allowPrivateIPOIDC` | `bool` | `false` | Allow the Dex issuer URL to resolve to a private/loopback IP during OIDC discovery. This is the discovery-path counterpart to `allowPrivateIPJWKS`. Emits a CWE-918 startup warning when set; only enable it when the issuer is genuinely fronted by an internal-only load balancer. |

#### Private-IP Token Exchange Endpoints

The same guard applies to the token endpoint of a remote Dex that muster exchanges tokens with (`spec.auth.tokenExchange.dexTokenEndpoint` on an MCPServer). When that Dex sits behind an internal-only load balancer, the exchange fails with a DNS-rebinding error although the endpoint is reachable.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `mcpClient.tokenExchange.allowPrivateIP` | `bool` | `false` | Allow a remote Dex token endpoint to resolve to a private/loopback IP. Implied by `--extra-ca-file` (in-cluster TLS endpoints). Emits a startup warning when set; only enable it when the remote Dex is genuinely fronted by an internal-only load balancer. TLS verification is unchanged. |

#### Private-IP CIMD Clients

The SSRF guard also covers Client ID Metadata Documents (CIMD): a `client_id` that is a URL is fetched by the OAuth server, and by default that URL must not resolve to a private, loopback or link-local address. On a cluster whose own hostnames resolve to an internal load balancer, a CIMD client hosted on the platform itself (klaus-gateway's `/auth/slack/client.json`, for example) is rejected with `invalid_client: ... client_id metadata URL resolves to private/internal IP address`. Lift the guard only for that host.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `allowPrivateIPClientMetadataHosts` | `[]string` | `[]` | Host-scoped allowance: a CIMD `client_id` URL may resolve to a private/loopback/link-local IP only when its hostname is listed (exact match, no port, no wildcard); every other host keeps the SSRF guard, at validation and at connection time. Emits a startup warning when set. Helm: `muster.oauth.server.allowPrivateIPClientMetadataHosts`. |
| `allowPrivateIPClientMetadata` | `bool` | `false` | Allow every CIMD `client_id` URL to resolve to a private/loopback/link-local IP. Prefer `allowPrivateIPClientMetadataHosts`; keep this for an installation where every hostname is private. Emits a startup warning when set. PKCE and redirect-URI validation are unchanged. Helm: `muster.oauth.server.allowPrivateIPClientMetadata`. |

The CIMD fetch verifies the client's TLS certificate against the system roots, plus the CA of `--extra-ca-file` when it is set (Helm: `muster.extraCaFile`), so a CIMD client served under an internal CA needs no further setting. The same pool verifies the JWKS of a private-IP Dex.

#### Private-IP Redirect URIs

A client's redirect URI is validated again when the authorization flow starts: a hostname that resolves to a private address is rejected with `redirect_uri: hostname resolves to private IP address (DNS rebinding protection)`, even after the client itself (for example a CIMD client allowed through `allowPrivateIPClientMetadata`) was accepted. On a cluster whose own hostnames resolve to an internal load balancer, every platform-hosted client's callback trips this check.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `allowPrivateIPRedirectURIs` | `bool` | `false` | Allow redirect URIs that are, or resolve to, private IP addresses. Emits a startup warning when set. Exact redirect-URI matching against the client's registration is unchanged. Helm: `muster.oauth.server.allowPrivateIPRedirectURIs`. |

#### Silent Re-Authentication (CLI Flag)

Silent re-authentication is controlled via CLI flags only, not configuration file.

By default, muster uses interactive authentication. If your IdP supports OIDC `prompt=none` (note: Dex does not), you can enable silent re-authentication with the `--silent` flag:

```bash
muster auth login --silent     # Attempt silent re-auth before interactive
muster agent --silent          # Enable silent auth for agent
```

When `--silent` is used:

1. If you have a previous session, muster opens the browser with OIDC `prompt=none`
2. If your IdP session is still valid, authentication completes without user interaction
3. If the IdP session has expired, muster falls back to interactive login

**Note:** Silent auth is disabled by default because Dex (the default IdP) does not support `prompt=none`. When silent auth fails with Dex, it causes two browser tabs to open.

**Security:** When enabled, silent re-authentication maintains full security:
- PKCE is enforced on every flow
- State parameter prevents CSRF attacks
- The IdP validates the session, not muster
- Any failure falls back to interactive authentication

### Example Configurations

#### Minimal Configuration
```yaml
# Uses all defaults (namespace: "default", aggregator defaults)
aggregator: {}
```

#### Development Configuration
```yaml
aggregator:
  port: 8091
  host: "0.0.0.0"
  transport: "streamable-http"
  enabled: true
```

#### Production Configuration (Kubernetes)
```yaml
namespace: "muster-system"      # Use dedicated namespace for muster CRs
kubernetes: true                # Use Kubernetes CRDs instead of filesystem
aggregator:
  port: 80
  host: "0.0.0.0"
  transport: "streamable-http"
  enabled: true
```

#### Multi-Tenant Configuration
```yaml
namespace: "team-alpha"         # Each team uses their own namespace
aggregator:
  port: 8090
  host: "localhost"
  transport: "streamable-http"
```

#### Writes-as-Caller

In Kubernetes mode, session-initiated MCPServer spec mutations
(`core_mcpserver_create` / `_update` / `_delete`) are written against the
Kubernetes API with the caller's own OIDC id_token as the bearer instead of
muster's ServiceAccount: the apiserver authenticates the real user, Kubernetes
RBAC authorizes the write, and the audit log records the true subject.
Reads, `core_mcpserver_validate`, and muster's own controller writes (status
reconciliation, service registration) are unaffected.

Service lifecycle actions on MCPServer-backed services are covered the same
way: `core_service_stop` writes `spec.suspended: true`, `core_service_start`
clears it (and also writes `spec.restartRequestedAt` when the service is
down), and `core_service_restart` writes `spec.restartRequestedAt` — all as
the caller, with the reconciler performing the actual start/stop/restart.
Lifecycle of muster's own internal services (e.g. the aggregator) keeps the
imperative path.

Workflow spec mutations (`core_workflow_create` / `_update` / `_delete`) go
through the same gate: the write happens with the caller's identity, and the
chart ships a `workflow-editor` Role/RoleBinding mirroring `mcpserver-editor`
(bound to `system:authenticated` by default; narrow
`rbac.workflowEditor.subjects` for admin-only workflow authoring).
`core_workflow_validate`, reads, and workflow execution are unaffected.

Caller-identity writes are always active in Kubernetes mode
(`kubernetes: true`); in filesystem mode there is no apiserver and mutations
write through the local client. The only knob is the audience override:

```yaml
writesAsCaller:
  kubernetesAudience: "dex-k8s-authenticator"  # Audience the session token must carry (default shown)
```

Preconditions: the login scope set must request the `kubernetesAudience`
cross-client audience, and the apiserver must trust the OIDC issuer. Sessions
whose token lacks the audience receive an actionable re-login error; callers
without RBAC permission receive a permission error naming the missing verb,
resource, and namespace.

## MCP Server Configuration

MCP servers can be configured through YAML files or Kubernetes CRDs. Each server requires:

```yaml
# Local server example
mcpservers:
  - name: filesystem-tools
    description: File system operations
    toolPrefix: fs
    type: stdio              # Server execution type
    autoStart: true
    command: ["npx", "@modelcontextprotocol/server-filesystem", "/workspace"]
    env:
      DEBUG: "1"

  # Streamable HTTP server example
  - name: api-server
    description: Remote API tools
    toolPrefix: api
    type: streamable-http
    url: "https://api.example.com/mcp"
    timeout: 30
    headers:
      Authorization: "Bearer token"

  # SSE server example
  - name: sse-server
    description: SSE-based MCP server
    toolPrefix: sse
    type: sse
    url: "https://api.example.com/sse"
    timeout: 45
```

#### MCP Server Fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `name` | `string` | ✅ | - | Unique server identifier |
| `description` | `string` | ❌ | - | Human-readable description |
| `toolPrefix` | `string` | ❌ | - | Tool name prefix |
| `type` | `string` | ✅ | - | Server type (`stdio`, `streamable-http`, or `sse`) |
| `autoStart` | `boolean` | ❌ | `false` | Auto-start server (stdio only) |
| `command` | `[]string` | ✅* | - | Command and args (*required for stdio) |
| `args` | `[]string` | ❌ | - | Command arguments (stdio only) |
| `env` | `map[string]string` | ❌ | `{}` | Environment variables (stdio only; refused on a remote server) |
| `url` | `string` | ✅* | - | Server URL (*required for streamable-http and sse) |
| `timeout` | `integer` | ❌ | `30` | Connection timeout in seconds |
| `progress.maxDuration` | `integer` | ❌ | `3600` | Seconds a tool call that reports progress may run in all; each progress notification restarts `timeout` up to it |
| `headers` | `map[string]string` | ❌ | `{}` | HTTP headers (streamable-http and sse only) |

### Workflow Configuration

**Location**: `workflows/*.yaml`

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: deploy-application
  namespace: default
spec:
  description: "Deploy application with health checks"
  args:                          # Workflow arguments
    appName:
      type: string
      required: true
      description: "Application name to deploy"
    environment:
      type: string
      default: "staging"
      description: "Target environment"
  steps:                         # Workflow steps
    - id: "build"
      tool: "build_application"
      args:
        name: "{{ .input.appName }}"
        env: "{{ .input.environment }}"
      store: true
      description: "Build the application"

    - id: "deploy"
      tool: "deploy_application"
      args:
        name: "{{ .input.appName }}"
        image: "{{ .results.build.image }}"
        env: "{{ .input.environment }}"
      condition:
        fromStep: "build"
        expect:
          success: true
      description: "Deploy to target environment"

    - id: "health-check"
      tool: "health_check"
      args:
        url: "{{ .results.deploy.url }}"
      allowFailure: false
      description: "Verify deployment health"
```

#### Workflow Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `description` | `string` | ❌ | Human-readable description |
| `args` | `map[string]ArgDefinition` | ❌ | Workflow argument schema |
| `steps` | `[]WorkflowStep` | ✅ | Sequence of execution steps |

#### Workflow Step Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | `string` | ✅ | Unique step identifier |
| `tool` | `string` | ✅ | Tool name to execute |
| `args` | `map[string]any` | ❌ | Tool arguments (supports templating) |
| `condition` | `WorkflowCondition` | ❌ | Execution condition |
| `store` | `bool` | ❌ | Store result for later steps |
| `allowFailure` | `bool` | ❌ | Continue on failure |
| `outputs` | `map[string]any` | ❌ | Output mappings |
| `description` | `string` | ❌ | Step documentation |

## Configuration Loading

### Loading Order

1. **Defaults**: Built-in default values
2. **Main Config**: `config.yaml` overrides defaults
3. **Resource Files**: Individual resource definitions

### Custom Configuration Path

Use `--config-path` to specify a custom configuration directory:

```bash
muster serve --config-path /etc/muster
muster list mcpserver --config-path ./project-config
```

### Environment-Specific Configuration

Create different configuration directories for different environments:

```bash
# Development
muster serve --config-path ~/.config/muster-dev

# Staging
muster serve --config-path ~/.config/muster-staging

# Production
muster serve --config-path /etc/muster-prod
```

## Configuration Validation

### Automatic Validation

muster validates configuration on startup and when resources are created:

- **Syntax**: YAML syntax validation
- **Schema**: Field types and required values
- **References**: Tool availability and dependencies

### Manual Validation

Check resource availability:

```bash
# Check specific resources
muster check mcpserver kubernetes
muster check workflow deploy-app
```

## Templating

### Template Syntax

Use Go template syntax for dynamic values. Workflow inputs are under `.input`,
stored step results under `.results` (the engine renders with `missingkey=error`,
so a bare `{{ .environment }}` errors at runtime):

```yaml
args:
  url: "https://{{ .input.environment }}.example.com"
  replicas: "{{ .input.replicas }}"
  config: "{{ .input.baseConfig }}/{{ .input.serviceName }}"
```

### Available Variables

In workflow templates, these context roots are available:

| Context | Reference | Description |
|---------|-----------|-------------|
| Workflow inputs | `.input.<arg>` | Values passed to the workflow |
| Step results | `.results.<step-id>` | Output of a previous `store: true` step (`.context.<step-id>` is an alias) |
| Loop/user variables | `.vars.<name>` | `forEach` item/index and other bound variables |

## CLI Commands

### Available Commands

| Command | Description |
|---------|-------------|
| `muster serve` | Start the muster aggregator server |
| `muster agent` | MCP client for the aggregator server |
| `muster create` | Create resources (service, workflow) |
| `muster get` | Get detailed information about resources |
| `muster list` | List resources |
| `muster start` | Start services or execute workflows |
| `muster stop` | Stop services |
| `muster check` | Check resource availability |
| `muster test` | Execute test scenarios |

### Resource Types

| Resource Type | Create | Get | List | Check | Start |
|---------------|--------|-----|------|-------|-------|
| `service` | ✅ | ✅ | ✅ | ❌ | ✅ |
| `mcpserver` | ❌ | ✅ | ✅ | ✅ | ❌ |
| `workflow` | ✅ | ✅ | ✅ | ✅ | ✅ |
| `workflow-execution` | ❌ | ✅ | ✅ | ❌ | ❌ |

## See Also

- [CLI Reference](cli/README.md) - Command-line interface documentation
- [CRDs Reference](crds.md) - Kubernetes resource specifications
- [MCP Tools Reference](mcp-tools.md) - Available tools and usage
- [API Reference](api.md) - HTTP and MCP API documentation
