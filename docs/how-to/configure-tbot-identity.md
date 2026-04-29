# Configure tbot Identity (Helm)

> **Audience: cluster-admin** owning the muster Helm release on the
> management cluster (Gazelle for Giant Swarm production). This doc covers
> the `transport.teleport` Helm values block and the tbot identity Secrets
> the chart provisions. SREs running the Teleport-side bot script should
> read [Provision the Teleport Bot](provision-teleport-bot.md). MCPServer
> CR authors should read
> [Access Private MCP Servers](access-private-mcp-servers.md).

## What this chart provisions

When `transport.teleport.enabled: true`, the muster Helm chart renders a
**separate tbot Deployment** (distinct from the muster Deployment) plus the
supporting ConfigMap, RBAC, ServiceAccount, and per-app Kubernetes Secrets
— one Secret per entry in `transport.teleport.apps[]`.

> **The chart provisions identity material only.** Per-CR transport
> routing is in `spec.transport.teleport` on each MCPServer CR (PLAN §6
> TB-0 revised 2026-04-29 — *explicit fields*). Each CR names the
> Teleport application AND the local Kubernetes Secret it expects to
> consume; the chart's `apps[]` list provisions Secrets with those exact
> names. CR and chart values grep against each other directly. There is
> no naming-convention derivation. See
> [Access private MCP servers](access-private-mcp-servers.md) for the CR
> side.

For each Teleport-routed CR, you generally want **two** entries in
`apps[]`: one for the MCP HTTP traffic and one for the Dex token-exchange
path. (Two Secrets per remote MC is a hard requirement: each Teleport
`application` certificate is bound to a single app via `RouteToApp`, see
PLAN §9 "Cert-to-app binding constraint".)

A typical configuration for a single remote MC `glean`:

```yaml
transport:
  teleport:
    enabled: true
    proxyServer: teleport.giantswarm.io:443
    apps:
      - appName: mcp-kubernetes-glean
        identitySecret: tbot-identity-mcp-glean
      - appName: dex-glean
        identitySecret: tbot-identity-tx-glean
```

The corresponding MCPServer CR uses the same names verbatim — see the
worked example in
[Access Private MCP Servers](access-private-mcp-servers.md#worked-example-mcp-kubernetes-on-glean).

### Verify with `helm template`

```bash
cd helm/muster
helm template muster . \
  --set transport.teleport.enabled=true \
  --set 'transport.teleport.apps[0].appName=mcp-kubernetes-glean' \
  --set 'transport.teleport.apps[0].identitySecret=tbot-identity-mcp-glean' \
  --set 'transport.teleport.apps[1].appName=dex-glean' \
  --set 'transport.teleport.apps[1].identitySecret=tbot-identity-tx-glean'
```

The rendered tbot ConfigMap looks like:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: muster-tbot-config
data:
  tbot.yaml: |
    version: v2
    proxy_server: teleport.giantswarm.io:443
    onboarding:
      join_method: kubernetes
      # Bot ↔ tbot contract — must match the Teleport join-token name created by
      # scripts/provision-teleport-bot.sh (TB-3).
      token: muster-aggregator
    storage:
      type: memory
    outputs:
      - type: application
        app_name: "mcp-kubernetes-glean"
        destination:
          type: kubernetes_secret
          name: "tbot-identity-mcp-glean"
      - type: application
        app_name: "dex-glean"
        destination:
          type: kubernetes_secret
          name: "tbot-identity-tx-glean"
```

After deployment, `kubectl get secret -n muster-system` shows the
provisioned Secrets:

```text
tbot-identity-mcp-glean    Opaque   3
tbot-identity-tx-glean     Opaque   3
```

Each Secret carries the three keys tbot's `application` output writes:
`tlscert`, `key`, `teleport-application-ca.pem`.

## Helm values

The full Helm schema lives in `helm/muster/values.yaml` — **that file is
the source of truth**. The relevant fragment:

```yaml
transport:
  teleport:
    # Enable tbot identity provisioning. Default off — leaves customer
    # in-VPN deployments unaffected.
    enabled: false
    # The single GS Teleport proxy address — one cluster fronts all MCs.
    proxyServer: teleport.giantswarm.io:443
    # Optional explicit Teleport cluster name (used as the SA-token audience
    # for the kubernetes join method). When empty, derived by stripping the
    # port from proxyServer.
    clusterName: ""
    image:
      repository: public.ecr.aws/gravitational/teleport-distroless
      tag: ""              # falls back to the chart-pinned 17.5.4
      pullPolicy: IfNotPresent
    # Explicit list of (Teleport application, Kubernetes Secret) pairs.
    # No derivation: every entry is stated verbatim, and every MCPServer
    # CR references these exact names via spec.transport.teleport.{mcp,dex}
    # .{appName,identitySecretRef.name}.
    apps: []
    # - appName: mcp-kubernetes-glean
    #   identitySecret: tbot-identity-mcp-glean
    # - appName: dex-glean
    #   identitySecret: tbot-identity-tx-glean
    resources:
      requests: { cpu: 50m,  memory: 64Mi }
      limits:   { cpu: 200m, memory: 256Mi }
    readinessGate:
      image:
        repository: bitnami/kubectl
        tag: "1.30"
        pullPolicy: IfNotPresent
      pollIntervalSeconds: 5
      timeoutSeconds: 300
```

| Field | Purpose |
|---|---|
| `enabled` | Master switch. Default `false`. Customer Muster (in-VPN) leaves it off; only Giant Swarm's Gazelle deployment turns it on. |
| `proxyServer` | The single Teleport proxy `host:port`. One Teleport cluster fronts every MC, so this is **not** a list. |
| `clusterName` | Optional Teleport-cluster audience for the kubernetes join token. Empty by default; derived from `proxyServer`. |
| `image` | tbot container image. Tag pinned by the chart; override per environment if you must. |
| `apps[]` | The explicit list of `(appName, identitySecret)` pairs. Each entry produces a tbot output and a Kubernetes Secret. Duplicate `appName` values are a template-time error. |
| `resources` | tbot Deployment pod resources. Conservative defaults; tbot is lightweight. |
| `readinessGate` | The init container on the muster Deployment that blocks startup until every Secret in `apps[]` exists. See [Readiness gate](#readiness-gate). |

The CRD-side `appName` regex (`^[a-z][a-z0-9-]*$`) means
`apps[].appName` must follow the same shape: lowercase alphanumeric +
hyphens, starting with a letter. Mismatches between an MCPServer CR's
`spec.transport.teleport.{mcp,dex}.identitySecretRef.name` and the chart's
provisioned Secrets surface at runtime as
`MCPServer.status.conditions[type=TransportReady]=False` with reason
`SecretMissing` (see
[Access private MCP servers](access-private-mcp-servers.md#drift-signals)).

## Naming recommendation (not enforced)

The chart and CRD do **not** enforce any naming convention. Pick whatever
makes sense in your GitOps tree. For Giant Swarm production we recommend:

- **`appName`**: matches the Teleport-side `public_addr` leftmost label —
  e.g. `mcp-kubernetes-glean` for `mcp-kubernetes-glean.teleport.giantswarm.io`.
  This isn't a Muster requirement; it's a Teleport requirement (the cert
  carries `RouteToApp` keyed on the app name).
- **`identitySecret`**: `tbot-identity-mcp-<cluster>` for the MCP role and
  `tbot-identity-tx-<cluster>` for the Dex token-exchange role. Pure
  convention; rename to fit your existing tbot-output topology if you
  already have one.

The pre-2026-04-29 design enforced these patterns via a `<role>-<cluster>`
derivation in the chart and dispatcher. We dropped the derivation in favor
of explicit fields because the implicit contract was hard to grep across
repos and forced cluster-admins onto a Muster-imposed pattern. The
convention is still useful as a *recommendation*; just not a constraint.

## Readiness gate

When `transport.teleport.enabled: true`, the muster Deployment gets an
init container `tbot-identity-wait` (rendered from
`transport.teleport.readinessGate.*`) that polls for every Secret named in
`apps[].identitySecret` to exist before muster goes Ready:

```bash
SECRETS=" tbot-identity-mcp-glean tbot-identity-tx-glean"
DEADLINE=$(( $(date +%s) + 300 ))
while :; do
  ...
done
```

The gate prevents MCPServer reconciles from racing tbot on a fresh
deploy: a missing Secret >5 minutes crashloops the init container, which
is the desired signal — fix the underlying problem (tbot stuck, role not
allowlisted on the Teleport side, etc.) before muster comes up. Tune the
deadline via `transport.teleport.readinessGate.timeoutSeconds`.

## Onboarding a new MC

The full six-step flow is documented in [PLAN.md §10](../../PLAN.md). The
parts cluster-admin owns are:

1. **Confirm the bot's role allowlist includes the new cluster** — this is
   the SRE's responsibility; see
   [Provision the Teleport Bot](provision-teleport-bot.md#onboarding-a-new-mc).
   Must happen **before** the Helm change below.
2. **Append two entries** to `transport.teleport.apps[]` in the muster
   Helm values for Gazelle — one for the MCP role, one for the Dex role:

   ```yaml
   transport:
     teleport:
       apps:
         - appName: mcp-kubernetes-glean
           identitySecret: tbot-identity-mcp-glean
         - appName: dex-glean
           identitySecret: tbot-identity-tx-glean
         # NEW for finch:
         - appName: mcp-kubernetes-finch
           identitySecret: tbot-identity-mcp-finch
         - appName: dex-finch
           identitySecret: tbot-identity-tx-finch
   ```

3. **Flux reconciles** the Helm release. The chart renders new tbot
   `outputs[]`; tbot picks them up; new Secrets land; muster Deployment
   rolls (annotated by the tbot ConfigMap checksum so a no-op spec
   change still triggers a rollout if you tweak the chart values).
4. **The MCPServer CR** (CR-author's responsibility) declares the same
   `appName` and `identitySecretRef.name` values verbatim under
   `spec.transport.teleport.{mcp,dex}`. Until that CR lands, no traffic
   flows over the new identity material — the Secrets exist and tbot
   keeps them rotated, but nothing references them.

A drift-detection alert (`MusterTransportSecretMissing`, TB-12) fires if
any MCPServer CR references a Secret that does **not** exist. The
`MCPServer.status.conditions[type=TransportReady]=False` reason
`SecretMissing` is the primary feedback channel for the CR author.

## Related

- [Provision the Teleport Bot (SRE)](provision-teleport-bot.md) — the
  Teleport-side counterpart that creates the join token this chart
  consumes.
- [Access Private MCP Servers (MCPServer author)](access-private-mcp-servers.md)
  — the CR-side schema and examples.
- [`helm/muster/values.yaml`](../../helm/muster/values.yaml) — source of
  truth for the values schema.
