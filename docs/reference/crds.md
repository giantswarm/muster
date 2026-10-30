# Custom resource definitions

Complete reference for muster's Kubernetes Custom Resource Definitions. These CRDs define the core resources managed by the muster system for orchestrating MCP servers, service lifecycle management, and workflow execution.

## Overview

muster provides these CRDs:

| CRD | API Version | Kind | Short Name | Purpose |
|-----|-------------|------|------------|---------|
| **MCPServer** | `muster.giantswarm.io/v1alpha1` | `MCPServer` | `mcps` | Manages MCP (Model Context Protocol) servers that provide tools |
| **Workflow** | `muster.giantswarm.io/v1alpha1` | `Workflow` | `wf` | Defines multi-step processes for automated task execution |
| **WorkflowExecution** | `muster.giantswarm.io/v1alpha1` | `WorkflowExecution` | `wfe` | Durable, immutable record of a single workflow run |

## MCPServer

MCPServer resources define and manage MCP (Model Context Protocol) servers that provide tools for various operations like Git, filesystem, database management, etc.

### Resource Definition

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: <server-name>
  namespace: <namespace>
  annotations:
    # Optional: Human-friendly name shown by UIs in place of metadata.name
    ui.giantswarm.io/display-name: "<Display Name>"
spec:
  # Required: Server execution type
  type: stdio|streamable-http|sse

  # Optional: Tool name prefix (applies to all types)
  toolPrefix: "<prefix>"

  # Optional: Family grouping. When set, the aggregator exposes tools as
  # x_<family.name>_<toolName> with a required parameter (named by
  # family.instanceArg) selecting which instance handles the call. Multiple
  # MCPServer CRs sharing the same family.name (for example, several kubernetes
  # MCP servers pointed at different clusters) appear to clients as a single
  # deduplicated tool surface. Both name and instanceArg are required when
  # family is set. A family tool groups by name and input schema, never by
  # description: members whose tool differs only in its own or its
  # arguments' descriptions (description, title, examples) share one tool,
  # which carries the most common description (a tie goes to the longest).
  # When members' input schemas accept different arguments, the largest
  # group keeps x_<family>_<tool> and the other members expose that tool
  # under their per-server names.
  family:
    name: "<family-name>"
    instanceArg: "<parameter-name>"  # e.g. management_cluster, country, model

  # Optional: Human-readable description
  description: "<description>"

  # For stdio servers: Auto-start behavior
  autoStart: false

  # For stdio servers: Command to execute (required when type: stdio)
  command: "<executable>"

  # For stdio servers: Command arguments (optional when type: stdio)
  args: ["<arg1>", "<arg2>"]

  # For remote servers: Server endpoint (required when type: streamable-http or sse)
  url: "https://api.example.com/mcp"

  # Optional: Environment variables (stdio servers only)
  env:
    KEY1: "value1"
    KEY2: "value2"

  # Optional: headers (remote servers). No credential: see headersFrom
  headers:
    X-MCP-Toolsets: "repos"

  # Optional: headers whose values muster reads from a Secret in the
  # MCPServer's own namespace when it connects (remote servers)
  headersFrom:
    - name: Authorization
      valueFrom:
        type: Secret
        name: "<secret-name>"
        key: "<key>"

  # Optional: budget of every operation in seconds, a tool call included (all types)
  timeout: 30

  # Optional: Authentication configuration (remote servers)
  auth:
    type: oauth|none          # Authentication type
    forwardToken: true        # Forward muster's ID token for SSO
    requiredAudiences:        # Audiences needed in forwarded token (e.g., for Kubernetes OIDC)
      - "dex-k8s-authenticator"

# Status is managed automatically by muster (via reconciliation)
status:
  state: running          # unknown|starting|running|stopping|stopped|failed
  health: healthy         # unknown|healthy|unhealthy|checking
  lastError: ""           # Any error from recent operations
  lastConnected: ""       # When the server was last successfully connected
  restartCount: 0         # Number of times the server has been restarted
  conditions: []          # Kubernetes standard conditions
```

### Server types and where `stdio` applies

`stdio` is CLI-only. A stdio server is started as a subprocess of the muster
process, so a muster running in Kubernetes mode rejects it rather than executing
arbitrary commands in its own pod under its ServiceAccount:

- `mcpserver_validate`, `mcpserver_create`, and `mcpserver_update` fail with a
  message naming the remote alternatives.
- A stdio MCPServer applied directly with `kubectl` reconciles to
  `status.state: Failed` and `status.lastError` explaining the rejection. No
  subprocess is started.

Filesystem mode — `muster serve` against a local config directory, which is what
the CLI runs — accepts and starts `stdio` servers unchanged. In Kubernetes, run
the MCP server as its own workload and register it with `streamable-http` or
`sse`.

### Field Reference

#### Annotations

| Annotation | Set by | Description |
|------------|--------|-------------|
| `ui.giantswarm.io/display-name` | The author, or `displayName` on `core_mcpserver_create` / `_update` | Human-friendly name UIs show in place of `metadata.name` (e.g. `Kagi Search` for `kagi-search`). Returned as `displayName` by `core_mcpserver_get` and `_list` |
| `ui.giantswarm.io/registered-by` | muster, on `core_mcpserver_create` | Subject of the authenticated caller that created the server. Returned as `registeredBy` |
| `ui.giantswarm.io/registered-by-email` | muster, on `core_mcpserver_create` | Email claim of that caller, when the token carried one. Returned as `registeredByEmail` |

`core_mcpserver_update` changes no annotation but the display name, and that only when `displayName` is given, so an annotation set with `kubectl` or GitOps survives an update.

#### Spec Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `type` | `string` | Yes | Execution method for the MCP server | Must be `stdio`, `streamable-http`, or `sse`; `stdio` is rejected in Kubernetes mode (see below) |
| `toolPrefix` | `string` | No | Per-server tool prefix used when `family` is unset. A server outside muster's namespace gets its namespace in front: `<namespace>_<toolPrefix or name>` | Pattern: `^[a-zA-Z][a-zA-Z0-9_-]*$`; in muster's namespace, neither it nor the name may start with `org-` |
| `family` | `object` | No | Family grouping for equivalent servers under a shared tool surface; a tool groups by name and input schema, never by description | `name` and `instanceArg` both required when set |
| `family.name` | `string` | Yes (in `family`) | Family identifier | Pattern: `^[a-zA-Z][a-zA-Z0-9_-]*$` |
| `family.instanceArg` | `string` | Yes (in `family`) | Name of the required parameter the LLM uses to select an instance (e.g. `management_cluster`, `country`, `model`) | Pattern: `^[a-zA-Z][a-zA-Z0-9_]*$` |
| `description` | `string` | No | Human-readable description | Max 500 characters |
| `autoStart` | `boolean` | No | Auto-start when system initializes | Default: `false`, only for stdio servers |
| `command` | `string` | Yes* | Executable path for stdio servers | Required when `type` is `stdio` |
| `args` | `[]string` | No | Command line arguments for stdio servers | Only for stdio servers |
| `url` | `string` | Yes* | Endpoint URL for remote servers | Required when `type` is `streamable-http` or `sse`; max 2048 characters; a user name or password in it is refused |
| `env` | `map[string]string` | No | Environment variables for stdio servers | Only for stdio servers; refused on a remote server |
| `headers` | `map[string]string` | No | HTTP headers sent with every request to a remote server, by the server-level client and by every person's session connection (their `Authorization` stays the session's). Everyone who can read the MCPServer can read them, so they hold no credential | Only for streamable-http and sse servers; max 32 entries; a non-empty `Authorization`, `Proxy-Authorization`, `Cookie` or `X-API-Key` (any case) is refused |
| `headersFrom` | `[]MCPServerHeaderFrom` | No | HTTP headers whose values muster reads from a Secret in the MCPServer's own namespace each time it connects; the place for a credential. The value never enters the MCPServer, muster's service data or a core tool's output. An entry wins over a `headers` entry of the same name. A person's session connection sends its own `Authorization`, so an `Authorization` entry reaches only muster's own connection | Only for streamable-http and sse servers; max 32 entries; Kubernetes mode only (filesystem mode has no Secret reader) |
| `timeout` | `integer` | No | Budget of every operation in seconds, a tool call included, for every type; for a remote server also the connect and the health probe | Min: 1, Max: 300, Default: 30 |
| `progress.maxDuration` | `integer` | No | Budget of a whole tool call in seconds. On a tool call, each progress notification from the server restarts `timeout`, up to this; muster sends the server a progressToken on every tool call, whether its caller asked for progress or not. Every other operation stays bounded by `timeout`. Equal to `timeout`, progress extends nothing | Min: `timeout`, Max: 3600, Default: 3600 |
| `auth` | `MCPServerAuth` | No | Authentication configuration | Only for streamable-http and sse servers |
| `suspended` | `boolean` | No | Desired lifecycle state: `true` stops the server's service and keeps it stopped; setting it back to `false` resumes it | Default: `false` |
| `restartRequestedAt` | `timestamp` | No | Requests a one-shot restart; processed by the reconciler's first attempt, whatever its outcome, which mirrors the value into `status.lastRestartedAt`. A failed attempt is not repeated by the reconciler: the service retries on its reconnect backoff (`status.nextRetryAfter`) | RFC 3339 timestamp |

#### MCPServerHeaderFrom Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `name` | `string` | Yes | HTTP header name, for example `Authorization` | 1 to 256 characters; unique in the list |
| `valueFrom.type` | `string` | Yes | Kind of object that holds the value | `Secret` |
| `valueFrom.name` | `string` | Yes | Name of the Secret, in the MCPServer's own namespace | There is no namespace field; its writer must be able to `get` it ([Secret references](#secret-references)) |
| `valueFrom.key` | `string` | Yes | Key in the Secret's data that holds the value | |

muster's ServiceAccount needs `get` on the Secret. The chart grants it in the release namespace and in each namespace listed in `rbac.additionalSecretNamespaces`; an MCPServer in another namespace needs that namespace listed there. A missing Secret or key fails the connect: the server is `Failed` and is retried on its reconnect backoff (`status.nextRetryAfter`) until the Secret exists. muster drops one trailing newline (`\n` or `\r\n`) of the value. A person's session connection sends its own `Authorization` and does not read the Secret of an `Authorization` entry.

#### Secret references

muster reads the Secrets that `headersFrom`, `auth.tokenExchange.clientCredentialsSecretRef` and `auth.authorizationServer.clientCredentialsSecretRef` name with its own ServiceAccount, and sends them to endpoints of the same spec. So the chart's ValidatingAdmissionPolicy `<fullname>-mcpserver-secret-references` admits an MCPServer only when its writer may `get` each of those Secrets, checked with the API server's authorizer:

- on every create, and on every update that changes `spec` (a new Secret, a new `url`, a new token endpoint). An update that leaves `spec` alone, such as a label, a finalizer or the status, is admitted. A `spec` change to an MCPServer being deleted is checked too, since a finalizer can hold it there and muster serves it until it is gone.
- with the writer's own RBAC: a person writing through muster is checked as that person, a GitOps controller as its ServiceAccount.

One reference is exempt: an `auth.authorizationServer.clientCredentialsSecretRef` of an MCPServer in muster's namespace, without a namespace or naming muster's, whose name is `<server>-oauth-client-<5 of [a-z0-9]>` (`<server>` cut to 44 characters). `core_mcpserver_create` and `core_mcpserver_update` generate that name for `clientCredentials` and write the Secret as the caller, who needs only `create` on `secrets`. muster reads a Secret with such a name only when it has an `ownerReference` to the MCPServer's UID and its `muster.giantswarm.io/authorization-server` annotation matches the `issuer`, `authorizationEndpoint`, `tokenEndpoint` and `revocationEndpoint` the spec names now; otherwise the server reads `Failed` with `the Secret is not owned by the MCPServer` or `the Secret holds a client stored for another authorization server`. Changing those fields needs the client again (`clientCredentials`). A replaced client's Secret stays, owned by the server, until the server is deleted, and an editor can point the reference back to it: a client that leaked must be revoked at the provider.

A refused write reads `the writer of an MCPServer may name only Secrets they may get; it names <namespace>/<name>, ...`. Helm: `mcpServerSecretReferences.enabled` (default `true`). The policy needs Kubernetes 1.30 or later.

muster does not rely on the policy being there. In Kubernetes mode it reads none of these Secrets unless the binding named in `secretBinding.admissionPolicyBinding` ([configuration](configuration.md#protected-secrets)) has `validationActions` with `Deny` and no `matchResources`, and its policy exists with `failurePolicy: Fail`. It checks again at most a minute after a check that passed. Otherwise the server reads `Failed` with `the MCPServer Secret reference policy is not in force` and the reason in `status.lastError`, and is retried. Disabling the chart's policy therefore turns these Secret references off.

Whoever writes it, muster never reads for an MCPServer its own Secrets (`secretBinding.protectedSecrets`, [configuration](configuration.md#protected-secrets)) or Secrets of type `helm.sh/release.v1`, `kubernetes.io/tls`, `kubernetes.io/service-account-token`, `bootstrap.kubernetes.io/token`, `kubernetes.io/dockerconfigjson` and `kubernetes.io/dockercfg`: the server reads `Failed` with the reason in `status.lastError`. A Secret muster's configuration names (a provider App's client, a broker target's credentials) is an ordinary Secret here: an MCPServer may share it.

#### Credentials in the definition

A person who can read an MCPServer can read every field of it. So a credential goes in a Secret that `headersFrom` refers to, never in `headers`, `url` or `env`:

- The CRD refuses a non-empty value in an `Authorization`, `Proxy-Authorization`, `Cookie` or `X-API-Key` header (any case), a user name or password in `url`, and `env` on a remote server.
- muster makes the same check where it loads a server: `mcpserver_validate`, `mcpserver_create` and `mcpserver_update` fail, and a server written before the rule reconciles to `status.state: Failed` with the reason in `status.lastError`. Filesystem mode refuses only `env` on a remote server: its definitions are the files of the person who runs muster, and it has no Secret reader for `headersFrom`.

#### MCPServerAuth Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `type` | `string` | No | Authentication type | Must be `oauth` or `none` |
| `forwardToken` | `boolean` | No | Forward muster's ID token for SSO | Default: `false` |
| `forwardIdentity` | `boolean` | No | Send the session's ID token in the `X-Muster-Id-Token` header next to the pinned grant in `Authorization` | Default: `false`; requires `type: oauth` and `authorizationServer` |
| `providerApps` | `[]string` | No | Provider Apps (`aggregator.providerApps`) whose grants the server receives, one `X-Muster-Grant-<app>` header per App the person granted | Requires `forwardToken` or `forwardIdentity`; muster's namespace only |
| `requiredAudiences` | `[]string` | No | Additional audiences to request from IdP for SSO | Used with `forwardToken`, `forwardIdentity` or `tokenExchange` |
| `tokenExchange` | `TokenExchangeConfig` | No | RFC 8693 token exchange for cross-cluster SSO | See below |
| `authorizationServer` | `MCPServerAuthAuthorizationServer` | No | Pins the OAuth authorization server when the MCP server publishes no RFC 9728 metadata; with endpoints, a pre-registered client and a grant scope it describes one muster cannot discover or register with (GitHub) | See below |

**Note on `requiredAudiences`**: When using SSO (token forwarding or token exchange) with downstream servers that require specific audience claims (e.g., Kubernetes OIDC authentication), specify the required audiences here.

- **Token and Identity Forwarding** (`forwardToken: true`, `forwardIdentity: true`): muster requests these audiences from its upstream IdP (e.g., Dex) using cross-client scopes (`audience:server:client_id:<audience>`). The resulting multi-audience token is forwarded to downstream servers. Required audiences are collected at muster startup - if you add MCPServers with new audiences after users have authenticated, they must re-authenticate.
- **Token Exchange** (`tokenExchange.enabled: true`): The audiences are appended as cross-client scopes to the token exchange request to the remote IdP. This ensures the exchanged token contains the audiences needed by the downstream server on the remote cluster.

Example: `requiredAudiences: ["dex-k8s-authenticator"]`.

**Security**: Access control for `requiredAudiences` relies on two layers: (1) Kubernetes RBAC controls who can create/modify MCPServer CRDs, and (2) the IdP's cross-client configuration determines which audiences are allowed. Audience values must not contain whitespace characters and are validated before use.

#### TokenExchangeConfig Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `enabled` | `boolean` | No | Enable token exchange | Default: `false` |
| `dexTokenEndpoint` | `string` | Yes* | URL to access Dex's token endpoint (may be via proxy) | Must be HTTPS, required when enabled |
| `expectedIssuer` | `string` | No | Expected issuer URL in exchanged token's `iss` claim | Must be HTTPS if specified. Default: derived from `dexTokenEndpoint` by removing `/token` suffix |
| `connectorId` | `string` | Yes* | ID of OIDC connector on remote Dex | Required when enabled |
| `scopes` | `string` | No | Scopes to request for exchanged token | Default: `openid profile email groups` |
| `clientCredentialsSecretRef` | `ClientCredentialsSecretRef` | No | Reference to secret containing OAuth client credentials | See below; its writer must be able to `get` it ([Secret references](#secret-references)) |

**Security Note**: muster validates that the exchanged token's `iss` claim matches `expectedIssuer` using constant-time comparison. This prevents token substitution attacks in proxied access scenarios. When `expectedIssuer` is not specified, the issuer is derived from `dexTokenEndpoint` by removing the `/token` suffix (backward compatible). Set `expectedIssuer` explicitly when accessing Dex through a proxy where the access URL differs from Dex's configured issuer.

#### MCPServerAuthAuthorizationServer Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `issuer` | `string` | Yes | Issuer URL of the authorization server the sign-in runs against, and the identity the grants are filed under (the token-store key) | HTTPS; no trailing slash, query or fragment |
| `authorizationEndpoint` | `string` | No | Authorization endpoint of a server without an RFC 8414 / OIDC discovery document (GitHub); muster then performs no discovery and assumes S256 PKCE | Set together with `tokenEndpoint`; HTTPS |
| `tokenEndpoint` | `string` | No | Token endpoint of such a server | Set together with `authorizationEndpoint`; HTTPS |
| `expectedIssuer` | `string` | No | Issuer identifier the server puts in the RFC 9207 `iss` parameter of its authorization responses when that differs from `issuer`, which stays the grant key -- two GitHub Apps pinned under `https://github.com/apps/<slug>` each, both with `expectedIssuer: https://github.com/login/oauth` | Needs `authorizationEndpoint` and `tokenEndpoint`; HTTPS. Default: `issuer` |
| `revocationEndpoint` | `string` | No | RFC 7009 revocation endpoint `core_auth_logout` revokes a `subject` grant at before dropping it, so the provider stops honouring the tokens (GitLab: `https://gitlab.example.com/oauth/revoke`) | HTTPS. Default: the metadata's `revocation_endpoint`; none: the grant is only dropped, logged |
| `clientCredentialsSecretRef` | `ClientCredentialsSecretRef` | No | Secret holding a client registered with the server out of band (a GitHub App or OAuth App); used instead of muster's Client ID Metadata Document or a dynamic registration | See below; its writer must be able to `get` it ([Secret references](#secret-references)) |
| `grantScope` | `string` | No | Whom a token belongs to: `session` (the muster session that obtained it; for a forwarded or trusted-issuer bearer that is the principal, see below) or `subject` (the person; every session of the same person reuses it until `core_auth_logout` on this server) | `session` or `subject`. Default: `session` |
| `scopes` | `string` | No | OAuth `scope` parameter value (space-separated scope tokens); leave empty for a GitHub App | |

See [Connecting to MCP servers that don't publish RFC 9728 metadata](../how-to/connecting-non-rfc9728-mcp-servers.md) for the flows these fields configure.

**What a `session` grant is bound to.** For a client that signs in to muster itself (`muster auth login`, Claude Code), the session is that login's refresh-token family: another login of the same person starts signed out. For a forwarded or trusted-issuer bearer (an ID token a portal or an agent gateway presents), the session is the token's principal -- issuer, subject, authorized party (`azp`, or the audience when the token has none) and the actor chain -- not the token itself, so a refreshed token keeps its session. A fresh token of the same person through the same client is the same principal: a `session` grant obtained under one such token serves every later one until `core_auth_logout` on the server. Another person, another client (`azp`) or another actor chain is another session and starts signed out. `subject` differs in reaching every session of the person, across clients and login families.

**One description per issuer.** The MCPServers of one namespace that name the same `issuer` share its sign-in: one client, one set of endpoints, one grant scope. When several of them pin something (`authorizationEndpoint`, `clientCredentialsSecretRef` or `grantScope: subject`), the oldest MCPServer's description is used, then the first by name. Another server may name the issuer only with the same endpoints, `expectedIssuer`, `revocationEndpoint`, `clientCredentialsSecretRef` and `grantScope`; `scopes` may differ. A server that describes the issuer differently is refused: it reads `Failed`, its `Ready` condition has reason `AuthorizationServerConflict` and names the server whose description is used, `core_auth_login` answers the same, and it does not connect with that server's grants. It is retried with backoff and reads `Auth Required` once both describe the issuer the same way or the other server goes. When the oldest server goes, the next oldest one's description takes over. A sign-in started under a description that changed before its callback is refused, and the person signs in again; a rotated client secret does not end it.

#### ClientCredentialsSecretRef Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `name` | `string` | Yes | Name of the Kubernetes Secret | Must exist in the specified namespace |
| `namespace` | `string` | No | Namespace of the secret | Default: same as MCPServer. An MCPServer outside muster's namespace may name only its own namespace; muster refuses the server otherwise |
| `clientIdKey` | `string` | No | Key in secret for client ID | Default: `client-id` |
| `clientSecretKey` | `string` | No | Key in secret for client secret | Default: `client-secret` |

**Usage Note**: Client credentials are required when the remote Dex's token exchange endpoint requires client authentication. The secret should be created before the MCPServer and should contain the OAuth client ID and secret registered on the remote Dex.

**RBAC Requirements**: muster's service account requires `get` permission on `secrets` resources in the namespace where credentials are stored. Cross-namespace access (when `namespace` differs from the MCPServer's namespace) is allowed only for an MCPServer in muster's own namespace; ensure RBAC policies explicitly grant access. Cross-namespace secret access is logged as a warning to aid security auditing. For an MCPServer outside muster's namespace, muster refuses a `namespace` other than the server's own (in `tokenExchange` and in `authorizationServer`), so such a server cannot have muster read a Secret of muster's namespace and send it to a host the server names.

Example RBAC configuration for secret access:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: muster-secret-reader
  namespace: secrets-namespace
rules:
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get"]
  resourceNames: ["token-exchange-credentials"]  # Optional: restrict to specific secrets
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: muster-secret-reader
  namespace: secrets-namespace
subjects:
- kind: ServiceAccount
  name: muster
  namespace: muster
roleRef:
  kind: Role
  name: muster-secret-reader
  apiGroup: rbac.authorization.k8s.io
```

**Secret Rotation Best Practices**:

1. **Zero-downtime rotation**: Update the secret with new credentials while keeping old credentials valid on the remote Dex. muster loads credentials at connection time, so new connections will use updated credentials.

2. **Rotation procedure**:
   - Register new client credentials on the remote Dex (keeping old credentials active)
   - Update the Kubernetes secret with new credentials
   - Verify new connections succeed with new credentials
   - Revoke old credentials on the remote Dex

3. **Monitoring**: After rotation, monitor logs for authentication failures. muster logs token exchange attempts (with client_id, not secrets) for troubleshooting.

4. **Automation**: Consider using external secrets management (e.g., External Secrets Operator, Vault) for automated rotation.

#### Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `state` | `string` | Current infrastructure state (see table below) |
| `health` | `string` | Health status: `unknown`, `healthy`, `unhealthy`, `checking` |
| `lastError` | `string` | Error message from the most recent operation |
| `lastConnected` | `*metav1.Time` | When the server was last successfully connected |
| `restartCount` | `int` | Number of times the server has been restarted |
| `lastRestartedAt` | `*metav1.Time` | The `spec.restartRequestedAt` value most recently processed by the reconciler; a restart runs only when the two differ. A request is processed by one attempt whether it succeeded or failed: after a failed attempt the value is recorded, the failure shows in `state` and `lastError`, and the retries are the service's own (`nextRetryAfter`), not the reconciler's |
| `consecutiveFailures` | `int` | Connection attempts that failed in a row (remote servers); the server is `Failed` from the third on. Reset when an attempt reaches the endpoint |
| `lastAttempt` | `*metav1.Time` | When the last connection attempt was made |
| `nextRetryAfter` | `*metav1.Time` | When muster tries again. The wait doubles from 30 s per failure and is capped at 2 minutes (`MUSTER_MCPSERVER_MAX_BACKOFF`); absent while no retry is scheduled |
| `lastFailureHTTPStatus` | `int` | The HTTP status the endpoint answered the last failed attempt with, e.g. `504` from a gateway in front of the server or `404` from a backend whose route is not served yet. Absent when the attempt got no HTTP response (connection refused, DNS, timeout) or the last attempt succeeded |
| `conditions` | `[]metav1.Condition` | Standard Kubernetes conditions |

##### CRD State Values

The MCPServer CRD status reflects **infrastructure state** (network reachability and authentication status):

| CRD State | Meaning | Server Response |
|-----------|---------|-----------------|
| `Connected` | Server is reachable and authenticated: muster's own connection, or a session's | 200 OK |
| `Auth Required` | Server is reachable; a person signs in to it through muster before it connects | 401 Unauthorized |
| `Awaiting Session` | Server is reachable and served per session with the caller's own identity (`auth.forwardToken`, `auth.tokenExchange`); no session is connected | 401, or a 200 muster discards |
| `Connecting` | Attempting to establish connection | Connection in progress |
| `Disconnected` | Not connected (intentionally) | N/A |
| `Failed` | The endpoint does not answer the initialize, or the token exchange is broken for every caller; retried with backoff where a retry can help | Connection refused, DNS failure, timeout, HTTP 5xx, or a 4xx other than 401 (the path is not served yet); a missing credentials Secret; `invalid_client`, an unknown connector or a token endpoint that does not answer |
| `Running` | Process is running (stdio servers) | N/A |
| `Starting` | Process is starting (stdio servers) | N/A |
| `Stopped` | Process is stopped (stdio servers) | N/A |

**Key point**: A 401 Unauthorized response indicates the server IS reachable (at the network level), so the CRD state is `Auth Required` or `Awaiting Session`, not `Failed`. Which of the two depends on who authenticates: `Auth Required` names a sign-in a person completes through muster (`muster auth login --server <name>`, `core_auth_login`, the Dev Portal's Sign in); `Awaiting Session` names a server that is used with each caller's own identity, where there is no login to run.

**Servers served per session** (`auth.forwardToken`, `auth.tokenExchange`) are connected with the caller's own token; muster's token-less probe only establishes whether the endpoint answers, and muster holds no connection of its own. Their state follows these rules:

- `Failed` while the endpoint does not answer the initialize (connection refused, DNS, timeout, 5xx, or a 4xx other than 401 such as the 404 of a backend mid-rollout). The attempt is retried with backoff (`nextRetryAfter`); no manual step is needed once the path answers.
- `Failed` while the client credentials Secret of a token exchange (`tokenExchange.clientCredentialsSecretRef`) is missing or unreadable: every caller's exchange would fail the same way. `lastError` names the Secret; the attempt is retried with backoff and the server settles on its own once the Secret exists.
- `Awaiting Session` as soon as the endpoint answers the initialize -- with a 401, or with a 200 that muster discards because the connection is made per session -- and the credentials Secret (if any) loads, while no session is connected.
- `Connected` after a session connects with its own token, and for as long as a session holds a live connection. When the last session's connection is lost the server returns to `Awaiting Session`.
- A caller's exchange or forwarding that fails for reasons of the caller's own token (`invalid_grant`, an expired subject token, a rejected audience) is reported on that session (`MCPServerTokenForwardingFailed` / `MCPServerTokenExchangeFailed` events) and never changes the server's state. An exchange that fails for reasons every caller shares -- the token endpoint does not answer or answers 5xx, the client credentials are rejected (`invalid_client`), the connector is unknown -- puts the server in `Failed` with the exchange error as `lastError`. It leaves `Failed` on the next successful exchange, on a restart (`core_service_restart`, `spec.restartRequestedAt`) or on a spec change.

##### The Ready condition

`status.conditions` carries one condition, `Ready`: `True` while the server is reachable (`Running`, `Connected`, `Auth Required`, `Awaiting Session`), `False` otherwise. The reason is the state in one word (`Connected`, `AwaitingSession`, `AuthRequired`, `Connecting`, `Disconnected`, `Suspended` for a deactivated server, `Failed`) or, for a server `Failed` by its token exchange, the class of the failure: `TokenExchangeCredentials` (the Secret or `invalid_client`), `TokenExchangeEndpoint` (the token endpoint does not answer; probed again on the reconnect backoff), `TokenExchangeConnector`. The message explains the state; for `Failed` with a retry scheduled it ends with the next attempt (`Next attempt at 2026-09-21T12:43:00Z.`); for `Awaiting Session` it names the mechanism, the token endpoint and connector, and when the last exchange succeeded:

```
kubectl get mcpserver remote-mcp-kubernetes -o jsonpath='{.status.state}{"\n"}{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
Awaiting Session
Served per session: each caller's token is exchanged (RFC 8693) at https://dex.remote.example.com/token through connector "remote-oidc"; muster holds no connection of its own. No session is connected; the last exchange succeeded at 2026-09-21T12:41:03Z.
```

##### Session State in CLI

The `muster list mcpserver` command shows both infrastructure state and session-specific authentication state:

```
NAME          STATE             SESSION         TYPE
server-a      Connected         Authenticated   streamable-http
server-b      Auth Required     Pending Auth    streamable-http
server-c      Failed            -               streamable-http
server-d      Awaiting Session  -               streamable-http
```

- **STATE**: Infrastructure state from CRD (reachability and auth status)
  - `Connected`: Server is fully operational
  - `Auth Required`: Server is reachable; a person signs in to it through muster
  - `Awaiting Session`: Server is reachable and used with each caller's own identity; no session is connected
  - `Failed`: Server cannot be reached, or its token exchange is broken for every caller
- **SESSION**: Per-user authentication state (only shown when logged in to muster)
  - `Authenticated`: User has successfully authenticated to this server
  - `Pending Auth`: Server requires authentication, user has not authenticated
  - `Expired`: User's token has expired
  - `-`: No session state (server unreachable or no auth required)

This aligns with the output from `muster auth status`, which shows per-server session state.

> **Note**: Available tools are computed per-session at runtime based on user authentication. See [ADR 007](../explanation/decisions/007-crd-status-reconciliation.md) for details on session-scoped tool visibility.

### Examples

#### Basic Git Tools Server (Stdio)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: git-tools
  namespace: default
  annotations:
    ui.giantswarm.io/display-name: "Git Tools"
spec:
  type: stdio
  description: "Git tools MCP server for repository operations"
  autoStart: true
  command: "npx"
  args: ["@modelcontextprotocol/server-git"]
  env:
    GIT_ROOT: "/workspace"
    LOG_LEVEL: "info"
```

#### Python Tools with Prefix (Stdio)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: python-tools
  namespace: default
spec:
  type: stdio
  toolPrefix: "py"
  description: "Python-based MCP server with custom tools"
  autoStart: true
  command: "python"
  args: ["-m", "mcp_server.custom"]
  env:
    PYTHONPATH: "/usr/local/lib/python3.9/site-packages"
    DEBUG: "true"
```

#### Streamable HTTP Server (Remote)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: api-tools
  namespace: default
spec:
  type: streamable-http
  description: "Remote API tools server"
  url: "https://api.example.com/mcp"
  timeout: 30
  headers:
    X-API-Version: "v1"
  headersFrom:
    - name: Authorization
      valueFrom:
        type: Secret
        name: api-tools-token   # holds "Bearer <token>" under the key "authorization"
        key: authorization
```

#### SSE Remote Server (Remote)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: sse-tools
  namespace: default
spec:
  type: sse
  toolPrefix: "remote"
  description: "Server-Sent Events MCP server"
  url: "https://mcp.example.com/sse"
  timeout: 60
  headers:
    Accept: "text/event-stream"
  headersFrom:
    - name: Authorization
      valueFrom:
        type: Secret
        name: sse-tools-token
        key: authorization
```

#### OAuth-Protected Server with SSO Token Forwarding
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: mcp-kubernetes
  namespace: default
spec:
  type: streamable-http
  toolPrefix: "k8s"
  description: "Kubernetes MCP server with SSO authentication"
  url: "https://mcp-kubernetes.example.com/mcp"
  timeout: 30
  auth:
    type: oauth
    forwardToken: true           # Forward muster's ID token for SSO
```

When `forwardToken: true` is configured:
1. User authenticates to muster once via OAuth
2. When calling this MCP server, muster forwards the caller's validated bearer (or the session's upstream ID token)
3. The downstream server validates the token against the issuer it trusts (the identity provider's issuer and keys, for example Dex's, since muster's own access tokens are opaque and never forwarded)

The forwarded token is audience-scoped to muster's resource identifier, not to the individual backend: the same token is accepted by every `forwardToken` backend and by muster itself, so all `forwardToken` backends must be equally trusted. The token's nested `act` claim carries the delegation chain for backend authorization decisions.

Downstream servers must be configured to trust muster's client ID:
- **mcp-kubernetes**: Set `oauth.trustedAudiences: ["muster-client"]` in Helm values
- **inboxfewer**: Set `oauthSecurity.trustedAudiences: ["muster-client"]` in Helm values

#### Pinned Server that also receives the person's ID token
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: two-hats
  namespace: default
spec:
  type: streamable-http
  url: "https://two-hats.example.com/mcp"
  auth:
    type: oauth
    authorizationServer:          # the grant in Authorization
      issuer: "https://github.com/login/oauth"
      scopes: "repo"
    forwardIdentity: true         # the session's ID token in X-Muster-Id-Token
    requiredAudiences:
      - "dex-k8s-authenticator"
```

A server that acts for the person on two systems needs two of their credentials: the grant of its pinned authorization server for one (a GitHub App user token, say), and the person's own identity for calls back through muster for the other (their Kubernetes access). `forwardIdentity: true` gives it both on one registration:

- `Authorization` carries the pinned grant, exactly as without the field.
- `X-Muster-Id-Token` carries the session's upstream ID token: the token a `forwardToken` server would receive in `Authorization`. It is read on every request, so a refreshed token is sent on the next call.
- A session without an upstream ID token sends no `X-Muster-Id-Token`; a server without the field never receives it.
- `requiredAudiences` are requested at login as for a `forwardToken` server, so the forwarded ID token carries them.
- Changing the field resets the sessions' connections to the server, as a change of `forwardToken` does.

The CRD rejects the field without `type: oauth` and `authorizationServer`; filesystem mode and `core_mcpserver_validate` run the same check. `forwardToken` stays mutually exclusive with `authorizationServer`.

The forwarded ID token is not audience-scoped to the server, as with `forwardToken`: the same token is accepted by every backend that trusts its issuer and audiences, so a `forwardIdentity` server must be trusted as much as a `forwardToken` backend.

#### Cross-Cluster SSO with Token Exchange (RFC 8693)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: remote-cluster-k8s
  namespace: default
spec:
  type: streamable-http
  toolPrefix: "remote_k8s"
  description: "Kubernetes tools on remote cluster via token exchange"
  url: "https://mcp-kubernetes.remote-cluster.example.com/mcp"
  timeout: 30
  auth:
    type: oauth
    tokenExchange:
      enabled: true
      dexTokenEndpoint: "https://dex.remote-cluster.example.com/token"
      connectorId: "local-cluster-dex"
      scopes: "openid profile email groups"
```

When `tokenExchange.enabled: true` is configured:
1. User authenticates to muster once via OAuth (gets token from local cluster's Dex)
2. When calling this MCP server, muster exchanges the local token for one valid on the remote cluster
3. The remote Dex must have an OIDC connector configured that trusts the local cluster's Dex
4. The exchanged token is used to authenticate to the remote MCP server

The remote Dex must be configured with an OIDC connector:
```yaml
# On remote cluster's Dex
connectors:
  - type: oidc
    id: local-cluster-dex
    name: "Local Cluster"
    config:
      issuer: https://dex.local-cluster.example.com
      getUserInfo: true
      insecureEnableGroups: true
```

#### Token Exchange with Client Credentials

When the remote Dex requires client authentication for token exchange, you can reference a Kubernetes secret containing the client credentials:

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: remote-cluster-k8s-authenticated
  namespace: default
spec:
  type: streamable-http
  toolPrefix: "remote_k8s"
  description: "Kubernetes tools on remote cluster with authenticated token exchange"
  url: "https://mcp-kubernetes.remote-cluster.example.com/mcp"
  timeout: 30
  auth:
    type: oauth
    tokenExchange:
      enabled: true
      dexTokenEndpoint: "https://dex.remote-cluster.example.com/token"
      connectorId: "local-cluster-dex"
      scopes: "openid profile email groups"
      # Reference to secret containing OAuth client credentials
      clientCredentialsSecretRef:
        name: remote-cluster-token-exchange-credentials
        namespace: muster  # Optional, defaults to MCPServer namespace
        clientIdKey: client-id      # Optional, defaults to "client-id"
        clientSecretKey: client-secret  # Optional, defaults to "client-secret"
```

The referenced secret should be created before the MCPServer:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: remote-cluster-token-exchange-credentials
  namespace: muster
type: Opaque
stringData:
  client-id: muster-token-exchange
  client-secret: <your-client-secret>
```

The client credentials must be registered as a static client on the remote Dex:

```yaml
# On remote cluster's Dex
staticClients:
  - id: muster-token-exchange
    name: "muster Token Exchange"
    secret: <your-client-secret>
    # No redirect URIs needed for token exchange
```

#### Cross-Cluster SSO via Proxy (OAuth Token Exchange)
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: private-cluster-k8s
  namespace: default
spec:
  type: streamable-http
  toolPrefix: "private_k8s"
  description: "Kubernetes tools on a private cluster accessed via a proxy"
  url: "https://mcp-kubernetes.private-cluster.proxy.example.com/mcp"
  timeout: 60
  auth:
    type: oauth
    tokenExchange:
      enabled: true
      # Access URL goes through proxy
      dexTokenEndpoint: "https://dex.private-cluster.proxy.example.com/token"
      # Expected issuer is the actual Dex issuer (not the proxy URL)
      expectedIssuer: "https://dex.private-cluster.internal.example.com"
      connectorId: "management-cluster-dex"
```

When accessing Dex through a proxy (e.g., VPN, HTTP proxy):
- `dexTokenEndpoint`: The proxy URL used to reach Dex's token endpoint
- `expectedIssuer`: The actual issuer URL configured in Dex (used for token validation)

This is necessary because Dex's tokens contain the configured issuer URL in the `iss` claim, not the proxy URL used to access it. muster validates that the exchanged token's issuer matches `expectedIssuer` for security.

> **Warning**: When accessing Dex through a proxy, you **MUST** set `expectedIssuer` explicitly. If omitted, muster derives the expected issuer from `dexTokenEndpoint` (the proxy URL), which will cause token validation to fail because the token's `iss` claim contains the actual Dex issuer URL, not the proxy URL. This validation failure is intentional - it ensures you explicitly configure the expected issuer for proxied scenarios.

#### Troubleshooting Token Exchange

**Common Issues and Solutions:**

| Symptom | Likely Cause | Solution |
|---------|--------------|----------|
| `issuer mismatch: expected "proxy-url", got "actual-issuer"` | Missing `expectedIssuer` for proxied access | Set `expectedIssuer` to the actual Dex issuer URL shown in the error |
| `token exchange failed: connection refused` | Wrong `dexTokenEndpoint` URL | Verify the endpoint is reachable from muster's network |
| `token exchange failed: 401 Unauthorized` | Remote Dex doesn't trust local Dex | Configure OIDC connector on remote Dex (see example above) |
| `token exchange failed: invalid_grant` | Token expired or connector misconfigured | Check token lifetime and connector ID matches |

**Debugging Steps:**

1. **Verify configuration**: Check the MCPServer's token exchange config
   ```bash
   kubectl get mcpserver <name> -o yaml | grep -A10 tokenExchange
   ```

2. **Check muster logs**: Look for token exchange events
   ```bash
   kubectl logs -l app=muster | grep -i "token.*exchange"
   ```

3. **Verify remote Dex connectivity**: Test the token endpoint is reachable
   ```bash
   curl -s -o /dev/null -w "%{http_code}" https://<dex-endpoint>/.well-known/openid-configuration
   ```

4. **Check Kubernetes events**: muster emits events for token exchange
   ```bash
   kubectl get events --field-selector involvedObject.kind=MCPServer
   ```

**Events Reference:**
- `MCPServerTokenExchanged`: Token exchange succeeded
- `MCPServerTokenExchangeFailed`: Token exchange failed (check message for details)

### CLI Usage

```bash
# List all MCP servers
kubectl get mcpservers
# or using short name
kubectl get mcps

# Get detailed information
kubectl describe mcpserver git-tools

# Check logs
kubectl logs mcpserver/git-tools

# Create from file
kubectl apply -f mcpserver.yaml
```

---

## Workflow

Workflow resources define multi-step processes that can be executed to automate complex tasks like application deployment, data migration, or system configuration.

### Resource Definition

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: <workflow-name>
  namespace: <namespace>
spec:
  # Optional: Human-readable description
  description: "<description>"

  # Optional: Argument schema for workflow execution
  args:
    <arg_name>:
      type: string|integer|boolean|number|object|array
      required: true|false
      default: <default_value>
      description: "<description>"

  # Required: Workflow steps. Each step is exactly one of: a tool call (tool),
  # a loop (forEach), or a concurrent group (parallel).
  steps:
    # 1) A plain tool call
    - id: "<step_id>"
      tool: "<tool_name>"
      args:
        <key>: <value_template>      # e.g. "{{.input.namespace}}"
      condition:
        # Catalog of fields — set exactly ONE source (template, tool, or fromStep).
        # template: a boolean Go-template gate, stands alone:
        template: "{{ eq .input.env \"production\" }}"
        # ...or tool/fromStep, which REQUIRE an expect or expectNot block:
        tool: "<condition_tool>"
        args:
          <key>: <value>
        fromStep: "<step_id>"
        expect:
          success: true|false
          jsonPath:
            <path>: <expected_value>
        expectNot:
          success: true|false
          jsonPath:
            <path>: <unexpected_value>
      paginate:                       # tool steps only: follow the tool's pages
        itemsField: items             # where a page's list is (default "items")
        tokenField: continue          # where the next-page token is (default "continue")
        tokenArg: continue            # the argument it is passed in (default "continue")
        maxPages: 10                  # 1-1000 (default 10)
      jq: '[.items[] | {name: .metadata.name}]'   # reshape the result before it is stored
      output: true|false              # include this step's result in the returned document
      store: true|false               # deprecated alias for output
      allowFailure: true|false
      description: "<step_description>"

    # 2) A loop over a list; a body sub-step may itself be a forEach, one level deep
    - id: "<step_id>"
      forEach:
        items: "{{ .input.<list_arg> }}"  # must resolve to an array
        as: item                          # loop variable -> {{.vars.item}} (default "item")
        parallelism: 4                    # 1-50 iterations at once (default: one after another)
        steps:
          - id: "<sub_step_id>"
            tool: "<tool_name>"
            args:
              <key>: "{{ .vars.item.<field> }}"
          - id: "<nested_loop_id>"        # a nested loop: tool calls only in its body
            forEach:
              items: "{{ .results.<sub_step_id>.items }}"
              as: inner                   # must differ from the outer loop's
              parallelism: 8
              steps:
                - id: "<inner_step_id>"
                  tool: "<tool_name>"
      jq: '[.[] | .results]'              # reshapes the loop's per-iteration records

    # 3) A concurrent group (sub-steps run in parallel; siblings are independent)
    - id: "<step_id>"
      parallel:
        - id: "<sub_step_id>"
          tool: "<tool_name>"
        - id: "<sub_step_id>"
          tool: "<tool_name>"

  # Optional: best-effort cleanup/rollback steps run when the workflow fails
  # on a step that does not allow failure.
  onFailure:
    - id: "<sub_step_id>"
      tool: "<rollback_tool>"

  # Optional: a templated output template rendered once after all steps complete and
  # returned in place of the default response. Each leaf is a Go-template/sprig
  # expression evaluated against .input/.results/.vars; JSON structure (objects,
  # arrays, numbers) is preserved.
  output:
    <key>: "{{ .results.<step_id>.<field> }}"

# Status is managed automatically by muster (via reconciliation)
status:
  valid: true|false                  # Spec passes structural validation
  validationErrors: []               # Any spec validation error messages
  referencedTools: []                # Tools mentioned in steps (informational)
  stepCount: 0                       # Number of steps in the workflow
  conditions: []                     # Kubernetes standard conditions
```

### Field Reference

#### Spec Fields

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `description` | `string` | No | Human-readable description | Max 1000 characters |
| `args` | `map[string]ArgDefinition` | No | Argument schema for execution validation | - |
| `steps` | `[]WorkflowStep` | Yes | Sequence of workflow steps | Min 1 item |
| `onFailure` | `[]WorkflowSubStep` | No | Cleanup/rollback steps run when the workflow fails on a non-`allowFailure` step | - |
| `output` | `map[string]any` | No | Templated output template rendered after all steps complete, returned in place of the default response. Each leaf is evaluated against `.input`/`.results`/`.vars` with JSON structure preserved | - |

#### WorkflowStep Fields

A step is exactly one of: a tool call (`tool`), a loop (`forEach`), or a concurrent group (`parallel`).

| Field | Type | Required | Description | Constraints |
|-------|------|----------|-------------|-------------|
| `id` | `string` | Yes | Unique step identifier within workflow | Pattern: `^[a-zA-Z0-9_-]+$`, Max 63 chars |
| `tool` | `string` | No* | Name of the tool to execute | Mutually exclusive with `forEach`/`parallel` |
| `args` | `map[string]any` | No | Arguments for tool execution (supports templating) | - |
| `condition` | `WorkflowCondition` | No | Optional execution condition | - |
| `forEach` | `WorkflowForEach` | No* | Run a body of sub-steps once per list item | Mutually exclusive with `tool`/`parallel` |
| `parallel` | `[]WorkflowSubStep` | No* | Sub-steps executed concurrently | Mutually exclusive with `tool`/`forEach` |
| `paginate` | `WorkflowPaginate` | No | Follow the tool's next-page token and store every page's items as one list | Tool steps only |
| `jq` | `string` | No | jq program reshaping the step's result before it is stored: the tool's result, or a `forEach`'s per-iteration records. `$input` and `$vars` are in scope; it must yield one value | Must compile |
| `output` | `boolean` | No | Include this step's result in the returned document. Every step result is referenceable by later steps (`{{.results.<id>}}`) regardless of this flag | Default: `false` |
| `store` | `boolean` | No | Deprecated alias for `output`; kept for backwards compatibility | Default: `false` |
| `allowFailure` | `boolean` | No | Continue on step failure | Default: `false` |
| `description` | `string` | No | Human-readable step documentation | Max 500 characters |

*Exactly one of `tool`, `forEach`, or `parallel` must be set. This is enforced by the CRD at apply time (a CEL validation rule), so `kubectl apply` rejects a step that sets none or more than one.

> **Referencing vs. returning**: Every step's result is referenceable by later
> steps as `{{.results.<step_id>}}` without any flag. The `output` flag (and its
> deprecated `store` alias) only controls whether the step's result is included
> in the document returned to the caller. To shape that document further, use the
> workflow-level [`output` template](#workflow-output-template).
>
> **Migration**: A previously documented `outputs:` field never did anything and
> has been removed. The `store` flag still works as a backwards-compatible alias
> for `output`; prefer `output`.

#### WorkflowForEach Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `items` | `string` | Yes | Template expression resolving to an array, e.g. `"{{ .input.clusters }}"` or `"{{ .results.clusters.items }}"` |
| `as` | `string` | No | Loop variable name, exposed as `{{ .vars.<as> }}` (default `item`); the zero-based index is `{{ .vars.<as>_index }}` |
| `parallelism` | `integer` | No | Iterations run at once, 1–50 (default: one after another). Results, step records and the loop's records come out in item order either way |
| `steps` | `[]WorkflowSubStep` | Yes | Body executed once per item; a sub-step may be a nested `forEach` (`WorkflowNestedForEach`) |

After the loop, the `forEach` step's id holds one record per iteration that
ran, in item order: `{index, item, status, results: {<sub_step_id>: result},
error}` (`error` for a failed iteration). See
[Loops with forEach](../how-to/workflow-creation.md#loops-with-foreach).

#### WorkflowNestedForEach Fields

A `forEach` inside a `forEach` body, one level deep; not allowed in
`parallel` or `onFailure`. Its sub-steps (`WorkflowNestedStep`) are tool calls
with the `WorkflowSubStep` fields other than `forEach`. A nested sub-step's
result is kept per iteration as `{{.results.<id>_<outer>_<inner>}}`.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `items` | `string` | Yes | Template expression resolving to an array, typically an earlier sub-step's result |
| `as` | `string` | No | Loop variable name (default `item`); must differ from the outer loop's |
| `parallelism` | `integer` | No | Iterations run at once within one outer iteration, 1–50 |
| `steps` | `[]WorkflowNestedStep` | Yes | Tool calls executed once per item; ids must differ from the outer body's |

#### WorkflowPaginate Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `itemsField` | `string` | No | Where a page's list is, a dotted path (default `items`) |
| `tokenField` | `string` | No | Where a page's next-page token is, a dotted path (default `continue`) |
| `tokenArg` | `string` | No | The tool argument the token is passed in (default `continue`) |
| `maxPages` | `integer` | No | The most pages fetched, 1–1000 (default 10) |

The stored result is the last page with its list holding every page's items,
plus `pagination.pages` and `pagination.truncated`; a truncated result keeps
the token the next page would be fetched with.

#### WorkflowSubStep Fields

Used by `forEach.steps`, `parallel`, and `onFailure`. A sub-step is a tool
call or, in a `forEach` body only, a nested `forEach`.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `id` | `string` | Yes | Unique sub-step identifier |
| `tool` | `string` | No* | Name of the tool to execute |
| `forEach` | `WorkflowNestedForEach` | No* | A nested loop (only in a `forEach` body) |
| `args` | `map[string]any` | No | Arguments for tool execution (supports templating) |
| `condition` | `WorkflowCondition` | No | Optional execution condition |
| `paginate` | `WorkflowPaginate` | No | Follow the tool's pages (tool sub-steps only) |
| `jq` | `string` | No | jq program reshaping the sub-step's result before it is stored |
| `output` | `boolean` | No | Include this sub-step's result in the returned document (default `false`). The result is referenceable by later steps regardless of this flag. Inside `forEach`, each iteration is also addressable as `{{.results.<id>_<index>}}` (after the loop, the plain `{{.results.<id>}}` keeps the last iteration that ran it); a sub-step skipped by its `condition` has no result in that iteration, under either key. |
| `store` | `boolean` | No | Deprecated alias for `output`; kept for backwards compatibility (default `false`) |
| `allowFailure` | `boolean` | No | Continue on failure (default `false`). In a `forEach` body the iteration is reported as failed in the loop's records and the loop goes on |
| `description` | `string` | No | Human-readable documentation |

*Exactly one of `tool` or `forEach` must be set (a CEL validation rule).

#### WorkflowCondition Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `template` | `string` | No* | Boolean Go-template gate, e.g. `"{{ eq .input.env \"production\" }}"`. The step runs when it renders to `true`. |
| `tool` | `string` | No* | Tool for condition evaluation |
| `args` | `map[string]any` | No | Arguments for condition tool |
| `fromStep` | `string` | No* | Reference step for condition evaluation |
| `expect` | `WorkflowConditionExpectation` | No | Positive expectations (with `tool`/`fromStep`) |
| `expectNot` | `WorkflowConditionExpectation` | No | Negative expectations (with `tool`/`fromStep`) |

*Note: Specify exactly one of `template`, `tool`, or `fromStep`. A `tool`/`fromStep` condition must declare `expect` or `expectNot` (without one, the engine defaults to expecting the call to fail). With `template`, `expect`/`expectNot` are ignored. Both rules are enforced at `kubectl apply` time via CEL. There are no `and`/`or` combinators.

#### WorkflowConditionExpectation Fields

| Field | Type | Description |
|-------|------|-------------|
| `success` | `boolean` | Whether the tool call should succeed |
| `jsonPath` | `map[string]any` | Path conditions to check against the result. Each key uses the workflow's expression language: a dotted/bracketed path navigated from the result (with array indexing, e.g. `items[0].name`), or a full Go-template expression where the result is exposed as `.result` (e.g. `"{{ (index .result.items 0).name }}"`). |

#### Workflow Output template

The optional workflow-level `output` field shapes the document returned to the
caller. It is a templated object rendered once after all steps complete, against
the same `.input` / `.results` / `.vars` context used by step args, and returned
in place of the default `{execution_id, workflow, status, input, steps[], ...}`
response.

```yaml
spec:
  steps:
    - id: pods
      tool: x_kubernetes_list
      args: { kind: Pod }
    - id: events
      tool: x_kubernetes_list
      args: { kind: Event }
  output:
    cluster: "{{ .input.management_cluster }}"
    notRunning: "{{ .results.pods.items }}"
    backoffCount: "{{ len .results.events.items }}"
```

- Each leaf is a Go-template/sprig expression. JSON structure is preserved:
  `notRunning` stays an array and `backoffCount` stays a number.
- Nested objects and arrays in the output template are rendered recursively.
- Every step result is referenceable here regardless of its `output` flag. When a
  output template is declared it replaces the response, so per-step `output`/`store`
  flags no longer affect the returned document (the create/validate path and the
  reconciler warn when such flags are left set and become inert).
- **Type preservation (no lossy coercion)**: a leaf's type comes from the value
  it evaluates to, never from how its rendered text looks. A *bare reference path*
  leaf (`"{{ .results.pods.items }}"`) keeps its exact JSON type. A single-action
  *computed* leaf keeps its real type too: `"{{ len .results.events.items }}"` is
  a number, while a computed string keeps its exact string form — so values whose
  form matters (versions, IDs, zero-padded values like `"08"`/`"1.20"`) are
  preserved with no workaround. Non-finite values (`NaN`/`Inf`) stay strings.
  Templates that mix literal text with actions (e.g. `"v{{ .v }}"`) render to a
  string.
- When `output` is omitted, the default response is returned unchanged.

#### Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `valid` | `boolean` | Whether the spec passes structural validation |
| `validationErrors` | `[]string` | Any spec validation error messages |
| `referencedTools` | `[]string` | Tools mentioned in workflow steps (informational only) |
| `stepCount` | `int` | Number of steps in the workflow |
| `conditions` | `[]metav1.Condition` | Standard Kubernetes conditions |

> **Note**: Tool availability is computed per-session at runtime based on user authentication. A workflow may show all referenced tools but only be executable by users with access to those tools. See [ADR 007](../explanation/decisions/007-crd-status-reconciliation.md) for details.

### Examples

#### Conditional Database Migration Workflow
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: Workflow
metadata:
  name: database-migration
  namespace: default
spec:
  description: "Perform database migration with rollback capability"
  args:
    database_name:
      type: string
      required: true
      description: "Target database name"
    migration_version:
      type: string
      required: true
      description: "Target migration version"
    dry_run:
      type: boolean
      default: false
      description: "Perform dry run without applying changes"
  steps:
    - id: check_database
      tool: postgres_status
      args:
        database: "{{.input.database_name}}"
      store: true
      description: "Verify database connectivity"

    - id: backup_database
      tool: postgres_backup
      args:
        database: "{{.input.database_name}}"
        backup_name: "pre-migration-{{.input.migration_version}}"
      condition:
        fromStep: "check_database"
        expect:
          success: true
          jsonPath:
            status: "connected"
      store: true
      description: "Create backup before migration"

    - id: run_migration
      tool: postgres_migrate
      args:
        database: "{{.input.database_name}}"
        version: "{{.input.migration_version}}"
        dry_run: "{{.input.dry_run}}"
      condition:
        fromStep: "backup_database"
        expect:
          success: true
      store: true
      description: "Execute database migration"

    - id: verify_migration
      tool: postgres_verify
      args:
        database: "{{.input.database_name}}"
        expected_version: "{{.input.migration_version}}"
      condition:
        fromStep: "run_migration"
        expect:
          success: true
      description: "Verify migration completed successfully"
  onFailure:
    - id: rollback_migration
      tool: postgres_restore
      args:
        database: "{{.input.database_name}}"
        backup_name: "{{.results.backup_database.backup_name}}"
      description: "Restore the pre-migration backup if the workflow fails"
```

### CLI Usage

```bash
# List all workflows
kubectl get workflows
# or using short name
kubectl get wf

# Get detailed information
kubectl describe workflow deploy-application

# Create from file
kubectl apply -f workflow.yaml
```

---

## WorkflowExecution

A `WorkflowExecution` is the durable, immutable record of a single workflow run. When muster runs in Kubernetes mode, every `workflow_<name>` call persists one `WorkflowExecution` so execution history survives process restarts and is visible across replicas and through `kubectl`. In standalone `muster serve` the same records are kept on the filesystem instead.

These records are written and pruned by muster — you do not create them by hand. They have **no status subresource**: the record is append-only history, not a reconciled resource. The object's `metadata.name` is the execution UUID.

### Resource Definition

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: WorkflowExecution
metadata:
  name: <execution-uuid>
  namespace: <namespace>
  labels:
    muster.giantswarm.io/workflow: <workflow-name>  # set by muster, used for list filtering
    muster.giantswarm.io/status: <status>           # set by muster, used for list filtering
spec:
  workflowName: <workflow-name>
  status: inprogress|completed|failed
  startedAt: <timestamp>
  completedAt: <timestamp>        # unset while in progress
  durationMs: <int>
  input: { ... }                  # original workflow arguments (arbitrary JSON)
  result: { ... }                 # final result (arbitrary JSON; unset on failure/in progress)
  error: "<message>"              # set only on failure
  truncated: false                # true when oversized payloads were truncated to fit
  steps:
    - stepId: <id>
      tool: <tool-name>
      status: inprogress|completed|failed
      startedAt: <timestamp>
      completedAt: <timestamp>
      durationMs: <int>
      input: { ... }
      result: { ... }
      error: "<message>"
      storedAs: <variable-name>
```

### Field Reference

| Field | Type | Description |
|-------|------|-------------|
| `spec.workflowName` | string (required) | Name of the workflow that was executed |
| `spec.status` | enum | `inprogress`, `completed`, or `failed` |
| `spec.startedAt` | timestamp (required) | When the execution began |
| `spec.completedAt` | timestamp | When the execution finished (unset while in progress) |
| `spec.durationMs` | int64 | Total execution duration in milliseconds |
| `spec.input` | object | Original arguments passed to the workflow |
| `spec.result` | object | Final result (unset on failure or while in progress) |
| `spec.error` | string | Error message when `status: failed` |
| `spec.steps[]` | list | Per-step records (id, tool, status, timings, input/result, error) |
| `spec.truncated` | bool | True when oversized payloads were replaced with a truncation marker to keep the record within the size limit (256 KB) |

### Retention

Records are pruned automatically by a background GC: anything older than 21 days, plus a newest-10000 burst safety cap. These bounds are fixed in the current version.

### CLI Usage

```bash
# List recent workflow executions
kubectl get workflowexecutions
# or using short name
kubectl get wfe

# Filter by workflow or status via labels
kubectl get wfe -l muster.giantswarm.io/workflow=deploy-application
kubectl get wfe -l muster.giantswarm.io/status=failed

# Inspect a single run
kubectl describe wfe <execution-uuid>
```

> Prefer the `core_workflow_execution_list` / `core_workflow_execution_get` tools for programmatic access — they return the same records through muster's API in both Kubernetes and filesystem modes.

---

## Templating

Workflow step arguments support Go template syntax (rendered with [sprig](https://masterminds.github.io/sprig/) functions). Templates are resolved server-side at execution time with `missingkey=error`, so referencing an undefined key fails the step.

### Available Variables

The template context exposes exactly these top-level keys:

| Key | Description |
|-----|-------------|
| `.input.<arg_name>` | Arguments passed during workflow execution |
| `.results.<step_id>` | Result of any previous step (navigate fields with `.results.<step_id>.<field>`; no flag required) |
| `.vars.<name>` | Loop variables inside `forEach` (e.g. `.vars.item`, `.vars.item_index`) |
| `.context.<step_id>` | Alias for `.results` |

> There is no bare `.<arg_name>`; always use `.input.<arg_name>`.

### Template Functions

The full [sprig](https://masterminds.github.io/sprig/) function set is available, e.g.:

| Function | Description | Example |
|----------|-------------|---------|
| `eq` / `ne` / `gt` | Comparisons (useful in `condition.template`) | `{{ eq .input.env "production" }}` |
| `replace` | String replacement | `{{ .input.image | replace "/" "-" }}` |
| `lower` / `upper` | Case conversion | `{{ .input.name | lower }}` |
| `trim` | Remove whitespace | `{{ .input.value | trim }}` |

### Examples

```yaml
# Workflow step args
args:
  service_name: "{{.input.app_name}}-{{.input.environment}}"
  image: "{{.results.build_image.image_id}}"
  replicas: "{{.input.replicas}}"
```

---

## Tool Availability and Dependencies

### Tool Discovery

muster automatically discovers available tools from:
1. **Core Tools**: Built-in muster tools (`core_service_*`, `core_workflow_*`, etc.)
2. **MCP Server Tools**: Tools provided by registered MCPServer resources
3. **Dynamic Tools**: Workflow execution tools (`workflow_<name>`)

### Session-Scoped Tool Visibility

**Important**: With session-scoped tool visibility (see [ADR 007](../explanation/decisions/007-crd-status-reconciliation.md)), tool availability is no longer a global property stored in CRD status. Instead:

- Tool availability is computed **per-session at runtime**
- Different users may have access to different tools based on their OAuth authentication
- CRD status fields show `referencedTools` (what tools the resource uses), not availability
- Actual executability depends on the user's authenticated session

This means:
- A Workflow requiring `kubernetes_deploy` is executable by users authenticated with `mcp-kubernetes`
- The same Workflow is not executable by users who haven't authenticated
- The `valid` status field indicates spec correctness, not tool availability

### Status Monitoring

CRDs provide status information about validation and referenced tools:

```bash
# Check referenced tools (informational)
kubectl get workflow deploy-app -o jsonpath='{.status.referencedTools}'

# Check MCP Server runtime state
kubectl get mcpserver git-tools -o jsonpath='{.status.state}'
```

### Reconciliation

muster automatically reconciles CRD status with runtime state:

- **MCPServer**: Status reflects actual process state (running, stopped, healthy, unhealthy)
- **Workflow**: Status reflects spec validation and lists referenced tools

Reconciliation works in both filesystem mode (watching YAML files) and Kubernetes mode (using informers). See the [reconciler package documentation](https://github.com/giantswarm/muster/blob/main/internal/reconciler/doc.go) for implementation details.

---

## Best Practices

### Naming Conventions

| Resource | Pattern | Examples |
|----------|---------|----------|
| **MCPServer** | `<tool-category>-tools` | `git-tools`, `filesystem-tools`, `database-tools` |
| **Workflow** | `<action>-<target>` | `deploy-application`, `backup-database`, `scale-service` |

### Resource Organization

```yaml
# Use consistent namespacing
metadata:
  namespace: muster-system    # For system resources
  namespace: applications     # For application resources
  namespace: default         # For development resources

# Use labels for grouping
metadata:
  labels:
    component: database
    environment: production
    version: v1.2.3
```

### Security Considerations

```yaml
# MCPServer environment variables
env:
  # Avoid storing secrets directly
  DATABASE_URL: "postgresql://user:pass@localhost:5432/db"  # ❌ BAD

  # Use references to secrets
  DATABASE_URL_FROM_SECRET: "database-credentials"         # ✅ GOOD
```

#### SSO Token Forwarding Security

When using `forwardToken: true` for SSO:

1. **Validated Tokens Only**: muster forwards the caller's bearer only after validating it at its own front door; opaque bearers are never forwarded
2. **TLS Required**: All communication must be over HTTPS
3. **Tokens Not Logged**: Tokens are never logged in plaintext
4. **Downstream Opt-In**: Downstream servers must explicitly trust the identity provider that signed the forwarded token (its issuer and keys), and one of the token's audiences
5. **Constant-Time Comparison**: mcp-oauth uses `subtle.ConstantTimeCompare` for audience matching
6. **Audit Logging**: Both muster and downstream servers log when cross-client tokens are used
7. **Shared Audience**: The forwarded token is audience-scoped to muster's resource identifier, so it is accepted by every `forwardToken` backend and by muster itself — treat all `forwardToken` backends as equally trusted; the token's nested `act` chain is the delegation provenance available to backends

Events emitted for SSO:
- `MCPServerTokenForwarded`: Successful SSO token forwarding
- `MCPServerTokenForwardingFailed`: Token forwarding failed
- `MCPServerTokenExchanged`: Successful RFC 8693 token exchange
- `MCPServerTokenExchangeFailed`: Token exchange failed

### Error Handling

```yaml
# Workflow error handling
steps:
  - id: risky_operation
    tool: external_api_call
    allowFailure: true    # Continue on failure

  - id: cleanup
    tool: cleanup_resources
    condition:
      fromStep: "risky_operation"
      expectNot:
        success: true     # Only run if previous step failed
```

### Performance Optimization

```yaml
# Health check tuning
healthCheck:
  enabled: true
  interval: "30s"        # Balance between responsiveness and load
  failureThreshold: 3    # Avoid false positives
  successThreshold: 1    # Quick recovery

# Timeout configuration
timeout:
  create: "5m"          # Generous for complex deployments
  delete: "2m"          # Quick cleanup
  healthCheck: "10s"    # Fast feedback
```

---

## Related Documentation

- **[CLI Reference](cli/README.md)** - Command-line tools for managing CRDs
- **[MCP Tools Reference](mcp-tools.md)** - Available tools for use in Workflows
- **[Workflow Creation Guide](../how-to/workflow-creation.md)** - Step-by-step workflow development
- **[Architecture Overview](../explanation/architecture.md)** - How CRDs fit into the muster system
