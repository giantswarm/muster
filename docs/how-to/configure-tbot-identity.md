# Configure tbot Identity (Helm)

> **Audience: cluster-admin** owning the muster Helm release on the
> management cluster (Gazelle for Giant Swarm production). This doc covers
> the `transport.teleport` Helm values block, the tbot identity Secrets the
> chart provisions, and the locked `<role>-<cluster>` naming convention.
> SREs running the Teleport-side bot script should read
> [Provision the Teleport Bot](provision-teleport-bot.md). MCPServer CR
> authors should read
> [Access Private MCP Servers](access-private-mcp-servers.md).

## What this chart provisions

When `transport.teleport.enabled: true`, the muster Helm chart renders, for
each entry in `transport.teleport.clusters[]`, a **separate tbot Deployment**
(distinct from the muster Deployment) plus the supporting ConfigMap, RBAC,
ServiceAccount, and per-cluster Kubernetes Secrets.

> **The chart provisions identity material only.** Per-CR transport routing
> is in `spec.transport.teleport.cluster` on each MCPServer CR, **not** in
> Helm values. There is no deployment-level routing ConfigMap. See
> [Access private MCP servers](access-private-mcp-servers.md) for the CR
> side.

For each `clusters[].name` (e.g. `glean`), the chart renders **two** tbot
outputs:

| Role | Teleport app name | tbot output Secret |
|---|---|---|
| MCP traffic | `mcp-kubernetes-{cluster}` | `tbot-identity-mcp-{cluster}` |
| Dex token-exchange | `dex-{cluster}` | `tbot-identity-tx-{cluster}` |

Two Secrets per cluster is a hard requirement: each Teleport `application`
certificate is bound to a single app via `RouteToApp` (PLAN §9, "Cert-to-app
binding constraint").

### Verify with `helm template`

```bash
cd helm/muster
helm template muster . \
  --set transport.teleport.enabled=true \
  --set 'transport.teleport.clusters[0].name=glean'
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

After deployment, `kubectl get secret -n muster-system` shows the rendered
Secrets:

```text
tbot-identity-mcp-glean    Opaque   3
tbot-identity-tx-glean     Opaque   3
```

Each Secret carries the three keys tbot's `application` output writes:
`tlscert`, `key`, `teleport-application-ca.pem`.

## Helm values

The full Helm schema lives in `helm/muster/values.yaml` — **that file is the
source of truth**. The relevant fragment:

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
    # Provisioning hint — one entry per remote MC.
    clusters: []
    # - name: glean
    # - name: finch
    # Escape hatch (rarely needed; see "When to use apps[]" below).
    apps: []
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
| `clusters[]` | The provisioning hint. Each entry's `name` is fed through the `<role>-<cluster>` derivation to produce two tbot outputs (MCP + Dex) and two Secrets. |
| `apps[]` | Escape hatch — see [When to use apps[]](#when-to-use-apps) below. Empty in normal use. |
| `resources` | tbot Deployment pod resources. Conservative defaults; tbot is lightweight. |
| `readinessGate` | The init container on the muster Deployment that blocks startup until every derived Secret exists. See [Readiness gate](#readiness-gate). |

The CRD-side schema for `spec.transport.teleport.cluster` (validated regex
`^[a-z][a-z0-9-]*$`) means `clusters[].name` must follow the same shape:
lowercase alphanumeric + hyphens, starting with a letter. Mismatches between
helm `clusters[]` and a CR's `spec.transport.teleport.cluster` are surfaced
at runtime as `MCPServer.status.conditions[type=TransportReady]=False` with
reason `ClusterNotConfigured` (see
[Access private MCP servers](access-private-mcp-servers.md#drift-signals)).

## The `<role>-<cluster>` naming convention is locked

The four derived names per cluster (`mcp-kubernetes-{cluster}`,
`dex-{cluster}`, `tbot-identity-mcp-{cluster}`, `tbot-identity-tx-{cluster}`)
are a **contract** between three components:

| Component | Where the contract lives |
|---|---|
| Teleport-side app names | `giantswarm-management-clusters` Helm values for `teleportKubeAgent.apps[]` (PLAN §6 TB-1/TB-2) |
| Helm chart (this doc) | `helm/muster/templates/_helpers.tpl` `muster.tbot.outputs` derivation, asserted by the helm-test Pod `muster-tbot-naming-test` |
| muster Go code | `internal/teleport/dispatcher.go` — `MCPAppName`, `DexAppName`, `MCPSecretName`, `DexSecretName` helpers |

Changing one side without the others **breaks the contract**:

- Change in the chart only → muster's dispatcher cannot find the Secret →
  `MCPServer.status.conditions[TransportReady]=False` with reason
  `SecretMissing`.
- Change in muster only → CR resolves to a name that the chart never
  rendered → same condition, same reason.
- Change in `giantswarm-management-clusters` only → tbot fails to obtain
  the cert (Teleport rejects an unknown `app_name`).

If you have a real reason to change the convention, it must land as a
single coordinated PR set across all three repos plus the helm-test
assertions in `helm/muster/templates/tests/test-tbot-naming.yaml`. **Talk
to the muster team before touching this.**

## When to use `apps[]`

`apps[]` is an escape hatch for non-conformant tbot output names. In normal
operation it is empty; the chart derives every output from `clusters[]`.

Reach for it only when:

- A Teleport-side app was registered with a name that does not follow the
  `<role>-<cluster>` convention (e.g. a legacy `monitoring-mcp-glean`
  instead of `mcp-kubernetes-glean`), and rerolling the registration is
  blocked.
- You are running a prototype third transport role for one cluster only
  and do not want to thread it through the full `clusters[]` derivation.

If you find yourself reaching for `apps[]`, **talk to the muster team
first** — there is almost always a better path. The escape hatch exists so
the chart never blocks emergency operational fixes, not so it can become a
second supported configuration surface.

`apps[]` entries with a `name` matching one derived from `clusters[]`
override that derived entry. Duplicate `name` values within `apps[]` itself
are a templating-time error.

## Readiness gate

When `transport.teleport.enabled: true`, the muster Deployment gets an init
container `tbot-identity-wait` (rendered from
`transport.teleport.readinessGate.*`) that polls for every derived Secret to
exist before muster goes Ready:

```bash
SECRETS=" tbot-identity-mcp-glean tbot-identity-tx-glean"
DEADLINE=$(( $(date +%s) + 300 ))
while :; do
  ...
done
```

The gate prevents MCPServer reconciles from racing tbot on a fresh deploy:
a missing Secret >5 minutes crashloops the init container, which is the
desired signal — fix the underlying problem (tbot stuck, role not
allowlisted on the Teleport side, etc.) before muster comes up. Tune the
deadline via `transport.teleport.readinessGate.timeoutSeconds`.

## Onboarding a new MC

The full six-step flow is documented in [PLAN.md §10](../../PLAN.md). The
parts cluster-admin owns are:

1. **Confirm the bot's role allowlist includes the new cluster** — this is
   the SRE's responsibility; see
   [Provision the Teleport Bot](provision-teleport-bot.md#onboarding-a-new-mc).
   Must happen **before** the Helm change below.
2. **Append the cluster** to `transport.teleport.clusters[]` in the muster
   Helm values for Gazelle:

   ```yaml
   transport:
     teleport:
       clusters:
         - name: glean
         - name: finch         # NEW
   ```

3. **Flux reconciles** the Helm release. The chart renders new tbot
   `outputs[]`; tbot picks them up; new Secrets land; muster Deployment
   rolls (annotated by the tbot ConfigMap checksum so a no-op spec change
   still triggers a rollout if you tweak the chart values).
4. **The MCPServer CR** (CR-author's responsibility) declares
   `spec.transport.teleport.cluster: finch`. Until that CR lands, no
   traffic flows over the new identity material — the Secrets exist and
   tbot keeps them rotated, but nothing references them.

A drift-detection alert (`MusterTransportClusterDrift`, TB-12) fires if any
MCPServer CR references a cluster that is **not** in this chart's
`clusters[]`. Catching it in alerts is the safety net; the
`MCPServer.status.conditions[type=TransportReady]=False` reason
`ClusterNotConfigured` is the primary feedback channel for the CR author.

## Related

- [Provision the Teleport Bot (SRE)](provision-teleport-bot.md) — the
  Teleport-side counterpart that creates the join token this chart
  consumes.
- [Access Private MCP Servers (MCPServer author)](access-private-mcp-servers.md)
  — the CR-side schema and examples.
- [`helm/muster/values.yaml`](../../helm/muster/values.yaml) — source of
  truth for the values schema.
- [PLAN.md §6 TB-4](../../PLAN.md) — design rationale for the chart shape.
- [PLAN.md §10](../../PLAN.md) — full operational runbook for new-MC
  onboarding.
