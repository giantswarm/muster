# Plan: gateway-first observability, token broker, slim cluster-mode muster

## Constraints

| Constraint | Notes |
|---|---|
| `MCPServer`, `Workflow`, `WorkflowRun` are the user-facing CRDs | stable contract |
| Filesystem mode is a first-class deploy mode | preserved throughout (CTO call) |
| Single Go module, single image, multiple commands | `muster operator`, `muster broker`, `muster agent`, `muster serve --filesystem-mode` |
| Broker built on `giantswarm/mcp-oauth` library | only OSS impl with CIMD; we own the lib |
| Workflow execution stays in muster | no equivalent in agentgateway |
| Teleport remains cross-cluster transport as a fallback | broker federation (Phase 7) is on the critical path; Teleport stays as the transport for spokes that haven't adopted federation yet, mixed per-`MCPServer` |
| Hexagonal: ports defined by the consumer, narrow per consumer | no shared `pkg/contracts/`, no shared `ports.go` |
| Existing package names kept | `internal/aggregator/`, `internal/workflow/`, `internal/reconciler/` — rename is a separate concern |
| Token validation is format-agnostic; issuance is format-toggled | mcp-oauth validates both opaque and JWT bearers regardless of mode; only issuance flips at deployment time |

## Architectural invariant — one stack per customer (default), per-MC is opt-in

**Default deployment shape: one stack per customer.**

The customer's central MC runs the full muster stack — muster operator + agentgateway + broker (in-process) + valkey + Dex client. The customer's other MCs (if any) host backends only, with no local muster, no local agw, no local broker. Cross-MC backends are reached from the central muster via Teleport tunnel using `MCPServer.spec.auth.teleport`.

```
Per customer (default):
  Central MC:    full stack (muster + agw + valkey + Dex client + all customer MCPServer CRDs)
  Other MCs:     backend pods only (mcp-kubernetes, mcp-prometheus, etc.)
                 reached via Teleport tunnel from the central muster
```

This is **what the glean deployment validated in Phase 1** and what most customers will run.

**Opt-in deployment shape: one stack per MC.**

Some customers will need per-MC isolation. Drivers include:

- Regulatory per-environment isolation (prod and dev auth boundaries must be distinct)
- Hard failure isolation (one MC's incident must not affect another MC's access)
- Cross-MC latency (multi-region customers where local-to-local routing matters)
- Per-MC RBAC variation (markedly different policies per environment)
- Scale on central stack (large customers whose one-stack throughput becomes a bottleneck)
- Per-tenant operations within a customer (team-A and team-B with strict isolation)

For these customers, individual MCs are upgraded from the default (backend-only) to the full stack (muster + agw + valkey + Dex client). The upgrade is per-MC, per-customer, additive — same Helm chart, same translator, same agw config. Once a target MC runs the full stack, its `MCPServer` entry on the central muster swaps `auth.teleport` for `auth.tokenExchange`; the upgraded MC's Dex registers the central broker as a trusted peer; cross-MC calls flow via broker federation (Phase 7) preserving user identity at the spoke side.

**Both shapes coexist within an installation.** Most customers stay on the default; specific customers upgrade specific MCs based on their drivers. The architecture supports both.

### What this means for the building blocks

- **muster the binary doesn't change between shapes.** Same image, same flags, same wiring. Only deployment topology differs.
- **agentgateway is deployed wherever the full stack lives** — default: only on the central MC, sized for the customer's whole traffic. Per-MC variant: on each upgraded MC.
- **`MCPServer.spec.auth.teleport`** is the default cross-MC transport. It exists in the schema today and works without Phase 7.
- **`MCPServer.spec.auth.tokenExchange`** (Phase 7) is the upgrade transport, gated on customer demand for per-MC isolation. Trigger-driven, not critical path.
- **Hub-vs-spoke** in the per-MC variant is config (which `MCPServer` CRDs each muster owns, which broker peers each Dex trusts). In the default shape there's no hub-vs-spoke — there's one muster per customer.

## Where federation logic lives

The broker handles federation entirely. muster's call sites are federation-agnostic.

```
muster operator             broker (in-process)              remote broker (peer)
─────────────────           ─────────────────────           ───────────────────────
reconcile MCPServer    ──▶  register(audience=X,
  with auth.tokenExchange     peerConfig=<from CRD>)

aggregator dispatch    ──▶  GetToken(sessionID, audience=X)
                              │
                              ├─ cache hit? return
                              └─ exchange: POST /token       ──▶ accept exchange,
                                          grant_type=8693          issue new JWT
                                          audience=X             ◀── (aud=X, sub=user)
                              cache result, return

outbound HTTPS         ──▶  ⟨Bearer: exchanged JWT⟩
to spoke agw URL              spoke agw validates against
                              remote broker JWKS
                              → routes to local backend
```

muster operator:
- reconciles `MCPServer` CRDs; passes `auth.tokenExchange` config to broker
- calls `broker.GetToken(sessionID, audience)` for each outbound MCP call
- makes the HTTPS call to the URL in the MCPServer CRD, using the bearer broker returned

Broker (`internal/broker/exchange.go`):
- knows audience-to-peer-broker mapping (from MCPServer config)
- performs RFC 8693 exchange against peer broker's token endpoint
- caches exchanged tokens by `{sessionID, audience}`
- maintains JWKS cache of peer brokers for inbound JWT validation

The `TokenBroker` interface in `internal/aggregator/token_broker.go` (Phase 2) is federation-agnostic. Phase 7 is purely a broker-domain change: extends `internal/broker/exchange.go` to dispatch across peer brokers. muster aggregator code does not change.

## Tool catalog and cluster attribution

When a hub fans out across many MCs, tool name disambiguation is load-bearing. Convention: **`toolPrefix` on hub-side MCPServer CRDs encodes cluster identity**.

```yaml
# Hub-side MCPServer
spec:
  toolPrefix: acme_prod_k8s
  url: https://muster-agw.acme-prod.azuretest.gigantic.io/mcp/k8s
  auth:
    tokenExchange: { ... }
```

User and LLM see `x_acme_prod_k8s_list_pods` in the tool catalog; the cluster targeting is unambiguous. Spoke backends are unchanged — they receive the post-prefix-strip name (`list_pods`) just like in single-MC deployments.

Tool catalog visibility is filtered per-user at the hub aggregator based on JWT claims (ADR-006 territory; the catalog filter stays at the hub aggregator because agw's `traffic.authorization` enforces individual calls, not catalog responses). Three enforcement layers in total:

1. Hub aggregator filters `tools/list` by user identity (catalog visibility)
2. Hub agw `traffic.authorization` enforces per-call access (Phase 6)
3. Spoke agw + spoke broker JWKS validate the federated JWT (defense in depth)

## Target architecture (end of Phase 8)

```
Per-customer cluster
─────────────────────────────────────────────────────────────
Claude Code / Cursor      ──► agentgateway (edge data plane, MCP-aware)
                                │ JWT or extAuth, RBAC, audit, tracing, metrics, rate limit
                                │
                                ├── /mcp/aggregator ──► muster operator
                                │                       (CRD reconciler + translator + workflow runtime
                                │                        + broker domain in-process)
                                │
                                ├── /mcp/<backend N> ──► backend MCP server (same cluster)
                                │
                                (HTTPRoute per backend, all emitted by translator)

                            broker HTTP/gRPC endpoints exposed by muster operator
                                │ OAuth 2.1 + CIMD + RFC 8693 + (opt-in) JWT issuance
                                └── upstream IdP: Dex

Local lab / BDD / demo
─────────────────────────────────────────────────────────────
muster serve --filesystem-mode --config-path ./.muster/
  ├── reads YAML (MCPServer, Workflow)
  ├── slim in-process MCP aggregator on :8090/mcp (preserved forever)
  ├── (optional) embedded broker subprocess for full-stack lab
  └── muster agent --local-dev → :8090/mcp
```

## Pre-Phase-1 — In flight

ServiceClass removal is underway on `remove-sc-*` branches (4 commits ahead of `origin/main`). Once those merge, the plan starts from a clean main. Phase 2 work below assumes service-class removal is complete; if it isn't yet, the broker refactor still works — service-locator cleanup just inherits more dead code to delete.

## Existing code map (verified against current branch)

| Path | LOC (non-test) | Plan role |
|---|---|---|
| `internal/aggregator/` | ~10,310 | hosts auth glue + capability store; HTTP server bypassed in cluster mode by Phase 8 |
| `internal/aggregator/auth_resource.go` | 476 | refactor through `TokenBroker` port — Phase 2 |
| `internal/aggregator/auth_tools.go` | 469 | same |
| `internal/aggregator/connection_helper.go` | 1153 | same |
| `internal/aggregator/capability_store.go` + `_valkey.go` | ~280+ | replaced by agw `traffic.authorization` — Phase 6 |
| `internal/oauth/` | ~3,050 | moves into `internal/broker/` — Phase 2 |
| `internal/server/oauth_http.go` | 867 | moves into `internal/broker/http/` — Phase 2 |
| `internal/orchestrator/` | ~860 | dropped from cluster-mode hot path — Phase 8; filesystem mode keeps it |
| `internal/reconciler/` | — | hosts `mcpserver_reconciler.go`, `kubernetes_detector.go`, `filesystem_detector.go`; gains translator logic — Phase 5 |
| `internal/api/` | ~1,500 | service-locator glue — replaced with constructor DI — Phase 2 |
| `internal/services/{aggregator,mcpserver}/` | ~700 | wrapper packages — deleted in Phase 2 |
| `internal/teleport/` | ~1,500 | stays until cross-cluster federation supersedes |
| `internal/workflow/` | — | gains its own narrow `TokenBroker` port — Phase 2 |
| `pkg/apis/muster/v1alpha1/mcpserver_types.go` | — | already has `Type`, `ForwardToken`, `RequiredAudiences`, `TokenExchange`, `Teleport` — schema is ready |

## Package layout (target)

```
internal/aggregator/
├── aggregator.go              existing domain
├── token_broker.go            TokenBroker port (consumer-defined, narrow)
├── entity_provider.go         EntityProvider port
├── auth_resource.go           refactored to consume aggregator.TokenBroker
├── auth_tools.go              same
├── connection_helper.go       same
└── broker/                    TokenBroker adapter (in-process now, gRPC client later if extracted)

internal/workflow/
├── executor.go                existing
├── token_broker.go            workflow's narrower TokenBroker (GetToken / ExchangeToken)
└── tool_caller.go             ToolCaller port

internal/reconciler/
├── mcpserver_reconciler.go    existing — extended in Phase 5 to emit AgentgatewayBackend + HTTPRoute
├── kubernetes_detector.go     becomes the K8s EntityProvider implementation
├── filesystem_detector.go     becomes the filesystem EntityProvider implementation
└── workflow_reconciler.go     existing

internal/broker/
├── manager.go                 broker domain
├── exchange.go                RFC 8693 routing logic
├── session.go                 session lifecycle
├── http/                      OAuth 2.1 endpoints (driving adapter, mounted in operator pod)
└── grpc/                      gRPC server (driving adapter, dormant until pod extraction)

cmd/muster/
├── main.go                    cobra root
├── operator.go                composition root for cluster mode (Phase 8 rename of serve.go)
├── broker.go                  composition root for separate broker pod (when extracted)
├── agent.go                   laptop CLI
└── serve.go                   stays for filesystem mode until Phase 8 rename

DELETED in Phase 2:
- internal/api/             service-locator glue (replaced by constructor DI in cmd/)
- internal/services/        wrapper packages around aggregator + mcpserver
```

Each consumer owns its own narrow `TokenBroker` interface. Both are satisfied structurally by the same broker implementation. No shared `pkg/contracts/`, no `ports.go` kitchen sinks. Ports live in single-purpose files.

## Phases

### Phase 1 — agw behind muster (per-backend observability)

*Half-day YAML change. Reversible by URL revert.*

**Goal:** every same-cluster MCP call gets audited, traced, and counted at agw, without disrupting auth or public routing.

**Topology:**

```
Claude Code → Envoy → muster → muster-agw → backend MCPs
                       └─→ OAuth/discovery on muster directly
```

**Work items:**

- Apply `AgentgatewayBackend` + `HTTPRoute` per backend on `muster-agw`, matching `/mcp/<name>`
- Revert the public `/mcp` split — Envoy goes back to a single rule pointing all of `muster.<cluster>.gigantic.io` to muster
- Update each `MCPServer.spec.url` from `http://mcp-<name>.<ns>.svc:8080/mcp` to `https://muster-agw.muster.svc/mcp/<name>`
- For `MCPServer.spec.auth.type=teleport`, leave the URL as the Teleport tunnel address — those backends bypass agw
- Restart muster so the orchestrator picks up the new URLs

**What you get:**

| Concern | After Phase 1 |
|---|---|
| Per-backend Loki audit (`route=muster/mcp-k8s`) | ✓ |
| Per-backend Mimir metrics (`agentgateway_requests_total{route=...}`) | ✓ |
| Per-backend Tempo spans (muster → agw → backend) | ✓ |
| Backend health-check via `AgentgatewayPolicy.backend.health` | ✓ |
| Cross-cluster (Teleport-tunneled MCPs) | unchanged — bypasses agw, keeps muster-side audit |
| User identity in agw audit log | ✗ (agw is internal-only here; muster's `security_audit` covers identity) |

**Filesystem mode:** untouched — no agw in lab, muster's in-process aggregator handles routing locally.

**Cross-cluster:** Teleport-tunneled MCPs continue exactly as today. agw doesn't see them. They get muster-side audit only. End-to-end cross-cluster identity is Phase 7's problem.

**Effort:** half a day. **Risk:** low. **Status: ✅ DONE on glean.**

---

## Current PR status (snapshot — 2026-05-11)

The phases below are not all sequential; many are in flight in parallel. Real-time state of the in-flight work in `giantswarm/muster`:

| Workstream | Open PRs | Status |
|---|---|---|
| **Pre-Phase-1 — ServiceClass removal** | #634, #635, #636 (steps 2/4, 3/4, 4/4) | in review; **merge to unblock everything else** |
| **Phase 2 — Broker series** | #647 (ports), #648 (broker package rename), #649 (broker/http), #651 (adapter), #654 (auth_tools), #656 (connection_helper), #657 (session lifecycle) | 7 of ~8 PRs drafted; needs review throughput. Still to author: EntityProvider migration, internal/api/ delete, import-boundary CI |
| **Phase 3 — Tracing + logging via mcp-toolkit** | #652 (tracing + metrics), #653 (logging + ctx-aware variants) | draft; review pending |
| **ADR-012** | #613 | revised 2026-05-11; review pending |
| **Architecture docs** | #650 | this document |
| **Approved / mergeable** | #570 (lean list_tools), #629 (architectbot teams alignment) | merge buttons available |
| **Stalled or needs review** | #496 (teemow — periodic capability polling, since March), #543 (TheoBrigitte — dedup tools, CHANGES_REQUESTED), #623 (workflow executor split), #627 (client dedup) | review or close-as-obsolete decisions needed |

---

## Reordered critical path — Phase 4 deferred to end

After implementation experience and PR-throughput realities, Phase 4 (Edge auth — agw moves in front of muster) is reordered to the **end** of the critical path. Rationale:

- **Lowest marginal observability value.** Phase 1 already delivers per-backend audit/traces/metrics. Phase 4's win is "identity column in agw audit log" — useful but additive; muster's `security_audit` logs already cover the identity story.
- **Most public-facing churn.** Changing the Envoy → agw → muster routing (vs Envoy → muster directly) is a real customer-visible event; best done as a coordinated milestone, not interleaved with code refactors.
- **External dependency on mcp-oauth JWT-mode PR.** That work has its own review cycle in `giantswarm/mcp-oauth`. Deferring Phase 4 gives the JWT PR time to land cleanly; otherwise Phase 4 ships in extAuth-only mode and Phase 4-revisit happens when JWT lands.
- **Phase 5 (translator) doesn't depend on Phase 4.** Translator emits agw routes regardless of agw position; emitted `AgentgatewayPolicy` shape adjusts per the deployment-time auth choice.
- **Phase 6 (ADR-006 → policy) doesn't strictly depend on Phase 4.** ADR-006 stays in muster's aggregator (works today). Cluster-mode migration to `traffic.authorization` is opt-in, not mandatory, and lands with Phase 4+8.
- **Phase 4 + Phase 6 + Phase 8 land together.** Bypass aggregator HTTP (Phase 8) needs agw in front; ADR-006 → policy (Phase 6) needs JWT claims at agw; both follow Phase 4. Three phases, one coordinated milestone.

```
Pre-Phase-1: ServiceClass removal (#634/635/636)
Phase 1:     agw behind muster (DONE on glean)
Phase 2:     TokenBroker interface + broker bounded context (~7 of 8 PRs in flight)
Phase 3:     OTel tracing + logging via mcp-toolkit (2 PRs in review)
Phase 5:     Translator MVP (next critical-path work)
             ─── SHIP TO CUSTOMERS HERE ───

Phase 4 + 6 + 8: Final architectural transition (coordinated milestone, when mcp-oauth JWT
                 lands AND a customer audit/RBAC requirement triggers it)

Phase 7:     Cross-MC broker federation (trigger-driven per customer; opt-in upgrade
             to per-MC variant)
```

**The platform is shippable at the end of Phase 5.** Customers can use the per-customer stack, get per-backend audit, MCPServer self-service, workflow CRDs, OTel tracing, OAuth via muster's broker, Teleport-tunneled cross-MC reach. The Phase 4 + 6 + 8 milestone polishes the architecture (identity at agw, RBAC at the edge, aggregator HTTP server bypassed in cluster mode) but is not required to be useful.

---

### Phase 2 — `TokenBroker` interface + broker bounded context (in-process), drop service locator

*Critical path. Establishes the architectural seam. Refactor only — no behavior change.*

**Status: 7 of ~8 PRs drafted, in review** — #647, #648, #649, #651, #654, #656, #657. Three remaining to author: EntityProvider migration for `internal/reconciler/{kubernetes,filesystem}_detector.go`, delete of `internal/api/` + `internal/services/{aggregator,mcpserver}/` + constructor DI in `cmd/`, import-boundary CI rule.

```go
// internal/aggregator/token_broker.go
package aggregator

type TokenBroker interface {
    BeginOAuthFlow(ctx context.Context, req BeginRequest) (FlowURL, error)
    CompleteOAuthFlow(ctx context.Context, code, state string) (Session, error)
    GetToken(ctx context.Context, sessionID, audience string) (Token, error)
    ExchangeToken(ctx context.Context, req ExchangeRequest) (Token, error)
    RevokeSession(ctx context.Context, sessionID string) error
    Introspect(ctx context.Context, bearer string) (Claims, error)  // accepts opaque or JWT
    WatchAuthEvents(ctx context.Context) <-chan AuthEvent
}
```

```go
// internal/aggregator/entity_provider.go
package aggregator

type EntityProvider interface {
    WatchMCPServers(ctx context.Context) <-chan EntityChange[MCPServer]
    WatchWorkflows(ctx context.Context) <-chan EntityChange[Workflow]
    UpdateStatus(ctx context.Context, kind, name string, status any) error
}
```

```go
// internal/workflow/token_broker.go
package workflow

type TokenBroker interface {
    GetToken(ctx context.Context, sessionID, audience string) (Token, error)
    ExchangeToken(ctx context.Context, req ExchangeRequest) (Token, error)
}
```

`Introspect` takes any bearer because mcp-oauth validates both formats regardless of which the server is currently configured to issue. Callers don't need to know the format.

**Work items:**

- Define narrow `TokenBroker` interface in each consumer package (one interface per file, no `ports.go` kitchen sink)
- Define `EntityProvider` interface in `internal/aggregator/` to seam K8s vs filesystem detector code paths
- Create `internal/broker/` bounded context: domain (`manager.go`, `exchange.go`, `session.go`) + adapters (`http/`, `grpc/`) wrapping `mcp-oauth`
- Mount `internal/broker/http/` HTTP endpoints inside the muster operator pod at `/oauth/*`
- Create `internal/aggregator/broker/` adapter (in-process call into `internal/broker/`)
- Refactor `internal/aggregator/auth_resource.go`, `auth_tools.go`, `connection_helper.go` to consume the local port instead of importing `internal/oauth/`
- Migrate `internal/reconciler/{kubernetes,filesystem}_detector.go` behind the `EntityProvider` interface
- **Delete `internal/api/` service locator + `internal/services/{aggregator,mcpserver}/` wrappers**; replace with constructor DI in `cmd/muster/`
- `noOpBroker` adapter for filesystem `--no-auth`
- Composition root in `cmd/serve.go` picks the adapter
- Import-boundary CI rule: `internal/aggregator/` may not import `internal/broker/` directly — only via `internal/aggregator/broker/`

Cluster mode: behavior unchanged. Filesystem mode: behavior unchanged.

**Effort:** 2–3 weeks. **Risk:** low–medium (refactor of widely-imported `internal/api/`).

**Parallel-runnable with Phase 1.**

---

### Phase 3 — OTel SDK in muster + backends via `mcp-toolkit`

*Parallel-safe. **Status: 2 PRs drafted, in review** — #652 (tracing + metrics), #653 (logging + ctx-aware variants for trace correlation).*

- Wire OTel SDK into muster operator + workflow runtime
- Honor inbound `traceparent`
- Verify backends propagate `traceparent` (small upstream patch if absent)

Cluster mode: rich tracing across all hops. After Phase 1, Tempo shows `client → Envoy → muster → muster-agw → backend MCP → real K8s API`. After Phase 4, identity attributes from JWT/extAuth get propagated into spans.
Filesystem mode: optional via `--otel-endpoint=`; default no-op exporter.

**Effort:** 1 week. **Risk:** low.

**Can land any time after Phase 1.**

---

### Phase 4 — Edge auth (deployment-time format choice)

*Final architectural milestone. Lands together with Phase 6 (ADR-006 → policy) and Phase 8 (aggregator bypass) as a coordinated transition. **Not on the early critical path** — the platform is shippable at end of Phase 5; this milestone is the polish that follows when mcp-oauth JWT-mode is ready AND a customer audit/RBAC requirement triggers it.*

mcp-oauth gains an opt-in JWT issuance mode (separate PR in `giantswarm/mcp-oauth`). Validation in mcp-oauth already accepts both opaque (TokenStore lookup) and JWT (JWKS verify) bearers; the new mode adds a third validation branch for self-issued JWTs. Only issuance toggles per server instance.

agw's edge-auth mechanism follows from the deployment-time issuance choice:

| Broker issues | agw policy | Per-request behavior |
|---|---|---|
| JWT (recommended when agw is in front) | `traffic.jwtAuthentication` against broker JWKS | local signature verify; no broker round-trip |
| Opaque (default; preserves encryption-at-rest) | `traffic.extAuth` → broker `/oauth/introspect` | broker validates and returns claims |

Both produce identity as CEL vars at agw (`jwt.sub`/`jwt.email`/`jwt.groups` directly in JWT mode; `extAuth.response.headers["x-user-*"]` in opaque mode). Audit log, RBAC rules, rate limits work either way.

**Topology change (when this lands):**

```
Phase 1 was:    Claude Code → Envoy → muster → muster-agw → backends
Phase 4 becomes: Claude Code → Envoy → muster-agw → muster (or directly → backends)
                                            ↘ /oauth, /.well-known on muster directly
```

agw moves to in front of muster. Public Envoy `HTTPRoute` splits again: `/mcp/*` to muster-agw, `/oauth/*` and `/.well-known/*` to muster. (This is the agw-in-front trial we already validated on glean — comes back at this phase.)

**Work items:**
- Land mcp-oauth JWT issuance PR (separate repo; prompt already drafted)
- Broker exposes `/.well-known/jwks.json` (active in JWT mode, 404 in opaque) and keeps `/oauth/introspect` working for both formats
- agw signs identity headers via httpsig (RFC 9421) before forwarding to muster, regardless of mode
- muster operator + backends drop bearer validation; trust httpsig identity headers via small middleware
- Translator (Phase 5) reads broker config and emits the matching agw policy

Cluster mode: identity at agw, audit-log attribution, RBAC and rate-limit policies usable. muster operator no longer per-request-validates bearer tokens.
Filesystem mode: unchanged when `--no-auth`. Full-stack lab broker subprocess can issue either format locally; the lab agw equivalent (or direct muster) validates accordingly.

**Effort:** 2–2.5 weeks (excluding mcp-oauth PR review cycle). **Risk:** medium. The dual-mode support adds little risk because mcp-oauth's validator already handles both.

**Depends on Phase 2 (broker boundary) for clean split between agw and muster. Lands with Phase 6 + Phase 8 as the final milestone.**

---

### Phase 5 — Translator MVP (extend `internal/reconciler/`)

*Self-service inflection point. Single user-facing CRD; agw stack emitted underneath. **This is the last critical-path phase before the platform is shippable to customers.***

- Extend `internal/reconciler/mcpserver_reconciler.go` to emit, for each `MCPServer` in cluster mode:
  - `AgentgatewayBackend` referencing user's Service from `MCPServer.spec.url`
  - `HTTPRoute` matching `/mcp/<name>` on the platform `Gateway`
  - `AgentgatewayPolicy` carrying audit configuration plus a placeholder auth posture (Phase-1-compatible: no edge JWT validation, no extAuth). When the Phase 4 + 6 + 8 milestone lands later, the translator is extended to emit the matching `traffic.jwtAuthentication` or `traffic.extAuth` block.
  - Owner references for GC
- Status sync: translate `AgentgatewayBackend` conditions back into `MCPServer.status`
- For `MCPServer.spec.auth.type=teleport`, translator emits a passthrough — no agw resources, muster handles it directly

Cluster mode: app teams author one `MCPServer` CRD; routing + audit emitted automatically. Auth still validated by muster (as in Phase 1); identity not yet at agw.
Filesystem mode: translator is no-op; reads YAML directly into in-process aggregator.

**`MCPServerClass` for platform-team defaults is deferred to trigger-driven** (when there are >5 MCPServers per cluster and copy-pasted policy YAML becomes painful). Phase 5 hardcodes sensible defaults until then.

**Effort:** 2 weeks. **Risk:** medium.

**Depends on Phase 2 (broker boundary) for clean DI.** Does NOT depend on Phase 4; Phase 5 ships before Phase 4 in the new order.

---

## Milestone — platform is shippable to customers

At the end of Phase 5, the platform is fully usable:

| Capability | Available end of Phase 5 |
|---|---|
| Per-customer stack deployment (Envoy → muster → muster-agw → backends) | ✓ |
| Per-backend audit, traces, metrics at agw | ✓ |
| Identity in muster's `security_audit` logs | ✓ |
| OTel tracing through muster + backends | ✓ |
| Self-service via `MCPServer` CRDs (translator emits agw resources) | ✓ |
| Workflow CRD + execution | ✓ |
| Cross-MC backends via Teleport tunnel (`auth.teleport`) | ✓ |
| OAuth 2.1 + CIMD via mcp-oauth (opaque tokens, served by muster) | ✓ |
| Per-MC stack upgrade option (`auth.tokenExchange`, opt-in per customer) | ✓ via Phase 7 when triggered |

Capabilities deferred to the Phase 4 + 6 + 8 milestone (not required for the platform to be useful):

| Capability | Deferred until Phase 4 + 6 + 8 |
|---|---|
| Identity column in agw access log rows | — |
| Per-tool RBAC enforced at agw via CEL on `jwt.groups` | — |
| Rate limit per identity at agw | — |
| Cluster-mode aggregator bypass / muster as operator-only | — |

Customers run on the Phase-5 shape indefinitely. The final-milestone work lands when there is a concrete trigger:
- mcp-oauth JWT-mode PR merged (external dependency)
- A customer audit/RBAC requirement that hardens the case
- A planned coordinated release window

---

### Phase 6 — ADR-006 → `traffic.authorization`

- Per-session tool filter expressed as CEL on `mcp.method.name == "tools/call"` + `mcp.tool.name` + `jwt.groups`
- Translator extended to emit `AgentgatewayPolicy` rules from `MCPServer.spec.tools` (or a new field) for per-tool RBAC
- `internal/aggregator/capability_store.go` + `_valkey.go` unwired from cluster-mode hot path (file kept in tree for filesystem mode)

Cluster mode: stateless, JWT-claim-based filtering at agw. ADR-006 outcome preserved, mechanism changed.
Filesystem mode: keeps server-side capability store; optional `--simulate-groups=readonly` for tests.

Deletes done for cluster mode must be careful not to remove code filesystem mode imports. The capability store file stays in the tree; only the cluster-mode composition root stops wiring it.

**Effort:** 1.5 weeks. **Risk:** medium (capability store migration — write parity tests first).

**Can run parallel with Phase 5; both depend on Phase 4 for JWT claims.**

---

### Phase 7 — Cross-cluster broker federation (trigger-driven, opt-in per customer)

*Schema is already designed (`MCPServer.spec.auth.tokenExchange`); this phase is broker plumbing. Not on the critical path — applies only to customers who upgrade specific MCs to the per-MC variant.*

`pkg/apis/muster/v1alpha1/mcpserver_types.go` already has:

```go
TokenExchange *TokenExchangeConfig  // RFC 8693 cross-cluster
Teleport      *TeleportAuthConfig
```

with documented semantics. What's missing is the broker runtime support for chained exchange across cluster Dexes.

**Work items (when triggered):**
- Broker `exchange.go` learns to dispatch RFC 8693 against a remote Dex per `TokenExchangeConfig`
- Cross-Dex JWKS fetch + cache for verifying tokens issued by a peer broker
- agw on the remote cluster validates JWTs from local broker via remote broker JWKS
- Translator (Phase 5) emits the right `AgentgatewayPolicy` based on whether `MCPServer.spec.auth.tokenExchange` or `auth.teleport` is set
- Teleport-based MCPServers continue working alongside via the existing `auth.teleport` path

**Topology after a Phase 7 upgrade for one customer MC:**

```
Central MC: Claude Code → Envoy → muster + agw → cross-MC HTTPS → upgraded MC agw → upgraded MC backend
                                       │ broker.GetToken(audience=upgraded-MC)        │
                                       │ → RFC 8693 with upgraded MC's broker         │
                                       │ → JWT (sub preserved, aud=upgraded-MC)       │
                                       └─                                              ◄─ JWT validated
                                                                                          against upgraded
                                                                                          MC's broker JWKS
```

**Triggers (Phase 7 is undertaken when at least one applies for a specific customer):**
- Regulatory per-environment isolation requirements
- Hard failure-isolation requirements ("prod must stay up if dev is broken")
- Cross-MC latency that hurts AI workloads in practice
- Per-MC RBAC variation (markedly different policies per environment)
- Scale on the central stack (throughput bottleneck)
- Per-tenant operations within a customer (team isolation)

Until at least one trigger applies, Teleport (`auth.teleport`) stays as the cross-MC transport — already supported, no Phase 7 work required.

**Effort:** 2 weeks of broker work plus per-customer cross-Dex trust setup (one-time per MC pair). **Risk:** medium (JWKS rotation timing, OIDC connector setup per cluster pair).

---

### Phase 8 — Bypass aggregator HTTP server in cluster mode

*The cleanup that completes "muster operator is the operator, agw is the data plane".*

After Phases 4–6, muster's in-process MCP HTTP server has no traffic in cluster mode. agw is the data plane both inbound (clients) and outbound (backends).

**Work items:**

- `cmd/muster/operator.go` no longer wires up `internal/aggregator/`'s in-process MCP HTTP server in cluster mode
- Filesystem mode keeps the server (it IS the filesystem-mode data plane)
- Rename `cmd/serve.go` → `cmd/operator.go` for cluster role; `serve` stays for filesystem mode
- `internal/orchestrator/` only used by filesystem mode going forward — code stays in the binary, dormant in cluster

**"Remove the aggregator" means *stop wiring it in cluster mode*, not delete the code.** The aggregator and orchestrator packages stay compiled in the binary because filesystem mode imports them. Cluster builds don't run them; the linker may strip dead code. No build tags needed unless binary size pressure justifies them later.

Cluster mode end-state: muster operator pod runs CRD reconciler + translator + workflow runtime + broker domain. ~25–35k LOC active instead of ~75k.
Filesystem mode end-state: unchanged from today.

**Effort:** 1 week. **Risk:** low (mostly composition-root deletes once Phases 4–6 land).

---

## Trigger-driven (defer until needed)

### Extract broker to its own pod

Phase 2 already separates broker code (`internal/broker/`) and gives it a gRPC adapter. The broker just runs in the same pod as the operator until something forces extraction. Triggers:

| Trigger | Action |
|---|---|
| Compliance or audit isolation requires broker secrets in a separate process boundary | New `cmd/muster/broker.go` entrypoint; switch operator's `aggregator/broker/` adapter from in-process to gRPC client; Helm chart adds broker deployment |
| Independent scaling: broker RPS for token exchange dwarfs operator's CRD load | same |
| Multi-tenant isolation: broker per tenant | same |

Effort when triggered: ~1 week (Phase 2 makes this a deployment topology change, not architectural).

### `MCPServerClass` for platform-team defaults

A cluster-scoped CRD that lets the platform team declare default policies (auth, rate limits, audit sink, RBAC rule sets) applied to every `MCPServer` in a namespace — analogous to `IngressClass` / `GatewayClass`.

Triggers:
- More than ~5 `MCPServer`s per cluster, where copy-pasting policy YAML becomes painful
- Per-namespace or per-tenant policy variation that platform team wants to enforce centrally
- Customer demand for "all my team's MCPs get these guardrails by default"

Until then, each `MCPServer` carries its own auth/RBAC fields (or the translator applies hard-coded sensible defaults). Effort when triggered: ~1.5–2 weeks.

## Critical path (revised — Phase 4 deferred to final milestone)

```
Pre-Phase-1 — ServiceClass removal (PRs #634/635/636 in review)
     │
     ▼
Phase 1 — agw behind muster (DONE on glean)
     │
     ▼
Phase 2 — TokenBroker interface + broker bounded context (#647-#657, ~7 PRs in flight)
     │
     ▼
Phase 3 — OTel tracing + logging via mcp-toolkit (#652, #653) ← parallel with Phase 2
     │
     ▼
Phase 5 — Translator MVP
     │
     ▼
─── SHIP TO CUSTOMERS ───
The platform is fully usable here. Customers deploy per-customer stack;
self-service via MCPServer CRDs; per-backend audit; OTel tracing;
OAuth via muster's broker; Teleport for cross-MC.
     │
     ▼
Final architectural milestone (coordinated; lands when mcp-oauth JWT PR ready
AND a customer audit/RBAC trigger materializes):
   - Phase 4: agw moves to user-facing edge, JWT-at-edge auth
   - Phase 6: ADR-006 → traffic.authorization
   - Phase 8: aggregator HTTP server bypassed in cluster mode
     │
     ▼
Steady state

Trigger-driven (opt-in per customer, no fixed schedule):
   Phase 7 — broker federation (only for customers upgrading to per-MC variant)
   Broker pod extraction (compliance / scale / multi-tenancy)
   MCPServerClass (when hub catalogs grow large)
```

**Effort to ship-to-customers (end of Phase 5)**: ~5-6 weeks once ServiceClass removal merges and Phase 2 reviews progress. Phase 2 dominates because of PR throughput; Phases 3 and 5 are smaller.

**Final-milestone (Phase 4 + 6 + 8) effort**: ~3-4 weeks of focused work plus the external mcp-oauth JWT PR cycle (out of our hands; runs in parallel from now). Not blocked on shipping.

**Per-customer Phase 7 upgrade**: ~2 weeks when triggered, per customer who needs it.

## Multi-cluster model

| Cluster type | Runs muster? | Runs agw? | Why |
|---|---|---|---|
| MC with platform/team agents | yes | yes | local agw handles muster ↔ same-cluster MCPs |
| WC running its own MCP backends only (no muster) | no | optional | only needed if MC-side translator emits routes via WC's agw — Phase 7+ |
| Remote MC with its own muster instance + MCPs | yes | yes | each MC's stack is symmetric; cross-MC calls go local-agw → remote-agw via broker federation |
| Cluster that's a pure backend host (no muster, MCPs reached via Teleport from elsewhere) | no | no | Teleport tunnel terminates at the MCP service directly; agw isn't in this path |

Rule of thumb: **wherever muster runs, run agw**. Wherever muster only reaches via Teleport, agw is unnecessary.

## Filesystem-mode behavior per phase

| Phase | Filesystem mode |
|---|---|
| 1 | unchanged (no agw in lab) |
| 2 | Ports added; default `noOpBroker` adapter for `--no-auth`, in-process broker for full-auth lab |
| 3 | OTel optional via `--otel-endpoint=` flag |
| 4 | JWT validation only if broker is wired in (full-auth lab); `--no-auth` lab unaffected |
| 5 | Translator is no-op; YAML reads directly into in-process aggregator |
| 6 | Keeps server-side capability store; optional `--simulate-groups` |
| 7 | N/A (cross-cluster needs K8s + remote Dex) |
| 8 | Untouched — aggregator HTTP server is filesystem mode's data plane |
| Trigger-driven | Broker pod extraction, MCPServerClass: no filesystem-mode impact |

## Preserved / replaced / lost

| Today's behavior | After plan | Workaround |
|---|---|---|
| `MCPServer` / `Workflow` / `WorkflowRun` self-service | preserved | translator emits agw stack |
| Filesystem mode for lab/dev/BDD | preserved | `EntityProvider` interface seam |
| Workflow execution in muster | preserved | unchanged |
| Cross-cluster MCP via Teleport | preserved indefinitely | broker federation as alternative when triggered (Phase 7) |
| `auth.forwardToken: true` SSO | preserved | broker handles token exchange |
| `auth.tokenExchange` (RFC 8693 cross-cluster) | preserved (already in schema) | wired in Phase 7 |
| `localCommand` MCPServer type | filesystem mode only | drop in cluster mode |
| Synthetic `auth_login_*` tool | replaced | agw RFC 9728 challenge → MCP client handles |
| `toolPrefix` deterministic naming | preserved | translator emits per-backend HTTPRoute paths; tool-name rewrite via transformation policy if needed |
| ADR-006 server-side capability store | replaced in cluster mode | stateless JWT-claim filter at agw; filesystem keeps server-side |
| Opaque access tokens | preserved as a deployment choice (default); JWT mode added as opt-in | both modes coexist — mcp-oauth validates either; agw policy is conditional on broker config |
| `internal/api/` service locator pattern | deleted in Phase 2 | constructor DI in `cmd/muster/` composition roots |
| `internal/services/` wrapper packages | deleted in Phase 2 | direct adapter wiring |

## Decision gates

| # | Question | Decision |
|---|---|---|
| 1 | `mcp-oauth` JWT mode | direct PR — we own the lib |
| 2 | Webhook validation vs status-driven errors | webhook + status fallback (decide in Phase 5) |
| 3 | Tool catalog visibility in `MCPServer.status` | yes |
| 4 | Cross-cluster: keep Teleport, wire `tokenExchange` schema in Phase 7 when triggered | accept |
| 5 | `localCommand` MCPServer type | filesystem-only post-Phase 8 |
| 6 | Package rename `internal/aggregator/` → `internal/aggregation/` | defer; separate PR if desired |
| 7 | Broker pod extraction | trigger-driven; not on critical path |
| 8 | `MCPServerClass` | trigger-driven; not on critical path |
| 9 | Drop opaque tokens entirely vs keep as deployment choice | keep both formats in both modes; broker config picks issuance, validators handle either |
| 10 | Service-locator removal scope | folded into Phase 2 alongside broker refactor |
| 11 | Per-client / per-resource issuance format selection | YAGNI; server-level toggle for now, additive override later if a consumer needs it |

## Practical first move

Tomorrow on glean:

1. Apply the Phase 1 topology flip (half a day): revert public `/mcp` split, add per-backend `HTTPRoute`s on `muster-agw`, update `MCPServer.spec.url` to point at agw, restart muster.
2. Verify Tempo shows muster→agw→backend chain for a tool call.
3. Verify per-backend rows in Loki with `route=muster/mcp-k8s` etc.
4. In parallel, draft the Phase 2 PR #1 (the `TokenBroker` interface skeleton in `internal/aggregator/token_broker.go`).
5. In parallel, open the mcp-oauth JWT-mode RFC issue.

Phase 1 completes that day. Phase 2 has its first reviewable PR within a few days. Phases 3–8 follow in sequence over ~10 weeks of focused engineering.
