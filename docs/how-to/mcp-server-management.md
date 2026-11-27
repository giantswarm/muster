# MCP Server Management

This guide covers how to create, configure, and manage MCP (Model Context Protocol) servers in muster.

## Overview

MCP servers provide structured access to tools and resources for AI assistants. muster supports three types of MCP servers:

- **Stdio servers**: Execute as local processes with configurable command lines
- **Streamable HTTP servers**: Connect to external MCP servers via HTTP
- **SSE servers**: Connect to external MCP servers via Server-Sent Events

### Goal

Add a new MCP server to extend muster's tool capabilities.

### Prerequisites

- muster control plane running
- MCP server binary available
- Understanding of the tool's requirements

## Creating MCP Servers

### Stdio Command Servers

Create a stdio server that runs as a local process:

1. **Create MCP server configuration**

   ```yaml
   # example-server.yaml
   apiVersion: muster.giantswarm.io/v1alpha1
   kind: MCPServer
   metadata:
     name: example-tool
     namespace: default
   spec:
     type: localCommand
     command: ["mcp-example-tool"]
     autoStart: true
     env:
       TOOL_CONFIG: "/path/to/config"
       LOG_LEVEL: "info"
     description: "Example MCP server providing custom tools"
   ```

2. **Register the server**

   ```bash
   muster create mcpserver example-server.yaml
   ```

3. **Verify server status**

   ```bash
   muster get mcpserver example-tool
   ```

4. **Test tool availability**

   ```bash
   muster agent --repl
   # In REPL:
   list tools
   # Or filter tools to see ones from this server:
   filter tools example
   ```

### Verification

- Server shows status "running"
- Tools from the server appear in tool listings
- Tools can be executed successfully

## Configure Auto-Start Behavior

### Goal

Control when MCP servers start automatically.

### Steps

1. **Enable auto-start** (start with muster)

   ```yaml
   apiVersion: muster.giantswarm.io/v1alpha1
   kind: MCPServer
   metadata:
     name: auto-start-server
     namespace: default
   spec:
     autoStart: true
     # ... other configuration
   ```

2. **Disable auto-start** (manual control)

   ```yaml
   apiVersion: muster.giantswarm.io/v1alpha1
   kind: MCPServer
   metadata:
     name: manual-server
     namespace: default
   spec:
     autoStart: false
     # ... other configuration
   ```

3. **Apply configuration**

   ```bash
   muster create mcpserver server-config.yaml
   ```

4. **Manual server control**

   ```bash
   # Check server status
   muster get mcpserver example-tool

   # List all servers
   muster list mcpserver

   # Check server availability
   muster check mcpserver example-tool
   ```

## Monitor MCP Server Health

### Goal

Set up monitoring and health checks for MCP servers.

### Steps

1. **Check server status**

   ```bash
   # List all servers with status
   muster list mcpserver

   # Get detailed server info
   muster get mcpserver example-tool
   ```

2. **Test server communication**

   ```bash
   # Check if server is available
   muster check mcpserver example-tool
   ```

3. **Set up health monitoring**

   ```yaml
   apiVersion: muster.giantswarm.io/v1alpha1
   kind: MCPServer
   metadata:
     name: monitored-server
     namespace: default
   spec:
     type: localCommand
     command: ["mcp-example-tool"]
     healthCheck:
       enabled: true
       interval: "30s"
       timeout: "10s"
       command: ["health-check"]
   ```

4. **Configure alerting** (if monitoring system available)

   muster exports logs, traces, and metrics via OpenTelemetry (OTLP). Point the
   standard `OTEL_EXPORTER_OTLP_*` environment variables at your collector and
   build alerts there; there is no `muster metrics` command.

## Troubleshoot Server Startup Issues

### Goal

Diagnose and fix common MCP server startup problems.

## Advanced Configuration

### Environment Variables
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: filesystem-tools
spec:
  description: "File system operations"
  toolPrefix: "fs"
  type: stdio
  autoStart: true
  command: "npx"
  args: ["@modelcontextprotocol/server-filesystem", "/workspace"]
  env:
    DEBUG: "1"
    LOG_LEVEL: "info"
```

### Remote Servers

Connect to external MCP servers:

#### Streamable HTTP Transport
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: remote-api
spec:
  description: "Remote API tools"
  toolPrefix: "api"
  type: streamable-http
  url: "https://api.example.com/mcp"
  timeout: 60
  headersFrom:
    - name: Authorization
      valueFrom:
        type: Secret
        name: remote-api-token
        key: authorization
```

#### Server-Sent Events (SSE) Transport
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: sse-server
spec:
  description: "SSE MCP server"
  toolPrefix: "sse"
  type: sse
  url: "https://sse.example.com/mcp"
  timeout: 90
  headersFrom:
    - name: Authorization
      valueFrom:
        type: Secret
        name: sse-server-token
        key: authorization
```

#### Credentials come from a Secret

Everyone who can read an MCPServer can read its `headers`, `url` and `env`, so
a credential goes in a Secret in the MCPServer's own namespace and the server
refers to it in `headersFrom`. muster reads the value each time it connects; it
never shows the value in the MCPServer, in `core_service_*` or in
`core_mcpserver_*` output.

muster's own connection to the server reads the Secret at the connect only. A
rotated value reaches it at the next connect: set `restartRequestedAt`, or wait
for a reconnect. A person's session connection reads the Secret each time it
connects.

```bash
kubectl create secret generic remote-api-token \
  --namespace <mcpserver-namespace> \
  --from-literal=authorization="Bearer <token>"
```

muster refuses a server with a value in an `Authorization`,
`Proxy-Authorization`, `Cookie` or `X-API-Key` header, with a user name or
password in its `url`, or with `env` on a remote server. A server written
before this rule reconciles to `Failed` and names the field; move the value to
a Secret. In filesystem mode muster has no Secret reader: there `headersFrom`
fails the connect, and `headers` may still carry a credential.

#### One sign-in for all of a person's sessions

A remote server with its own OAuth sign-in (`auth.type: oauth`) that publishes
RFC 9728 Protected Resource Metadata needs no authorization server in its
definition: muster discovers it from the server's metadata, and the person
signs in with `core_auth_login`. By default the grant belongs to the muster
session that signed in, so every other session of the person -- another
client, each agent conversation -- asks for the sign-in again. With
`auth.grantScope: subject` the grant belongs to the person, and every later
session of theirs connects with it on its first call, also after a muster
restart, until they sign out of the server with `core_auth_logout`:

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: connector
spec:
  type: streamable-http
  url: "https://mcp.example.com/mcp"
  auth:
    type: oauth
    grantScope: subject
```

muster decides grant scope per authorization server: the setting makes the
issuer muster discovers for the server subject-scoped, for every server of the
namespace that uses that issuer, as `auth.authorizationServer.grantScope:
subject` does for a pinned issuer. An issuer that an MCPServer of the namespace
names in `auth.authorizationServer` keeps that server's description: the
discovered server then signs in under it, session-scoped unless it is
`grantScope: subject`. `core_auth_status` reports the scope muster applies:
`grant_scope: subject` once the discovered issuer is known and subject-scoped. Use it for accounts the person owns, never for tokens
that carry the authority of one session. See
[MCPServerAuth fields](../reference/crds.md#mcpserverauth-fields).

## SSO Authentication

muster supports Single Sign-On (SSO) for MCP servers, allowing users to authenticate once and access multiple servers without separate authentication flows.

### SSO Mechanisms

muster supports two SSO mechanisms:

| Mechanism | What You Do | What Happens | Configuration |
|-----------|-------------|--------------|---------------|
| **Token Forwarding** | Authenticate once to muster | muster forwards its ID token to downstream servers | `auth.forwardToken: true` |
| **Token Exchange** | Authenticate once to muster | muster exchanges its token for one valid on the remote IdP | `auth.tokenExchange` config |

### Token Forwarding (Recommended for Trusted Servers)

When Token Forwarding is enabled, muster forwards its ID token to the downstream MCP server. This provides seamless SSO without requiring users to authenticate to each server individually.

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: mcp-kubernetes
spec:
  description: "Kubernetes MCP with SSO"
  toolPrefix: "k8s"
  type: streamable-http
  url: "https://mcp-kubernetes.example.com/mcp"
  auth:
    forwardToken: true  # Enable SSO via token forwarding
    # Specify audiences required by downstream server (e.g., for Kubernetes OIDC)
    requiredAudiences:
      - "dex-k8s-authenticator"
```

**How it works:**
1. User runs `muster auth login` to authenticate to muster
2. muster requests tokens with all `requiredAudiences` from the IdP via cross-client scopes
3. On first MCP request, muster proactively connects to all SSO-enabled servers using the multi-audience token
4. User can immediately access SSO servers without additional authentication
5. The CLI shows the SSO type for each server: `mcp-kubernetes  Connected [SSO: Forwarded]`

**Requirements:**
- The downstream MCP server must trust muster's OAuth client ID
- Both muster and the downstream server must use the same identity provider (issuer)
- For Kubernetes OIDC auth, the IdP must support cross-client authentication (`audience:server:client_id:*` scopes)

**Important:** Required audiences are collected at muster startup and during user authentication. If you add or modify MCPServers with `requiredAudiences` after users have authenticated, those users must re-authenticate (`muster auth logout` followed by `muster auth login`) to obtain tokens with the new audiences.

**Security Note:** Access control for `requiredAudiences` is enforced at two levels:
1. **Kubernetes RBAC**: Only users with permissions to create/modify MCPServer CRDs can configure `requiredAudiences`
2. **IdP Cross-Client Configuration**: The identity provider (e.g., Dex) must be configured to allow cross-client authentication for the specified audiences. Unauthorized audience requests will be rejected by the IdP.

### Token Exchange (Cross-Cluster SSO)

When clusters have separate Identity Providers, muster can use RFC 8693 Token Exchange to obtain a token valid on the remote cluster's IdP. This enables cross-cluster SSO without requiring shared trust.

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: remote-cluster-mcp
spec:
  description: "MCP on remote cluster with Token Exchange"
  type: streamable-http
  url: "https://mcp.remote-cluster.example.com/mcp"
  auth:
    tokenExchange:
      enabled: true
      tokenEndpoint: "https://dex.remote-cluster.example.com/token"
      audience: "mcp-server"
```

**How it works:**
1. User authenticates to muster
2. When accessing the remote server, muster exchanges its token at the remote IdP
3. Remote IdP validates the token and issues a new one valid for that cluster
4. muster uses the exchanged token for downstream requests

### Checking SSO Status

Use `muster auth status` to see which servers are using SSO:

```bash
$ muster auth status

muster: authenticated
  Endpoint: https://muster.example.com
  Expires:  in 23 hours

MCP Servers:
  mcp-kubernetes  Connected [SSO: Forwarded]
  remote-cluster  Connected [SSO: Exchanged]
  isolated-server Not authenticated          Run: muster auth login --server isolated-server
```

### Troubleshooting SSO

**SSO server not connecting automatically:**
- Verify `forwardToken: true` is set in the MCPServer spec
- Check that the downstream server trusts muster's OAuth client ID
- Run with `--debug` to see detailed SSO connection logs

## AWS SigV4 Request Signing

An AWS-hosted MCP server accepts no bearer token. It authenticates each request
by an AWS Signature Version 4 signature. Set `auth.type: sigv4` to make muster
sign every request that it sends to such a server.

This is not SSO. The signature carries muster's own machine identity, not the
identity of the user who made the call, so all users of the server share one AWS
identity. CloudTrail records muster, not a named engineer.

`auth.sigv4` is only valid with `type: streamable-http`. muster rejects it
together with `forwardToken`, `tokenExchange` or `authorizationServer`, because
none of them apply to a machine identity. These rules hold in both Kubernetes
and filesystem mode.

A connection failure reports itself as such. A 401 from a SigV4 server is not
"Auth Required" — there is no login flow to send a user to — so muster keeps the
server in `Failed` and retries with backoff. Check the signing region, the
assumed role and its policy.

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: aws-root
spec:
  type: streamable-http
  url: "https://aws-mcp.eu-central-1.api.aws/mcp"
  timeout: 120
  auth:
    type: sigv4
    sigv4:
      region: eu-central-1
  requestMeta:
    AWS_REGION: eu-central-1
```

**Fields:**

| Field | Required | Purpose |
|-------|----------|---------|
| `auth.sigv4.region` | yes | The signing region. It must match the region in `url`, because the endpoint checks the credential scope of the signature. |
| `auth.sigv4.service` | no | The signing service name. It defaults to the first hostname label of `url`, so `aws-mcp.eu-central-1.api.aws` signs as `aws-mcp`. |
| `auth.sigv4.roleArn` | no | An IAM role that muster assumes before it signs. Leave it empty to sign as muster's own identity. |
| `requestMeta.AWS_REGION` | yes | The region the AWS MCP server operates in. `requestMeta` is not a SigV4 field: see [Request metadata](#request-metadata). |

muster gets its base credentials from the default AWS credential chain. In
Kubernetes that means IRSA: the pod identity webhook injects `AWS_ROLE_ARN` and
`AWS_WEB_IDENTITY_TOKEN_FILE`, and the chain exchanges the projected token for
credentials. Set `roleArn` to chain one more hop from there, which is how one
muster reaches many accounts. Each account gets its own MCPServer, and a shared
`family` puts them all behind one tool name with an account selector.

The assumed role's session is named `muster-aggregator.<MCPServer name>`, so
CloudTrail shows which server made a call.

**A SigV4 server must set `requestMeta.AWS_REGION`.** The AWS MCP server takes
the region it operates in from `params._meta.AWS_REGION`. None of its tools has
a region argument. This region is a different value from the signing region,
even when the two strings match: the signing region belongs to the endpoint,
and the operating region belongs to the resources that the call reads.

muster refuses a SigV4 server without it, because the backend does not fail
without it: it runs the call in us-east-1 and returns a correct-looking answer
about the wrong region. Measured against `aws-mcp.eu-central-1.api.aws`: the
same query for CloudWatch log groups returned nothing without the entry, and
the one log group that exists in the account with `AWS_REGION: eu-north-1`. An
agent reads the first result as "there are none".

## Request metadata

`requestMeta` is a remote-server field, not a SigV4 field. muster adds its
entries to the `params._meta` object of every outbound JSON-RPC request that
carries `params`. Use it for a backend that reads server-wide configuration from
the MCP metadata field instead of from tool arguments.

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: regional-tools
spec:
  type: streamable-http
  url: "https://mcp.example.com/mcp"
  requestMeta:
    com.example/tenant-id: giantswarm
```

Rules:

- The merge applies to `type: streamable-http` and `type: sse`, with any auth
  type and with none. A server that needs a login keeps its entries after the
  login: the per-session connection carries them too.
- Keys follow the MCP `_meta` key format: an optional prefix of dot-separated
  labels ending in `/`, then a name that begins and ends with a letter or
  digit. Use a reverse-DNS prefix you own for your own keys, as the MCP
  specification recommends; `AWS_REGION` is unprefixed because that is the key
  AWS defined.
- muster refuses keys it must not set: any prefix whose second label is
  `modelcontextprotocol` or `mcp` (reserved for MCP), `progressToken` (set by
  muster per call), and `traceparent`, `tracestate` and `baggage`
  (OpenTelemetry).
- Everyone who can read the MCPServer can read these values, so they hold no
  secret.
- muster relays negotiated application extension metadata (see
  [Extension metadata](#extension-metadata)), so an entry here applies to every
  call. It never replaces a key already set on the request (`progressToken`,
  the protocol's own `io.modelcontextprotocol/` keys, and relayed extension
  entries).
- A request without `params` is left untouched. `initialize` does carry
  `params`, so the handshake gets the entries as well.
- `type: stdio` rejects the field. A stdio server speaks over a pipe, so no HTTP
  transport can add the entries, and muster refuses the definition instead of
  accepting the map and dropping it.
- `meta` is the former name of `requestMeta` and is still read. muster writes
  it as `requestMeta` when it creates or updates a server, and refuses a
  definition that sets both. It will be removed in a later release.

## Tool approval

`spec.approval` sets, per tool of a server, whether a call runs (`Allow`), asks a
person to confirm the exact call first (`Ask`) or is hidden and refused
(`Block`). Whoever may `update` MCPServers in the server's namespace (a
connector admin) sets it.

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: github
spec:
  type: streamable-http
  url: "https://mcp.example.com/mcp"
  approval:
    rule: Ask                    # every tool without its own entry, new ones too
    tools:
      get_issue: Allow
      merge_pull_request: Ask
      delete_repository: Block
```

- `tools` keys are the server's own tool names (no `toolPrefix`, family name or
  `x_` prefix). An entry wins over `rule`. Values are case-sensitive; at most 512
  entries.
- `rule: Block` hides and refuses every tool without its own entry, new tools
  included.
- No `approval` means `Allow` for every tool, reported as not reviewed. There is
  no default, so upgrading changes no existing server.
- Unknown values count as `Ask`. Changes apply to the next listing or call.
- A blocked tool is absent from `list_tools`, `filter_tools` and `describe_tool`,
  and `call_tool` (a workflow step too) refuses it as not found. Non-admins do
  not see a server's `Block` entries in `core_mcpserver_get` and `_list`. The
  meta-tools report each tool's setting and its source
  ([Tool approval](../reference/mcp-tools.md#tool-approval)).
- Fails closed: a registered server whose MCPServer object is gone is refused.
- Keep secrets out of a connector's URL (use `headersFrom`); the audit line
  redacts userinfo, query and token-like path segments, but not a short secret in
  the path.

**The CRD must know `spec.approval`.** An older CRD prunes the field. While the
served CRD lacks it, every server tool call is refused (`approval_unavailable`)
and the write tools refuse to store settings. Upgrade the CRDs with the chart
([upgrading](#upgrading-to-tool-approval)).

**API server outage.** Nobody can change settings then, so a call is decided by
the server's last definition muster read, if at most `approval.lastObservedMaxAge`
old (default `30m`, max `1h`; Helm `muster.approval.lastObservedMaxAge`). Listings
use the same definitions. Otherwise the call is refused as `approval settings
unavailable` (reasons `last_observed_too_old`, `never_observed`, `throttled`
for HTTP 429, or any other failed read), and listings report `unavailable`.
Agent settings and governance floors apply unchanged. Fallbacks and refusals
are logged and counted ([metrics](../explanation/observability.md#approval-metrics)).

**Setting it.** `core_mcpserver_set_approval` takes `approval` (`{rule, tools}`) or
a `preset`, which muster expands once from the tools the server lists now:

| Preset | Result |
|---|---|
| `recommended` | rule `Ask`; tools with `readOnlyHint: true` `Allow` |
| `allow-all` | everything runs |
| `ask-all` | everything asks |

`core_mcpserver_create` requires `preset` or `approval`. A server that lists no
tools yet gets only the preset's rule (every tool reported `new`): set the
preset again once it lists them. `expectedGeneration` (from `core_mcpserver_get`)
refuses a write when the server changed since. Every settings change is audited
old to new with who made it, and a write the API server dropped is refused.

The connector write tools are split so each can be governed on its own:
`core_mcpserver_create`, `_update` (description, display name, tags, `autoStart`,
`timeout`, `progress`, `suspended`, `restartRequestedAt`), `_connect` (type, URL,
command, args, env, headers, `headersFrom`, `requestMeta`, auth,
`clientCredentials`, family, `toolPrefix`), `_set_approval` and `_delete`. Each
refuses an argument another one takes and names that tool.

### When a call asks

An `Ask` call runs only after its person approved that exact call:

1. `call_tool` records a pending approval bound to the person (issuer and `sub`),
   the muster session (OAuth client `azp` and acting party, or the agent's
   Session; not the transport session), the target (server, UID, generation and
   tool; or workflow and definition) and the RFC 8785 canonical arguments. It
   answers with a link `https://<publicUrl>/approvals/<id>`: as a URL elicitation
   for a client that declares it (`input_required` on protocol 2026-07-28,
   `-32042` before), else as a tool error `approval_required`.
2. The person opens the link and signs in to muster's identity provider; only
   that person sees the call. The page shows the tool, connector, whether it
   changes data and every argument in full. After the decision, the session that
   got the `-32042` elicitation receives `notifications/elicitation/complete`
   with the `elicitationId` only, via the shared Valkey store, whichever replica
   serves the page. If the store does not take the notice the decision still
   stands and the client retries on its own.
3. The client calls again with the same arguments. muster finds the decision
   itself (the client's own "accept" counts for nothing): approved runs once with
   the arguments the person saw, declined answers `declined`, and an identical
   call before a decision gets the same link.

For an agent's call the page names the agent (display name plus technical name,
`Alert Review (kagent/alert-review)`), the approval is bound to it, and the
`reason` the agent passed to `call_tool` is shown as *Agent says*, never used to
decide. Agents' own settings and grants:
[an agent's approval settings](../reference/mcp-tools.md#an-agents-approval-settings).

- An approval expires after `approval.ttl` (5 minutes) and is used up by one run.
- Caps: 10 pending per person, 3 per client; the refusal names the cap. Arguments
  at most 64 KiB. Resources and prompts are not gated.
- A workflow asks once, up front, for its definition, inputs and called
  workflows, if any step asks. A blocked step refuses the run before anything
  runs; a step that resolves to `Block`, to an unseen tool or to an unapproved
  `ask` fails the run there.
- Prerequisites: Valkey shared store (`aggregator.oauth.server.storage.type:
  valkey`), muster's own OAuth server and a public URL
  (`aggregator.oauth.mcpClient.publicUrl`, on the OAuth server's origin).
  Without them, or when the store does not answer, every asking call is refused
  and the meta-tools report `approval.unavailable`.
- The page returns the browser to the client's `redirect` if
  `postLoginRedirectAllowlist` admits it, with `elicitationId`, never the decision.
- Security: an agent that drives the person's signed-in browser acts as the
  person and can approve.
- Every request, decision, use and refusal is an `[AUDIT]` log line with issuer,
  full subject, caller kind and approval id.

#### Trusted clients

`approval.trustedClients` lists the OAuth client ids (token `azp`, else its single
audience) muster trusts with a person's approvals: the portal's own client and
nothing else. Such a client can decide approvals in its own UI and skip muster's
browser sign-in (handoff). `approval.handoffClients`, the earlier name, is still
read. Default empty: approvals are decided on muster's page only.

Both calls below need the person's bearer and the client backend's own projected
ServiceAccount token (audience `muster`) in `X-Muster-Approval-Client`. muster
accepts it only if a `TokenReview` passes and a `LocalSubjectAccessReview` allows
the identity verb `decide` on `toolapprovals` (group `muster.giantswarm.io`) in
muster's namespace. The chart ships that as the Role `muster-approval-decider`
(`rbac.approvalDecider.subjects`, bound to nobody by default). Without a
Kubernetes API neither call works.

The backend must present only the token of the person signed in to it; a
backend that relays any token it is handed lets its users decide for others.

#### Deciding inline from a trusted client

```
GET /approvals/<id>
Authorization: Bearer <the person's token>
X-Muster-Approval-Client: <backend ServiceAccount token, audience muster>
-> 200 {"id", "status", "heading", "message", "tool", "connector", "changesData",
        "arguments": [{"key", "label", "value", "type"}], "agentSays", "agent",
        "why", "actsFor", "callerKind", "expiresAt", "digest", ...}

POST /approvals/<id>/decision        (same headers)
{"decision": "approve" | "decline", "digest": "<digest from the GET>"}
-> 200 {"status": "approved" | "declined"}
```

The view is what the page shows. Invisible, bidi and blank look-alike characters
are marked `⟨U+XXXX⟩`; render it as text, never markup. A decision with another
`digest` is refused (409 `changed`), so a client cannot approve something it did
not show.

| Refusal | Cause |
|---|---|
| 403 `acting_party` | token names an acting party (`act`), is delegated, or is any credential of an agent's Session: agents never decide, not even their own |
| 403 `untrusted_client` | client not in `approval.trustedClients` |
| 403 `untrusted_workload` | workload token fails the `decide` review (503 `unavailable` while the review cannot be made) |
| 403 `foreign` | not the person the request was made for (issuer and subject) |
| 404 `not_found`, 409 `decided`, 410 `expired` | not pending: unknown, used, decided or expired |

A decision is audited as `approval_decision` with `path=api` and the `client`
(the page's carry `path=page`); refusals too, with `outcome=refused`, `reason` and
the caller's truncated subject. Metrics:
[Approval metrics](../explanation/observability.md#approval-metrics).

Security: the trusted client's backend can approve without muster's sign-in, so it
must decide only on the person's click, never from message content. XSS in its UI
can ask the backend to approve the person's pending requests, as with the handoff.
Never list a client an agent, a CLI or kubectl signs in through, and bind
`muster-approval-decider` to the portal backend's ServiceAccount only.

#### Sign-in handoff from a trusted client

A trusted client can spare a signed-in person muster's browser sign-in on one page:

```
POST /handoff-tickets
Authorization: Bearer <the person's token>
X-Muster-Approval-Client: <backend ServiceAccount token, audience muster>
{"url": "https://<publicUrl>/approvals/<id>"}
-> 201 {"ticket": "<one-time ticket>", "expiresIn": 60}
```

The client opens the link with `&handoff=<ticket>`. The page redeems the ticket,
treats the browser as signed in as that person for that request and redirects
(303) to the address without it. A connector sign-in start
(`/oauth/proxy/start?state=...`) takes a ticket too but still shows muster's
confirmation *Connect <connector> as <person>?* (verified email, else subject at
its issuer); only *Continue*, posted from the same browser with the anti-forgery
token and `Origin`, binds the flow. Without it a person could pass a link to
someone else's browser and file that browser's connector account under their own
session.

- Issued only for a listed client's bearer without an acting party, with the
  workload token, for the person the approval is bound to (another's or unknown:
  403 alike), and only for muster's approval page or sign-in start on its public
  URL (else 400).
- 130 random bits, used once, 60 seconds, bound to page and person; muster stores
  only its SHA-256 and never logs it.
- An unknown, expired, used or mismatched ticket changes nothing: the page signs
  the browser in itself.
- The decision keeps every other check (anti-forgery token, `Origin`, bound
  person).
- Never list a client an agent or CLI signs in through.

Metrics: `muster_approval_handoffs_total{outcome, page, reason}`.

### Governance and floors

Agent callers (a Session's actor JWT, a delegated token) are asked before some
calls whatever the connector's settings say. A person acting directly is not:
the person is the approval.

`governance` in muster's configuration (Helm `muster.governance`) sets the mode,
the governed servers and optional floors:

```yaml
governance:
  mode: governed                  # or trusted
  roleFloors:                     # optional: floors added to the built-in ones
    exampleManager: [approve_change, publish_change]
  governedServers:
    - server: agent-manager       # server key
      role: agentManager          # which floors hold on it
      safe: [get_info, list_agents, get_agent]   # tools agents call unasked
    - server: example-manager
      role: exampleManager        # a role roleFloors declares
      safe: [list_changes]
```

Built-in roles: `agentManager`, `connectorManager`, `sessionManager`,
`modelManager`, `clusterManager`, `vmManager`, `workspaceManager`,
`toolchainManager`, `skillManager`, `infrastructure`. `roleFloors` only adds:
nothing removes a built-in floor. muster (and the chart, at render) refuses to
start on an unknown role, an entry without tools, a server named twice, a floor on
any safe list, the removed `servers` shape, or any key other than `mode`,
`roleFloors` and `governedServers`.

| Mode | Agents are asked |
|---|---|
| both | before every floor: muster's `core_mcpserver_create`, `_connect`, `_set_approval`, `core_workflow_create`, `_update`, `_delete`; per role, agentManager `set_agent_approval`, `set_organization_connector`, `add_organization_member`, `set_organization_model`, `set_organization_egress_policy`, `replace_organization_credential`; connectorManager `mcpserver_create`, `_connect`, `_set_approval`; toolchainManager `approve_source`; modelManager `add_backend`; plus `roleFloors`. A floor name asks on every governed server whatever its role. |
| `governed` (default, also with no readable configuration) | additionally before every tool of a governed server not on its safe list (new or renamed tools included) and every core tool except muster's reads (`core_*_list`, `_get`, `_validate`, `_detect`, `_status`, `_available`, `_execution_*`, `core_events`, `core_auth_*`). Workflow steps resolve the same way. |
| `trusted` | floors only |

- A `Block` still wins. `core_config_update_aggregator`, `_save` and `_reload` ask
  every caller.
- A floor name on a non-governed server is refused to agents while no governed
  server holds that role; once one does, it asks on governed (an unnamed second
  instance fails closed) and follows the server's own settings on trusted.
- A safe-list entry matching none of the server's tools is logged and counted in
  `muster_governance_safe_list_unmatched{server}`; that tool keeps asking.
- `filter_tools`, `describe_tool` and `list_tools` report what a call will do for
  the caller (source `governance` or `builtin`); `filter_tools` and
  `core_mcpserver_list` also report `governance: trusted|governed`.
- Requests of agent callers to a governed server carry `X-Muster-Caller-Kind:
  agent` on HTTP connections; agents are refused on a governed stdio server.

#### What governed mode does not cover

- A client holding a person's own token (Claude Code, a script) is a person
  caller: only connector settings apply.
- Servers the configuration does not name follow connector settings only; name
  every server that can change the platform.
- Calls that never reach muster (kagent `RemoteMCPServer`s, sub-agent and A2A
  calls).
- Whoever can write muster's ConfigMap or HelmRelease changes mode and safe lists.

### Upgrading to tool approval

- **CRDs first.** `helm upgrade` does not update `crds/`: `helm show crds
  giantswarm/muster | kubectl apply --server-side -f -`, or Flux `upgrade.crds:
  CreateReplace`. Until the CRD knows `spec.approval`, every server tool call is
  refused.
- **Connector write tools are split.** `core_mcpserver_update` no longer takes
  `url`, `type`, `command`, `args`, `env`, `headers`, `headersFrom`,
  `requestMeta`/`meta`, `auth`, `clientCredentials`, `family` or `toolPrefix`
  (use `_connect`) or approval settings (use `_set_approval`).
  `core_mcpserver_create` and `muster create mcpserver` need `preset` or
  `approval`.
- **`mcpserver-editor` is bound to nobody by default** (it was
  `system:authenticated`). Set `rbac.mcpServerEditor.subjects` (to keep the old
  behaviour `[{kind: Group, apiGroup: rbac.authorization.k8s.io, name:
  system:authenticated}]`) or `rbac.mcpServerEditor.allowRemovingBinding: true`;
  otherwise the upgrade fails while the old RoleBinding exists, which breaks
  unattended upgrades (Flux, Renovate). `helm template`-only pipelines (Argo CD)
  are not protected: set the value first. `workflowEditor` keeps
  `system:authenticated`.
- **Governance.** Without `muster.governance`, agents get the governed default;
  set it before agents use the platform's management servers.
- `core_config_update_aggregator`, `_save` and `_reload` now ask every caller; in
  filesystem mode (no Valkey, no browser sign-in) edit the configuration file and
  restart instead.

## Extension metadata

muster relays application entries in each tool call's `params._meta` to its
destination backend without requiring extension declarations. Values keep their
identifiers and JSON shapes. For example, `_meta["com.example/context"]` and
`_meta["org.example/request-id"]` reach any backend called by that request,
including one that declares no extensions. No policy allowlist, accepted-key
settings or nested-field filtering applies.

The outgoing connection owns its protocol version, client information,
capabilities, log level, server information, subscription IDs and progress token.
These core fields are not copied from the caller.
Tracing fields (`traceparent`, `tracestate`, `baggage`) are also owned by the
outgoing hop. Other existing outbound metadata takes precedence without merging
nested objects. Authentication continues to use each backend's credentials;
metadata is never used as an inbound bearer token.

Workflow steps receive the initiating call's metadata. A request without
metadata inherits no values. Prompts, resources, lists, discovery and
initialization do not carry the request's application values. Every backend
called by a workflow can receive the full object, so callers must not use a
metadata field as a confidentiality boundary. Values are untrusted application
data, not credentials. Backends validate and authorize operations independently.

Relaying metadata does not implement an extension or infer its client
capabilities. The separate capability view contains the application extensions
of visible, connected backends. Identical settings are preserved; conflicting
settings appear as `{}`. On MCP 2026-07-28, client declarations travel with tool
calls. Legacy connections retain their existing capability handshake behavior;
extensions requiring specific client behavior need their own implementation.
Initial discovery waits for session bootstrap only when a client declares
application extensions. Ordinary metadata forwarding adds no discovery wait.

## Display name

UIs such as the developer portal show an MCPServer by the
`ui.giantswarm.io/display-name` annotation when it is set, and by
`metadata.name` otherwise.

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: kagi-search
  annotations:
    ui.giantswarm.io/display-name: "Kagi Search"
spec:
  type: streamable-http
  url: "https://mcp.example.com/kagi"
```

Through the core tools, pass `displayName` to `core_mcpserver_create` or
`core_mcpserver_update`, or `--display-name` to `muster create mcpserver`.
Surrounding whitespace is trimmed, and on create a blank value sets no
annotation. On update, omitting `displayName` keeps the annotation and an empty
or blank value removes it. `core_mcpserver_get` and `core_mcpserver_list`
return it as `displayName`.

## Using the CLI

### Creating Servers via CLI

Create a stdio server:
```bash
muster create mcpserver filesystem-tools \
  --type stdio \
  --command "npx" \
  --args "@modelcontextprotocol/server-filesystem,/workspace" \
  --auto-start \
  --tool-prefix fs \
  --description "File system operations" \
  --preset allow-all
```

Create a streamable HTTP server:
```bash
muster create mcpserver remote-api \
  --type streamable-http \
  --url "https://api.example.com/mcp" \
  --timeout 60 \
  --tool-prefix api \
  --display-name "Remote API" \
  --description "Remote API tools" \
  --preset recommended
```

Create an SSE server:
```bash
muster create mcpserver sse-server \
  --type sse \
  --url "https://sse.example.com/mcp" \
  --timeout 90 \
  --tool-prefix sse \
  --description "SSE MCP server" \
  --preset ask-all
```

### Listing Servers
```bash
muster list mcpserver
```

### Getting Server Details
```bash
muster get mcpserver filesystem-tools
```

### Updating Servers
```bash
# Update stdio server
muster update mcpserver filesystem-tools \
  --auto-start=false \
  --description "Updated file system tools"

# Update remote server
muster update mcpserver remote-api \
  --url "https://new-api.example.com/mcp" \
  --timeout 120
```

### Deleting Servers
```bash
muster delete mcpserver filesystem-tools
```

## Configuration Best Practices

### Stdio Servers
- Use absolute paths for commands when possible
- Set appropriate environment variables for configuration
- Enable auto-start for critical servers
- Use descriptive tool prefixes to avoid conflicts

### Remote Servers (Streamable HTTP and SSE)
- Use HTTPS endpoints when possible for security
- Set appropriate timeouts based on server response times
- Test connectivity before deploying to production
- Monitor server availability and health
- Include necessary authentication headers

### Tool Prefixes

- Use short but descriptive prefixes (e.g., `k8s`, `git`, `fs`)
- Avoid generic prefixes like `tools` or `server`
- Be consistent across related servers

```yaml
spec:
  toolPrefix: "custom"  # Tools will be prefixed as "x_custom_*"
```

## Troubleshooting

### Stdio Server Issues

**Command not found:**
```bash
# Check if the command is available
which npx
npm install -g @modelcontextprotocol/server-filesystem

# Verify the server definition
muster get mcpserver filesystem-tools
```

**Permission errors:**
```bash
# Check file permissions
ls -la /workspace
chmod +x /path/to/mcp-server

# Run with appropriate user
sudo -u mcpuser muster start mcpserver filesystem-tools
```

### Remote Server Issues

**Connection timeouts:**
```bash
# Test connectivity
curl -v https://api.example.com/mcp

# Increase timeout
muster update mcpserver remote-api --timeout 120
```

**Transport errors:**
```bash
# Check server type and endpoint
muster get mcpserver remote-api

# For SSE servers, ensure endpoint supports Server-Sent Events
# For HTTP servers, ensure endpoint supports streaming HTTP
```

**Authentication errors:**
```bash
# Update headers for authentication
muster update mcpserver remote-api \
  --header "Authorization=Bearer new-token"
```

**Upstream 5xx, a path not served yet, and reconnect backoff:**

A remote server whose connection attempt fails for a transient reason (connection
refused, DNS, timeout, HTTP 5xx, or a 4xx other than 401 to the initialize -- the
404 of a backend whose route is still held by the previous pod during a rollout,
the 405 of a server that speaks only legacy SSE) is retried automatically. The
wait between attempts starts at 30 s, doubles on every consecutive failure and is
capped at 2 minutes; from the third failure on the server is reported as `Failed`
(`unreachable` in `core_service_status`). The orchestrator checks every 30 s
whether an attempt is due, so a server whose upstream has recovered is back
within the cap plus one tick -- in `Connected`; in `Auth Required` for a server
a person signs in to through muster; in `Awaiting Session` for a server served
per session with the caller's own identity (`forwardToken`, `tokenExchange`),
which is `Connected` while a session holds a live connection.

A `429 Too Many Requests` or `503 Service Unavailable` to the initialize is a
transient refusal, not a transport mismatch: the log and `status` name the
status (`endpoint answered the initialize POST with HTTP 429 Too Many Requests;
retry after 1s`), never a legacy SSE server, and when the answer carried a
`Retry-After` the next attempt is scheduled for exactly that delay instead of
the doubling backoff. The status alone that means legacy SSE is the `405`.

A `tokenExchange` server is also `Failed`, on the same schedule, while its
client credentials Secret (`clientCredentialsSecretRef`) is missing or
unreadable -- every caller's exchange would fail the same way -- and settles in
`Awaiting Session` on its own once the Secret exists. An exchange that fails
for every caller alike (the token endpoint does not answer, `invalid_client`,
an unknown connector) puts the server in `Failed` with the exchange error as
`lastError` and the class as the `Ready` condition's reason
(`TokenExchangeCredentials`, `TokenExchangeEndpoint`, `TokenExchangeConnector`).
A token endpoint that does not answer -- a timeout, a refused connection, a 5xx
or a non-OAuth page from a gateway in front of it -- is probed again on the same
backoff, with the exchange's client credentials and a subject token no issuer
signed: the server stays `Failed` (never `Unreachable`) while the probe gets no
answer, and returns to `Awaiting Session` on the first OAuth answer, with no
caller involved. An OAuth error (`invalid_client`, an unknown connector) is not
retried: the next successful exchange, a restart or a spec change clears it. A failure
of one caller's own token (`invalid_grant`, an expired subject token) stays that
session's and never changes the server's state.

```bash
# Why did the last attempt fail and when is the next one?
kubectl get mcpserver remote-api -o jsonpath='{.status.lastError}{"\n"}{.status.lastFailureHTTPStatus}{"\n"}{.status.nextRetryAfter}{"\n"}'

# The same from the events: HTTP status and scheduled retry per attempt
muster events --resource-type MCPServer --resource-name remote-api
```

`status.lastFailureHTTPStatus` is set when the endpoint answered -- `504` from a
gateway or tunnel in front of a healthy server, `503` from the server itself,
`404` from a backend whose route is not served yet -- and absent when nothing
answered at all. Three environment variables on the
muster process tune the schedule: `MUSTER_MCPSERVER_INITIAL_BACKOFF` (default
`30s`), `MUSTER_MCPSERVER_MAX_BACKOFF` (default `2m`) and
`MUSTER_ORCHESTRATOR_RETRY_INTERVAL` (default `30s`), each a Go duration.

A connected server is also probed with an MCP ping every
`MUSTER_ORCHESTRATOR_HEALTH_CHECK_INTERVAL` (default `30s`, a Go duration), each
probe bounded by the server's `spec.timeout`. Three failed probes in a row close
the client and put the server on the schedule above with a reconnect due at
once; `metadata.consecutiveHealthCheckFailures` in `core_service_status` shows
the running count. Servers served per session (`forwardToken`, `tokenExchange`,
OAuth) have no shared client and are not probed.

A probe heals the connection; it does not re-read the tools. A server learns
its tools at registration and again when the server sends
`notifications/tools/list_changed` -- which a backend redeployed behind its
Service with another tool set never does, having never seen muster's session.
Every `MUSTER_AGGREGATOR_CAPABILITY_POLL_INTERVAL` (default `5m`, a Go duration)
the aggregator therefore re-lists every connected server's tools -- and the
resources and prompts of a server that declared them at its handshake, so a
tools-only server costs one request -- and updates what changed, notifying
connected clients as it does after a `list_changed`: a server with a shared
client once, a server served per session through each session's own live
connection into that session's list. The listings are spread over the
interval rather than started at once: with a hundred connections, one every
three seconds, in a fixed order, so a backend with a per-caller rate limit
sees one listing per interval. Nothing is polled that is not connected, and a
listing that fails leaves the cached list as it is. The log line for a change
names its trigger, `Capability refresh (poll): updated 3 tools for <server>`,
as against `(notification)`.

The poll is the catch-all; a backend redeployed with another catalogue is
usually re-listed before it runs. The new process does not know the MCP
sessions the old one issued, so the next request on any connection to it is
answered 404 and the client re-establishes its session with a fresh handshake
(session recovery). That is muster's sign of a new process behind the address
-- no watch on the backend's pods, so it works for a backend off the cluster
too -- and every recovered session re-lists the server through the recovered
connection: a shared client's within one health-probe interval, a session's
on the session's next call. The server is marked rolled at the same time, and
every other session's cached list from before the mark is re-listed, through
that session's connection, before `describe_tool`, `filter_tools` or a
`tools/list` serves it (bounded to 15 s, the cached list is served when the
backend does not answer in time). Three more signs mark a server rolled where
no session is lost: a listing that answers differently from the one cached
after the last mark (a stateless backend has no session to forget), a call the
backend refuses as invalid params (the caller followed a schema the backend no
longer serves: the server is re-listed first, and when the called tool's
schema turns out to have changed the refusal says so and points at
`describe_tool`), and a connection a tool call opens for a session that holds a
cached list from an earlier one. The log line names these triggers too:
`(recovery)`, `(read)`, `(refusal)`, `(connect)`. What stays until the poll
is a backend whose catalogue changed without a session being lost and that no
session calls or reads meanwhile.

## Advanced Configuration

### Environment Variables for Stdio Servers
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: custom-tools
spec:
  type: stdio
  command: "python"
  args: ["-m", "my_mcp_server"]
  env:
    PYTHONPATH: "/usr/local/lib/python3.9/site-packages"
    API_KEY: "your-api-key"
    DEBUG: "true"
    LOG_LEVEL: "info"
```

### Custom Headers for Remote Servers
```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: authenticated-api
spec:
  type: streamable-http
  url: "https://secure-api.example.com/mcp"
  headers:
    X-API-Version: "v2"
    Content-Type: "application/json"
  headersFrom:
    - name: Authorization
      valueFrom:
        type: Secret
        name: authenticated-api-token
        key: authorization
  timeout: 45
```

### Monitoring and Health Checks

Check server status:
```bash
# List all servers with status
muster list mcpserver

# Get detailed server information
muster get mcpserver <server-name>

# Check if server is available
muster check mcpserver <server-name>
```

Inspect server state and logs:
```bash
# Server status and any startup error
muster get mcpserver <server-name> -o yaml
muster list mcpserver --all --verbose

# Aggregator logs (servers log through it) — there is no `muster logs`
muster serve --debug
```

## Integration Examples

### With Cursor/VS Code
Configure Cursor to use muster MCP servers:

```json
{
  "mcpServers": {
    "muster": {
      "command": "muster",
      "args": ["standalone"]
    }
  }
}
```

### With Other AI Assistants
Most MCP-compatible assistants can connect to muster's aggregator endpoint at `http://localhost:8090/mcp`.

## Configuration Best Practices

### 1. Naming Conventions

- Use descriptive names: `git-tools`, `k8s-cluster-prod`
- Include environment in name for multi-env setups
- Avoid special characters and spaces

### 2. Configuration Management

- Store configurations in version control
- Use environment-specific overlays
- Document required environment variables

### 3. Monitoring

- Always enable health checks for production
- Set appropriate timeouts
- Monitor resource usage

### 4. Security

- Run with minimal required permissions
- Use read-only filesystems where possible
- Regularly update server binaries

## Related Documentation

- [Configuration Reference](../reference/configuration.md) - Detailed configuration options
- [HTTP endpoints](../reference/api.md) - The endpoints the aggregator serves
- [CRD Reference](../reference/crds.md) - Kubernetes CRD schema
- [Architecture](../explanation/architecture.md) - How MCP servers fit into muster
- [Server Configuration Schema](../reference/configuration.md#mcp-server-configuration)
- [Troubleshooting Guide](troubleshooting.md)
