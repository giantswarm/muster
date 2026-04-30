# Onboarding a Private Management Cluster to muster

## When to use this runbook

This runbook covers two distinct onboarding tasks for a Giant Swarm private
management cluster (private MC):

1. **Add `<NEW_MC>` as a target** of an existing host MC's muster aggregator
   so a connected client can reach `<NEW_MC>`'s `mcp-kubernetes` (and any
   future MCP servers on `<NEW_MC>`) through the host MC's muster.
2. **Stand up muster on `<NEW_MC>` itself** so `<NEW_MC>` becomes a host MC
   for one or more target MCs.

A "host MC" is a management cluster that runs the muster aggregator
Deployment. A "target MC" is any MC whose `mcp-kubernetes` (or other MCP
server) the host MC's muster reaches over the network. The same MC can be
both: the host MC always has a "self" target whose `mcp-kubernetes` lives on
its own apiserver. Pick Pattern A, B, or C below based on the topology you
need.

## Architecture recap

Three layers compose end-to-end:

- **Transport** (network reach to the target MC's MCP endpoint). Either
  direct HTTPS over the public Internet, or mTLS through Teleport
  Application Access for private MCs.
- **Token exchange (TX)** (cross-cluster identity). The host MC's user-OIDC
  token is exchanged via RFC 8693 at the target MC's Dex for a
  target-issued token. Required whenever the target MC's `mcp-kubernetes`
  validates against the target MC's own Dex issuer.
- **Bot identity for Teleport-routed transport.** A `tbot` Deployment in the
  host MC's muster namespace joins the Teleport tenant and writes one
  per-Teleport-app Kubernetes Secret holding mTLS material. muster's
  transport dispatcher loads each Secret in-memory and produces an
  `*http.Client` per app on demand.

Transport and token exchange are independent. A target MC can be reached
direct-HTTPS while still using TX, and a Teleport-routed target can be
reached without TX (rare — only the host MC's own self-target has neither).

```
                          Connected user
                                |
                                | OIDC bearer (issued by host MC's Dex)
                                v
            +------------------------+----------------------+
            |  Host MC (runs muster aggregator + tbot)      |
            |                                               |
            |  muster aggregator                            |
            |    +-- direct-HTTPS path (no transport CR)    |
            |    |     to host MC's own mcp-kubernetes      |  Pattern C
            |    |                                          |
            |    +-- direct-HTTPS path                      |  Pattern A1
            |    |     -> public ingress of <TARGET_MC>     |
            |    |        TX through <TARGET_MC>'s Dex      |
            |    |                                          |
            |    +-- Teleport mTLS path (transport.teleport)|  Pattern A2
            |          -> mcp-k8s-<TGT>.teleport...         |
            |             TX through dex-<TGT>.teleport...  |
            |                                               |
            |  tbot Deployment (separate from muster pod)   |
            |    joins <TELEPORT_PROXY>, writes one         |
            |    tbot-identity-* Secret per Teleport app    |
            +-----------------------------------------------+
                                |
                                | Teleport tenant (mTLS)
                                v
            +-----------------------------------------------+
            |  Target MC (private)                          |
            |    Teleport apps:  dex-<TGT>, mcp-k8s-<TGT>   |
            |    Dex:            issuer = dex.<TGT>...      |
            |       trusts host MC's Dex via federation     |
            |       provider (simpleprovider, oidc connector|
            |       id "<HOST>-federation-simple-oidc"      |
            |       — connectorId override).                |
            +-----------------------------------------------+
```

## Decision: which connection pattern do you need?

```
Adding the host MC's own mcp-kubernetes as a CR?            -> Pattern C
Target MC has a public *.gigantic.io ingress reachable
from the host MC's network?                                 -> Pattern A1
Target MC is private and only reachable via the giantswarm
Teleport tenant?                                            -> Pattern A2
You are bringing up muster on a brand-new host MC?          -> Pattern B
                                                               (then run A
                                                                or C per
                                                                target)
```

## Prerequisites

### Software

- **muster Helm chart**: a chart version that includes the post-pilot
  transport-pipeline fixes, namely (a) the dispatcher is propagated from
  `AggregatorService` into the aggregator server inside `Start()`, (b) the
  autoStart probe injects the dispatcher-issued mTLS client, (c) the CA
  filename matches `tbot`'s `application` output (`teleport-host-ca.crt`),
  (d) `Transport` propagates through `infoToMCPServer` and the orchestrator,
  (e) `internal/teleport/security.ValidateNamespace` allows the muster pod's
  own namespace via the `K8S_NAMESPACE`/`POD_NAMESPACE` downward-API env,
  and (f) the TLS pool is built from `x509.SystemCertPool()` then has the
  `tbot` CA appended (so the Teleport proxy's public-CA-signed serving cert
  verifies). Confirm the chart version pinned in
  `management-cluster-bases//extras/muster/helm-release.yaml` is at or
  beyond the release that landed all of the above. Pre-fix chart versions
  will silently fall back to direct-HTTPS for Teleport-routed CRs.
- **dex-operator app version**: a release that includes the
  `simpleprovider` `connectorId` override (overrides the auto-derived
  `<owner>-simple-<connectorType>`) and the `centralCluster` skip (avoids
  writing a self-referencing connector). Without the override, two
  federation entries with the same `<owner>-simple-oidc` derived id collide
  in target Dex and the federation entry is shadowed.
- **Teleport tenant**: the giantswarm tenant
  (`teleport.giantswarm.io:443`), Teleport server v17 or newer (matches the
  resource shape that `provision-teleport-bot.yaml.tmpl` emits).

### Access

- SOPS age key for the host MC (1Password:
  `op://Dev Common/<HOST_MC>.agekey/notesPlain`).
- SOPS age key for any target MC whose existing TX-credentials Secret you
  want to copy (typical source: `gazelle`).
- `tctl` against `<TELEPORT_PROXY>` with admin role (required to upsert
  `bot/`, `role/`, `token/`).
- `kubectl` context for the host MC and each target MC.
- GitHub write to `giantswarm/giantswarm-management-clusters` (host MC and
  target MC user-values + Teleport apps), to
  `giantswarm/management-cluster-bases` (chart version pin), and to
  `giantswarm/giantswarm-configs` (Dex federation provider).

### MC-side preconditions

The new private MC must already be a Giant Swarm-managed MC with:

- Its own Dex (`https://dex.<NEW_MC>.<base-domain>`) reconciled by
  dex-operator, with the dex-operator app version containing the
  `connectorId` override.
- A publicly reachable IRSA issuer (`https://irsa.<NEW_MC>.<base-domain>`)
  serving `/.well-known/openid-configuration` and `/openid/v1/jwks`.
  Required if you intend to host muster's `tbot` on this MC (Pattern B).
- Its own flux setup tracking `management-clusters/<NEW_MC>/...`.
- For Pattern A2: the giantswarm Teleport tenant's bot allowlist already
  includes `<NEW_MC>` (or you are ready to extend it via the muster
  provisioning script — see Pattern B step 2).

## Pattern A — Add a new private MC as a target of an existing host MC

Goal: the host MC's muster can call `mcp-kubernetes` (and exchange tokens
through Dex) on `<NEW_MC>`. The host MC already runs muster.

### A1 — Direct-HTTPS + token-exchange (no Teleport)

Use when `<NEW_MC>`'s `mcp-kubernetes` and Dex are reachable from the host
MC's network over public DNS (e.g. both MCs are behind the same internet
gateway and DNS-resolvable via `*.gigantic.io`). No Teleport hop.

1. **Configure target Dex to trust the host MC's issuer.** GitOps via PR
   on `giantswarm/giantswarm-configs`. Add a federation
   `simpleprovider` entry under
   `oidc.giantswarm.providers` for `<NEW_MC>`'s dex-operator. The
   `connectorId` override produces a unique connector id
   (`<HOST_MC>-federation-simple-oidc` by convention) that avoids
   colliding with other federation entries pointing at the same
   `<owner>-simple-oidc`. Also set `centralCluster: <HOST_MC>` so the
   provider is skipped on the host MC's own dex-operator (prevents a
   self-referencing connector).
   ```yaml
   # giantswarm-configs (encrypted via SOPS), under <NEW_MC>'s
   # dex-operator credential secret values:
   oidc:
     giantswarm:
       providers:
         - name: <HOST_MC>-federation
           credentials:
             connectorType: oidc
             issuer: https://dex.<HOST_MC>.<host-base-domain>
             clientId: <federation-client-id>
             clientSecret: <federation-client-secret>
             connectorId: <HOST_MC>-federation-simple-oidc
             centralCluster: <HOST_MC>
   ```
   Verify (after PR merges and dex-operator reconciles on `<NEW_MC>`):
   ```sh
   kubectl --context <NEW_MC> -n giantswarm \
     get secret dex-app-default-dex-config -o yaml \
     | yq '.data."config.yaml"' \
     | base64 -d \
     | yq '.connectors[].id'
   # expect <HOST_MC>-federation-simple-oidc to appear alongside
   # giantswarm-ad and any other federation connectors.
   ```

2. **Register the muster TX static client on `<NEW_MC>`'s Dex.** GitOps via
   PR on `giantswarm-management-clusters`. Add an `extraStaticClients`
   entry to `<NEW_MC>`'s `dex-app` user-values. The client-id /
   client-secret pair this produces is what the host MC presents during the
   TX flow.
   ```yaml
   # in <NEW_MC>'s dex-app user-values:
   extraStaticClients:
     - id: muster-token-exchange-<NEW_MC>
       name: muster-token-exchange-<NEW_MC>
       secretEnv: MUSTER_TOKEN_EXCHANGE_<NEW_MC>_SECRET
       trustedPeers:
         - dex-k8s-authenticator
   ```
   Verify:
   ```sh
   kubectl --context <NEW_MC> -n giantswarm logs deploy/dex \
     | grep "registered client.*muster-token-exchange-<NEW_MC>"
   ```

3. **Commit a SOPS-encrypted `<NEW_MC>-token-exchange-credentials` Secret on
   the host MC.** GitOps via PR on `giantswarm-management-clusters` under
   `management-clusters/<HOST_MC>/extras/muster/secrets/`. The shape:
   ```yaml
   apiVersion: v1
   kind: Secret
   metadata:
     name: <NEW_MC>-token-exchange-credentials
     namespace: muster
     labels:
       muster.giantswarm.io/management-cluster: <NEW_MC>
       muster.giantswarm.io/type: token-exchange-credentials
   type: Opaque
   stringData:
     client-id: ENC[...]      # encrypted with the host MC's age key
     client-secret: ENC[...]
   ```
   Easiest production path: re-encrypt the same client-id/client-secret
   pair from `gazelle/extras/muster/secrets/<NEW_MC>-token-exchange-
   credentials.yaml` under the host MC's age key. The credentials
   authenticate the same `muster` static client on `<NEW_MC>`'s Dex
   regardless of which host MC presents them. Add the new file to
   `secrets/kustomization.yaml`.

4. **Create the MCPServer CR for the new target.** GitOps via PR on
   `giantswarm-management-clusters` under
   `management-clusters/<HOST_MC>/extras/muster/mcpservers/<NEW_MC>.yaml`.
   ```yaml
   apiVersion: muster.giantswarm.io/v1alpha1
   kind: MCPServer
   metadata:
     name: <NEW_MC>-mcp-kubernetes
     namespace: muster
     labels:
       muster.giantswarm.io/management-cluster: <NEW_MC>
       muster.giantswarm.io/type: mcp-kubernetes
   spec:
     autoStart: true
     type: streamable-http
     # Public-Internet URL of <NEW_MC>'s mcp-kubernetes ingress.
     url: https://mcp-kubernetes.<NEW_MC>.<base-domain>/mcp
     auth:
       type: oauth
       forwardToken: false
       tokenExchange:
         enabled: true
         dexTokenEndpoint: https://dex.<NEW_MC>.<base-domain>/token
         # Federation connector id that the dex-operator wrote on
         # <NEW_MC>'s Dex in step 1. The CR must reference it verbatim.
         connectorId: <HOST_MC>-federation-simple-oidc
         scopes: openid profile email groups
         clientCredentialsSecretRef:
           name: <NEW_MC>-token-exchange-credentials
           namespace: muster
           clientIdKey: client-id
           clientSecretKey: client-secret
       requiredAudiences:
         - dex-k8s-authenticator
     # No spec.transport — direct-HTTPS path is the chart default.
   ```
   Add the new file to `mcpservers/kustomization.yaml`.

5. **Verify on reconcile.**
   ```sh
   kubectl --context <HOST_MC> -n muster get mcpserver -o wide
   # expect <NEW_MC>-mcp-kubernetes in state Auth Required (success
   # signal: the upstream answered 401, so the network path works and
   # token-exchange is the next step on the next user request).

   kubectl --context <HOST_MC> -n muster logs deploy/muster \
     | grep -E "<NEW_MC>-mcp-kubernetes"
   ```
   Once a connected client makes the first call:
   ```promql
   sum(rate(muster_token_exchange_total{result="success"}[5m]))
     by (cluster) > 0
   ```

### A2 — Teleport-routed + token-exchange

Use when `<NEW_MC>`'s `mcp-kubernetes` is private and only reachable via
the giantswarm Teleport tenant.

Steps 1–3 are identical to A1 (federation provider on `<NEW_MC>`'s Dex,
muster static client on `<NEW_MC>`'s Dex, SOPS TX-credentials Secret on the
host MC). Steps 4–7 below replace A1 step 4 onward.

4. **Register the two Teleport apps on `<NEW_MC>`.** GitOps via PR on
   `giantswarm-management-clusters` under
   `management-clusters/<NEW_MC>/cluster-app-manifests.yaml`, in the
   `teleportKubeAgent.values.apps[]` list. The label set is the contract
   with the muster bot's role; missing or wrong labels cause `tbot` to fail
   to issue identities for the apps.
   ```yaml
   teleportKubeAgent:
     values:
       apps:
         - name: dex-<NEW_MC>
           uri: "http://dex.giantswarm.svc.cluster.local:5556"
           public_addr: "dex-<NEW_MC>.teleport.giantswarm.io"
           labels:
             app: dex
             cluster: <NEW_MC>
             purpose: muster-aggregator
         - name: mcp-kubernetes-<NEW_MC>
           uri: "http://mcp-kubernetes.mcp-kubernetes.svc.cluster.local:8080"
           public_addr: "mcp-kubernetes-<NEW_MC>.teleport.giantswarm.io"
           labels:
             app: mcp-kubernetes
             cluster: <NEW_MC>
             purpose: muster-aggregator
   ```
   Verify:
   ```sh
   tctl get app/dex-<NEW_MC> app/mcp-kubernetes-<NEW_MC>
   # both must appear with public_addr matching the registration above.
   tsh app login --proxy=<TELEPORT_PROXY> mcp-kubernetes-<NEW_MC>
   # must succeed from an SRE workstation (proves Teleport-side wiring
   # end-to-end before muster touches it).
   ```

5. **Extend the muster bot's cluster allowlist.** SRE imperative on the
   workstation that has `tctl` against `<TELEPORT_PROXY>`. The provisioning
   script is idempotent and re-derives the role's `app_labels.cluster:` set
   from `CLUSTER_ALLOWLIST`.
   ```sh
   # If muster's tbot runs on a different MC than Teleport's home cluster,
   # the join token must use kubernetes.type: static_jwks with the host
   # MC's IRSA JWKS body inlined. Otherwise the default in_cluster
   # validator is used.
   kubectl --context <HOST_MC> get --raw /openid/v1/jwks \
     > /tmp/<HOST_MC>-jwks.json

   JWKS_FILE=/tmp/<HOST_MC>-jwks.json \
     CLUSTER_ALLOWLIST=<existing-mc>,<NEW_MC> \
     ./scripts/provision-teleport-bot.sh --yes
   ```
   Verify:
   ```sh
   tctl get role/muster-aggregator-role -o yaml \
     | yq '.spec.allow.app_labels.cluster'
   # expect the list to include <NEW_MC>.
   tctl get token/muster-aggregator -o yaml \
     | yq '.spec.kubernetes.type'
   # expect "static_jwks" if the host MC is not Teleport's home cluster,
   # otherwise "in_cluster".
   ```

6. **Extend `transport.teleport.apps[]` on the host MC's muster.** GitOps
   via PR on `giantswarm-management-clusters` under
   `management-clusters/<HOST_MC>/extras/muster/user-values.yaml`. The
   chart references `.Values.transport.teleport.*` at the chart-values
   root, NOT under a `muster.*` umbrella key. A common mistake is to put
   the block under `muster.transport.*` — the chart silently ignores it
   and `tbot` never starts. Verify the wrapping against the existing file
   before applying.
   ```yaml
   muster:
     oauth:
       server:
         dex:
           connectorId: "giantswarm-github"
         trustedAudiences:
           - dex-k8s-authenticator
   # Chart-root key, NOT under "muster:" above.
   transport:
     teleport:
       enabled: true
       proxyServer: <TELEPORT_PROXY>      # e.g. teleport.giantswarm.io:443
       apps:
         - appName: mcp-kubernetes-<NEW_MC>
           identitySecret: tbot-identity-mcp-<NEW_MC>
         - appName: dex-<NEW_MC>
           identitySecret: tbot-identity-tx-<NEW_MC>
         # ... pre-existing app pairs for other targets ...
       readinessGate:
         image:
           repository: gsoci.azurecr.io/giantswarm/kubectl
           tag: "1.31.4"
   ```
   Identity-secret names are arbitrary but must match exactly what the
   MCPServer CR references in step 7. Convention: `tbot-identity-<role>-
   <cluster>` with `<role>` in `{mcp,tx}`.

7. **Create the MCPServer CR for the Teleport-routed target.** GitOps via
   PR on `giantswarm-management-clusters`. The CR's `url` host MUST be the
   per-app subdomain on the Teleport tenant — not the public ingress
   hostname. The dispatcher returns an mTLS `*http.Client` for the Teleport
   app but does not rewrite the URL; if the URL resolves to anything other
   than the Teleport proxy, requests do not flow through Teleport and time
   out.
   ```yaml
   apiVersion: muster.giantswarm.io/v1alpha1
   kind: MCPServer
   metadata:
     name: <NEW_MC>-mcp-kubernetes
     namespace: muster
     labels:
       muster.giantswarm.io/management-cluster: <NEW_MC>
       muster.giantswarm.io/type: mcp-kubernetes
   spec:
     autoStart: true
     type: streamable-http
     # Per-app subdomain on the Teleport tenant. Required.
     url: https://mcp-kubernetes-<NEW_MC>.teleport.giantswarm.io/mcp
     auth:
       type: oauth
       forwardToken: false
       tokenExchange:
         enabled: true
         # Teleport-routed Dex token endpoint.
         dexTokenEndpoint: https://dex-<NEW_MC>.teleport.giantswarm.io/token
         # iss claim is decoupled from the routing URL — token-exchange
         # responses still carry <NEW_MC>'s original Dex issuer.
         expectedIssuer: https://dex.<NEW_MC>.<base-domain>
         connectorId: <HOST_MC>-federation-simple-oidc
         scopes: openid profile email groups
         clientCredentialsSecretRef:
           name: <NEW_MC>-token-exchange-credentials
           namespace: muster
           clientIdKey: client-id
           clientSecretKey: client-secret
       requiredAudiences:
         - dex-k8s-authenticator
     transport:
       type: teleport
       teleport:
         mcp:
           appName: mcp-kubernetes-<NEW_MC>
           identitySecretRef:
             name: tbot-identity-mcp-<NEW_MC>
         dex:
           appName: dex-<NEW_MC>
           identitySecretRef:
             name: tbot-identity-tx-<NEW_MC>
   ```
   CRD admission rejects the CR if `auth.tokenExchange.enabled=true` but
   `transport.teleport.dex` is absent (CEL guard). Add the new file to
   `mcpservers/kustomization.yaml`.

8. **Verify on reconcile.** Order matters: `tbot` writes the identity
   Secrets first, then muster's autoStart probe consumes them.
   ```sh
   # tbot Deployment Ready and bot identity Secrets present:
   kubectl --context <HOST_MC> -n muster rollout status deploy/muster-tbot
   kubectl --context <HOST_MC> -n muster get secret \
     tbot-identity-mcp-<NEW_MC> tbot-identity-tx-<NEW_MC>

   # MCPServer state:
   kubectl --context <HOST_MC> -n muster get mcpserver -o wide
   # expect <NEW_MC>-mcp-kubernetes in state Auth Required.

   # Dispatcher resolved the per-app HTTP client:
   kubectl --context <HOST_MC> -n muster logs deploy/muster \
     | grep -E "Created Teleport client provider from secret: muster/tbot-identity-(mcp|tx)-<NEW_MC>"
   ```

## Pattern B — Stand up a new host MC for muster

Goal: bring up muster on `<NEW_MC>` so it can act as the entry point for
end users targeting one or more remote MCs. After this section completes,
follow Pattern A (per target MC) and Pattern C (for `<NEW_MC>`'s self
target).

1. **Add the muster HelmRelease + Konfiguration.** GitOps via PR on
   `giantswarm-management-clusters` under
   `management-clusters/<NEW_MC>/extras/muster/`. Pull from the shared
   chart in `management-cluster-bases//extras/muster?ref=main`. The
   directory must contain at minimum:
   - `kustomization.yaml` referencing the shared chart and a per-cluster
     `muster-user-values` ConfigMap (overlay), wired into the HelmRelease's
     `valuesFrom` via a kustomize patch.
   - `oauth-credentials.enc.yaml` and `valkey-credentials.enc.yaml`
     (SOPS-encrypted, mirroring an existing host MC's layout).
   - `user-values.yaml` (start minimal — `oauth.server.dex.connectorId` +
     `trustedAudiences`).
   - `mcpservers/` directory with the `<NEW_MC>` self CR (Pattern C).

2. **Provision the muster bot on the Teleport tenant.** SRE imperative.
   The script registers `bot/muster-aggregator`,
   `role/muster-aggregator-role`, and `token/muster-aggregator` on
   `<TELEPORT_PROXY>`. Run with `JWKS_FILE` set when muster's `tbot` will
   run on `<NEW_MC>` — i.e. when `<NEW_MC>` is not the same cluster as
   Teleport's home cluster. Without `JWKS_FILE` the join token uses the
   `kubernetes.type: in_cluster` validator, which only works if Teleport's
   auth service can reach `<NEW_MC>`'s apiserver in-cluster.
   ```sh
   # Capture <NEW_MC>'s IRSA JWKS body (publicly resolvable via the IRSA
   # issuer on a Giant Swarm-managed MC).
   kubectl --context <NEW_MC> get --raw /openid/v1/jwks \
     > /tmp/<NEW_MC>-jwks.json

   # Allowlist all target MCs <NEW_MC>'s muster will eventually reach.
   JWKS_FILE=/tmp/<NEW_MC>-jwks.json \
     CLUSTER_ALLOWLIST=<TARGET_MC1>,<TARGET_MC2> \
     ./scripts/provision-teleport-bot.sh --yes
   ```
   Verify:
   ```sh
   tctl get bot/muster-aggregator
   tctl get role/muster-aggregator-role -o yaml \
     | yq '.spec.allow.app_labels.cluster'
   tctl get token/muster-aggregator -o yaml \
     | yq '.spec.kubernetes.type'    # static_jwks for cross-MC tbot.
   ```
   No SOPS-encrypted bootstrap-token Secret is needed. The chart uses the
   Kubernetes join method — `tbot` presents an audience-bound projected SA
   token at `/var/run/secrets/tokens/join-sa-token`; the bot identity is
   established at `tbot` startup, not from a static token in a Secret.

3. **Configure transport.teleport in user-values.** GitOps via PR.
   Identical shape to Pattern A2 step 6 but with the apps[] list seeded
   for the target MCs `<NEW_MC>` will reach.
   ```yaml
   muster:
     oauth:
       server:
         dex:
           connectorId: "giantswarm-github"
         trustedAudiences:
           - dex-k8s-authenticator
   transport:
     teleport:
       enabled: true
       proxyServer: <TELEPORT_PROXY>
       apps:
         - appName: mcp-kubernetes-<TARGET_MC1>
           identitySecret: tbot-identity-mcp-<TARGET_MC1>
         - appName: dex-<TARGET_MC1>
           identitySecret: tbot-identity-tx-<TARGET_MC1>
   ```

4. **Configure muster's own OAuth/SSO.** Same `muster.oauth.server.dex.*`
   block as above. `connectorId` is the host MC's user-facing Dex
   connector (typically `giantswarm-github`). `trustedAudiences` lists the
   bearer-token audiences muster validates inbound. The token-exchange
   `connectorId` in target MCPServer CRs is a different concept — verify
   you are not conflating them.

5. **Reconcile and verify.**
   ```sh
   # muster pod healthy:
   kubectl --context <NEW_MC> -n muster rollout status deploy/muster
   # tbot Deployment healthy and identity Secrets present:
   kubectl --context <NEW_MC> -n muster rollout status deploy/muster-tbot
   kubectl --context <NEW_MC> -n muster get secrets \
     -l 'app.kubernetes.io/name=muster' | grep tbot-identity-

   # Aggregator wired up the dispatcher:
   kubectl --context <NEW_MC> -n muster logs deploy/muster \
     | grep -E "CR-driven transport dispatcher (wired|applied)"

   # Metrics endpoint reachable (expose path varies by Service config):
   kubectl --context <NEW_MC> -n muster port-forward svc/muster 8080:8080 &
   curl -s localhost:8080/metrics | grep -E "muster_(transport|token_exchange)_"
   ```

6. **Per-target onboarding** — for each target MC follow Pattern A (A1 for
   public-Internet-reachable, A2 for private). Add a Pattern C CR for
   `<NEW_MC>`'s own `mcp-kubernetes`.

## Pattern C — Add a target MC reachable directly (no Teleport, no TX)

Goal: a CR for the host MC's own `mcp-kubernetes` running on the host
MC's apiserver. The host MC already validates the user's bearer token via
its own Dex; the user's token's `iss` matches the host MC's `mcp-kubernetes`
expected issuer; no token exchange needed.

GitOps via PR on `giantswarm-management-clusters` under
`management-clusters/<HOST_MC>/extras/muster/mcpservers/<HOST_MC>.yaml`:

```yaml
apiVersion: muster.giantswarm.io/v1alpha1
kind: MCPServer
metadata:
  name: <HOST_MC>-mcp-kubernetes
  namespace: muster
  labels:
    muster.giantswarm.io/management-cluster: <HOST_MC>
    muster.giantswarm.io/type: mcp-kubernetes
spec:
  autoStart: true
  type: streamable-http
  url: https://mcp-kubernetes.<HOST_MC>.<base-domain>/mcp
  timeout: 30
  auth:
    type: oauth
    forwardToken: true     # forward the user's bearer token directly
    requiredAudiences:
      - dex-k8s-authenticator
  # No spec.transport, no auth.tokenExchange.
```

Verify: `kubectl get mcpserver <HOST_MC>-mcp-kubernetes -n muster` reaches
state `Auth Required`.

## Common pitfalls

| Symptom | Cause |
|---|---|
| `MCPServer/<TGT>` status `Failed` with `context deadline exceeded`, dispatcher logs absent. | The aggregator never received the dispatcher (`SetTransportDispatcher` was a no-op because the manager was nil at registration time). Indicates a chart version older than the dispatcher-wiring fix. Bump muster chart in `management-cluster-bases`. |
| Teleport-routed CR error changes from `context deadline exceeded` to `unexpected content type: text/html`. | Teleport's web-launcher 302 fallback — the request reached Teleport without a client cert. Cause: the autoStart probe built its `MCPClientConfig` without the dispatcher-issued mTLS client. Indicates a chart version older than the autoStart-dispatcher fix. |
| TLS error `x509: certificate signed by unknown authority` against the Teleport proxy. | muster built its TLS pool from `x509.NewCertPool()` instead of `x509.SystemCertPool()` and only had the `tbot` CA appended. The Teleport proxy's serving cert is signed by a public CA. Indicates a chart version older than the system-roots-+-tbot-CA fix. |
| MCPServer remains `Failed` even after dispatcher logs say "Created Teleport client provider…". | `Transport` was dropped during `MCPServerInfo → api.MCPServer` conversion (reconciler or orchestrator). Indicates a chart version older than the Transport-propagation fixes. |
| `tbot` Deployment cannot read identity Secrets — `ValidateNamespace` rejects the muster pod's namespace. | Older `internal/teleport/security.go` hardcoded `{teleport-system, muster}`. Indicates a chart version older than the namespace-allowlist fix that adds the pod's own namespace via `K8S_NAMESPACE`/`POD_NAMESPACE`. |
| muster pod `Init:ImagePullBackOff` on `tbot-identity-wait` init container. | Chart default `bitnami/kubectl:1.30` was archived from Docker Hub. Override `transport.teleport.readinessGate.image.{repository, tag}` in user-values to a maintained mirror (e.g. `gsoci.azurecr.io/giantswarm/kubectl:1.31.4`). Verify upstream chart defaults at install time and drop the override once upstream ships a maintained tag. |
| `tbot` pod `CrashLoopBackOff` with `invalid configuration: no configuration has been provided`. | Chart sets `automountServiceAccountToken: false` on the `tbot` Deployment but `tbot`'s `kubernetes_secret` destination uses the in-cluster kube client which expects the standard SA token at `/var/run/secrets/kubernetes.io/serviceaccount/token`. Workaround: a flux postRenderer that flips `automountServiceAccountToken: true` on `Deployment/muster-tbot`. Verify upstream chart defaults at install time and drop the workaround once upstream ships the fix. |
| `tbot` join error `kubernetes failed to validate token: [invalid bearer token, unknown]`. | The Teleport join token's `kubernetes.type` is `in_cluster` but `tbot` runs on a different MC than Teleport's home cluster. Re-run `provision-teleport-bot.sh` with `JWKS_FILE=<host-MC-jwks>` so the token uses `kubernetes.type: static_jwks` and Teleport can validate SA tokens minted by the host MC's apiserver. |
| TX returns `401 access_denied` despite the federation provider being committed. | Two federation providers collide on the same auto-derived `<owner>-simple-oidc` connector id. Cause: dex-operator app version older than the `connectorId` override release. Bump dex-operator to a release that honours the `connectorId` credentials key, then verify the per-host id (e.g. `<HOST_MC>-federation-simple-oidc`) appears in the target MC's Dex `config.yaml`. |
| `transport.teleport.enabled: true` set but no `tbot` Deployment is rendered, no identity Secrets appear. | The values block is nested under `muster.transport.*` instead of at the chart-values root `transport.*`. The chart silently ignores the wrong path. Move the block to the chart root in `user-values.yaml`. |
| Teleport-routed MCPServer reaches state `Auth Required` but end-to-end calls hit `context deadline exceeded`. | The CR's `url` host points at the public ingress (`mcp-kubernetes.<TGT>.<base-domain>`) instead of the per-app Teleport subdomain (`mcp-kubernetes-<TGT>.teleport.giantswarm.io`). The dispatcher returns an mTLS client but does not rewrite the URL. |
| `<NEW_MC>` muster `MCPServer` referencing namespace other than `muster` is rejected with a security error. | The `internal/teleport/security.ValidateNamespace` allowlist is `{teleport-system, muster, <pod-own-ns>}`. Identity Secrets must live in one of those namespaces. Convention: keep them in the muster pod's namespace (`muster`). |

## Verification checklist (post-onboarding)

- `kubectl -n muster get mcpserver -o wide` — every CR reaches `Auth
  Required` (success signal: the upstream answered 401, transport works).
- muster logs include:
  ```
  CR-driven transport dispatcher wired
  CR-driven transport dispatcher applied to aggregator server
  Built TLS pool: system_roots=true system_count=<N>
  ```
- For Teleport-routed CRs, muster logs include for each app:
  ```
  Created Teleport client provider from secret: muster/tbot-identity-mcp-<TGT>
  Created Teleport client provider from secret: muster/tbot-identity-tx-<TGT>
  ```
- `tbot` pod is `1/1 Running`; identity Secrets
  (`tbot-identity-mcp-<TGT>`, `tbot-identity-tx-<TGT>`) exist in the muster
  namespace.
- Metrics:
  ```promql
  sum(rate(muster_transport_lookup_total{result="resolved"}[5m])) > 0
  sum(rate(muster_token_exchange_total{result="success"}[5m])) > 0
  ```
- Alerts `MusterTransportSecretMissing`, `MusterTransportClusterDrift`,
  `MusterTokenExchangeFailures` quiet for at least one hour.
- End-to-end: a connected client (`muster agent`) invokes a tool through
  the new CR and gets a successful response.

## Rollback (per layer)

Each rollback layer can be applied independently. Prefer the lowest-impact
path that matches the regression.

- **Per-target rollback (no muster restart).** Delete the new MCPServer
  CR. The aggregator drops it from the registry within seconds; other CRs
  are unaffected. Use this when only one new target misbehaves.
- **Disable Teleport transport.** Set `transport.teleport.enabled: false`
  in the host MC's user-values and re-apply. The chart removes the `tbot`
  Deployment and identity Secrets; Teleport-routed CRs go `NotReady`;
  direct-HTTPS CRs (Pattern A1, Pattern C) keep working. Use this when
  Teleport-side wiring (proxy, bot, role, app registration) is the
  suspect layer.
- **Revert the muster chart version.** Revert the chart-version pin in
  `management-cluster-bases//extras/muster/helm-release.yaml`. Use this
  when a chart-side regression is the suspect. If any CR with
  `spec.transport` was authored against the newer CRD between bump and
  rollback, remove `spec.transport` from those CRs first; otherwise the
  older CRD rejects them on next reconcile.
- **Unwind dex-operator changes.** Revert the federation `simpleprovider`
  entry in `giantswarm-configs` and let the next dex-operator reconcile on
  the target MC drop the federation connector. Token-exchange against the
  unwound target then fails with `401 access_denied`; the
  `MusterTokenExchangeFailures` alert fires.
- **De-provision the bot.** `tctl rm bot/muster-aggregator
  role/muster-aggregator-role token/muster-aggregator` against
  `<TELEPORT_PROXY>`. Stops all Teleport-routed traffic across every host
  MC that uses this bot. Coordinate broadly before doing this.

## Related docs

- How-to: [`teleport-authentication.md`](../how-to/teleport-authentication.md)
  for the per-app `MCPServer` Teleport client config (filesystem vs.
  Kubernetes mode, identity-file format, troubleshooting, security
  considerations).
- Operations: [`installation.md`](installation.md) for the broader muster
  install paths (Homebrew, binary, chart) and prerequisites.
- Explanation: [`../explanation/architecture.md`](../explanation/architecture.md)
  for the aggregator + dispatcher data-flow.
