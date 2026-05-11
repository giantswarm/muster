# Runbook — Deploy muster + agentgateway on a Giant Swarm cluster

End-to-end runbook for the **per-customer bootstrap deployment** of muster + agentgateway. Validated on glean (azuretest installation, May 2026).

This is the **default deployment shape**: one stack per customer, deployed on the customer's central MC. The customer's other MCs (if any) host MCP backend pods only — no local muster, no local agw — and are reached from the central muster via Teleport tunnel (see Phase D').

For customers who need per-MC isolation (regulatory boundaries, hard failure isolation, cross-MC latency, etc.), the upgrade path is to deploy this same runbook on each non-central MC and swap `MCPServer.spec.auth` from `teleport` to `tokenExchange` (Phase 7 of the architecture-review plan, trigger-driven).

Reproduces:

- Bitnami Valkey for OAuth token storage
- muster (with mcp-oauth, CIMD, Dex federation)
- agentgateway data plane **behind muster** (Phase 1 topology — every same-cluster MCP call traverses agw for audit/metrics/traces)
- Public Envoy HTTPRoute pointing all traffic at muster (no `/mcp` split)
- Per-backend `AgentgatewayBackend` + `HTTPRoute` (`/mcp/k8s`, `/mcp/prom` for local backends; cross-MC backends via Teleport)
- MCPServer CRDs whose `url` points at agw for local backends or at Teleport tunnel for remote backends
- PodMonitor scraping `agentgateway_*` metrics
- AgentgatewayPolicy for tracing (OTLP gRPC → Tempo with multi-tenant `X-Scope-OrgID`)

## Topology

```
Claude Code / Cursor → Envoy(giantswarm-default) → muster pod (OAuth + aggregator)
                                                      │
                                                      ↓ per backend, http://muster-agw.muster.svc/mcp/<name>
                                                   muster-agw pod (agentgateway)
                                                      │
                                                      ├─► mcp-kubernetes.mcp-kubernetes.svc:8080
                                                      ├─► mcp-prometheus.mcp-prometheus.svc:8080
                                                      └─► (any future MCPServer pointed at agw)

                                     Backends reached via Teleport tunnel keep their direct path
                                     and bypass agw — `MCPServer.spec.auth.type=teleport`
```

Cluster used as reference: `<CLUSTER>` = `glean`, `<INSTALLATION>` = `azuretest`. Substitute for your target cluster.

## Prerequisites

| Component | Why | Verify |
|---|---|---|
| `dex` running, reachable at `https://dex.<CLUSTER>.<INSTALLATION>.gigantic.io` | upstream IdP for muster | `curl -s https://dex.<CLUSTER>.<INSTALLATION>.gigantic.io/.well-known/openid-configuration` returns JSON |
| `dex-k8s-authenticator` static client in dex | downstream audience for SSO to mcp-kubernetes / mcp-prometheus | `kubectl -n giantswarm get secret dex -o jsonpath='{.data.config\.yaml}' \| base64 -d \| grep dex-k8s-authenticator` |
| `dex-k8s-authenticator` has `muster-token-exchange-<CLUSTER>` as a trusted peer | enables RFC 8693 token exchange | check `trustedPeers` list under `dex-k8s-authenticator` static client |
| `mcp-kubernetes` deployment in `mcp-kubernetes` namespace, Service on port 8080 | downstream MCP backend | `kubectl -n mcp-kubernetes get svc,deploy` |
| `mcp-prometheus` deployment in `mcp-prometheus` namespace, Service on port 8080 | downstream MCP backend | same |
| `giantswarm-default` Gateway (Envoy Gateway) accepting `*.<CLUSTER>.<INSTALLATION>.gigantic.io` | public TLS termination | `kubectl -n envoy-gateway-system get gateway giantswarm-default` |
| `tempo-distributor` Service in `tempo` namespace, port 4317 | OTLP gRPC endpoint for traces | `kubectl -n tempo get svc tempo-distributor` |
| Tempo tenant ID for trace ingestion (multi-tenant Tempo on GS) | required `X-Scope-OrgID` header on OTLP writes | usually `giantswarm` — confirm with your platform team |
| Prometheus operator CRDs (`PodMonitor`, `ServiceMonitor`) | metrics scraping | `kubectl get crd podmonitors.monitoring.coreos.com` |
| Loki + alloy/promtail scraping pod logs in `muster` namespace | audit log pipeline | `kubectl get app -A \| grep loki` |
| Kyverno PSS policies (if enforced) | all manifests below are PSS-restricted-compliant | `kubectl get clusterpolicy \| grep pod-security` |

## Phase A — Pre-deployment

### A.1 Register the muster OAuth2 client in dex

Edit dex's static config (typically managed via the GS config-controller or a Flux-managed Secret). Add under `staticClients`:

```yaml
- id: muster-<CLUSTER>
  name: muster-<CLUSTER>
  redirectURIs:
    - https://muster.<CLUSTER>.<INSTALLATION>.gigantic.io/oauth/callback
  secret: <GENERATE-RANDOM-32-BYTES-BASE64>
```

> NOTE: the redirect URI **must** be `/oauth/callback`, not `/oauth/proxy/callback`.
> `/oauth/proxy/callback` is muster's outbound RP callback (muster → downstream MCPs);
> the dex-to-muster server callback path is `/oauth/callback`.

Rolling out depends on how dex is managed in the target cluster:
- If dex config is in a Flux-managed Secret: edit the source repo, push, wait for reconcile.
- If managed via `dex-operator` `OAuth2Client` CRD: `kubectl apply` the CR.
- If hand-managed: `kubectl -n giantswarm edit secret dex` and trigger pod restart.

### A.2 Generate the OAuth credentials secret

```bash
DEX_CLIENT_SECRET=$(openssl rand -base64 32)            # match what you put in dex
ENCRYPTION_KEY=$(openssl rand -base64 32)               # AES-256-GCM key for token store
VALKEY_PASSWORD=$(openssl rand -base64 32)              # Valkey AUTH

kubectl --context=teleport.giantswarm.io-<CLUSTER> create namespace muster

kubectl --context=teleport.giantswarm.io-<CLUSTER> -n muster create secret generic muster-oauth-credentials \
  --from-literal=dex-client-secret="${DEX_CLIENT_SECRET}" \
  --from-literal=encryption-key="${ENCRYPTION_KEY}" \
  --from-literal=valkey-password="${VALKEY_PASSWORD}"
```

The secret keys are referenced by both Valkey and muster Helm values below — keep names identical.

## Phase B — Helm installs

### B.1 Bitnami Valkey

`valkey-values.yaml`:

```yaml
architecture: standalone

auth:
  existingSecret: muster-oauth-credentials
  existingSecretPasswordKey: valkey-password

primary:
  persistence:
    enabled: false
  podSecurityContext:
    enabled: true
    fsGroupChangePolicy: Always
    sysctls: []
    supplementalGroups: []
    fsGroup: 1001
  containerSecurityContext:
    enabled: true
    runAsUser: 1001
    runAsGroup: 1001
    runAsNonRoot: true
    privileged: false
    readOnlyRootFilesystem: true
    allowPrivilegeEscalation: false
    capabilities:
      drop:
        - ALL
    seccompProfile:
      type: RuntimeDefault
  resources:
    requests:
      cpu: 50m
      memory: 64Mi
    limits:
      memory: 256Mi

metrics:
  enabled: false

networkPolicy:
  enabled: false
```

Install:

```bash
helm repo add bitnami https://charts.bitnami.com/bitnami
helm repo update

helm --kube-context=teleport.giantswarm.io-<CLUSTER> -n muster upgrade --install muster-valkey bitnami/valkey \
  -f valkey-values.yaml \
  --version 9.0.3
```

### B.2 agentgateway CRDs + controller

```bash
helm --kube-context=teleport.giantswarm.io-<CLUSTER> -n agentgateway-system upgrade --install \
  agentgateway-crds oci://cr.agentgateway.dev/charts/agentgateway-crds \
  --version v1.1.0 --create-namespace

helm --kube-context=teleport.giantswarm.io-<CLUSTER> -n agentgateway-system upgrade --install \
  agentgateway oci://cr.agentgateway.dev/charts/agentgateway \
  --version v1.1.0
```

Verify the GatewayClass:

```bash
kubectl --context=teleport.giantswarm.io-<CLUSTER> get gatewayclass agentgateway
# CONTROLLER agentgateway.dev/agentgateway   ACCEPTED True
```

### B.3 muster

`muster-values.yaml`:

```yaml
replicaCount: 1

crds:
  install: false   # CRDs must be applied separately or via a different mechanism

image:
  pullPolicy: IfNotPresent
  tag: "0.1.139"   # PIN — chart's appVersion default is 0.1.0 which is way too old

# PSS-compliant pod + container security context (required if Kyverno PSS is enforced)
podSecurityContext:
  runAsNonRoot: true
  seccompProfile:
    type: RuntimeDefault

securityContext:
  runAsNonRoot: true
  allowPrivilegeEscalation: false
  capabilities:
    drop:
      - ALL
  seccompProfile:
    type: RuntimeDefault

# No public exposure from the chart — Envoy fronts muster directly via HTTPRoute below
ingress:
  enabled: false

gatewayAPI:
  enabled: false

resources:
  requests:
    cpu: 50m
    memory: 128Mi
  limits:
    memory: 512Mi

muster:
  aggregator:
    port: 8090
    transport: streamable-http

  events: false
  debug: true

  oauth:
    mcpClient:
      enabled: true
      publicUrl: https://muster.<CLUSTER>.<INSTALLATION>.gigantic.io
      callbackPath: /oauth/proxy/callback     # muster as RP to downstream MCPs
      cimd:
        path: /.well-known/oauth-client.json

    server:
      enabled: true
      baseUrl: https://muster.<CLUSTER>.<INSTALLATION>.gigantic.io
      provider: dex
      dex:
        issuerUrl: https://dex.<CLUSTER>.<INSTALLATION>.gigantic.io
        clientId: muster-<CLUSTER>            # matches the dex static client id from Phase A.1

      existingSecret: muster-oauth-credentials

      storage:
        type: valkey
        valkey:
          url: muster-valkey-primary.muster.svc:6379
          existingSecret: muster-oauth-credentials
          secretKeyPassword: valkey-password

      encryptionKey: true
      allowPublicClientRegistration: false
```

Install (use the in-tree Helm chart from the muster repo):

```bash
helm --kube-context=teleport.giantswarm.io-<CLUSTER> -n muster upgrade --install muster \
  /path/to/muster-repo/helm/muster \
  -f muster-values.yaml
```

Apply CRDs separately:

```bash
kubectl --context=teleport.giantswarm.io-<CLUSTER> apply -f /path/to/muster-repo/helm/muster/crds/
```

## Phase C — agw resources (Phase 1 topology: agw behind muster)

### C.1 AgentgatewayParameters + Gateway

`agentgateway-<CLUSTER>.yaml`:

```yaml
---
apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayParameters
metadata:
  name: muster-trial
  namespace: muster
spec:
  logging:
    format: json
    level: info
  env:
    # Required when Tempo is multi-tenant (GS observability stack default).
    # The OTel SDK in agw reads this env var and adds the header to OTLP exports.
    - name: OTEL_EXPORTER_OTLP_TRACES_HEADERS
      value: X-Scope-OrgID=<TEMPO_TENANT_ID>     # typically "giantswarm"
  service:
    spec:
      type: ClusterIP
  deployment:
    spec:
      template:
        spec:
          securityContext:
            runAsNonRoot: true
            seccompProfile:
              type: RuntimeDefault
          containers:
            - name: agentgateway
              securityContext:
                runAsNonRoot: true
                allowPrivilegeEscalation: false
                capabilities:
                  drop:
                    - ALL
                seccompProfile:
                  type: RuntimeDefault
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: muster-agw
  namespace: muster
spec:
  gatewayClassName: agentgateway
  infrastructure:
    parametersRef:
      group: agentgateway.dev
      kind: AgentgatewayParameters
      name: muster-trial
  listeners:
    - name: http
      port: 80
      protocol: HTTP
      allowedRoutes:
        namespaces:
          from: Same
```

### C.2 Per-backend `AgentgatewayBackend` + `HTTPRoute`

> NOTE: `AgentgatewayBackend.spec.mcp.targets[].static.backendRef` does not accept a `namespace` field — cross-namespace references aren't supported via `backendRef`. Use `static.host` with the FQDN of the target service instead.

`agw-per-backend-<CLUSTER>.yaml`:

```yaml
---
apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayBackend
metadata:
  name: muster-mcp-k8s
  namespace: muster
spec:
  mcp:
    targets:
      - name: kubernetes
        static:
          host: mcp-kubernetes.mcp-kubernetes.svc.cluster.local
          port: 8080
          protocol: StreamableHTTP
---
apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayBackend
metadata:
  name: muster-mcp-prom
  namespace: muster
spec:
  mcp:
    targets:
      - name: prometheus
        static:
          host: mcp-prometheus.mcp-prometheus.svc.cluster.local
          port: 8080
          protocol: StreamableHTTP
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: muster-mcp-k8s
  namespace: muster
spec:
  parentRefs:
    - name: muster-agw
      namespace: muster
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /mcp/k8s
      backendRefs:
        - group: agentgateway.dev
          kind: AgentgatewayBackend
          name: muster-mcp-k8s
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: muster-mcp-prom
  namespace: muster
spec:
  parentRefs:
    - name: muster-agw
      namespace: muster
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /mcp/prom
      backendRefs:
        - group: agentgateway.dev
          kind: AgentgatewayBackend
          name: muster-mcp-prom
```

### C.3 Public HTTPRoute on Envoy + BackendTrafficPolicy

In Phase 1 topology, agw is **internal-only**. The public route is a single rule pointing all traffic at muster (which then calls agw outbound for backends).

The `BackendTrafficPolicy` is needed only if your platform `Gateway` has a gateway-level error-pages policy that intercepts upstream errors with a 14k HTML 401 page (default on GS Envoy gateways) — its mere existence per-route overrides that.

`public-route-<CLUSTER>.yaml`:

```yaml
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: muster-public
  namespace: muster
spec:
  parentRefs:
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: giantswarm-default
      namespace: envoy-gateway-system
  hostnames:
    - muster.<CLUSTER>.<INSTALLATION>.gigantic.io
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /
      backendRefs:
        - group: ""
          kind: Service
          name: muster
          port: 8090
          weight: 1
---
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: BackendTrafficPolicy
metadata:
  name: muster-public
  namespace: muster
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: HTTPRoute
      name: muster-public
  # Empty otherwise — its existence overrides the gateway-level error-pages policy
```

Apply all three files:

```bash
kubectl --context=teleport.giantswarm.io-<CLUSTER> apply \
  -f agentgateway-<CLUSTER>.yaml \
  -f agw-per-backend-<CLUSTER>.yaml \
  -f public-route-<CLUSTER>.yaml
```

## Phase D — MCPServer CRDs

In Phase 1 topology, `MCPServer.spec.url` points at the **gateway URL for that backend**, not at the backend service directly. Same-cluster backends only.

For backends reached via Teleport (`auth.type: teleport`), leave `spec.url` as the Teleport tunnel address — those bypass agw entirely.

`mcpservers-<CLUSTER>.yaml`:

```yaml
---
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: mcp-kubernetes
  namespace: muster
spec:
  type: streamable-http
  url: http://muster-agw.muster.svc/mcp/k8s
  autoStart: true
  toolPrefix: k8s
  description: Kubernetes MCP server (via agw)
  auth:
    forwardToken: true
    requiredAudiences:
      - dex-k8s-authenticator
---
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: mcp-prometheus
  namespace: muster
spec:
  type: streamable-http
  url: http://muster-agw.muster.svc/mcp/prom
  autoStart: true
  toolPrefix: prom
  description: Prometheus/Mimir MCP server (via agw)
  auth:
    forwardToken: true
    requiredAudiences:
      - dex-k8s-authenticator
```

Apply:

```bash
kubectl --context=teleport.giantswarm.io-<CLUSTER> apply -f mcpservers-<CLUSTER>.yaml
```

> NOTE: muster's orchestrator only registers MCPServer services at startup today. After applying new MCPServer CRs, restart the muster pod so they get registered:
>
> ```bash
> kubectl --context=teleport.giantswarm.io-<CLUSTER> -n muster rollout restart deploy/muster
> ```
>
> Expected steady state: `STATUS: Auth Required` for both — they're waiting for a per-session SSO token from a connecting user.

## Phase D' — Cross-MC backends reached via Teleport (when applicable)

If the customer has multiple MCs and the central muster (deployed in Phase B.3 above) needs to reach backends on the customer's other MCs, those remote backends are reached via Teleport tunnel. **The remote MCs themselves do not run muster, agw, or valkey** — only the backend MCP pods plus a Teleport app registration.

### D'.1 Teleport identity on the central muster pod

The central muster pod needs a Teleport machine-ID credential scoped to the customer's MCs. Two common patterns:

| Pattern | When |
|---|---|
| **tbot sidecar** | Identity is rotated by tbot inside the muster pod. Same model GS uses for other in-pod Teleport access. |
| **Mounted identity Secret** | An external operator rotates the identity Secret; muster pod mounts it. Simpler if you already have a rotator. |

Either way, the muster pod is started with `MUSTER_TELEPORT_IDENTITY_PATH` (or equivalent) pointing at the credential. The Helm chart supports both patterns; pick one and configure it in `muster-values.yaml`.

### D'.2 Teleport app registration on the remote MC

On each remote MC hosting backends the central muster needs to reach, register the backend services as Teleport applications. Standard GS pattern via the `teleport-applications` App CR or `Application` resource:

```yaml
apiVersion: app.giantswarm.io/v1alpha1
kind: App
metadata:
  name: mcp-k8s-app
  namespace: teleport
spec:
  ...
  # registers mcp-kubernetes.<remote-MC-namespace>.svc as a Teleport-accessible app
  # reachable as https://mcp-k8s-<remote-MC>.<TELEPORT_PROXY_DOMAIN>/mcp
```

(Exact CR shape depends on the customer's existing Teleport setup; see the customer's existing Teleport app definitions for analogous services as reference.)

### D'.3 Teleport role grants on the central muster's identity

Grant the muster pod's Teleport identity access to the Teleport apps registered in D'.2. Role scoped to the customer's MCs:

```yaml
kind: role
metadata:
  name: muster-<CUSTOMER>-mcp-access
spec:
  allow:
    app_labels:
      customer: <CUSTOMER>
      service: mcp-*
```

### D'.4 `MCPServer` CRD for the remote backend

On the central MC (same `muster` namespace as the local MCPServer CRDs from Phase D), add one MCPServer entry per remote backend:

```yaml
---
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: mcp-k8s-<REMOTE-MC>
  namespace: muster
spec:
  type: streamable-http
  url: https://mcp-k8s-<REMOTE-MC>.<TELEPORT_PROXY_DOMAIN>/mcp
  autoStart: true
  toolPrefix: <REMOTE_MC_SHORTNAME>_k8s     # disambiguates tools in the catalog
  description: Kubernetes MCP server on <REMOTE-MC> (reached via Teleport)
  auth:
    type: teleport
    teleport:
      appName: mcp-k8s-<REMOTE-MC>
      identitySecretName: muster-teleport-identity
      identitySecretNamespace: muster
```

After applying, restart the muster pod (same caveat as Phase D — orchestrator registers on startup).

### D'.5 Verify

```bash
# MCPServer should appear with auth.type=teleport
kubectl --context=teleport.giantswarm.io-<CENTRAL-CLUSTER> -n muster get mcpserver mcp-k8s-<REMOTE-MC>

# muster pod logs should show Teleport tunnel established (lazy: first tool call triggers it)
kubectl --context=teleport.giantswarm.io-<CENTRAL-CLUSTER> -n muster logs deploy/muster | grep -i teleport

# From a Claude Code session connected to the customer's central muster, list tools and
# look for the toolPrefix from D'.4 (e.g., x_<REMOTE_MC_SHORTNAME>_k8s_list_pods).
# Calling the tool should succeed and the call should appear in the central muster-agw's
# audit log with route=muster/muster-mcp-<REMOTE-MC>.
```

### When to upgrade a remote MC to its own full stack (per-MC variant)

If the customer hits any of the following, run this entire runbook on the remote MC as well (treating it as a new central MC for its own users), then swap the central muster's MCPServer entry for that backend from `auth.teleport` to `auth.tokenExchange` (Phase 7 of the architecture-review plan):

- Regulatory requirement for per-MC auth boundary
- Hard requirement for failure isolation (central muster outage must not affect remote MC's local users or workloads)
- Cross-MC latency that hurts AI workloads
- Markedly different RBAC requirements per MC

Phase 7 is trigger-driven; the default `auth.teleport` pattern keeps working for as long as the customer doesn't need the upgrade.

## Phase E — Observability

### E.1 PodMonitor for agentgateway metrics

`agw-podmonitor.yaml`:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
metadata:
  name: muster-agw
  namespace: muster
  labels:
    application.giantswarm.io/team: bumblebee   # adjust to your team
spec:
  selector:
    matchLabels:
      gateway.networking.k8s.io/gateway-name: muster-agw
  podMetricsEndpoints:
    - port: metrics
      path: /metrics
      interval: 30s
```

agentgateway exposes Prometheus metrics on port 15020 named `metrics`, path `/metrics`. Metric names are prefixed `agentgateway_*` (request counters, MCP method counts, latency histograms, byte counts, xDS messages, tokio runtime stats).

### E.2 AgentgatewayPolicy — tracing

agw natively logs MCP-aware fields in its access log when `AgentgatewayParameters.spec.logging.format=json` is set, including `mcp.method.name`, `mcp.target` (backend name from `AgentgatewayBackend.spec.mcp.targets[].name`), `mcp.tool.name` (a structured object with `target`/`name`/`arguments`/`result`), `mcp.session.id`, `trace.id`, and `span.id`. No CEL extension is required for the access log itself — Loki's JSON parser flattens the structured field into queryable labels (`mcp_tool_name_name`, `mcp_tool_name_target`, etc.).

For tracing, the policy points at Tempo and adds a couple of CEL-derived span attributes that make trace search by tool name useful.

`agw-frontend-policy.yaml`:

```yaml
---
apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayPolicy
metadata:
  name: muster-agw-frontend
  namespace: muster
spec:
  targetRefs:
    - group: gateway.networking.k8s.io
      kind: Gateway
      name: muster-agw
  frontend:
    accessLog: {}
    tracing:
      protocol: GRPC
      randomSampling: "1.0"        # 100% in trial; drop to 0.1 in prod
      backendRef:
        kind: Service
        name: tempo-distributor
        namespace: tempo
        port: 4317
      resources:
        - name: service.name
          expression: '"muster-agw"'
        - name: deployment.environment
          expression: '"<CLUSTER>"'
      attributes:
        add:
          - name: mcp.tool
            expression: 'has(mcp.tool) ? mcp.tool.name : ""'
          - name: mcp.target
            expression: 'has(mcp.target) ? mcp.target : ""'
```

> NOTE: the `tracing` block in the CRD does not expose a way to add request headers to the OTLP export. Multi-tenant Tempo (GS default) requires `X-Scope-OrgID`. We set this via the `OTEL_EXPORTER_OTLP_TRACES_HEADERS` env var in `AgentgatewayParameters.spec.env` (Phase C.1) — the OTel Rust SDK reads this env var and applies the header automatically. Without it, agw spans are silently rejected by Tempo with `BatchSpanProcessor.ExportError "no org id"`.

Apply both:

```bash
kubectl --context=teleport.giantswarm.io-<CLUSTER> apply -f agw-podmonitor.yaml -f agw-frontend-policy.yaml
```

After applying, restart agw to pick up params/policy changes:

```bash
kubectl --context=teleport.giantswarm.io-<CLUSTER> -n muster rollout restart deploy/muster-agw
```

> NOTE: restarting agw invalidates muster's existing MCP sessions to backends. After agw restart, also restart muster:
>
> ```bash
> kubectl --context=teleport.giantswarm.io-<CLUSTER> -n muster rollout restart deploy/muster
> ```

## Verification

### Public OAuth + MCP endpoints

```bash
# Public PRM endpoint (used by MCP clients during auth bootstrap)
curl -s https://muster.<CLUSTER>.<INSTALLATION>.gigantic.io/.well-known/oauth-protected-resource | jq

# Discovery
curl -s https://muster.<CLUSTER>.<INSTALLATION>.gigantic.io/.well-known/oauth-authorization-server | jq

# /mcp returns 401 with WWW-Authenticate (correct — bearer required, browser flow follows)
curl -s -o /dev/null -w "%{http_code}\n" -X POST https://muster.<CLUSTER>.<INSTALLATION>.gigantic.io/mcp \
  -H 'Accept: application/json,text/event-stream' \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","method":"initialize","id":1,"params":{}}'
# expect: 401
```

### Pod state

```bash
kubectl --context=teleport.giantswarm.io-<CLUSTER> -n muster get pods
# Expect: muster, muster-agw, muster-valkey-primary all Running 1/1

kubectl --context=teleport.giantswarm.io-<CLUSTER> -n muster get mcpservers
# Expect: STATUS=Auth Required for both, url=http://muster-agw.muster.svc/mcp/...

kubectl --context=teleport.giantswarm.io-<CLUSTER> -n muster get agentgatewaybackends
# Expect: muster-mcp-k8s, muster-mcp-prom both ACCEPTED=True

kubectl --context=teleport.giantswarm.io-<CLUSTER> -n muster get agentgatewaypolicy muster-agw-frontend
# Expect: ACCEPTED=True, ATTACHED=True
```

### Add to Claude Code and run a tool

```bash
claude mcp add --transport http muster-<CLUSTER> https://muster.<CLUSTER>.<INSTALLATION>.gigantic.io/mcp
# OAuth flow runs in browser. After it completes, run any K8s tool, e.g. list pods.
```

### Audit log (Grafana → Loki)

URL: `https://grafana.<CLUSTER>.<INSTALLATION>.gigantic.io` → Explore → Loki

```logql
# Per-backend MCP tool calls — the Phase 1 audit story
{namespace="muster", pod=~"muster-agw-.*"}
  | json
  | __error__ = ""
  | mcp_method_name = "tools/call"
  | line_format "{{.time}} {{.route}} {{.mcp_target}}/{{.mcp_tool_name_name}} status={{.http_status}} dur={{.duration}} trace={{.trace_id}}"

# muster security audit — token issuance and SSO events with user identity
{namespace="muster", pod=~"muster-.*", container="muster"}
  | json
  | msg = "security_audit"
  | line_format "{{.time}} event={{.event_type}} user={{.user_id_hash}} client={{.client_id}}"
```

### Metrics (Grafana → Mimir)

```promql
# Per-backend request rate
sum by (route) (rate(agentgateway_requests_total{namespace="muster"}[1m]))

# MCP method breakdown per backend
sum by (route, method) (agentgateway_mcp_requests_total{namespace="muster"})

# P95 latency per backend
histogram_quantile(0.95,
  sum by (le, route) (rate(agentgateway_request_duration_seconds_bucket{namespace="muster", route=~"muster/muster-mcp-.*"}[5m])))

# Bytes returned per backend
sum by (route) (agentgateway_response_bytes_total{namespace="muster", route=~"muster/muster-mcp-.*"})

# Inventory of agw metrics (18 total)
{__name__=~"agentgateway_.*"}
```

### Traces (Grafana → Tempo)

Service Name: `muster-agw`. Filter span attributes by `mcp.tool=<name>` or `mcp.target=kubernetes|prometheus`. Click any `trace.id` from the Loki access-log row to drill in.

If Tempo shows no spans, check agw logs for `BatchSpanProcessor.ExportError`:

```bash
kubectl --context=teleport.giantswarm.io-<CLUSTER> -n muster logs deploy/muster-agw \
  | grep -E "ExportError|opentelemetry_sdk"
```

The most common cause is the missing `X-Scope-OrgID` header — Phase C.1 env var must be set.

## Cleanup

Reverse order of installation. Cluster-only resources first; cross-cluster references (dex client) last.

```bash
CTX=teleport.giantswarm.io-<CLUSTER>

# E. Observability
kubectl --context=$CTX -n muster delete agentgatewaypolicy muster-agw-frontend
kubectl --context=$CTX -n muster delete podmonitor muster-agw

# D. MCPServer CRs
kubectl --context=$CTX -n muster delete mcpserver mcp-kubernetes mcp-prometheus

# C. Routes / Gateway / Backend / Parameters
kubectl --context=$CTX -n muster delete backendtrafficpolicy muster-public
kubectl --context=$CTX -n muster delete httproute muster-public muster-mcp-k8s muster-mcp-prom
kubectl --context=$CTX -n muster delete agentgatewaybackend muster-mcp-k8s muster-mcp-prom
kubectl --context=$CTX -n muster delete gateway muster-agw
kubectl --context=$CTX -n muster delete agentgatewayparameters muster-trial

# B. Helm releases
helm --kube-context=$CTX -n muster uninstall muster
helm --kube-context=$CTX -n muster uninstall muster-valkey

# B. agentgateway controller (only if no other workloads in the cluster use it)
helm --kube-context=$CTX -n agentgateway-system uninstall agentgateway
helm --kube-context=$CTX -n agentgateway-system uninstall agentgateway-crds
kubectl --context=$CTX delete namespace agentgateway-system

# muster CRDs (CAUTION — only if no other workloads use these CRDs)
kubectl --context=$CTX delete crd \
  mcpservers.muster.giantswarm.io \
  workflows.muster.giantswarm.io \
  workflowruns.muster.giantswarm.io 2>/dev/null

# A. Secrets, namespace
kubectl --context=$CTX -n muster delete secret muster-oauth-credentials
kubectl --context=$CTX delete namespace muster

# A. Dex static client (manual revert in dex source-of-truth)
# - Remove the muster-<CLUSTER> entry from dex's staticClients config
# - Push to gitops repo / re-apply the OAuth2Client CRD / re-edit the source secret
# - Restart dex pods to pick up the change
```

## Known issues / caveats

| Issue | Workaround |
|---|---|
| Chart `appVersion: "0.1.0"` in `helm/muster/Chart.yaml` is stale; default image tag would be `0.1.0` (much older than current binary) | always set `image.tag` explicitly in values |
| Old muster image (≤ 0.1.0) lacks `mcp-oauth` v0.2.117's RFC 8252 port-agnostic loopback redirect URI matching → Claude Code OAuth fails with `redirect URI not registered for client` | use `image.tag: 0.1.139` or later |
| New `MCPServer` CRs created post-startup show `Disconnected` because muster's orchestrator only registers at boot | restart the muster pod after `kubectl apply` |
| Restarting `muster-agw` invalidates muster's MCP sessions to backends; tools/call returns 422 "session header is required" until muster reconnects | restart muster after every agw restart |
| `Auth Required` is the steady state for `forwardToken: true` MCPServers, not an error | a per-session SSO flow activates them when a user connects |
| `AgentgatewayBackend.spec.mcp.targets[].static.backendRef` rejects a `namespace` field (cross-namespace not supported via `backendRef`) | use `static.host` with the full DNS name of the target Service |
| Multi-tenant Tempo silently rejects spans with `BatchSpanProcessor.ExportError "no org id"` if `X-Scope-OrgID` header is absent | set `OTEL_EXPORTER_OTLP_TRACES_HEADERS` env var on agw via `AgentgatewayParameters.spec.env` |
| `MCPServer.spec.url` for backends in *other clusters* must use Teleport (`auth.teleport`); cluster-local agw routing only works for same-cluster backends | cross-cluster broker federation is a later phase of the architecture-review plan |
| agw audit log shows the **post-prefix-strip** tool name (e.g. `capi_list_clusters`, not `x_k8s_capi_list_clusters`) — toolPrefix is added by muster's aggregator and stripped before forwarding | combine `mcp_target` + `mcp_tool_name_name` for unambiguous identification; the user-visible (prefixed) name lives in muster's logs |
| Gateway-level `BackendTrafficPolicy` on `giantswarm-default` injects a 14k HTML 401 error page that masks muster's JSON OAuth errors | per-route `BackendTrafficPolicy` (even empty) overrides — see C.3 |

## File inventory (apply order)

```
muster-oauth-credentials secret  (kubectl create secret, Phase A.2)
valkey-values.yaml               (helm install, Phase B.1)
agentgateway-crds chart          (helm install, Phase B.2)
agentgateway chart               (helm install, Phase B.2)
muster-values.yaml               (helm install, Phase B.3)
muster CRDs                      (kubectl apply, Phase B.3)
agentgateway-<CLUSTER>.yaml      (kubectl apply, Phase C.1)
agw-per-backend-<CLUSTER>.yaml   (kubectl apply, Phase C.2)
public-route-<CLUSTER>.yaml      (kubectl apply, Phase C.3)
mcpservers-<CLUSTER>.yaml        (kubectl apply, Phase D)
agw-podmonitor.yaml              (kubectl apply, Phase E.1)
agw-frontend-policy.yaml         (kubectl apply, Phase E.2)
```

Restart points (after which a pod must roll):
- `muster` after applying new MCPServer CRs
- `muster-agw` after changing AgentgatewayParameters or AgentgatewayPolicy
- `muster` after `muster-agw` restart (to re-establish backend MCP sessions)

Approximate end-to-end deploy time: 30–45 minutes including verification (excluding waiting for dex config rollout, which depends on the GS gitops loop).
