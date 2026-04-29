# Access Private MCP Servers via Teleport

> **Audience: engineers authoring `MCPServer` CRs** that need to reach
> mcp-kubernetes (or any other MCP server) on a **private** Giant Swarm
> management cluster — i.e. a cluster only reachable through Teleport
> Application Access. Cluster-admins owning the muster Helm release should
> read [Configure tbot identity](configure-tbot-identity.md). SREs running
> the Teleport-side bot script should read
> [Provision the Teleport Bot](provision-teleport-bot.md).

## Mental model

Three bullets to internalise before writing the CR:

- **Identity (OAuth) is separate from access (Teleport).** OAuth tokens
  carry user identity; Teleport mTLS provides network-level reachability
  into the private MC. They are configured independently on the same CR.
- **`spec.transport` is the access knob.** It declares "how do I reach
  this URL?". Today the only value is `teleport`; tomorrow it could be
  `wireguard` (see [Future transports](#future-transports)).
- **`spec.auth` is the identity knob.** Token forwarding, RFC 8693 token
  exchange, audience requirements — all unchanged from before. Setting
  `spec.transport.teleport` does **not** change `spec.auth` semantics; the
  two compose.

The CR declares its **transport intent verbatim**: every Teleport-routed
CR states the Teleport application name AND the local Kubernetes Secret
carrying its tbot identity, for both the MCP and (when token exchange is
enabled) Dex paths. No naming-convention derivation. The muster Deployment
provisions Secrets with those exact names via the chart's
`transport.teleport.apps[]` list — see
[Configure tbot identity](configure-tbot-identity.md). CR and chart values
grep against each other directly.

## Customer-Muster compatibility

**Omitting `spec.transport` falls back to direct HTTPS to `spec.url`.**
This preserves today's behaviour for in-VPN customer Muster deployments
that reach their own MCs directly without going through Teleport. The
field is purely additive — existing CRs without `spec.transport` remain
valid and behave identically.

Set `spec.transport` only when the URL in `spec.url` is a
`*.teleport.giantswarm.io` proxy address that requires per-app mTLS to
reach.

## Worked example: mcp-kubernetes on `glean`

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: mcp-kubernetes-glean
  namespace: default
spec:
  type: streamable-http
  url: https://mcp-kubernetes-glean.teleport.giantswarm.io/mcp
  transport:
    type: teleport
    teleport:
      mcp:
        appName: mcp-kubernetes-glean
        identitySecretRef:
          name: tbot-identity-mcp-glean
      dex:
        appName: dex-glean
        identitySecretRef:
          name: tbot-identity-tx-glean
  auth:
    type: oauth
    tokenExchange:
      enabled: true
      dexTokenEndpoint: https://dex-glean.teleport.giantswarm.io/token
      expectedIssuer: https://dex.glean.azuretest.gigantic.io
      connectorId: giantswarm
```

Field-by-field walkthrough:

| Field | Why this value |
|---|---|
| `spec.type: streamable-http` | mcp-kubernetes speaks streamable-HTTP. Stdio servers cannot use `spec.transport` — the schema rejects it (CEL rule, see [Drift signals](#drift-signals)). |
| `spec.url` | The Teleport public address for the mcp-kubernetes app. Must match the `public_addr` registered in `giantswarm-management-clusters` for `mcp-kubernetes-glean`. |
| `spec.transport.type: teleport` | Today the only allowed value. Discriminator for future transports — see [Future transports](#future-transports). |
| `spec.transport.teleport.mcp.appName` | The Teleport application name muster routes the MCP HTTP call through. Must match the `public_addr` leftmost label registered on the Teleport side. |
| `spec.transport.teleport.mcp.identitySecretRef.name` | The local Kubernetes Secret carrying the tbot-issued mTLS identity for that app. Must match a `transport.teleport.apps[].identitySecret` value in the muster Helm release. Convention `tbot-identity-mcp-<cluster>` is recommended but **not enforced** — name it whatever you want, as long as the chart and CR agree. |
| `spec.transport.teleport.dex.{appName,identitySecretRef.name}` | Same shape, for the Dex token-exchange path. **Required when `auth.tokenExchange.enabled=true`** (CEL-enforced); otherwise omit. |
| `spec.auth.type: oauth` | Standard OAuth/OIDC identity. The legacy `auth.type: teleport` value was removed in TB-8b — Teleport is no longer an *identity* type, only a transport. |
| `spec.auth.tokenExchange.enabled: true` | Cross-cluster SSO: muster exchanges its local token for one valid on `glean`'s Dex. Required when `mcp-kubernetes` validates against `glean`'s Dex (it does; see PLAN §2). |
| `spec.auth.tokenExchange.dexTokenEndpoint` | The proxied URL muster reaches Dex through. Resolves to the Teleport proxy from Gazelle's pod network. |
| `spec.auth.tokenExchange.expectedIssuer` | The actual Dex issuer URL — what `iss` claim muster expects in the exchanged token. **Must be the in-cluster issuer**, not the proxy URL, because Dex's tokens carry the configured issuer regardless of how they were fetched (PLAN §2 invariant). Setting this prevents token substitution attacks via the proxy. |
| `spec.auth.tokenExchange.connectorId: giantswarm` | Canonical connector ID, **decoupled from cluster name**, locked in the 2026-04-29 design review. The `giantswarm` connector on the remote Dex (provisioned by `dex-operator` per TB-5) trusts Gazelle's Dex as the federated upstream. |

If the remote Dex requires client credentials for token exchange, add a
`clientCredentialsSecretRef` block — see
[`docs/reference/crds.md`](../reference/crds.md) for the full schema.

### `spec.transport` and `spec.auth` compose freely

The two blocks do not interact at the schema level: a CR with
`spec.transport.teleport` set and no `spec.auth.tokenExchange` is valid
(direct mTLS, no token exchange). Conversely, a CR with token exchange
enabled but no `spec.transport` reaches Dex over direct HTTPS (the
customer-Muster default). When **both** are set, muster sends the
`/token` POST through the same per-cluster Dex mTLS client it uses for
the MCP traffic — token exchange flows through the Teleport proxy too.

## Token-exchange config in detail

The token-exchange fields live under `spec.auth.tokenExchange`:

| Field | Purpose |
|---|---|
| `enabled` | Master switch. `false` by default. |
| `dexTokenEndpoint` | URL muster POSTs the token-exchange request to. Use the Teleport proxy URL (`https://dex-{cluster}.teleport.giantswarm.io/token`) when accessing through Teleport. |
| `expectedIssuer` | Expected `iss` claim on the exchanged token. **Set this explicitly** when accessing through a proxy — otherwise muster derives it from `dexTokenEndpoint`, which is the proxy URL, and validation fails. |
| `connectorId` | OIDC connector on the remote Dex. Canonical value `giantswarm` (locked 2026-04-29) for production; do not invent per-cluster IDs. |
| `scopes` | Optional override. Default `openid profile email groups` covers Kubernetes RBAC needs. |
| `clientCredentialsSecretRef` | Reference to a Kubernetes Secret with the OAuth client ID + secret registered as a static client on the remote Dex. Required when the remote Dex is configured to require client auth on `/token`. |

The `giantswarm` connector is decoupled from cluster name: every remote
Dex provisioned by `dex-operator` (TB-5) carries the same connector ID,
regardless of MC. CR authors do **not** need to vary `connectorId` per
cluster.

For the full TokenExchange / ClientCredentialsSecretRef schema (including
RBAC requirements for cross-namespace secret access), see
[`docs/reference/crds.md`](../reference/crds.md).

## Drift signals

Muster surfaces transport problems on
`MCPServer.status.conditions[type=TransportReady, status=False]` with a
machine-readable `reason`. **Look there first** — `kubectl get mcpserver
<name> -o yaml` shows the condition and a human-readable message naming
the offending resource.

| `reason` | Means | Fix |
|---|---|---|
| `SecretMissing` | The Secret named in `spec.transport.teleport.{mcp,dex}.identitySecretRef.name` does not exist in muster's namespace. | Either (preferred) add a matching entry to the muster Helm release's `transport.teleport.apps[]` list — see [Configure tbot identity](configure-tbot-identity.md#onboarding-a-new-mc) — or `kubectl patch` the CR to reference a Secret the chart already provisions. |
| `SecretInvalid` | Secret exists but is missing one of `tlscert` / `key` / `teleport-application-ca.pem`, or contains malformed PEM. | The chart RBAC restricts which Secrets tbot can write — usually a manual `kubectl edit` corrupted the Secret. Delete it; tbot will recreate it on the next renewal cycle. |
| `ConfigInvalid` | `spec.transport` is structurally invalid (e.g. `mcp.appName` is empty). CRD-level CEL catches this at admission; the runtime check is defense-in-depth. | Reapply the CR — the CEL message names the bad field. |
| `TransportError` | Catch-all for unexpected dispatcher errors (e.g. RBAC denied on the Secret read). | The condition message carries the underlying error string; check the muster pod logs for full context. |

These reasons are emitted by the `internal/teleport` dispatcher and are
authoritative — they map 1:1 to the sentinel errors in
`internal/teleport/dispatcher.go` (`ErrSecretMissing`, `ErrSecretInvalid`,
`ErrTransportInvalid`).

The same conditions feed Prometheus alerts (`MusterTransportSecretMissing`,
`MusterTransportClusterDrift`, `MusterTokenExchangeFailures` — see PLAN §6
TB-12). **The alerts are SRE-side**, intended to catch fleet-wide
problems. As a CR author, **always look at the CR's status first** — the
condition will tell you exactly which Secret or cluster is the problem,
which an alert at fleet scale cannot.

If `TransportReady` is `True`, transport is fine. Auth-side problems
(token exchange failures, audience mismatches, downstream 401s) surface
through events (`MCPServerTokenExchangeFailed`, `MCPServerTokenForwardingFailed`)
and the existing CR `state` field — see
[`docs/reference/crds.md`](../reference/crds.md#troubleshooting-token-exchange).

## Future transports

`spec.transport.type` is a discriminator. Today only `teleport` is
allowed; the schema is designed so additional sibling transports can be
added without breaking existing CRs:

```yaml
# Hypothetical future shape — NOT currently supported.
spec:
  transport:
    type: wireguard
    wireguard:
      peer: ...
```

A future `wireguard` (or any other transport) would land as a new sibling
field under `spec.transport` plus an additional enum value on
`spec.transport.type`. CRs that use `type: teleport` keep working
unchanged.

If you want a transport that does not exist yet, open an issue rather
than reaching for `headers:` or other side channels — the discriminator
exists exactly so we can add transports cleanly.

## Related

- [Configure tbot Identity (cluster-admin)](configure-tbot-identity.md) —
  what the muster Helm release provisions for the `appName` /
  `identitySecretRef` values you reference here.
- [Provision the Teleport Bot (SRE)](provision-teleport-bot.md) — the
  Teleport-side state that backs the identity material.
- [`docs/reference/crds.md`](../reference/crds.md) — the full
  `MCPServer` schema, including `spec.auth` and `spec.transport`
  reference tables.
- [PLAN.md §2](../../PLAN.md) — the OAuth-vs-Teleport separation of
  concerns that motivates this design.
- [PLAN.md §10](../../PLAN.md) — the six-step new-MC onboarding runbook
  (this doc owns step 6).
- [RFC 8693 — OAuth 2.0 Token Exchange](https://datatracker.ietf.org/doc/html/rfc8693)
